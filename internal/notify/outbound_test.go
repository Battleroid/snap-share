package notify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBlocked(t *testing.T) {
	for _, tc := range []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"127.8.9.10", true},
		{"::1", true},
		{"10.1.2.3", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"fd12:3456::1", true},     // ULA
		{"fd00:ec2::254", true},    // AWS metadata over IPv6
		{"169.254.169.254", true},  // metadata
		{"169.254.10.10", true},    // link-local
		{"fe80::1", true},          // link-local
		{"100.64.0.1", true},       // CGNAT
		{"100.127.255.254", true},  // CGNAT
		{"224.0.0.1", true},        // multicast
		{"ff02::1", true},          // multicast
		{"0.0.0.0", true},          // unspecified
		{"::", true},               // unspecified
		{"255.255.255.255", true},  // broadcast
		{"::ffff:127.0.0.1", true}, // IPv4-mapped loopback
		{"::ffff:10.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		{"::127.0.0.1", true},       // IPv4-compatible
		{"64:ff9b::a00:1", true},    // NAT64 of 10.0.0.1
		{"2002:c0a8:101::1", true},  // 6to4 of 192.168.1.1
		{"fe80::1%eth0", true},      // zoned
		{"1.1.1.1", false},          // public
		{"8.8.8.8", false},          //
		{"2606:4700::1111", false},  // public IPv6
		{"::ffff:1.1.1.1", false},   // mapped public
		{"64:ff9b::808:808", false}, // NAT64 of 8.8.8.8
		{"100.63.255.255", false},   // just below CGNAT
		{"172.32.0.1", false},       // just above 172.16/12
		{"2002:808:808::1", false},  // 6to4 of 8.8.8.8
		{"169.253.255.255", false},  // just below link-local
		{"192.169.0.1", false},      // just above 192.168/16
		{"11.0.0.1", false},         //
		{"2001:4860:4860::8888", false},
	} {
		if got := Blocked(netip.MustParseAddr(tc.ip)); got != tc.blocked {
			t.Errorf("Blocked(%s) = %v, want %v", tc.ip, got, tc.blocked)
		}
	}
	if !Blocked(netip.Addr{}) {
		t.Error("the zero address must be blocked")
	}
}

func TestValidAllowlist(t *testing.T) {
	if err := validAllowlist([]string{"10.0.0.0/8", "172.18.0.5", "ntfy", "ntfy.lab.home", "::1", "fd00::/8"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"http://ntfy"}, {"ntfy:80"}, {"a b"}, {"10.0.0.0/33"}, {""}} {
		if validAllowlist(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if d := OutboundAllow.Default(); len(d) != 0 {
		t.Fatalf("default allowlist %v", d)
	}
}

// target is a TLS test server that counts the requests it gets.
type target struct {
	srv  *httptest.Server
	hits atomic.Int64
	port string
}

func newTarget(t *testing.T, h http.HandlerFunc) *target {
	t.Helper()
	tg := &target{}
	tg.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tg.hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(tg.srv.Close)
	u, _ := url.Parse(tg.srv.URL)
	tg.port = u.Port()
	return tg
}

func (tg *target) tls() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(tg.srv.Certificate())
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// fakeDNS maps names to addresses; anything else is not found.
func fakeDNS(m map[string][]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, a := range m[strings.ToLower(host)] {
			out = append(out, netip.MustParseAddr(a))
		}
		if len(out) == 0 {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		return out, nil
	}
}

// reply is a finished response, body read (up to the client's cap) and closed.
type reply struct {
	StatusCode int
	Body       []byte
}

func get(t *testing.T, o *Outbound, rawURL string) (*reply, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := o.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	return &reply{StatusCode: res.StatusCode, Body: b}, err
}

func TestOutboundRefusesInternalAddresses(t *testing.T) {
	tg := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	dns := fakeDNS(map[string][]string{
		"example.com":      {"127.0.0.1"},             // rebinding: a public name answering with loopback
		"mixed.example":    {"10.0.0.1", "127.0.0.1"}, // every candidate is checked
		"metadata.example": {"169.254.169.254"},
		"mapped.example":   {"::ffff:127.0.0.1"},
		"ula.example":      {"fd00:ec2::254"},
		"cgnat.example":    {"100.64.0.1"},
		"private.example":  {"203.0.113.5"},
	})
	o := NewOutbound(OutboundOptions{Lookup: dns, TLS: tg.tls()})
	for _, u := range []string{
		"https://127.0.0.1:" + tg.port + "/",
		"https://[::1]:" + tg.port + "/",
		"https://[::ffff:127.0.0.1]:" + tg.port + "/", // IPv4-mapped literal
		"https://example.com:" + tg.port + "/",
		"https://mixed.example:" + tg.port + "/",
		"https://metadata.example:" + tg.port + "/",
		"https://mapped.example:" + tg.port + "/",
		"https://ula.example:" + tg.port + "/",
		"https://cgnat.example:" + tg.port + "/",
		"https://private.example:" + tg.port + "/",
		"https://169.254.169.254/latest/meta-data/",
	} {
		_, err := get(t, o, u)
		if !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: err = %v, want ErrBlockedAddress", u, err)
		} else if Describe(err) != "address not allowed" || strings.Contains(Describe(err), "example") {
			t.Errorf("%s: describe %q", u, Describe(err))
		}
	}
	if n := tg.hits.Load(); n != 0 {
		t.Fatalf("the target got %d requests", n)
	}
}

func TestOutboundAllowlist(t *testing.T) {
	tg := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	dns := fakeDNS(map[string][]string{"example.com": {"127.0.0.1"}})
	allow := []string{}
	o := NewOutbound(OutboundOptions{Lookup: dns, TLS: tg.tls(), Allow: func() []string { return allow }})

	if _, err := get(t, o, "https://example.com:"+tg.port+"/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("before allowlisting: %v", err)
	}
	// An exact host name allows whatever it resolves to (e.g. the ntfy container).
	allow = []string{"EXAMPLE.com."}
	res, err := get(t, o, "https://example.com:"+tg.port+"/")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("host allowlisted: %v %v", res, err)
	}
	// The name does not allow the address itself.
	if _, err := get(t, o, "https://127.0.0.1:"+tg.port+"/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("literal with only the name allowed: %v", err)
	}
	// A CIDR allows addresses in it, by literal or by name.
	allow = []string{"127.0.0.0/8"}
	for _, u := range []string{"https://127.0.0.1:" + tg.port + "/", "https://example.com:" + tg.port + "/"} {
		if res, err := get(t, o, u); err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("%s with CIDR: %v %v", u, res, err)
		}
	}
	// A CIDR elsewhere allows nothing here.
	allow = []string{"10.0.0.0/8", "other.example"}
	if _, err := get(t, o, "https://example.com:"+tg.port+"/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("unrelated entries: %v", err)
	}
}

