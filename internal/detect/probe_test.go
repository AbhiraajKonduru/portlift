package detect

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestProbeHTTP(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	port := portOf(t, srv.URL)

	got, ok := Probe(context.Background(), port)
	if want := fmt.Sprintf("http://127.0.0.1:%d", port); !ok || got != want {
		t.Errorf("Probe = %q, %v; want %q, true", got, ok, want)
	}
}

func TestProbeHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	port := portOf(t, srv.URL)

	got, ok := Probe(context.Background(), port)
	if want := fmt.Sprintf("https://127.0.0.1:%d", port); !ok || got != want {
		t.Errorf("Probe = %q, %v; want %q, true", got, ok, want)
	}
}

func TestProbeIPv6Only(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("no IPv6 loopback:", err)
	}
	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: http.NotFoundHandler()}}
	srv.Start()
	defer srv.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	got, ok := Probe(context.Background(), port)
	if want := fmt.Sprintf("http://[::1]:%d", port); !ok || got != want {
		t.Errorf("Probe = %q, %v; want %q, true", got, ok, want)
	}
}

func TestProbeNothingListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if got, ok := Probe(context.Background(), port); ok {
		t.Errorf("Probe on a closed port = %q, true; want false", got)
	}
}

func TestProbeRejectsNonHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("220 not a web server\r\n"))
			conn.Close()
		}
	}()

	if got, ok := Probe(context.Background(), ln.Addr().(*net.TCPAddr).Port); ok {
		t.Errorf("Probe on a non-HTTP service = %q, true; want false", got)
	}
}

func TestHostBlock(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"vite", 403, "Blocked request. This host (\"x.trycloudflare.com\") is not allowed.\nTo allow this host, add \"x.trycloudflare.com\" to `server.allowedHosts` in vite.config.js.", "Vite"},
		{"rails", 403, "<h1>Blocked hosts: x.trycloudflare.com</h1>", "Rails"},
		{"django", 400, "DisallowedHost at /", "Django"},
		{"webpack", 200, "Invalid Host header", "webpack-dev-server"},
		{"healthy page", 200, "<html>how to set allowedHosts</html>", ""},
		{"unrelated error", 500, "boom", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var host string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				host = r.Host
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			got := ""
			if b := HostBlock(context.Background(), srv.URL); b != nil {
				got = b.Server
			}
			if got != tt.want {
				t.Errorf("HostBlock reported %q, want %q", got, tt.want)
			}
			if !strings.HasSuffix(host, ".trycloudflare.com") {
				t.Errorf("request Host was %q, want a trycloudflare.com name", host)
			}
		})
	}
}
