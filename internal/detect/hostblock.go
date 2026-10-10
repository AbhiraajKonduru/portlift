package detect

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Block describes a dev server that will refuse some or all of what arrives
// through the tunnel because it does not recognise the public hostname.
type Block struct {
	Problem string
	Fix     string
}

var hostBlocks = []struct {
	marker string
	whole  bool // marker must be the entire response body
	server string
	fix    string
}{
	{"allowedHosts", false, "Vite", "in vite.config, set server: { allowedHosts: ['.trycloudflare.com'] }"},
	{"Blocked host", false, "Rails", `in config/environments/development.rb, add config.hosts << ".trycloudflare.com"`},
	{"DisallowedHost", false, "Django", `in settings.py, add ".trycloudflare.com" to ALLOWED_HOSTS`},
	{"Invalid Host header", true, "webpack-dev-server", "in the devServer config, set allowedHosts: ['.trycloudflare.com']"},
}

// HostBlock asks the server at origin for its front page the way the tunnel
// will, with a trycloudflare.com Host header, and reports whether the server
// rejected it. It also reports whether the server is Next.js, which blocks in
// a different way that NextDevBlock checks for.
func HostBlock(ctx context.Context, origin string) (block *Block, nextJS bool) {
	req, cancel, err := newCheck(ctx, origin, "portlift-check.trycloudflare.com")
	if err != nil {
		return nil, false
	}
	defer cancel()
	status, header, body, err := send(req)
	if err != nil {
		return nil, false
	}
	nextJS = strings.EqualFold(header.Get("X-Powered-By"), "Next.js")

	for _, b := range hostBlocks {
		hit := status >= 400 && strings.Contains(body, b.marker)
		if b.whole {
			hit = strings.TrimSpace(body) == b.marker
		}
		if hit {
			return &Block{b.server + " is rejecting requests that come through the tunnel", b.fix}, nextJS
		}
	}
	return nil, nextJS
}

// nextDevResource is a path under /_next that Next.js answers without
// compiling anything.
const nextDevResource = "/_next/static/chunks/portlift-check.js"

// NextDevBlock reports whether a Next.js dev server at origin will refuse its
// own dev resources to a page opened at publicURL. Next.js serves those,
// hot reload included, only to origins it has been told about, so the page
// loads through the tunnel but stops updating when files change.
func NextDevBlock(ctx context.Context, origin, publicURL string) *Block {
	public, err := url.Parse(publicURL)
	if err != nil {
		return nil
	}
	req, cancel, err := newCheck(ctx, strings.TrimSuffix(origin, "/")+nextDevResource, public.Host)
	if err != nil {
		return nil
	}
	defer cancel()
	// The real hostname goes in Origin so that the one warning Next.js logs
	// for this request is the same one a browser would cause anyway.
	req.Header.Set("Origin", publicURL)
	status, _, body, err := send(req)
	if err != nil || status != http.StatusForbidden || strings.TrimSpace(body) != "Unauthorized" {
		return nil
	}
	return &Block{
		Problem: "Next.js will not hot reload pages opened through the tunnel",
		Fix:     `add allowedDevOrigins: ["*.trycloudflare.com"] to next.config and restart the dev server`,
	}
}

func newCheck(ctx context.Context, target, host string) (*http.Request, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Host = host
	return req, cancel, nil
}

var checkClient = &http.Client{
	// Loopback only; local HTTPS servers use self-signed certificates.
	Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func send(req *http.Request) (status int, header http.Header, body string, err error) {
	resp, err := checkClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	return resp.StatusCode, resp.Header, string(raw), nil
}
