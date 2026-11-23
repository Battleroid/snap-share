// Package testutil starts a full snap server for integration tests.
package testutil

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/obs"
)

// Server is a running test instance over TLS (so __Host- cookies work).
type Server struct {
	App  *app.App
	URL  string
	Auth *authtest.Fake // nil when a custom Authenticator was supplied
	Mail *mail.Fake
	srv  *httptest.Server
}

// Option adjusts the server before it starts.
type Option func(*setup)

type setup struct {
	env        map[string]string
	host       string
	auth       auth.Authenticator
	realAuth   bool
	oidcClient *http.Client
	logw       io.Writer
	logFormat  string
	appOpts    []func(*app.Options)
}

// Env sets SNAP_* variables (without the prefix), e.g. Env("RAW_URL", "https://raw.test").
func Env(k, v string) Option { return func(s *setup) { s.env["SNAP_"+k] = v } }

// Host serves the instance as https://<host> instead of its loopback IP
// address. host must be example.com or a name under it: the test client
// sends those to the server and the test certificate covers them. Passkeys
// (WebAuthn) need it, because they refuse IP addresses.
func Host(host string) Option { return func(s *setup) { s.host = host } }

// WithAuth replaces the fake authenticator.
func WithAuth(a auth.Authenticator) Option { return func(s *setup) { s.auth = a } }

// RealAuth uses the accounts Authenticator (real sessions and sign-in) instead
// of the fake; Server.Auth is nil. Create users with s.App.Accounts.Store().
func RealAuth() Option { return func(s *setup) { s.realAuth = true } }

// WithSMTP marks mail as configured, so features that need email are on.
// Messages still go to the fake mailer (Server.Mail); nothing is sent.
func WithSMTP() Option {
	return func(s *setup) {
		s.env["SNAP_SMTP_HOST"] = "smtp.invalid"
		s.env["SNAP_SMTP_FROM"] = "snap <snap@example.com>"
	}
}

// OIDCClient sets the HTTP client the server uses to reach the OIDC provider
// (for example oidctest.Provider.Client()).
func OIDCClient(c *http.Client) Option { return func(s *setup) { s.oidcClient = c } }

// LogTo sends the server's log, at debug level, to w (which must be safe
// for concurrent writes), so tests can check what is and isn't logged.
func LogTo(w io.Writer) Option { return func(s *setup) { s.logw = w } }

// LogFormat sets the format LogTo writes: "text" (the default) or "json".
func LogFormat(f string) Option { return func(s *setup) { s.logFormat = f } }

// AppOptions adjusts the app.Options the server is built with, e.g. to pass
// a blob store.
func AppOptions(f func(*app.Options)) Option {
	return func(s *setup) { s.appOpts = append(s.appOpts, f) }
}

// NewServer starts an instance with a temp data dir, fake mail and fake auth.
func NewServer(t testing.TB, opts ...Option) *Server {
	t.Helper()
	dir := t.TempDir()
	s := &setup{env: map[string]string{
		"SNAP_SECRET_KEY":     strings.Repeat("t", 32),
		"SNAP_DATA_DIR":       dir,
		"SNAP_METRICS_LISTEN": "off",
	}}
	for _, o := range opts {
		o(s)
	}

	var h atomic.Value // http.Handler, set once the app exists
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Load().(http.Handler).ServeHTTP(w, r)
	}))
	ts.StartTLS()
	t.Cleanup(ts.Close)

	s.env["SNAP_PUBLIC_URL"] = ts.URL
	if s.host != "" {
		s.env["SNAP_PUBLIC_URL"] = "https://" + s.host
	}
	cfg, err := config.Load(func(k string) string { return s.env[k] })
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	out := &Server{URL: s.env["SNAP_PUBLIC_URL"], Mail: &mail.Fake{}, srv: ts}
	a := s.auth
	if a == nil && !s.realAuth {
		out.Auth = authtest.New()
		a = out.Auth
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("SNAP_TEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if s.logw != nil {
		format := s.logFormat
		if format == "" {
			format = "text"
		}
		log = slog.New(obs.NewHandler(format, slog.LevelDebug, s.logw)) // the server's handler, request ids included
	}
	ao := app.Options{Auth: a, Mail: out.Mail, OIDCClient: s.oidcClient}
	for _, f := range s.appOpts {
		f(&ao)
	}
	application, err := app.New(context.Background(), cfg, log, ao)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	h.Store(application.Handler())
	out.App = application
	return out
}

// Client returns an HTTPS client trusting the test server, with a cookie jar,
// that does not follow redirects.
//
// Each call returns an independent client (its own cookie jar), so two clients
// behave like two browsers.
func (s *Server) Client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Transport:     s.srv.Client().Transport,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Request builds a request to path. Same-origin headers are set as a browser
// would, so cookie-authenticated POSTs pass CSRF checks.
func (s *Server) Request(t testing.TB, method, path string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, s.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", s.URL)
	return req
}

// Do sends req with a fresh client and returns the response and body.
func (s *Server) Do(t testing.TB, req *http.Request) (*http.Response, string) {
	t.Helper()
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(b)
}

// DataDir returns the instance's data directory.
func (s *Server) DataDir() string { return s.App.Cfg.DataDir }
