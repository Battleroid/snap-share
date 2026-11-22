package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/testutil"
	"github.com/Battleroid/snap-share/internal/version"
)

// uaRecorder records the User-Agent and whether a key was sent.
type uaRecorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	uas  map[string]bool
}

func (u *uaRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	u.mu.Lock()
	u.uas[r.Header.Get("User-Agent")] = strings.HasPrefix(r.Header.Get("Authorization"), "Bearer snap_")
	u.mu.Unlock()
	return u.base.RoundTrip(r)
}

type cliEnv struct {
	t    *testing.T
	s    *testutil.Server
	user *accounts.User
	key  string
	http *http.Client
	ua   *uaRecorder
}

func newCLIEnv(t *testing.T) *cliEnv {
	t.Helper()
	s := testutil.NewServer(t, testutil.RealAuth())
	ctx := context.Background()
	if err := local.MinFreeBytes.Set(ctx, s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	u, err := s.App.Accounts.Store().CreateUser(ctx, accounts.NewUser{Username: "ann", Email: "ann@example.test", EmailVerified: true, Role: auth.RoleUser}, accounts.AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	e := &cliEnv{t: t, s: s, user: u}
	e.key = e.mint(auth.AllScopes[:8]...)
	base := s.Client()
	e.ua = &uaRecorder{base: base.Transport, uas: map[string]bool{}}
	e.http = &http.Client{Transport: e.ua, CheckRedirect: base.CheckRedirect}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SNAP_URL", s.URL)
	t.Setenv("SNAP_KEY", e.key)
	return e
}

func (e *cliEnv) mint(scopes ...auth.Scope) string {
	e.t.Helper()
	tok, _, err := e.s.App.Keys.Store().Create(context.Background(), e.user.ID, keys.NewKey{Name: "cli", Scopes: scopes}, accounts.AuditEntry{})
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

// sp runs one command as main would, with stdin and captured output.
func (e *cliEnv) sp(stdin string, args ...string) (stdout, stderr string, err error) {
	e.t.Helper()
	var out, errb bytes.Buffer
	c, ok := commands[args[0]]
	if !ok {
		e.t.Fatalf("no command %s", args[0])
	}
	ctx := withEnv(context.Background(), &env{stdin: strings.NewReader(stdin), stdout: &out, stderr: &errb, http: e.http})
	err = c.run(ctx, args[1:])
	return out.String(), errb.String(), err
}

func (e *cliEnv) mustSP(stdin string, args ...string) string {
	e.t.Helper()
	out, errOut, err := e.sp(stdin, args...)
	if err != nil {
		e.t.Fatalf("sp %s: %v\n%s", strings.Join(args, " "), err, errOut)
	}
	return out
}

type row struct {
	kind, visibility string
	expires          sql.NullInt64
	password         sql.NullString
	title, filename  string
}

func (e *cliEnv) item(url string) row {
	e.t.Helper()
	slug := url[strings.LastIndexByte(url, '/')+1:]
	var r row
	err := e.s.App.DB.R.QueryRow(`SELECT kind, visibility, expires_at, password_hash, title, coalesce(filename, '') FROM items WHERE slug = ?`, slug).
		Scan(&r.kind, &r.visibility, &r.expires, &r.password, &r.title, &r.filename)
	if err != nil {
		e.t.Fatalf("item %s: %v", slug, err)
	}
	return r
}

func (e *cliEnv) get(url string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	res, err := e.http.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLogin(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("SNAP_URL", "")
	t.Setenv("SNAP_KEY", "")
	if _, _, err := e.sp("", "whoami"); err == nil || !strings.Contains(err.Error(), "sp login") {
		t.Fatalf("not logged in: %v", err)
	}

	// A revoked key is refused and nothing is saved.
	revoked := e.mint(auth.ScopeAccountRead)
	list, _ := e.s.App.Keys.Store().List(context.Background(), e.user.ID)
	if err := e.s.App.Keys.Store().Revoke(context.Background(), e.user.ID, list[0].ID, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.sp(revoked+"\n", "login", e.s.URL); err == nil || !strings.Contains(err.Error(), "Invalid credentials.") {
		t.Fatalf("bad key: %v", err)
	}
	if _, _, err := e.sp("not-a-key\n", "login", e.s.URL); err == nil {
		t.Fatal("garbage key accepted")
	}
	path, _ := configPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config written after a failed login: %v", err)
	}

	// URL and key from stdin (no terminal: no prompts).
	_, stderr, err := e.sp(e.s.URL+"/\n"+e.key+"\n", "login")
	if err != nil || !strings.Contains(stderr, "as ann") {
		t.Fatalf("login: %v %s", err, stderr)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode: %v %v", info, err)
	}
	var cfg clientConfig
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &cfg); err != nil || cfg.URL != e.s.URL || cfg.Key != e.key {
		t.Fatalf("config %s", b)
	}
	out := e.mustSP("", "whoami")
	for _, want := range []string{"ann (user)", e.s.URL, "items:read items:write items:delete", "expires  never", "0 B used", "1.0 GB"} {
		if !strings.Contains(strings.Join(strings.Fields(out), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("whoami lacks %q:\n%s", want, out)
		}
	}

	// A key without account:read still logs in.
	limited := e.mint(auth.ScopeItemsWrite)
	if _, stderr, err := e.sp(limited+"\n", "login", strings.TrimPrefix(e.s.URL, "https://")); err != nil || !strings.Contains(stderr, "account:read") {
		t.Fatalf("limited key: %v %s", err, stderr)
	}
	if _, _, err := e.sp("", "whoami"); err == nil || !strings.Contains(err.Error(), "account:read") {
		t.Errorf("whoami with a limited key: %v", err)
	}

	for ua, keyed := range e.ua.uas {
		if ua != "sp/"+version.Version || !keyed {
			t.Errorf("request with User-Agent %q (key sent: %v)", ua, keyed)
		}
	}
}

func TestUploadPasteShortListRemove(t *testing.T) {
	e := newCLIEnv(t)
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", "alpha\n")
	b := writeFile(t, dir, "photo one.txt", "bravo\n")
	pw := writeFile(t, dir, "pw", "hunter2\n")

	// Several files, one URL each, flags after the files too.
	out := e.mustSP("", "up", a, b, "--expire", "1d", "--private")
	urls := strings.Fields(out)
	if len(urls) != 2 {
		t.Fatalf("up printed %q", out)
	}
	for i, u := range urls {
		it := e.item(u)
		if it.kind != "file" || it.visibility != "private" || !it.expires.Valid {
			t.Errorf("file %d: %+v", i, it)
		}
		if d := time.Until(time.UnixMilli(it.expires.Int64)); d < 23*time.Hour || d > 25*time.Hour {
			t.Errorf("file %d expires in %v", i, d)
		}
	}
	if e.item(urls[1]).filename != "photo one.txt" {
		t.Errorf("filename %q", e.item(urls[1]).filename)
	}

	out = e.mustSP("", "up", "--password-file", pw, "--name", "words", a)
	if it := e.item(strings.TrimSpace(out)); !it.password.Valid || strings.Count(strings.TrimPrefix(strings.TrimSpace(out), e.s.URL+"/"), "-") != 2 {
		t.Errorf("password/words upload: %s %+v", out, it)
	}
	out = e.mustSP("", "up", "--slug", "custom-name", a)
	if strings.TrimSpace(out) != e.s.URL+"/custom-name" {
		t.Errorf("slug upload: %q", out)
	}
	if _, _, err := e.sp("", "up", "--slug", "x", a, b); err == nil {
		t.Error("--slug with two files accepted")
	}

	// A missing file fails the command, but the others still upload.
	out, stderr, err := e.sp("", "up", a, filepath.Join(dir, "missing"))
	if err == nil || len(strings.Fields(out)) != 1 || !strings.Contains(stderr, "missing") {
		t.Errorf("partial failure: %v %q %q", err, out, stderr)
	}
	// Server errors come through as messages.
	if _, _, err := e.sp("", "up", "--slug", "custom-name", a); err == nil || !strings.Contains(err.Error(), "1 of 1") {
		t.Errorf("taken slug: %v", err)
	}
	if _, stderr, _ := e.sp("", "up", "--slug", "custom-name", a); !strings.Contains(stderr, "That name is taken.") {
		t.Errorf("taken slug message: %q", stderr)
	}

	// Paste from stdin and from a file.
	src := "package main\n\nfunc main() {}\n"
	out = e.mustSP(src, "paste", "--lang", "go", "--name", "main.go", "--expire", "1h", "-")
	pasteURL := strings.TrimSpace(out)
	if it := e.item(pasteURL); it.kind != "paste" || it.filename != "main.go" || !it.expires.Valid {
		t.Errorf("paste: %+v", it)
	}
	if code, body := e.get(pasteURL + "/raw"); code != 200 || body != src {
		t.Errorf("paste raw: %d %q", code, body)
	}
	out = e.mustSP("", "paste", a)
	if it := e.item(strings.TrimSpace(out)); it.filename != "a.txt" {
		t.Errorf("paste file: %+v", it)
	}
	if _, _, err := e.sp("\xff\xfe", "paste"); err == nil {
		t.Error("binary paste accepted")
	}

	// Short links.
	out = e.mustSP("", "short", "https://example.com/some/where", "--slug", "docs", "--expire", "7d")
	if strings.TrimSpace(out) != e.s.URL+"/docs" {
		t.Errorf("short: %q", out)
	}
	if it := e.item(strings.TrimSpace(out)); it.kind != "link" || !it.expires.Valid {
		t.Errorf("link: %+v", it)
	}
	if _, _, err := e.sp("", "short", "ftp://example.com/"); err == nil || !strings.Contains(err.Error(), "http or https") {
		t.Errorf("ftp link: %v", err)
	}

	// List.
	out = e.mustSP("", "ls", "--limit", "50")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// 5 files (2 + password + slug + the partial run), 2 pastes, 1 link.
	if len(lines) != 9 || !strings.HasPrefix(lines[0], "id") || !strings.Contains(lines[1], "docs") || !strings.Contains(out, "(private)") {
		t.Fatalf("ls:\n%s", out)
	}
	if _, stderr, _ := e.sp("", "ls", "--limit", "2"); !strings.Contains(stderr, "2 of 8 items") {
		t.Errorf("ls limit: %q", stderr)
	}

	// Remove by slug, by link (with extension) and by id.
	var pasteID string
	for _, l := range lines {
		if f := strings.Fields(l); len(f) > 2 && f[2] == "paste" && strings.HasSuffix(pasteURL, "/"+f[1]) {
			pasteID = f[0]
		}
	}
	if pasteID == "" {
		t.Fatalf("paste not in ls:\n%s", out)
	}
	out = e.mustSP("", "rm", "DOCS", urls[0]+".txt", pasteID)
	if strings.Count(out, "deleted") != 3 {
		t.Errorf("rm: %q", out)
	}
	for _, u := range []string{e.s.URL + "/docs", urls[0], pasteURL} {
		var n int
		_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM items WHERE slug = ?`, u[strings.LastIndexByte(u, '/')+1:]).Scan(&n)
		if n != 0 {
			t.Errorf("%s still exists after rm", u)
		}
	}
	if _, stderr, err := e.sp("", "rm", "nope-not-here"); err == nil || !strings.Contains(stderr, "no such item") {
		t.Errorf("rm unknown: %v %q", err, stderr)
	}
	if _, _, err := e.sp("", "rm", "https://elsewhere.example/abc"); err == nil || !strings.Contains(err.Error(), "not on") {
		t.Errorf("rm other host: %v", err)
	}

	// A key without items:delete is told which scope is missing.
	t.Setenv("SNAP_KEY", e.mint(auth.ScopeItemsRead))
	if _, stderr, err := e.sp("", "rm", "custom-name"); err == nil || !strings.Contains(stderr, "items:delete") {
		t.Errorf("rm without scope: %v %q", err, stderr)
	}
	t.Setenv("SNAP_KEY", "snap_bad")
	if _, _, err := e.sp("", "ls"); err == nil || !strings.Contains(err.Error(), "Invalid credentials.") {
		t.Errorf("bad key: %v", err)
	}
}

func newFlags(private *bool, expire *string) *flag.FlagSet {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.BoolVar(private, "private", false, "")
	fs.StringVar(expire, "expire", "", "")
	return fs
}

func TestInterleaved(t *testing.T) {
	var private bool
	var expire string
	fs := newFlags(&private, &expire)
	got := interleaved(fs, []string{"a.png", "--private", "b.png", "--expire", "1d", "-", "--", "--odd"})
	want := []string{"--private", "--expire", "1d", "--", "a.png", "b.png", "-", "--odd"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %q", got)
	}
}

func TestNormalizeBase(t *testing.T) {
	for in, want := range map[string]string{
		"share.example.com":           "https://share.example.com",
		"https://share.example.com/":  "https://share.example.com",
		"http://203.0.113.5:8443/x?y": "http://203.0.113.5:8443",
		" https://share.example.com ": "https://share.example.com",
	} {
		if got, err := normalizeBase(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "ftp://x", "https://user:pw@x.example", "https://"} {
		if _, err := normalizeBase(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
