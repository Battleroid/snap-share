package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
)

// logInfo is filled in by the route wrapper so the outer logger knows the
// matched pattern and whether the path may be logged.
type logInfo struct {
	pattern string
	redact  bool
	user    int64
	key     int64
}

type logKey struct{}

func logInfoFrom(ctx context.Context) *logInfo {
	li, _ := ctx.Value(logKey{}).(*logInfo)
	return li
}

// statusWriter records status and size. Unwrap lets http.ResponseController
// reach the underlying writer (Flush, deadlines).
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Metrics are the HTTP metrics exported on the metrics listener.
type Metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics registers HTTP metrics on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_http_requests_total", Help: "HTTP requests by route pattern and status.",
		}, []string{"route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "snap_http_request_duration_seconds", Help: "HTTP request duration by route pattern.",
			Buckets: []float64{.005, .02, .1, .5, 2, 10, 60},
		}, []string{"route"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

// Logging logs one line per request: route pattern, status, size, duration,
// client IP and principal. The path is logged only for routes not marked Redact.
// Query strings, headers and cookies are never logged.
func Logging(log *slog.Logger, m *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			li := &logInfo{}
			sw := &statusWriter{ResponseWriter: w}
			r = r.WithContext(context.WithValue(r.Context(), logKey{}, li))
			next.ServeHTTP(sw, r)
			if sw.status == 0 {
				sw.status = http.StatusOK
			}
			d := time.Since(start)
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("route", li.pattern),
				slog.Int("status", sw.status),
				slog.Int64("bytes", sw.bytes),
				slog.Int64("ms", d.Milliseconds()),
				slog.String("ip", clientip.From(r.Context()).String()),
			}
			if !li.redact {
				attrs = append(attrs, slog.String("path", r.URL.Path))
			}
			if li.user != 0 {
				attrs = append(attrs, slog.Int64("user", li.user))
			}
			if li.key != 0 {
				attrs = append(attrs, slog.Int64("key", li.key))
			}
			level := slog.LevelInfo
			if sw.status >= 500 {
				level = slog.LevelError
			}
			log.LogAttrs(r.Context(), level, "request", attrs...)
			if m != nil {
				route := li.pattern
				if route == "" {
					route = "unmatched"
				}
				m.requests.WithLabelValues(route, strconv.Itoa(sw.status)).Inc()
				m.duration.WithLabelValues(route).Observe(d.Seconds())
			}
		})
	}
}

// Recover turns a panic into a 500 and logs the stack.
func Recover(log *slog.Logger, errs *ErrorPages) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(v)
					}
					log.Error("panic", "err", v, "stack", string(debug.Stack()))
					errs.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets headers common to every response. CSP is set per route.
func SecurityHeaders(secure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
			if secure {
				h.Set("Strict-Transport-Security", "max-age=31536000")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Authenticate resolves the principal and enforces CSRF protection for
// cookie-authenticated requests. Bearer requests are exempt (no cookies are
// consulted for them); invalid credentials get 401 with no fallback.
func Authenticate(a auth.Authenticator, cop *http.CrossOriginProtection, errs *ErrorPages) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, method, err := a.Authenticate(r)
			if err != nil {
				msg := "Invalid credentials."
				if errors.Is(err, auth.ErrForbiddenNetwork) {
					msg = "This key is not allowed from this address."
				}
				if r.Header.Get("Authorization") != "" {
					w.Header().Set("WWW-Authenticate", `Bearer realm="snap"`)
				}
				errs.Error(w, r, http.StatusUnauthorized, msg)
				return
			}
			if method != auth.MethodBearer {
				if err := cop.Check(r); err != nil {
					errs.Error(w, r, http.StatusForbidden, "Cross-origin request refused.")
					return
				}
			}
			if p != nil {
				if li := logInfoFrom(r.Context()); li != nil {
					li.user, li.key = p.UserID, p.KeyID
				}
				r = r.WithContext(auth.WithPrincipal(r.Context(), p))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Chain applies middleware so the first listed runs outermost.
func Chain(h http.Handler, mw ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}
