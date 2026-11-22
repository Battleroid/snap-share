// Package redteam_test holds proof-of-concept tests from the security review.
// Each test asserts the vulnerable behaviour it demonstrates, so it passes
// while the bug exists and fails once it is fixed.
package redteam_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/testutil"
)

type env struct {
	t      *testing.T
	s      *testutil.Server
	ctx    context.Context
	client *http.Client
	log    *syncBuf
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func newEnv(t *testing.T, opts ...testutil.Option) *env {
	t.Helper()
	lb := &syncBuf{}
	s := testutil.NewServer(t, append([]testutil.Option{testutil.RealAuth(), testutil.LogTo(lb)}, opts...)...)
	ctx := context.Background()
	if err := local.MinFreeBytes.Set(ctx, s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	base := s.Client()
	return &env{t: t, s: s, ctx: ctx, log: lb,
		client: &http.Client{Transport: base.Transport, CheckRedirect: base.CheckRedirect}}
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

func (e *env) mint(u *accounts.User, nk keys.NewKey) string {
	e.t.Helper()
	if nk.Name == "" {
		nk.Name = "test"
	}
	tok, _, err := e.s.App.Keys.Store().Create(e.ctx, u.ID, nk, accounts.AuditEntry{})
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

type opt func(*http.Request)

func withCookie(c *http.Cookie) opt { return func(r *http.Request) { r.AddCookie(c) } }

func bearer(tok string) opt {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Del("Origin")
		r.Header.Del("Sec-Fetch-Site")
	}
}

func header(k, v string) opt { return func(r *http.Request) { r.Header.Set(k, v) } }

func anonymous() opt {
	return func(r *http.Request) {
		r.Header.Del("Origin")
		r.Header.Del("Sec-Fetch-Site")
	}
}

type resp struct {
	StatusCode int
	Header     http.Header
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
	return &resp{StatusCode: res.StatusCode, Header: res.Header}, string(b)
}

func (e *env) doJSON(method, path string, in any, opts ...opt) (*resp, map[string]any) {
	e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	res, s := e.do(method, path, body, append([]opt{header("Content-Type", "application/json"), header("Accept", "application/json")}, opts...)...)
	m := map[string]any{}
	if strings.TrimSpace(s) != "" {
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			e.t.Fatalf("%s %s: not JSON (%d): %q", method, path, res.StatusCode, s)
		}
	}
	return res, m
}

// paste creates a paste with a browser session and returns its JSON.
func (e *env) paste(c *http.Cookie, body map[string]any) map[string]any {
	e.t.Helper()
	res, m := e.doJSON("POST", "/api/v1/pastes", body, withCookie(c))
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("create paste: %d %v", res.StatusCode, m)
	}
	return m
}
