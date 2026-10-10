package cloudflared

import (
	"context"
	"fmt"
	"os"
	"strings"
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

func TestStartReturnsURLAndStops(t *testing.T) {
	t.Setenv(fakeEnv, "ok")
	tunnel, err := Start(context.Background(), os.Args[0], "https://127.0.0.1:8443", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://calm-river-demo.trycloudflare.com"; tunnel.URL != want {
		t.Errorf("URL = %q, want %q", tunnel.URL, want)
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
