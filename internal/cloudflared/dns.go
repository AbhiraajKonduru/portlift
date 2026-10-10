package cloudflared

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"
)

const (
	tunnelZone = "trycloudflare.com"

	// dnsWait is how long to hold the URL back while its hostname is still
	// missing from DNS.
	dnsWait     = 15 * time.Second
	dnsInterval = 250 * time.Millisecond
	dnsTimeout  = 2 * time.Second
	// dnsFailures is how many failed lookups it takes to conclude the check
	// itself is unavailable, for instance on a network that only lets DNS
	// through to its own resolver.
	dnsFailures = 3
	// dnsFallback is how long to wait instead when the check is unavailable.
	// The hostname has shown up within about two seconds whenever measured.
	dnsFallback = 2 * time.Second
)

// lookupFunc reports whether a hostname exists. An error means the question
// could not be asked, not that the answer was no.
type lookupFunc func(ctx context.Context, host string) (bool, error)

var newLookup = authoritativeLookup

// awaitDNS waits for host to exist in DNS, or for stop to close.
//
// cloudflared reports a quick tunnel's URL a second or two before the
// hostname exists. Anyone opening it in that gap gets "no such host", and
// resolvers keep handing that answer back for a minute afterwards, so the URL
// looks dead well after the tunnel is up. Holding the URL until the name
// exists avoids that.
func awaitDNS(ctx context.Context, host string, stop <-chan struct{}) {
	ctx, cancel := context.WithTimeout(ctx, dnsWait)
	defer cancel()

	lookup, err := newLookup(ctx)
	if err != nil {
		pause(ctx, dnsFallback, stop)
		return
	}
	tick := time.NewTicker(dnsInterval)
	defer tick.Stop()
	for failures := 0; ; {
		found, err := lookup(ctx, host)
		if found {
			return
		}
		if err != nil {
			if failures++; failures == dnsFailures {
				pause(ctx, dnsFallback, stop)
				return
			}
		}
		select {
		case <-tick.C:
		case <-stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

// authoritativeLookup returns a lookup that asks the tunnel zone's own
// nameservers. Asking an ordinary resolver, the system's included, would make
// it remember each "no such host" for a minute, which is the very problem
// awaitDNS exists to avoid; the zone's nameservers always answer afresh.
func authoritativeLookup(ctx context.Context) (lookupFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	nameservers, err := net.DefaultResolver.LookupNS(ctx, tunnelZone)
	if err != nil {
		return nil, err
	}
	var servers []string
	for _, ns := range nameservers {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", ns.Host)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			servers = append(servers, net.JoinHostPort(ip.String(), "53"))
		}
	}
	if len(servers) == 0 {
		return nil, errors.New("no nameserver address for " + tunnelZone)
	}

	var turn atomic.Uint32
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			server := servers[int(turn.Add(1))%len(servers)]
			return (&net.Dialer{Timeout: dnsTimeout}).DialContext(ctx, network, server)
		},
	}
	return func(ctx context.Context, host string) (bool, error) {
		ctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		defer cancel()
		// The trailing dot stops the resolver trying local search domains.
		ips, err := resolver.LookupIP(ctx, "ip4", host+".")
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return len(ips) > 0, nil
	}, nil
}

func pause(ctx context.Context, d time.Duration, stop <-chan struct{}) {
	select {
	case <-time.After(d):
	case <-stop:
	case <-ctx.Done():
	}
}
