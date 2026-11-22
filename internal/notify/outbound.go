package notify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Outbound limits.
const (
	OutboundConnectTimeout = 5 * time.Second
	OutboundTimeout        = 10 * time.Second
	OutboundMaxBody        = 64 << 10
)

// Outbound errors callers branch on. Their messages are safe to show and
// log: they never carry the URL, topic or token.
var (
	ErrBlockedAddress = errors.New("address not allowed")
	ErrScheme         = errors.New("only https URLs are allowed")
	ErrRedirect       = errors.New("redirects are not followed")
)

// OutboundOptions configures an Outbound client. Nil funcs mean "off" or
// "empty"; zero durations use the defaults above.
type OutboundOptions struct {
	// AllowHTTP permits plain http URLs (admin setting notify.allow_http).
	AllowHTTP func() bool
	// Allow returns the admin allowlist (notify.outbound_allow): CIDRs, IP
	// addresses and exact host names that may be reached even when they
	// resolve to loopback, private, link-local or metadata addresses.
	Allow func() []string
	// Lookup resolves a host name (nil: the system resolver). Tests use it
	// to map names to addresses without DNS.
	Lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	// TLS is the client TLS config (nil: system roots).
	TLS *tls.Config

	ConnectTimeout time.Duration
	Timeout        time.Duration
	MaxBody        int64
}

// Outbound is the one HTTP client for requests to hosts that users choose
// (ntfy servers). It refuses to reach the server's own networks:
//
//   - https only, unless AllowHTTP;
//   - every address a host resolves to is checked when it is dialled, in
//     net.Dialer.Control, so a DNS answer that changes between a check and
//     the connection (rebinding) cannot slip through;
//   - loopback, private (RFC 1918, ULA), link-local, CGNAT, multicast,
//     unspecified, reserved and cloud metadata addresses are refused, also
//     when written as IPv4-mapped, IPv4-compatible, NAT64 or 6to4 IPv6,
//     unless the address or the exact host name is on the allowlist;
//   - no redirects, proxy settings from the environment are ignored, short
//     timeouts, and response bodies are cut at MaxBody.
type Outbound struct {
	opts   OutboundOptions
	client *http.Client
}

// NewOutbound builds the client.
func NewOutbound(o OutboundOptions) *Outbound {
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = OutboundConnectTimeout
	}
	if o.Timeout <= 0 {
		o.Timeout = OutboundTimeout
	}
	if o.MaxBody <= 0 {
		o.MaxBody = OutboundMaxBody
	}
	out := &Outbound{opts: o}
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.TLS != nil {
		tlsConf = o.TLS.Clone()
	}
	tr := &http.Transport{
		Proxy:                  nil, // never HTTP(S)_PROXY from the environment
		DialContext:            out.dial,
		TLSClientConfig:        tlsConf,
		TLSHandshakeTimeout:    o.ConnectTimeout,
		ResponseHeaderTimeout:  o.Timeout,
		MaxResponseHeaderBytes: 16 << 10,
		DisableKeepAlives:      true, // each request dials, so each is checked
		ForceAttemptHTTP2:      true,
	}
	out.client = &http.Client{
		Transport:     tr,
		Timeout:       o.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrRedirect },
	}
	return out
}

// Do sends req after checking its URL. The response body is limited to
// MaxBody bytes; the caller closes it.
func (o *Outbound) Do(req *http.Request) (*http.Response, error) {
	if err := o.CheckURL(req.URL.Scheme, req.URL.Host, req.URL.User != nil); err != nil {
		return nil, err
	}
	res, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	res.Body = limitedBody{Reader: io.LimitReader(res.Body, o.opts.MaxBody), Closer: res.Body}
	return res, nil
}

type limitedBody struct {
	io.Reader
	io.Closer
}

// CheckURL checks the parts of a URL the client looks at before dialling.
func (o *Outbound) CheckURL(scheme, host string, hasUser bool) error {
	switch {
	case scheme == "https":
	case scheme == "http" && o.opts.AllowHTTP != nil && o.opts.AllowHTTP():
	default:
		return ErrScheme
	}
	if host == "" || hasUser {
		return errors.New("invalid URL")
	}
	return nil
}

