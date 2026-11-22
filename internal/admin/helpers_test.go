package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// env is a server with one admin ("root", also "root-key" for an API key
// with every scope and "root-nokey" for one without admin) and one user ("ann").
type env struct {
	t    *testing.T
	s    *testutil.Server
	ctx  context.Context
	root *accounts.User
	ann  *accounts.User
}

func newEnv(t *testing.T, opts ...testutil.Option) *env {
	t.Helper()
	s := testutil.NewServer(t, opts...)
	e := &env{t: t, s: s, ctx: context.Background()}
	if err := local.MinFreeBytes.Set(e.ctx, s.App.Settings, 0); err != nil { // the test filesystem may be small
		t.Fatal(err)
	}
	e.root = e.user("root", auth.RoleAdmin)
	e.ann = e.user("ann", auth.RoleUser)
	var rest []auth.Scope
	for _, sc := range auth.AllScopes {
		if sc != auth.ScopeAdmin {
			rest = append(rest, sc)
		}
	}
	s.Auth.Add("root-key", auth.Principal{UserID: e.root.ID, Username: "root", Role: auth.RoleAdmin, EmailVerified: true, KeyID: 7, Scopes: auth.AllScopes})
	s.Auth.Add("root-nokey", auth.Principal{UserID: e.root.ID, Username: "root", Role: auth.RoleAdmin, EmailVerified: true, KeyID: 8, Scopes: rest})
	s.Auth.Add("ann-key", auth.Principal{UserID: e.ann.ID, Username: "ann", Role: auth.RoleUser, EmailVerified: true, KeyID: 9, Scopes: rest})
	return e
}

// user creates an account and a session principal of the same name.
func (e *env) user(name string, role auth.Role) *accounts.User {
	e.t.Helper()
	u, err := e.s.App.Accounts.Store().CreateUser(e.ctx, accounts.NewUser{
		Username: name, Email: name + "@example.com", EmailVerified: true, Role: role,
	}, accounts.AuditEntry{})
	if err != nil {
		e.t.Fatal(err)
	}
	e.s.Auth.Add(name, auth.Principal{UserID: u.ID, Username: name, Role: role, EmailVerified: true, SessionID: "s-" + name})
	return u
}

type resp struct {
	StatusCode int
	Header     http.Header
}

type opt func(*http.Request)

func as(name string) opt { return func(r *http.Request) { authtest.Session(r, name) } }

func bearer(name string) opt {
	return func(r *http.Request) {
		authtest.Bearer(r, name)
		r.Header.Del("Origin")
		r.Header.Del("Sec-Fetch-Site")
	}
}

func crossSite() opt {
	return func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}
}

func header(k, v string) opt { return func(r *http.Request) { r.Header.Set(k, v) } }

func (e *env) do(method, path string, body io.Reader, opts ...opt) (*resp, string) {
	e.t.Helper()
	req := e.s.Request(e.t, method, path, body)
	for _, o := range opts {
		o(req)
	}
	res, b := e.s.Do(e.t, req) //nolint:bodyclose // Do reads and closes the body
	return &resp{res.StatusCode, res.Header}, b
}

func (e *env) get(path string, opts ...opt) (*resp, string) {
	e.t.Helper()
	return e.do("GET", path, nil, opts...)
}

func (e *env) post(path string, form url.Values, opts ...opt) (*resp, string) {
	e.t.Helper()
	return e.do("POST", path, strings.NewReader(form.Encode()), append([]opt{header("Content-Type", "application/x-www-form-urlencoded")}, opts...)...)
}

func (e *env) json(method, path string, in any, opts ...opt) (*resp, map[string]any) {
	e.t.Helper()
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	res, s := e.do(method, path, body, append([]opt{header("Content-Type", "application/json"), header("Accept", "application/json")}, opts...)...)
	out := map[string]any{}
	if strings.TrimSpace(s) != "" {
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			e.t.Fatalf("%s %s: not JSON: %q", method, path, s)
		}
	}
	return res, out
}

func (e *env) audits(action string) int {
	e.t.Helper()
	var n int
	if err := e.s.App.DB.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.s.App.DB.R.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// paste creates a paste as name and returns its id.
func (e *env) paste(name, content string) int64 {
	e.t.Helper()
	res, b := e.do("POST", "/api/v1/pastes", strings.NewReader(content), as(name), header("Content-Type", "text/plain"), header("Accept", "application/json"))
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("paste: %d %s", res.StatusCode, b)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &out); err != nil || out.ID == 0 {
		e.t.Fatalf("paste response %q", b)
	}
	return out.ID
}

// secret creates a sealed text secret as name and returns its link and id.
func (e *env) secret(name, text string) (link, id string) {
	e.t.Helper()
	res, b := e.do("POST", "/api/v1/secrets/sealed", strings.NewReader(text), as(name), header("Content-Type", "text/plain"), header("Accept", "application/json"))
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("secret: %d %s", res.StatusCode, b)
	}
	var out struct {
		URL string `json:"url"`
		ID  string `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &out); err != nil || out.URL == "" {
		e.t.Fatalf("secret response %q", b)
	}
	return out.URL, out.ID
}

// blobExists reports whether a blob key's file exists under dir.
func blobExists(t *testing.T, dir, key string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, key[:2], key[2:4], key))
	return err == nil
}

func (e *env) itemBlobs(id int64) []string {
	e.t.Helper()
	var keys []string
	row := e.s.App.DB.R.QueryRow(`SELECT coalesce(blob_key, ''), coalesce(render_key, '') FROM items WHERE id = ?`, id)
	var b, r string
	if err := row.Scan(&b, &r); err != nil {
		e.t.Fatal(err)
	}
	for _, k := range []string{b, r} {
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func (e *env) secretBlobs(id string) []string {
	e.t.Helper()
	rows, err := e.s.App.DB.R.Query(`SELECT blob_key FROM secret_blobs WHERE secret_id = ?`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			e.t.Fatal(err)
		}
		keys = append(keys, k)
	}
	return keys
}

// syncBuffer is an io.Writer safe for the server's concurrent log writes.
type syncBuffer struct {
	mu  chan struct{}
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{mu: make(chan struct{}, 1)} }

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()
	return b.buf.String()
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
