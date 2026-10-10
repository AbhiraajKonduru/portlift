package cloudflared

import (
	"context"
	"os"
	"testing"
)

// Talks to the real nameservers, so it only runs when asked:
//
//	PORTLIFT_LIVE=1 go test ./internal/cloudflared -run Live
func TestLiveAuthoritativeLookup(t *testing.T) {
	if os.Getenv("PORTLIFT_LIVE") == "" {
		t.Skip("set PORTLIFT_LIVE=1 to run")
	}
	lookup, err := authoritativeLookup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found, err := lookup(context.Background(), "portlift-no-such-tunnel."+tunnelZone)
	if err != nil {
		t.Fatalf("lookup of a missing name failed instead of answering no: %v", err)
	}
	if found {
		t.Error("a made-up tunnel name was reported as existing")
	}
}
