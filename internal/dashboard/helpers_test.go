package dashboard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/testutil"
)

type env struct {
	t      *testing.T
	s      *testutil.Server
	ctx    context.Context
	client *http.Client
}

func newEnv(t *testing.T, opts ...testutil.Option) *env {
	t.Helper()
	s := testutil.NewServer(t, opts...)
	ctx := context.Background()
	if err := local.MinFreeBytes.Set(ctx, s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	base := s.Client()
	return &env{t: t, s: s, ctx: ctx, client: &http.Client{Transport: base.Transport, CheckRedirect: base.CheckRedirect}}
}

var allScopes = []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite, auth.ScopeItemsDelete, auth.ScopeAlbumsRead, auth.ScopeAlbumsWrite}

// user creates a users row (email name@example.test, verified or not), a
// session principal name, and a key principal name+"-key" with scopes (all
// item and album scopes when none are given).
func (e *env) user(name string, id int64, verified bool, scopes ...auth.Scope) {
	e.t.Helper()
	var at any
	if verified {
		at = int64(1)
	}
	e.exec(`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at) VALUES (?, ?, ?, ?, 1, 1)`,
		id, name, name+"@example.test", at)
	e.s.Auth.Add(name, auth.Principal{UserID: id, EmailVerified: verified})
	if len(scopes) == 0 {
		scopes = allScopes
	}
	e.s.Auth.Add(name+"-key", auth.Principal{UserID: id, EmailVerified: verified, KeyID: id * 100, Scopes: scopes})
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.s.App.DB.W.Exec(q, args...); err != nil {
		e.t.Fatal(err)
	}
}

type reqOpt func(*http.Request)

func as(name string) reqOpt {
	return func(r *http.Request) {
		if strings.HasSuffix(name, "-key") {
			authtest.Bearer(r, name)
			r.Header.Del("Sec-Fetch-Site")
			r.Header.Del("Origin")
			return
		}
		authtest.Session(r, name)
	}
}
func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

type resp struct {
	StatusCode int
	Header     http.Header
	Cookies    []*http.Cookie
}

func (e *env) do(method, path string, body io.Reader, opts ...reqOpt) (*resp, string) {
	e.t.Helper()
	req := e.s.Request(e.t, method, path, body)
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0")
	for _, o := range opts {
		o(req)
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatalf("%s %s: read: %v", method, path, err)
	}
	return &resp{StatusCode: res.StatusCode, Header: res.Header, Cookies: res.Cookies()}, string(b)
}

func (e *env) doJSON(method, path string, v any, opts ...reqOpt) (*resp, map[string]any) {
	e.t.Helper()
	var body io.Reader
	if v != nil {
		b, _ := json.Marshal(v)
		body = bytes.NewReader(b)
	}
	res, s := e.do(method, path, body, append([]reqOpt{header("Content-Type", "application/json"), header("Accept", "application/json")}, opts...)...)
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

// put uploads content as name and returns the item JSON.
func (e *env) put(user, name, content string) map[string]any {
	e.t.Helper()
	res, body := e.do("PUT", "/api/v1/upload/"+url.PathEscape(name), strings.NewReader(content), as(user), header("Accept", "application/json"))
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("PUT %s: %d %s", name, res.StatusCode, body)
	}
	return decode(e.t, body)
}

func (e *env) paste(user, name, content string) map[string]any {
	e.t.Helper()
	res, m := e.doJSON("POST", "/api/v1/pastes", map[string]any{"name": name, "content": content}, as(user))
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("paste: %d %v", res.StatusCode, m)
	}
	return m
}

func (e *env) link(user, target string) map[string]any {
	e.t.Helper()
	res, m := e.doJSON("POST", "/api/v1/links", map[string]any{"url": target}, as(user))
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("link: %d %v", res.StatusCode, m)
	}
	return m
}

func (e *env) album(user, title string, members ...int64) map[string]any {
	e.t.Helper()
	if members == nil {
		members = []int64{}
	}
	res, m := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": title, "items": members}, as(user))
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("album: %d %v", res.StatusCode, m)
	}
	return m
}

func (e *env) bulk(user string, body map[string]any) (*resp, map[string]any) {
	e.t.Helper()
	return e.doJSON("POST", "/api/v1/items/bulk", body, as(user))
}

// names lists the item names GET /api/v1/items?query returns, in order.
func (e *env) names(user, query string) []string {
	e.t.Helper()
	res, m := e.doJSON("GET", "/api/v1/items?limit=200&"+query, nil, as(user))
	if res.StatusCode != http.StatusOK {
		e.t.Fatalf("list %s: %d %v", query, res.StatusCode, m)
	}
	var out []string
	for _, x := range m["items"].([]any) {
		out = append(out, x.(map[string]any)["name"].(string))
	}
	return out
}

func (e *env) item(user string, id int64) map[string]any {
	e.t.Helper()
	res, m := e.doJSON("GET", fmt.Sprintf("/api/v1/items/%d", id), nil, as(user))
	if res.StatusCode != http.StatusOK {
		e.t.Fatalf("item %d: %d", id, res.StatusCode)
	}
	return m
}

func id(m map[string]any) int64 {
	v, _ := m["id"].(float64)
	return int64(v)
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func num(m map[string]any, k string) int {
	v, _ := m[k].(float64)
	return int(v)
}

func path(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return p.RequestURI()
}

// Content that sniffs as each type.
const (
	pngData  = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00"
	mp4Data  = "\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"
	flacData = "fLaC\x00\x00\x00\x22"
	zipData  = "PK\x03\x04\x14\x00\x00\x00\x08\x00"
)

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// asKey signs a request in as the bearer principal name.
func asKey(name string) reqOpt {
	return func(r *http.Request) {
		authtest.Bearer(r, name)
		r.Header.Del("Sec-Fetch-Site")
		r.Header.Del("Origin")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
