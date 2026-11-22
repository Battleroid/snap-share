package items_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/testutil"
)

type env struct {
	t      *testing.T
	s      *testutil.Server
	svc    *items.Service
	ctx    context.Context
	client *http.Client // no cookie jar: requests carry only what the test adds
}

func newEnv(t *testing.T, opts ...testutil.Option) *env {
	t.Helper()
	s := testutil.NewServer(t, opts...)
	ctx := context.Background()
	// The test filesystem may be small; the guard has its own unit test.
	if err := local.MinFreeBytes.Set(ctx, s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	// Server.Client mutates one shared client, so build ours once for concurrent use.
	base := s.Client()
	client := &http.Client{Transport: base.Transport, CheckRedirect: base.CheckRedirect}
	return &env{t: t, s: s, svc: s.App.Items, ctx: ctx, client: client}
}

// user creates a users row and a session principal named name. Keys named
// name+"-key" get the given scopes (all item scopes when none are given).
func (e *env) user(name string, id int64, verified bool, scopes ...auth.Scope) {
	e.t.Helper()
	var at any
	if verified {
		at = int64(1)
	}
	if _, err := e.s.App.DB.W.Exec(`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at) VALUES (?, ?, ?, ?, 1, 1)`,
		id, name, name+"@example.test", at); err != nil {
		e.t.Fatal(err)
	}
	e.s.Auth.Add(name, auth.Principal{UserID: id, EmailVerified: verified})
	if len(scopes) == 0 {
		scopes = []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite, auth.ScopeItemsDelete}
	}
	e.s.Auth.Add(name+"-key", auth.Principal{UserID: id, EmailVerified: verified, KeyID: id * 100, Scopes: scopes})
}

type reqOpt func(*http.Request)

func as(name string) reqOpt { return func(r *http.Request) { authtest.Session(r, name) } }
func bearer(name string) reqOpt {
	return func(r *http.Request) {
		authtest.Bearer(r, name)
		r.Header.Del("Sec-Fetch-Site")
		r.Header.Del("Origin")
	}
}
func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func host(h string) reqOpt      { return func(r *http.Request) { r.Host = h } }
func cookie(c *http.Cookie) reqOpt {
	return func(r *http.Request) {
		if c != nil {
			r.AddCookie(c)
		}
	}
}
func jsonBody() reqOpt { return header("Content-Type", "application/json") }

// resp is a finished response (the body is already read and closed).
type resp struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r *resp) Cookies() []*http.Cookie { return r.cookies }

func (e *env) do(method, path string, body io.Reader, opts ...reqOpt) (*resp, string) {
	e.t.Helper()
	req := e.s.Request(e.t, method, path, body)
	req.Header.Set("User-Agent", "Mozilla/5.0 test")
	for _, o := range opts {
		o(req)
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Errorf("%s %s: %v", method, path, err)
		return &resp{Header: http.Header{}}, ""
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Errorf("%s %s: read: %v", method, path, err)
	}
	return &resp{StatusCode: res.StatusCode, Header: res.Header, cookies: res.Cookies()}, string(b)
}

func (e *env) doJSON(method, path string, v any, opts ...reqOpt) (*resp, map[string]any) {
	e.t.Helper()
	var body io.Reader
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
	}
	res, s := e.do(method, path, body, append([]reqOpt{jsonBody(), header("Accept", "application/json")}, opts...)...)
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

// put uploads content with PUT /api/v1/upload/{name} and returns the item JSON.
func (e *env) put(user, name, content, query string, opts ...reqOpt) map[string]any {
	e.t.Helper()
	path := "/api/v1/upload/" + name
	if query != "" {
		path += "?" + query
	}
	res, body := e.do("PUT", path, strings.NewReader(content), append([]reqOpt{as(user), header("Accept", "application/json")}, opts...)...)
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("PUT %s: %d %s", path, res.StatusCode, body)
	}
	return decode(e.t, body)
}

func (e *env) usage(id int64) (used, reserved int64) {
	e.t.Helper()
	if err := e.s.App.DB.R.QueryRow(`SELECT used_bytes, reserved_bytes FROM users WHERE id = ?`, id).Scan(&used, &reserved); err != nil {
		e.t.Fatal(err)
	}
	return used, reserved
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.s.App.DB.W.Exec(q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) item(slug string) *items.Item {
	e.t.Helper()
	it, err := e.svc.GetBySlug(e.ctx, slug)
	if err != nil {
		e.t.Fatalf("item %s: %v", slug, err)
	}
	return it
}

func (e *env) setting(name string, v any) {
	e.t.Helper()
	b, _ := json.Marshal(v)
	switch name {
	case "uploads.max_bytes":
		if err := items.MaxUploadBytes.SetJSON(e.ctx, e.s.App.Settings, b); err != nil {
			e.t.Fatal(err)
		}
	case "uploads.default_quota_bytes":
		if err := items.DefaultQuotaBytes.SetJSON(e.ctx, e.s.App.Settings, b); err != nil {
			e.t.Fatal(err)
		}
	case "uploads.blocked_extensions":
		if err := items.BlockedExtensions.SetJSON(e.ctx, e.s.App.Settings, b); err != nil {
			e.t.Fatal(err)
		}
	default:
		e.t.Fatalf("unknown setting %s", name)
	}
}

// tmpFiles lists the blob store's temp directory.
func (e *env) tmpFiles() []string {
	e.t.Helper()
	ents, err := os.ReadDir(filepath.Join(e.s.App.Cfg.FilesDir, ".tmp"))
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, x := range ents {
		out = append(out, x.Name())
	}
	return out
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func num(m map[string]any, k string) int64 {
	v, _ := m[k].(float64)
	return int64(v)
}

func path(url string) string {
	i := strings.Index(url, "://")
	if i < 0 {
		return url
	}
	rest := url[i+3:]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[j:]
	}
	return "/"
}

func itemPath(id any) string { return fmt.Sprintf("/api/v1/items/%v", id) }

var _ = db.Millis
