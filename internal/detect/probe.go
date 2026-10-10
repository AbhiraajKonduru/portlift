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
	"time"
)

const (
	dialTimeout = 300 * time.Millisecond
	// A dev server can spend a while compiling before it answers its first
	// request, so the reply gets longer than the connection does.
	replyTimeout = 1500 * time.Millisecond
)

// loopback is in order of preference when more than one address answers.
var loopback = []string{"127.0.0.1", "::1"}

type answer struct {
	scheme string
	sure   bool // false when the server took the request but had not replied in time
}

// Probe checks whether an HTTP server is answering on a loopback port and
// returns the exact URL that answered. Node binds "localhost" to ::1 on some
// machines and 127.0.0.1 on others, so both are tried.
func Probe(ctx context.Context, port int) (string, bool) {
	answers := make([]*answer, len(loopback))
	done := make(chan struct{})
	for i, host := range loopback {
		go func() {
			answers[i] = probeHost(ctx, host, port)
			done <- struct{}{}
		}()
	}
	for range loopback {
		<-done
	}

	best := -1
	for i, a := range answers {
		if a == nil {
			continue
		}
		if best < 0 || (a.sure && !answers[best].sure) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	return fmt.Sprintf("%s://%s", answers[best].scheme, net.JoinHostPort(loopback[best], strconv.Itoa(port))), true
}

func probeHost(ctx context.Context, host string, port int) *answer {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(replyTimeout))
	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: localhost:%d\r\nUser-Agent: portlift\r\nAccept: */*\r\nConnection: close\r\n\r\n", port)
	head := make([]byte, len("HTTP/1.1 400"))
	n, err := io.ReadFull(conn, head)
	var netErr net.Error
	switch {
	case n == 0 && errors.As(err, &netErr) && netErr.Timeout():
		return &answer{scheme: "http"}
	case !strings.HasPrefix(string(head[:n]), "HTTP/"):
		// Not plain HTTP, but it may be HTTPS.
	case !strings.HasSuffix(string(head[:n]), " 400"):
		return &answer{scheme: "http", sure: true}
	default:
		// Some HTTPS servers answer a plain request with a 400 saying so; a
		// plain server can send a 400 too, so let the handshake decide.
		if !speaksTLS(dialer, addr) {
			return &answer{scheme: "http", sure: true}
		}
		return &answer{scheme: "https", sure: true}
	}
	if !speaksTLS(dialer, addr) {
		return nil
	}
	return &answer{scheme: "https", sure: true}
}

// speaksTLS reports whether addr completes a TLS handshake. A local HTTPS
// server almost always has a self-signed certificate, and this only ever
// talks to loopback, so the certificate is not verified.
func speaksTLS(dialer *net.Dialer, addr string) bool {
	tlsConn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return false
	}
	tlsConn.Close()
	return true
}
