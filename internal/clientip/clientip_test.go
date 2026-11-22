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

func TestLimitKey(t *testing.T) {
	cases := []struct{ addr, want string }{
		{"203.0.113.9", "ip4:203.0.113.9"},
		{"::ffff:203.0.113.9", "ip4:203.0.113.9"},
		{"2001:db8:aaaa:bbbb::1", "ip6:2001:db8:aaaa:bbbb::/64"},
		{"2001:db8:aaaa:bbbb:ffff:1:2:3", "ip6:2001:db8:aaaa:bbbb::/64"},
		{"2001:db8:aaaa:bbbc::1", "ip6:2001:db8:aaaa:bbbc::/64"},
		{"fe80::1%eth0", "ip6:fe80::/64"},
		{"::1", "ip6:::/64"},
	}
	for _, c := range cases {
		if got := LimitKey(netip.MustParseAddr(c.addr)); got != c.want {
			t.Errorf("LimitKey(%s) = %q, want %q", c.addr, got, c.want)
		}
	}
	if got := LimitKey(netip.Addr{}); got != "ip:invalid" {
		t.Errorf("LimitKey(zero) = %q", got)
	}
	// One /64 shares a key; the exempt check still sees the full address.
	a, b := netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::2")
	if LimitKey(a) != LimitKey(b) {
		t.Error("one /64 must share a key")
	}
	if In(b, []netip.Prefix{netip.MustParsePrefix("2001:db8::1/128")}) {
		t.Error("the exempt check must use the full address")
	}
}
