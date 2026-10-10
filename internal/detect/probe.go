package detect

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dialTimeout = 300 * time.Millisecond
	// replyTimeout is how long a port may stay silent before it is ruled out.
	// The probe is answered by the server's request parser, not by the app, so
	// even a server that is busy compiling replies at once.
	replyTimeout = time.Second
	// siblingGrace is how much longer a process's other ports get once one of
	// them has answered. Dev servers often hold extra ports open that accept
	// connections but never speak HTTP, and a process that has just answered
	// on one port is plainly not too busy to answer on another.
	siblingGrace = 250 * time.Millisecond
)

// The probe carries a line that is not a valid header. Strict servers, which
// is nearly all of them, reject it with a 400 from the parser without running
// any application code, so it is instant and has no side effects. Lenient
// ones ignore the line and answer normally. Either reply shows HTTP is spoken.
const (
	malformedRequest = "GET / HTTP/1.1\r\nHost: localhost\r\nUser-Agent: portlift\r\nportlift probe\r\n\r\n"
	plainRequest     = "GET / HTTP/1.1\r\nHost: localhost\r\nUser-Agent: portlift\r\nAccept: */*\r\nConnection: close\r\n\r\n"
)

// loopback is in order of preference when more than one address answers.
var loopback = []string{"127.0.0.1", "::1"}

// Probe checks whether an HTTP server is answering on a loopback port and
// returns the exact URL that answered. Node binds "localhost" to ::1 on some
// machines and 127.0.0.1 on others, so both are tried.
func Probe(ctx context.Context, port int) (string, bool) {
	schemes := make([]string, len(loopback))
	var wg sync.WaitGroup
	for i, host := range loopback {
		wg.Go(func() { schemes[i] = probeHost(ctx, net.JoinHostPort(host, strconv.Itoa(port))) })
	}
	wg.Wait()
	for i, scheme := range schemes {
		if scheme != "" {
			return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(loopback[i], strconv.Itoa(port))), true
		}
	}
	return "", false
}

// probeHost returns "http" or "https" if addr serves it, or "" if not.
func probeHost(ctx context.Context, addr string) string {
	switch exchange(ctx, addr, malformedRequest) {
	case replySilent:
		return ""
	case replyHTTP:
		return "http"
	case replyBadRequest:
		// The expected answer, but also what some HTTPS servers send when
		// spoken to in plain text, so let a handshake decide.
		if speaksTLS(ctx, addr) {
			return "https"
		}
		return "http"
	}
	if speaksTLS(ctx, addr) {
		return "https"
	}
	// A server may drop a malformed request without a word and still answer
	// a proper one.
	if r := exchange(ctx, addr, plainRequest); r == replyHTTP || r == replyBadRequest {
		return "http"
	}
	return ""
}

type reply int

const (
	replyOther      reply = iota // refused, closed, or answered in another protocol
	replySilent                  // took the request and said nothing
	replyHTTP                    // an HTTP response
	replyBadRequest              // an HTTP 400
)

// exchange sends request to addr and classifies what comes back.
func exchange(ctx context.Context, addr, request string) reply {
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return replyOther
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(replyTimeout))
	// Cancelling ctx cuts a silent port short.
	defer context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })()

	if _, err := io.WriteString(conn, request); err != nil {
		return replyOther
	}
	head := make([]byte, len("HTTP/1.1 400"))
	n, err := io.ReadFull(conn, head)
	var netErr net.Error
	switch {
	case n == 0 && errors.As(err, &netErr) && netErr.Timeout():
		return replySilent
	case !strings.HasPrefix(string(head[:n]), "HTTP/"):
		return replyOther
	case strings.HasSuffix(string(head[:n]), " 400"):
		return replyBadRequest
	}
	return replyHTTP
}

// speaksTLS reports whether addr completes a TLS handshake. A local HTTPS
// server almost always has a self-signed certificate, and this only ever
// talks to loopback, so the certificate is not verified.
func speaksTLS(ctx context.Context, addr string) bool {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: dialTimeout},
		Config:    &tls.Config{InsecureSkipVerify: true},
	}
	ctx, cancel := context.WithTimeout(ctx, replyTimeout)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
