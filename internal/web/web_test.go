package web

import (
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
)

// Templates must not carry inline styles or event handlers: the CSP forbids both.
func TestTemplatesHaveNoInlineStyleOrHandlers(t *testing.T) {
	bad := regexp.MustCompile(`(?i)<[^>]*\s(style|on[a-z]+)\s*=`)
	inlineScript := regexp.MustCompile(`(?i)<script(\s[^>]*)?>\s*[^<\s]`)
	err := fs.WalkDir(templateFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(templateFS, p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := bad.FindString(line); m != "" {
				t.Errorf("%s:%d: inline %q is blocked by the CSP; use a class or data attribute", p, i+1, m)
			}
			if inlineScript.MatchString(line) {
				t.Errorf("%s:%d: inline <script> is blocked by the CSP; add a module under static/js", p, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStaticJSHasNoEval(t *testing.T) {
	re := regexp.MustCompile(`\b(eval|new Function)\s*\(`)
	_ = fs.WalkDir(staticFS, "static/js", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(staticFS, p)
		if re.Match(b) {
			t.Errorf("%s uses eval/new Function, which the CSP blocks", p)
		}
		return nil
	})
}

func testRouter(t *testing.T) (*Router, *authtest.Fake, http.Handler) {
	t.Helper()
	assets, err := NewAssets()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rd, err := NewRenderer(assets, func(*http.Request) Site { return Site{Name: "snap"} }, log)
	if err != nil {
		t.Fatal(err)
	}
	errs := &ErrorPages{R: rd}
	rt := NewRouter(NewCSP("https://snap.test", ""), nil, errs)
	fake := authtest.New()
	fake.Add("user", auth.Principal{UserID: 1})
	fake.Add("admin", auth.Principal{UserID: 2, Role: auth.RoleAdmin})
	fake.Add("key", auth.Principal{UserID: 1, KeyID: 5, Scopes: []auth.Scope{auth.ScopeItemsRead}})
	ok := func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }
	rt.HandleFunc("GET /pub", ok, Public())
	rt.HandleFunc("GET /me", ok, SignedIn())
	rt.HandleFunc("POST /api/v1/items", ok, Scope(auth.ScopeItemsWrite))
	rt.HandleFunc("GET /api/v1/items", ok, Scope(auth.ScopeItemsRead))
	rt.HandleFunc("GET /admin", ok, Admin())
	rt.HandleFunc("GET /raw", func(w http.ResponseWriter, _ *http.Request) { SetRawHeaders(w.Header()) }, Public(), WithCSP(CSPNone))
	rt.HandleFunc("GET /s/{id}", ok, Public(), Redact(), WithCSP(CSPWasm))
	h := Chain(rt.Handler(), Recover(log, errs), SecurityHeaders(true), Authenticate(fake, http.NewCrossOriginProtection(), errs))
	return rt, fake, h
}

func TestRouteAccess(t *testing.T) {
	_, _, h := testRouter(t)
	cases := []struct {
		method, path string
		as           func(*http.Request)
		want         int
	}{
		{"GET", "/pub", nil, 200},
		{"GET", "/me", nil, 303}, // browser: redirect to sign in
		{"GET", "/me", func(r *http.Request) { authtest.Session(r, "user") }, 200},
		{"GET", "/me", func(r *http.Request) { authtest.Bearer(r, "key") }, 403}, // SignedIn is session-only
		{"GET", "/admin", func(r *http.Request) { authtest.Session(r, "user") }, 403},
		{"GET", "/admin", func(r *http.Request) { authtest.Session(r, "admin") }, 200},
		{"GET", "/api/v1/items", nil, 401},
		{"GET", "/api/v1/items", func(r *http.Request) { authtest.Bearer(r, "key") }, 200},
		{"POST", "/api/v1/items", func(r *http.Request) { authtest.Bearer(r, "key") }, 403}, // scope missing
		{"GET", "/api/v1/items", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, 401},
		// Invalid bearer never falls back to a valid cookie.
		{"GET", "/me", func(r *http.Request) { authtest.Session(r, "user"); r.Header.Set("Authorization", "Bearer nope") }, 401},
		{"GET", "/nothing", nil, 404},
		{"DELETE", "/nothing", nil, 404},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "https://snap.test"+c.path, nil)
		if c.as != nil {
			c.as(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s %s: got %d want %d (%s)", c.method, c.path, rec.Code, c.want, rec.Body.String())
		}
	}
}

func TestCSRF(t *testing.T) {
	_, fake, h := testRouter(t)
	fake.Add("writer", auth.Principal{UserID: 3})
	send := func(mut func(*http.Request)) int {
		req := httptest.NewRequest("POST", "https://snap.test/api/v1/items", nil)
		mut(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := send(func(r *http.Request) {
		authtest.Session(r, "writer")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}); c != 403 {
		t.Errorf("cross-site cookie POST: %d", c)
	}
	if c := send(func(r *http.Request) {
		authtest.Session(r, "writer")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}); c != 200 {
		t.Errorf("same-origin cookie POST: %d", c)
	}
	fake.Add("wkey", auth.Principal{UserID: 3, KeyID: 7, Scopes: []auth.Scope{auth.ScopeItemsWrite}})
	if c := send(func(r *http.Request) {
		authtest.Bearer(r, "wkey")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}); c != 200 {
		t.Errorf("bearer POST must be exempt from CSRF: %d", c)
	}
}

func TestHeaders(t *testing.T) {
	_, _, h := testRouter(t)
	get := func(p string) http.Header {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "https://snap.test"+p, nil))
		return rec.Header()
	}
	app := get("/pub")
	csp := app.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src https://snap.test/static/js/ https://snap.test/static/vendor/", "frame-ancestors 'none'", "img-src 'self' data: blob:"} {
		if !strings.Contains(csp, want) {
			t.Errorf("app CSP missing %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "wasm-unsafe-eval") || strings.Contains(csp, "'unsafe-inline'") {
		t.Errorf("app CSP too permissive: %s", csp)
	}
	if app.Get("X-Content-Type-Options") != "nosniff" || app.Get("Strict-Transport-Security") == "" {
		t.Errorf("base headers: %v", app)
	}
	if !strings.Contains(get("/s/abc").Get("Content-Security-Policy"), "'wasm-unsafe-eval'") {
		t.Error("secret route must allow wasm")
	}
	raw := get("/raw")
	if !strings.HasPrefix(raw.Get("Content-Security-Policy"), "sandbox") || raw.Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Errorf("raw headers: %v", raw)
	}
}

func TestRegisterWithoutAccessPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	NewRouter(NewCSP("https://x", ""), nil, nil).HandleFunc("GET /x", func(http.ResponseWriter, *http.Request) {})
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/dashboard?x=1":       "/dashboard?x=1",
		"//evil.example":       "/",
		"/\\evil.example":      "/",
		"https://evil.example": "/",
		"javascript:alert(1)":  "/",
	}
	for in, want := range cases {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 999: "999 B", 1000: "1 KB", 1_000_000_000: "1 GB", 1_500_000: "1.5 MB", 4_100_000: "4.1 MB", 820_000: "820 KB"}
	for n, want := range cases {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q want %q", n, got, want)
		}
	}
}
