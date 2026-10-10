package detect

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The test binary doubles as a dev server: when serveEnv is set it serves
// HTTP on a free loopback port, prints its ports and waits to be killed.
const serveEnv = "PORTLIFT_TEST_SERVE"

func TestMain(m *testing.M) {
	if os.Getenv(serveEnv) == "" {
		os.Exit(m.Run())
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Like a Next.js dev server, also hold open a port that accepts
	// connections and never answers them.
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	go func() {
		for {
			if _, err := silent.Accept(); err != nil {
				return
			}
		}
	}()
	fmt.Println(ln.Addr().(*net.TCPAddr).Port, silent.Addr().(*net.TCPAddr).Port)
	http.Serve(ln, http.NotFoundHandler())
}

// startServer runs a server process whose working folder is dir and returns
// the port it serves HTTP on and the port it merely holds open.
func startServer(t *testing.T, dir string) (port, silent int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), serveEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscan(line, &port, &silent); err != nil {
		t.Fatal(err)
	}
	return port, silent
}

func find(t *testing.T) map[int]Server {
	t.Helper()
	res, err := Find(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byPort := map[int]Server{}
	for _, s := range res.Servers {
		byPort[s.Port] = s
	}
	return byPort
}

func TestFindScopesByFolder(t *testing.T) {
	base := canonical(t.TempDir())
	repo := filepath.Join(base, "repo")
	web := filepath.Join(repo, "apps", "web")
	webSrc := filepath.Join(web, "src")
	api := filepath.Join(repo, "apps", "api")
	other := filepath.Join(base, "other")
	for _, dir := range []string{filepath.Join(repo, ".git"), webSrc, api, other} {
		mustMkdir(t, dir)
	}

	webPort, webSilent := startServer(t, web)
	apiPort, apiSilent := startServer(t, api)
	otherPort, _ := startServer(t, other)

	t.Run("from the server's own folder", func(t *testing.T) {
		t.Chdir(web)
		began := time.Now()
		got := find(t)
		// Each process's HTTP port answers at once, so its silent port should
		// only be waited on for the short grace, not the full reply timeout.
		if took := time.Since(began); took > replyTimeout {
			t.Errorf("Find took %s; a port that never answers should not hold it up", took)
		}
		for name, port := range map[string]int{"web": webSilent, "api": apiSilent} {
			if _, ok := got[port]; ok {
				t.Errorf("the %s server's silent port was reported as a server", name)
			}
		}
		if s, ok := got[webPort]; !ok || s.Tier != TierBelow {
			t.Errorf("web server: found=%v tier=%d, want TierBelow", ok, s.Tier)
		} else {
			if s.Dir != web {
				t.Errorf("web server folder = %q, want %q", s.Dir, web)
			}
			if want := fmt.Sprintf("http://127.0.0.1:%d", webPort); s.URL != want {
				t.Errorf("web server URL = %q, want %q", s.URL, want)
			}
		}
		if s, ok := got[apiPort]; !ok || s.Tier != TierSibling {
			t.Errorf("api server: found=%v tier=%d, want TierSibling", ok, s.Tier)
		}
		if _, ok := got[otherPort]; ok {
			t.Error("a server outside the repo was reported")
		}
	})

	t.Run("from a subfolder of the server's folder", func(t *testing.T) {
		t.Chdir(webSrc)
		got := find(t)
		if s, ok := got[webPort]; !ok || s.Tier != TierAbove {
			t.Errorf("web server: found=%v tier=%d, want TierAbove", ok, s.Tier)
		}
	})

	t.Run("from the repo root", func(t *testing.T) {
		t.Chdir(repo)
		got := find(t)
		for name, port := range map[string]int{"web": webPort, "api": apiPort} {
			if s, ok := got[port]; !ok || s.Tier != TierBelow {
				t.Errorf("%s server: found=%v tier=%d, want TierBelow", name, ok, s.Tier)
			}
		}
	})

	t.Run("from a folder that is not in a repo", func(t *testing.T) {
		t.Chdir(other)
		got := find(t)
		if s, ok := got[otherPort]; !ok || s.Tier != TierBelow {
			t.Errorf("other server: found=%v tier=%d, want TierBelow", ok, s.Tier)
		}
		if len(got) != 1 {
			t.Errorf("found %d servers, want only the one started here: %v", len(got), got)
		}
	})
}
