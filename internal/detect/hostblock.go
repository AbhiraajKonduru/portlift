package detect

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"time"
)

// Block describes a dev server that refuses requests arriving through the
// tunnel because it does not recognise the public hostname.
type Block struct {
	Server string
	Fix    string
}

var blocks = []struct {
	marker string
	whole  bool // marker must be the entire response body
	Block
}{
	{"allowedHosts", false, Block{"Vite", "in vite.config, set server: { allowedHosts: ['.trycloudflare.com'] }"}},
	{"Blocked host", false, Block{"Rails", `in config/environments/development.rb, add config.hosts << ".trycloudflare.com"`}},
	{"DisallowedHost", false, Block{"Django", `in settings.py, add ".trycloudflare.com" to ALLOWED_HOSTS`}},
	{"Invalid Host header", true, Block{"webpack-dev-server", "in the devServer config, set allowedHosts: ['.trycloudflare.com']"}},
}

// HostBlock asks the server at origin for its front page the way the tunnel
// will, with a trycloudflare.com Host header, and reports whether the server
// rejected it.
func HostBlock(ctx context.Context, origin string) *Block {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin, nil)
	if err != nil {
		return nil
	}
	req.Host = "portlift-check.trycloudflare.com"
	client := &http.Client{
		// Loopback only; local HTTPS servers use self-signed certificates.
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	body := string(raw)

	for _, b := range blocks {
		if b.whole {
			if strings.TrimSpace(body) == b.marker {
				return &b.Block
			}
		} else if resp.StatusCode >= 400 && strings.Contains(body, b.marker) {
			return &b.Block
		}
	}
	return nil
}
