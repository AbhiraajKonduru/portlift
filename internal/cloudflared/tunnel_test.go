package cloudflared

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as a stand-in for cloudflared: when fakeEnv is set
// it prints the log lines the real one would and then behaves as told.
const fakeEnv = "PORTLIFT_FAKE_CLOUDFLARED"

func TestMain(m *testing.M) {
	switch os.Getenv(fakeEnv) {
	case "":
		os.Exit(m.Run())
	case "ok":
		fmt.Fprintln(os.Stderr, "args: "+strings.Join(os.Args[1:], " "))
		fmt.Fprintln(os.Stderr, `2026-10-10T00:00:00Z INF Requesting new quick Tunnel on trycloudflare.com...`)
		fmt.Fprintln(os.Stderr, `2026-10-10T00:00:01Z INF |  https://calm-river-demo.trycloudflare.com                                            |`)
		fmt.Fprintln(os.Stderr, `2026-10-10T00:00:02Z INF Registered tunnel connection connIndex=0 location=sjc01 protocol=quic`)
		time.Sleep(time.Minute)
	case "fail":
		fmt.Fprintln(os.Stderr, `2026-10-10T00:00:00Z ERR failed to request quick Tunnel: Post "https://api.trycloudflare.com/tunnel": dial tcp: no route to host`)
		os.Exit(1)
	case "hang":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

// fakeDNS replaces the hostname check with one that says a name does not
// exist until it has been asked `misses` times, and returns a function
// reporting the names it was asked about.
func fakeDNS(t *testing.T, misses int) func() []string {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	swapLookup(t, func(context.Context) (lookupFunc, error) {
		return func(_ context.Context, host string) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, host)
			return len(asked) > misses, nil
		}, nil
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

func swapLookup(t *testing.T, fn func(context.Context) (lookupFunc, error)) {
	t.Helper()
	old := newLookup
	newLookup = fn
	t.Cleanup(func() { newLookup = old })
}

func TestStartReturnsURLAndStops(t *testing.T) {
	t.Setenv(fakeEnv, "ok")
	asked := fakeDNS(t, 2)
	tunnel, err := Start(context.Background(), os.Args[0], "https://127.0.0.1:8443", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://calm-river-demo.trycloudflare.com"; tunnel.URL != want {
		t.Errorf("URL = %q, want %q", tunnel.URL, want)
	}
	// Start must hold the URL back until the hostname resolves: two misses,
	// then the lookup that succeeds.
	if got := asked(); len(got) != 3 || got[0] != "calm-river-demo.trycloudflare.com" {
		t.Errorf("hostname lookups = %q, want 3 for calm-river-demo.trycloudflare.com", got)
	}

	stopped := make(chan struct{})
	go func() { tunnel.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return")
	}
	select {
	case <-tunnel.Done():
	default:
		t.Error("Done is not closed after Stop")
	}
	if log := tunnel.Err().Error(); !strings.Contains(log, "args: tunnel --no-autoupdate --url https://127.0.0.1:8443 --no-tls-verify") {
		t.Errorf("cloudflared was not run with the expected arguments; log:\n%s", log)
	}
}

func TestStartFallsBackWhenDNSCheckCannotRun(t *testing.T) {
	failing := func(context.Context, string) (bool, error) { return false, errors.New("blocked") }
	setups := map[string]func(context.Context) (lookupFunc, error){
		"nameservers unknown": func(context.Context) (lookupFunc, error) { return nil, errors.New("blocked") },
		"every lookup fails":  func(context.Context) (lookupFunc, error) { return failing, nil },
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			t.Setenv(fakeEnv, "ok")
			swapLookup(t, setup)
			began := time.Now()
			tunnel, err := Start(context.Background(), os.Args[0], "http://127.0.0.1:3000", false)
			if err != nil {
				t.Fatal(err)
			}
			defer tunnel.Stop()
			if took := time.Since(began); took < dnsFallback || took > dnsWait/2 {
				t.Errorf("Start took %s; want the %s fallback pause and no more", took, dnsFallback)
			}
		})
	}
}

func TestStartReportsFailureWithLog(t *testing.T) {
	t.Setenv(fakeEnv, "fail")
	_, err := Start(context.Background(), os.Args[0], "http://127.0.0.1:3000", false)
	if err == nil {
		t.Fatal("Start succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "failed to request quick Tunnel") {
		t.Errorf("error does not carry cloudflared's output: %v", err)
	}
}

func TestStartHonoursCancel(t *testing.T) {
	t.Setenv(fakeEnv, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := Start(ctx, os.Args[0], "http://127.0.0.1:3000", false); err == nil {
		t.Fatal("Start succeeded, want the context error")
	}
}

func TestEveryAssetHasAChecksum(t *testing.T) {
	for platform, a := range assets {
		if len(a.sha256) != 64 || a.size == 0 || !strings.HasPrefix(a.name, "cloudflared-") {
			t.Errorf("%s: incomplete asset %+v", platform, a)
		}
	}
}
