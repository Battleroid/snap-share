package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// logInfo is filled in by the route wrapper and the authenticator so the
// outer logger knows the matched pattern and the principal.
type logInfo struct {
	pattern string
	user    int64
	key     int64
	kind    string // principal kind: session, key or "" (anonymous)
}

type logKey struct{}

type requestIDKey struct{}

func logInfoFrom(ctx context.Context) *logInfo {
	li, _ := ctx.Value(logKey{}).(*logInfo)
	return li
}

// RequestID returns the id the logging middleware gave the request, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// newRequestID is 16 random hex characters. It is generated here, never
// taken from the client, so it can't carry anything into the log.
func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
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

	// Principal, if set, is called after each request that acted as a
	// user or an API key (the per-user and per-key series).
	Principal func(userID, keyID int64)
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

// Logging logs one line per request: request id, method, route pattern,
// status, size, duration, client IP and principal. The path, query string,
// headers and cookies are never logged: the route pattern names the page,
// and a path may carry a token or a secret id. Each response carries the
// id as X-Request-Id, and log records made with the request's context get
// it too (see RequestID).
func Logging(log *slog.Logger, m *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			li := &logInfo{}
			id := newRequestID()
			w.Header().Set("X-Request-Id", id)
			sw := &statusWriter{ResponseWriter: w}
			ctx := context.WithValue(context.WithValue(r.Context(), logKey{}, li), requestIDKey{}, id)
			r = r.WithContext(ctx)
			next.ServeHTTP(sw, r)
			if sw.status == 0 {
				sw.status = http.StatusOK
			}
			d := time.Since(start)
			principal := li.kind
			if principal == "" {
				principal = "anonymous"
			}
			route := li.pattern
			if route == "" {
				route = "unmatched"
			}
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status", sw.status),
				slog.Int64("bytes", sw.bytes),
				slog.Int64("ms", d.Milliseconds()),
				slog.String("ip", clientip.From(r.Context()).String()),
				slog.String("principal", principal),
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
			// The request id is added by the handler from the context.
			log.LogAttrs(r.Context(), level, "request", attrs...)
			if m != nil {
				m.requests.WithLabelValues(route, strconv.Itoa(sw.status)).Inc()
				m.duration.WithLabelValues(route).Observe(d.Seconds())
				if m.Principal != nil && (li.user != 0 || li.key != 0) {
					m.Principal(li.user, li.key)
				}
			}
		})
	}
}

// LogHandler adds the request id (RequestID) to every record logged with a
// request's context.
func LogHandler(h slog.Handler) slog.Handler { return requestIDHandler{h} }

type requestIDHandler struct{ slog.Handler }

func (h requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h requestIDHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return requestIDHandler{h.Handler.WithAttrs(as)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{h.Handler.WithGroup(name)}
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
					log.ErrorContext(r.Context(), "panic", "err", v, "stack", string(debug.Stack()))
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
					li.kind = "session"
					if p.KeyID != 0 {
						li.kind = "key"
					}
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
