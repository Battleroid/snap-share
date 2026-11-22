// Package clientip resolves the client address of a request behind trusted proxies.
package clientip

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver trusts X-Forwarded-For only when the direct peer is a trusted proxy.
type Resolver struct {
	Trusted []netip.Prefix
}

func (r Resolver) trusted(a netip.Addr) bool {
	for _, p := range r.Trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// IP returns the client address. With a trusted peer it walks X-Forwarded-For
// from the right and returns the first address that is not a trusted proxy.
func (r Resolver) IP(req *http.Request) netip.Addr {
	peer := parse(req.RemoteAddr)
	if !peer.IsValid() || !r.trusted(peer) {
		return peer
	}
	var hops []string
	for _, h := range req.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a := parse(strings.TrimSpace(hops[i]))
		if !a.IsValid() {
			break // malformed hop: stop at the last good address
		}
		client = a
		if !r.trusted(a) {
			break
		}
	}
	return client
}

func parse(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

type ctxKey struct{}

// Middleware stores the resolved address in the request context.
func (r Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, r.IP(req))))
	})
}

// From returns the address stored by Middleware (invalid if absent).
func From(ctx context.Context) netip.Addr {
	a, _ := ctx.Value(ctxKey{}).(netip.Addr)
	return a
}

// In reports whether a is inside any prefix.
func In(a netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