// dial resolves addr's host, then dials each address in turn; Control
// refuses blocked addresses at connect time.
func (o *Outbound) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("bad port: %w", err)
	}
	allow := parseAllowlist(o.allowlist())
	hostAllowed := allow.hasHost(host)

	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else {
		lookup := o.opts.Lookup
		if lookup == nil {
			lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
				return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			}
		}
		if addrs, err = lookup(ctx, host); err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
		}
	}

	d := net.Dialer{
		Timeout: o.opts.ConnectTimeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return ErrBlockedAddress
			}
			if hostAllowed || allow.hasAddr(ap.Addr()) || !Blocked(ap.Addr()) {
				return nil
			}
			return ErrBlockedAddress
		},
	}
	var first error
	for _, a := range addrs {
		c, err := d.DialContext(ctx, network, netip.AddrPortFrom(a.WithZone(""), uint16(port)).String())
		if err == nil {
			return c, nil
		}
		if first == nil || errors.Is(err, ErrBlockedAddress) {
			first = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, first
}

func (o *Outbound) allowlist() []string {
	if o.opts.Allow == nil {
		return nil
	}
	return o.opts.Allow()
}

// Address ranges the client refuses.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",          // "this network", unspecified
		"10.0.0.0/8",         // private
		"100.64.0.0/10",      // CGNAT
		"127.0.0.0/8",        // loopback
		"169.254.0.0/16",     // link-local, incl. 169.254.169.254 metadata
		"172.16.0.0/12",      // private
		"192.0.0.0/24",       // IETF protocol assignments
		"192.0.2.0/24",       // documentation
		"192.88.99.0/24",     // 6to4 relay anycast
		"192.168.0.0/16",     // private
		"198.18.0.0/15",      // benchmarking
		"198.51.100.0/24",    // documentation
		"203.0.113.0/24",     // documentation
		"224.0.0.0/4",        // multicast
		"240.0.0.0/4",        // reserved, incl. 255.255.255.255 broadcast
		"::/128",             // unspecified
		"::1/128",            // loopback
		"100::/64",           // discard
		"2001:db8::/32",      // documentation
		"fc00::/7",           // unique local, incl. fd00:ec2::254 metadata
		"fe80::/10",          // link-local
		"fec0::/10",          // site-local (deprecated)
		"ff00::/8",           // multicast
		"fd00:ec2::254/128",  // AWS metadata (inside fc00::/7; listed for clarity)
		"2001::/32",          // Teredo: embeds a client IPv4
		"2001:10::/28",       // ORCHID
		"2001:20::/28",       // ORCHIDv2
		"64:ff9b:1::/48",     // local-use NAT64
		"::ffff:0:0:0/96",    // SIIT IPv4-translated
		"169.254.169.254/32", // metadata (inside link-local; listed for clarity)
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// IPv6 ranges that carry an IPv4 address.
var (
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour = netip.MustParsePrefix("2002::/16")
)

// Blocked reports whether a may not be reached without an allowlist
// entry. IPv6 forms that embed an IPv4 address (mapped ::ffff:a.b.c.d,
// compatible ::a.b.c.d, NAT64 64:ff9b::/96, 6to4 2002::/16) are judged by
// the IPv4 address they carry.
func Blocked(a netip.Addr) bool {
	if !a.IsValid() {
		return true
	}
	a = a.WithZone("").Unmap()
	if a.Is6() {
		b := a.As16()
		switch {
		case isZero(b[:12]) && !isZero(b[12:]) && a != netip.IPv6Loopback(): // ::a.b.c.d (deprecated compatible form)
			return Blocked(netip.AddrFrom4([4]byte(b[12:])))
		case nat64.Contains(a):
			return Blocked(netip.AddrFrom4([4]byte(b[12:])))
		case sixToFour.Contains(a):
			return Blocked(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// allowlist is the parsed notify.outbound_allow setting.
type allowlist struct {
	prefixes []netip.Prefix
	hosts    map[string]bool
}

func parseAllowlist(entries []string) allowlist {
	al := allowlist{hosts: map[string]bool{}}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if p, err := netip.ParsePrefix(e); err == nil {
			al.prefixes = append(al.prefixes, p.Masked())
		} else if a, err := netip.ParseAddr(e); err == nil {
			al.prefixes = append(al.prefixes, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		} else if validHostName(e) {
			al.hosts[normHost(e)] = true
		}
	}
	return al
}

func (al allowlist) hasHost(host string) bool { return al.hosts[normHost(host)] }

func (al allowlist) hasAddr(a netip.Addr) bool {
	a = a.WithZone("").Unmap()
	for _, p := range al.prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func normHost(h string) string { return strings.TrimSuffix(strings.ToLower(h), ".") }

// validHostName accepts letters, digits, hyphens and dots, 1-253 chars,
// with labels of 1-63 characters.
func validHostName(h string) bool {
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 253 {
		return false
	}
	for label := range strings.SplitSeq(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !wordChar(r) {
				return false
			}
		}
	}
	return true
}

// wordChar reports whether r is an ASCII letter, digit, - or _.
func wordChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

// validAllowlist validates the notify.outbound_allow setting.
func validAllowlist(entries []string) error {
	if len(entries) > 100 {
		return errors.New("at most 100 entries")
	}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if _, err := netip.ParsePrefix(e); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(e); err == nil {
			continue
		}
		if validHostName(e) {
			continue
		}
		return fmt.Errorf("%q is not a CIDR, IP address or host name", e)
	}
	return nil
}

// Describe turns an outbound error into a short message that is safe to
// show and log: no URL, topic or token.
func Describe(err error) string {
	var (
		dnsErr  *net.DNSError
		netErr  net.Error
		certErr *tls.CertificateVerificationError
		unknown x509.UnknownAuthorityError
		host    x509.HostnameError
	)
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrBlockedAddress):
		return "address not allowed"
	case errors.Is(err, ErrScheme):
		return ErrScheme.Error()
	case errors.Is(err, ErrRedirect):
		return ErrRedirect.Error()
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timed out"
	case errors.As(err, &dnsErr):
		return "host not found"
	case errors.As(err, &certErr), errors.As(err, &unknown), errors.As(err, &host):
		return "TLS certificate not valid"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	}
	return "connection failed"
}
