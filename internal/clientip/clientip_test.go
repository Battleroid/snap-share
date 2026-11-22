package clientip

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestIP(t *testing.T) {
	r := Resolver{Trusted: []netip.Prefix{netip.MustParsePrefix("172.20.0.0/14")}}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.9:5000", "", "203.0.113.9"},
		{"203.0.113.9:5000", "1.2.3.4", "203.0.113.9"},                  // untrusted peer: header ignored
		{"172.20.0.5:5000", "198.51.100.7", "198.51.100.7"},             // via Caddy
		{"172.20.0.5:5000", "6.6.6.6, 198.51.100.7", "198.51.100.7"},    // spoofed left hop ignored
		{"172.20.0.5:5000", "198.51.100.7, 172.21.0.2", "198.51.100.7"}, // two trusted hops
		{"172.20.0.5:5000", "garbage, 198.51.100.7", "198.51.100.7"},
		{"172.20.0.5:5000", "", "172.20.0.5"},
		{"[::ffff:203.0.113.9]:80", "", "203.0.113.9"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := r.IP(req).String(); got != c.want {
			t.Errorf("%s / %q: got %s want %s", c.remote, c.xff, got, c.want)
		}
	}
}