func TestOutboundHTTPSOnly(t *testing.T) {
	var hits atomic.Int64
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	t.Cleanup(plain.Close)
	httpOK := false
	o := NewOutbound(OutboundOptions{
		AllowHTTP: func() bool { return httpOK },
		Allow:     func() []string { return []string{"127.0.0.1"} },
	})
	for _, u := range []string{plain.URL, "ftp://127.0.0.1/", "file:///etc/passwd", "gopher://127.0.0.1/"} {
		if _, err := get(t, o, u); !errors.Is(err, ErrScheme) {
			t.Errorf("%s: %v, want ErrScheme", u, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("plain http was sent")
	}
	httpOK = true
	if res, err := get(t, o, plain.URL); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("http allowed: %v %v", res, err)
	}
	// User info in the URL is refused.
	if _, err := get(t, o, strings.Replace(plain.URL, "http://", "http://u:p@", 1)); err == nil {
		t.Fatal("user info accepted")
	}
}

func TestOutboundNoRedirectsAndCappedBody(t *testing.T) {
	var landed atomic.Int64
	tg := newTarget(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "https://169.254.169.254/", http.StatusFound)
		case "/local":
			http.Redirect(w, r, "/landed", http.StatusFound)
		case "/landed":
			landed.Add(1)
		default:
			_, _ = io.WriteString(w, strings.Repeat("x", 200<<10))
		}
	})
	o := NewOutbound(OutboundOptions{TLS: tg.tls(), Allow: func() []string { return []string{"127.0.0.1"} }, MaxBody: 1000})
	for _, p := range []string{"/redirect", "/local"} {
		if _, err := get(t, o, "https://127.0.0.1:"+tg.port+p); !errors.Is(err, ErrRedirect) {
			t.Errorf("%s: %v, want ErrRedirect", p, err)
		}
	}
	if landed.Load() != 0 {
		t.Fatal("a redirect was followed")
	}
	res, err := get(t, o, "https://127.0.0.1:"+tg.port+"/big")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Body) != 1000 {
		t.Fatalf("read %d bytes, want the 1000-byte cap", len(res.Body))
	}
}

func TestOutboundIgnoresProxyEnv(t *testing.T) {
	var proxied atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxied.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	tg := newTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "direct") })
	o := NewOutbound(OutboundOptions{TLS: tg.tls(), Allow: func() []string { return []string{"127.0.0.1"} }})
	res, err := get(t, o, "https://127.0.0.1:"+tg.port+"/")
	if err != nil || res.StatusCode != http.StatusOK || proxied.Load() != 0 {
		t.Fatalf("res %v err %v proxied %d", res, err, proxied.Load())
	}
}

func TestDescribeHidesDetails(t *testing.T) {
	o := NewOutbound(OutboundOptions{Lookup: fakeDNS(nil)})
	_, err := get(t, o, "https://nowhere.example/secret-topic?token=tk_abc")
	msg := Describe(err)
	if msg != "host not found" || strings.Contains(msg, "secret-topic") || strings.Contains(msg, "tk_abc") {
		t.Fatalf("describe %q (err %v)", msg, err)
	}
}
