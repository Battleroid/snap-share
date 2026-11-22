package keys_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// env is a full server with the real accounts Authenticator and API keys.
type env struct {
	t      *testing.T
	s      *testutil.Server
	ctx    context.Context
	client *http.Client // no cookie jar: requests carry only what the test adds
}

func newEnv(t *testing.T, opts ...testutil.Option) *env {
	t.Helper()
	s := testutil.NewServer(t, append([]testutil.Option{testutil.RealAuth()}, opts...)...)
	ctx := context.Background()
	if err := local.MinFreeBytes.Set(ctx, s.App.Settings, 0); err != nil { // the test filesystem may be small
		t.Fatal(err)
	}
	base := s.Client()
	return &env{t: t, s: s, ctx: ctx, client: &http.Client{Transport: base.Transport, CheckRedirect: base.CheckRedirect}}
}

func (e *env) user(name string, role auth.Role) *accounts.User {
	e.t.Helper()
	u, err := e.s.App.Accounts.Store().CreateUser(e.ctx, accounts.NewUser{
		Username: name, Email: name + "@example.test", EmailVerified: true, Role: role,
	}, accounts.AuditEntry{})
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// session signs u in and returns the session cookie.
func (e *env) session(u *accounts.User) *http.Cookie {
	e.t.Helper()
	var raw string
	err := e.s.App.DB.Tx(e.ctx, func(tx *sql.Tx) error {
		var err error
		raw, err = e.s.App.Accounts.Sessions().Create(e.ctx, tx, u.ID, "127.0.0.1", "test")
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return &http.Cookie{Name: "__Host-snap_session", Value: raw}
}

// mint creates a key for u (scopes default to the item scopes).
func (e *env) mint(u *accounts.User, nk keys.NewKey) string {
	e.t.Helper()
	if nk.Name == "" {
		nk.Name = "test"
	}
	if nk.Scopes == nil {
		nk.Scopes = keys.DefaultScopes
	}
	tok, _, err := e.s.App.Keys.Store().Create(e.ctx, u.ID, nk, accounts.AuditEntry{})
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

type opt func(*http.Request)

func withCookie(c *http.Cookie) opt { return func(r *http.Request) { r.AddCookie(c) } }

// bearer authenticates with a key, as API clients do (no browser headers).
func bearer(tok string) opt {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Del("Origin")
		r.Header.Del("Sec-Fetch-Site")
	}
}

func header(k, v string) opt { return func(r *http.Request) { r.Header.Set(k, v) } }

func jsonBody() opt { return header("Content-Type", "application/json") }

func crossSite() opt {
	return func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}
}

// resp is a finished response (the body is already read and closed).
type resp struct {
	StatusCode    int
	Header        http.Header
	ContentLength int64
}

func (e *env) do(method, path string, body io.Reader, opts ...opt) (*resp, string) {
	e.t.Helper()
	req := e.s.Request(e.t, method, path, body)
	for _, o := range opts {
		o(req)
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return &resp{StatusCode: res.StatusCode, Header: res.Header, ContentLength: res.ContentLength}, string(b)
}

func (e *env) doJSON(method, path string, in any, opts ...opt) (*resp, map[string]any) {
	e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	res, s := e.do(method, path, body, append([]opt{jsonBody(), header("Accept", "application/json")}, opts...)...)
	return res, decode(e.t, s)
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	m := map[string]any{}
	if strings.TrimSpace(s) != "" {
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("not JSON: %q", s)
		}
	}
	return m
}
