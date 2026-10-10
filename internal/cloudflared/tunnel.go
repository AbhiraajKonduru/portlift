package cloudflared

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// startTimeout is how long cloudflared gets to hand back a URL.
	startTimeout = 30 * time.Second
	// connectGrace is how long to keep waiting, once the URL is known, for
	// the first connection to Cloudflare's edge before showing it anyway.
	connectGrace = 10 * time.Second
	logLines     = 15
)

var (
	urlPattern = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	connected  = "Registered tunnel connection"
)

// Tunnel is a running quick tunnel.
type Tunnel struct {
	URL string

	cmd  *exec.Cmd
	done chan struct{}

	mu   sync.Mutex
	tail []string
}

// Start runs cloudflared against origin and returns once the public URL is
// live. insecure skips certificate checks on an https origin.
func Start(ctx context.Context, bin, origin string, insecure bool) (*Tunnel, error) {
	args := []string{"tunnel", "--no-autoupdate", "--url", origin}
	if insecure {
		args = append(args, "--no-tls-verify")
	}
	cmd := exec.Command(bin, args...)
	prepare(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting cloudflared: %w", err)
	}
	killWithParent(cmd)

	t := &Tunnel{cmd: cmd, done: make(chan struct{})}
	urls := make(chan string, 1)
	ready := make(chan struct{})
	go func() {
		var gotURL, gotReady bool
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			t.remember(line)
			if !gotURL {
				if u := urlPattern.FindString(line); u != "" && !strings.Contains(u, "//api.") {
					gotURL = true
					urls <- u
				}
			}
			if !gotReady && strings.Contains(line, connected) {
				gotReady = true
				close(ready)
			}
		}
		cmd.Wait()
		close(t.done)
	}()

	timeout := time.NewTimer(startTimeout)
	defer timeout.Stop()
	select {
	case t.URL = <-urls:
	case <-t.done:
		return nil, t.failure("cloudflared exited before the tunnel was ready")
	case <-timeout.C:
		t.Stop()
		return nil, t.failure("cloudflared did not return a URL in time")
	case <-ctx.Done():
		t.Stop()
		return nil, ctx.Err()
	}

	timeout.Reset(connectGrace)
	select {
	case <-ready:
	case <-timeout.C:
	case <-t.done:
		return nil, t.failure("cloudflared exited before the tunnel was ready")
	case <-ctx.Done():
		t.Stop()
		return nil, ctx.Err()
	}
	return t, nil
}

// Done is closed when cloudflared exits.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

// Err explains why cloudflared exited, including its last log lines. Call it
// only after Done is closed.
func (t *Tunnel) Err() error { return t.failure("cloudflared exited") }

// Stop shuts cloudflared down and waits for it to exit.
func (t *Tunnel) Stop() {
	interrupt(t.cmd)
	select {
	case <-t.done:
		return
	case <-time.After(2 * time.Second):
	}
	t.cmd.Process.Kill()
	<-t.done
}

func (t *Tunnel) remember(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tail = append(t.tail, line)
	if len(t.tail) > logLines {
		t.tail = t.tail[1:]
	}
}

func (t *Tunnel) failure(msg string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.tail) == 0 {
		return errors.New(msg)
	}
	return fmt.Errorf("%s; its last output was:\n  %s", msg, strings.Join(t.tail, "\n  "))
}
