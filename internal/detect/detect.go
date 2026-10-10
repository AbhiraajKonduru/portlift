// Package detect finds the local server portlift should expose: an HTTP server
// whose process was started from the folder portlift was run in.
package detect

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

// Server is a local HTTP server that is in scope.
type Server struct {
	Port int
	Name string // process name, e.g. "node"
	Dir  string // folder the process was started in
	Tier Tier
	URL  string // loopback URL that answered, e.g. http://127.0.0.1:5173
}

func (s Server) String() string {
	return fmt.Sprintf("%s · %d · %s", s.Name, s.Port, s.Dir)
}

// Result is what a scan found.
type Result struct {
	Cwd     string   // folder portlift was run from
	Root    string   // repo root above Cwd, or "" outside a repo
	Servers []Server // closest first
}

// Auto returns the server to expose without asking, or nil when the user has
// to choose: a server on the current path (below, then above) is used as long
// as nothing else ties with it, and a server elsewhere in the repo is never
// exposed without confirmation.
func (r Result) Auto() *Server {
	if len(r.Servers) == 0 || r.Servers[0].Tier == TierSibling {
		return nil
	}
	if len(r.Servers) > 1 && r.Servers[1].Tier == r.Servers[0].Tier {
		return nil
	}
	return &r.Servers[0]
}

// Find scans listening ports for HTTP servers started from the current folder,
// a folder above it up to the repo root, or elsewhere in the repo.
func Find(ctx context.Context) (Result, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Result{}, err
	}
	res := Result{Cwd: canonical(wd)}
	res.Root = repoRoot(res.Cwd)

	conns, err := gnet.ConnectionsWithContext(ctx, "tcp")
	if err != nil {
		return res, fmt.Errorf("listing listening ports: %w", err)
	}
	self := int32(os.Getpid())
	ports := map[int32][]int{}
	for _, c := range conns {
		if c.Status != "LISTEN" || c.Pid <= 0 || c.Pid == self {
			continue
		}
		if port := int(c.Laddr.Port); !slices.Contains(ports[c.Pid], port) {
			ports[c.Pid] = append(ports[c.Pid], port)
		}
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for pid, pidPorts := range ports {
		wg.Go(func() {
			// Reading another process's folder fails for processes owned by
			// other users or the system, which keeps those out of scope.
			p, err := process.NewProcessWithContext(ctx, pid)
			if err != nil {
				return
			}
			dir, err := p.CwdWithContext(ctx)
			if err != nil || dir == "" {
				return
			}
			dir = canonical(dir)
			tier := classify(dir, res.Cwd, res.Root)
			if tier == TierOutside {
				return
			}
			name, _ := p.NameWithContext(ctx)
			if cut, ok := strings.CutSuffix(strings.ToLower(name), ".exe"); ok {
				name = name[:len(cut)]
			}
			// Probe this process's ports together. Once one answers, the rest
			// get only a short grace before they are given up on.
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			var answered sync.Once
			var probes sync.WaitGroup
			for _, port := range pidPorts {
				probes.Go(func() {
					url, ok := Probe(ctx, port)
					if !ok {
						return
					}
					answered.Do(func() { time.AfterFunc(siblingGrace, cancel) })
					mu.Lock()
					res.Servers = append(res.Servers, Server{Port: port, Name: name, Dir: dir, Tier: tier, URL: url})
					mu.Unlock()
				})
			}
			probes.Wait()
		})
	}
	wg.Wait()

	slices.SortFunc(res.Servers, func(a, b Server) int {
		if a.Tier != b.Tier {
			return int(a.Tier) - int(b.Tier)
		}
		return a.Port - b.Port
	})
	return res, nil
}
