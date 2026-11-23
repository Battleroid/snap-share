package metrics

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/netip"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Access limits who may scrape the metrics listener.
type Access struct {
	// Token, if set, must arrive as "Authorization: Bearer <token>".
	Token string
	// CIDRs, if set, are the peer addresses allowed. The peer is the TCP
	// connection's address: the listener is not behind the proxy, so
	// X-Forwarded-For and the like are ignored.
	CIDRs []netip.Prefix
}

// Handler serves GET /metrics from g, after the Access checks; every other
// path is 404.
func Handler(g prometheus.Gatherer, a Access) http.Handler {
	prom := promhttp.HandlerFor(g, promhttp.HandlerOpts{})
	var want [32]byte
	if a.Token != "" {
		want = sha256.Sum256([]byte(a.Token))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(a.CIDRs) > 0 && !peerAllowed(r.RemoteAddr, a.CIDRs) {
			http.Error(w, "Forbidden.", http.StatusForbidden)
			return
		}
		if a.Token != "" {
			// Hashes have a fixed length, so the comparison doesn't leak the token's.
			got := sha256.Sum256([]byte(bearer(r)))
			if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="snap-metrics"`)
				http.Error(w, "Unauthorized.", http.StatusUnauthorized)
				return
			}
		}
		prom.ServeHTTP(w, r)
	}))
	return mux
}

func bearer(r *http.Request) string {
	hs := r.Header.Values("Authorization")
	if len(hs) != 1 {
		return ""
	}
	scheme, tok, ok := strings.Cut(hs[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(tok)
}

func peerAllowed(remote string, cidrs []netip.Prefix) bool {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return false
	}
	ip := ap.Addr().Unmap()
	for _, p := range cidrs {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
