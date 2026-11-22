package web

import (
	"net/http"
	"net/url"
	"strings"
)

// RawHost confines the separate raw origin (SNAP_RAW_URL) to raw content.
// Requests whose Host is raw's host reach the app only for
//
//	GET|HEAD /{slug}.{ext}
//	GET|HEAD /{slug}/raw, /download, /thumb, /preview
//
// (with or without a signature in the query). Every other GET or HEAD is
// redirected to the same path and query on publicOrigin; other methods get
// 404. Requests on the raw host are always anonymous: Cookie and
// Authorization are removed before anything else sees them, and no
// response from it sets a cookie. With raw nil it does nothing.
func RawHost(raw *url.URL, publicOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if raw == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.EqualFold(r.Host, raw.Host) {
				next.ServeHTTP(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				SetRawHeaders(w.Header())
				http.Error(w, "Not found.", http.StatusNotFound)
				return
			}
			if !rawContentPath(r.URL.Path) {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, publicOrigin+r.URL.RequestURI(), http.StatusFound) // #nosec G710 -- fixed origin, path only
				return
			}
			r = r.Clone(r.Context())
			r.Header.Del("Cookie")
			r.Header.Del("Authorization")
			next.ServeHTTP(noCookies{w}, r)
		})
	}
}

// rawContentPath reports whether p is one of the raw content paths. The
// slug itself (and whether it is reserved) is the dispatcher's business.
func rawContentPath(p string) bool {
	p, ok := strings.CutPrefix(p, "/")
	if !ok || p == "" {
		return false
	}
	first, rest, sub := strings.Cut(p, "/")
	if sub {
		switch rest {
		case "raw", "download", "thumb", "preview":
			return first != "" && !strings.Contains(first, ".")
		}
		return false
	}
	i := strings.LastIndexByte(first, '.')
	return i > 0 && i < len(first)-1
}

// noCookies drops Set-Cookie from a response.
type noCookies struct{ http.ResponseWriter }

func (w noCookies) WriteHeader(code int) {
	w.Header().Del("Set-Cookie")
	w.ResponseWriter.WriteHeader(code)
}

func (w noCookies) Write(b []byte) (int, error) {
	w.Header().Del("Set-Cookie")
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w noCookies) Unwrap() http.ResponseWriter { return w.ResponseWriter }
