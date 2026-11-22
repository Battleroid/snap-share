package items_test

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/testutil"
)

type gf = map[string]any

// gist creates a gist and returns its JSON.
func (e *env) gist(user string, body map[string]any, opts ...reqOpt) map[string]any {
	e.t.Helper()
	res, it := e.doJSON("POST", "/api/v1/gists", body, append([]reqOpt{as(user)}, opts...)...)
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("POST /api/v1/gists: %d %v", res.StatusCode, it)
	}
	return it
}

// blobs counts the blobs in the files store (not its temp directory).
func (e *env) blobs() int {
	e.t.Helper()
	n := 0
	root := e.s.App.Cfg.FilesDir
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".tmp" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func files(m map[string]any) []map[string]any {
	l, _ := m["files"].([]any)
	out := make([]map[string]any, 0, len(l))
	for _, x := range l {
		f, _ := x.(map[string]any)
		out = append(out, f)
	}
	return out
}

func unzip(t *testing.T, body string) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		if f.Method != zip.Store {
			t.Errorf("%s: method %d", f.Name, f.Method)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

const (
	goSrc = "package main\n\nfunc main() {}\n"
	pySrc = "def helper():\n    return 'quokka'\n"
	mdSrc = "# Notes\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1))\n"
)

func threeFiles() []gf {
	return []gf{
		{"name": "main.go", "content": goSrc},
		{"lang": "python", "content": pySrc},
		{"name": "notes.md", "content": mdSrc},
	}
}

func TestGistCreateAndServe(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.gist("ann", gf{"title": "Example", "files": threeFiles()})
	slug := str(it, "slug")
	page := e.s.URL + "/" + slug
	if str(it, "kind") != "gist" || str(it, "name") != "Example" || str(it, "url") != page || str(it, "page_url") != page {
		t.Fatalf("gist JSON: %v", it)
	}
	if _, ok := it["raw_url"]; ok || str(it, "download_url") != page+"/download" || str(it, "deletion_url") == "" {
		t.Errorf("gist URLs: %v", it)
	}
	if num(it, "size") != int64(len(goSrc)+len(pySrc)+len(mdSrc)) {
		t.Errorf("size %d", num(it, "size"))
	}
	fl := files(it)
	want := []struct{ name, lang, src string }{{"main.go", "Go", goSrc}, {"file2.py", "Python", pySrc}, {"notes.md", "markdown", mdSrc}}
	if len(fl) != len(want) {
		t.Fatalf("files: %v", it["files"])
	}
	for i, w := range want {
		f := fl[i]
		if str(f, "name") != w.name || str(f, "lang") != w.lang || num(f, "size") != int64(len(w.src)) ||
			str(f, "raw_url") != fmt.Sprintf("%s/raw?f=%d", page, i) || str(f, "download_url") != fmt.Sprintf("%s/download?f=%d", page, i) {
			t.Errorf("file %d: %v", i, f)
		}
	}
	// Quota: content and renders, as the items table says.
	g := e.item(slug)
	if g.BlobKey != "" || g.RenderKey != "" || g.RenderSize <= 0 || g.Filename != "main.go" {
		t.Errorf("items row: %+v", g)
	}
	if used, reserved := e.usage(1); used != e.charged(1) || used != g.Size+g.RenderSize || reserved != 0 {
		t.Errorf("used %d reserved %d, charged %d", used, reserved, e.charged(1))
	}
	if n := e.blobs(); n != 6 {
		t.Errorf("%d blobs, want 3 files and 3 renders", n)
	}
	// The hourly reconcile comes to the same usage.
	used, _ := e.usage(1)
	if err := e.svc.Reconcile(e.ctx); err != nil {
		t.Fatal(err)
	}
	if after, _ := e.usage(1); after != used {
		t.Errorf("reconcile moved usage from %d to %d", used, after)
	}

	// The API lists the files too.
	_, got := e.doJSON("GET", itemPath(num(it, "id")), nil, as("ann"))
	if len(files(got)) != 3 || str(files(got)[1], "name") != "file2.py" {
		t.Errorf("GET item: %v", got)
	}
	_, list := e.doJSON("GET", "/api/v1/items?type=gist", nil, as("ann"))
	if num(list, "total") != 1 {
		t.Fatalf("type=gist: %v", list)
	}
	if l := list["items"].([]any); len(files(l[0].(map[string]any))) != 3 {
		t.Errorf("list: %v", l[0])
	}

	// The page: highlighted code, sanitized Markdown, one toggle per Markdown file.
	res, body := e.do("GET", "/"+slug, nil)
	for _, s := range []string{`<span class="tok-k">package</span>`, `<span class="tok-k">def</span>`, `id="user-content-notes"`,
		`data-md-rendered="f2-rendered"`, `id="f0-src"`, `id="f1-src"`, "Download zip", ">files<", ">3<", "file2.py",
		`href="` + page + `/raw?f=1"`, `href="` + page + `/download?f=2"`} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %q", s)
		}
	}
	if res.StatusCode != 200 || strings.Contains(body, "<script>alert") || strings.Contains(body, `href="javascript:`) || strings.Contains(body, "Stats</a>") {
		t.Errorf("page: %d", res.StatusCode)
	}
	if _, owned := e.do("GET", "/"+slug, nil, as("ann")); !strings.Contains(owned, "/stats") || !strings.Contains(owned, "Manage") {
		t.Error("owner page lacks Stats and Manage")
	}

	// Each file is served like a paste.
	for i, w := range want {
		for _, sub := range []string{"/raw", "/download", ".txt"} {
			p := fmt.Sprintf("/%s%s?f=%d", slug, sub, i)
			res, body := e.do("GET", p, nil)
			h := res.Header
			if res.StatusCode != 200 || body != w.src {
				t.Errorf("%s: %d %q", p, res.StatusCode, body)
				continue
			}
			disp := "inline; "
			if sub == "/download" {
				disp = "attachment; "
			}
			if h.Get("Content-Type") != "text/plain; charset=utf-8" || !strings.HasPrefix(h.Get("Content-Security-Policy"), "sandbox") ||
				h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Robots-Tag") != "noindex" || h.Get("ETag") != `"`+sha(w.src)+`"` ||
				!strings.HasPrefix(h.Get("Content-Disposition"), disp) || !strings.Contains(h.Get("Content-Disposition"), "filename*=UTF-8''"+w.name) {
				t.Errorf("%s: headers %v", p, h)
			}
		}
	}
	// The zip of all files.
	res, body = e.do("GET", "/"+slug+"/download", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/zip" || !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "sandbox") ||
		res.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), `attachment; filename="Example.zip"`) {
		t.Errorf("zip: %d %v", res.StatusCode, res.Header)
	}
	if z := unzip(t, body); len(z) != 3 || z["main.go"] != goSrc || z["file2.py"] != pySrc || z["notes.md"] != mdSrc {
		t.Errorf("zip entries: %v", z)
	}
	// Anything else is not found.
	for _, p := range []string{"/raw", "/raw?f=3", "/raw?f=-1", "/raw?f=01", "/raw?f=1x", "/raw?f=", "/raw?f=0&f=1",
		"/raw?f=%2B1", "/raw?f=1e0", "/download?f=50", "/thumb", "/preview", "/thumb?f=0", ".txt"} {
		if res, _ := e.do("GET", "/"+slug+p, nil); res.StatusCode != 404 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
}

// Text that is HTML, script or SVG in a gist is still plain text, never a
// page.
func TestGistNeverHTML(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	html := "<!DOCTYPE html><html><body><script>alert(document.cookie)</script></body></html>"
	it := e.gist("ann", gf{"files": []gf{
		{"name": "index.html", "content": html},
		{"name": "logo.svg", "content": `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`},
		{"name": "sw.js", "lang": "html", "content": "self.addEventListener('fetch', () => {})"},
	}})
	for i := range 3 {
		for _, sub := range []string{"raw", "download"} {
			res, _ := e.do("GET", fmt.Sprintf("/%s/%s?f=%d", str(it, "slug"), sub, i), nil)
			if ct := res.Header.Get("Content-Type"); res.StatusCode != 200 || ct != "text/plain; charset=utf-8" || !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "sandbox") {
				t.Errorf("file %d %s: %d %s", i, sub, res.StatusCode, ct)
			}
		}
	}
	if _, page := e.do("GET", "/"+str(it, "slug"), nil); strings.Contains(page, "<script>alert") || strings.Contains(page, "<svg xmlns") {
		t.Error("gist page inlines raw content")
	}
}

func TestGistValidation(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	post := func(body any, opts ...reqOpt) (int, string) {
		t.Helper()
		b, ok := body.(string)
		if !ok {
			j, _ := json.Marshal(body)
			b = string(j)
		}
		res, out := e.do("POST", "/api/v1/gists", strings.NewReader(b), append([]reqOpt{as("ann"), header("Accept", "application/json")}, opts...)...)
		return res.StatusCode, out
	}
	ok := `{"files":[{"content":"x"}]}`
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x", "application/jsonx"} {
		if code, _ := post(ok, header("Content-Type", ct)); code != 415 {
			t.Errorf("Content-Type %q: %d", ct, code)
		}
	}
	js := header("Content-Type", "application/json")
	if code, _ := post(ok, header("Content-Type", "application/json; charset=utf-8")); code != 201 {
		t.Errorf("JSON with charset: %d", code)
	}
	many := make([]gf, items.MaxGistFiles+1)
	for i := range many {
		many[i] = gf{"content": "x"}
	}
	cases := []struct {
		name string
		body any
		code int
		msg  string
	}{
		{"no files", gf{"files": []gf{}}, 400, "at least one"},
		{"too many", gf{"files": many}, 400, "at most 50"},
		{"empty", gf{"files": []gf{{"name": "a.txt", "content": "x"}, {"name": "b.txt", "content": " \n\t"}}}, 400, "b.txt is empty"},
		{"duplicate", gf{"files": []gf{{"name": "A.go", "content": "x"}, {"name": "a.GO", "content": "y"}}}, 400, "Two files are named"},
		{"generated duplicate", gf{"files": []gf{{"content": "x"}, {"name": "file1.txt", "content": "y"}}}, 400, "Two files are named file1.txt"},
		{"unknown field", gf{"files": []gf{{"content": "x"}}, "body": "x"}, 400, ""},
		{"bad option", gf{"files": []gf{{"content": "x"}}, "visibility": "public"}, 400, ""},
		{"not JSON", "{", 400, ""},
	}
	for _, c := range cases {
		code, out := post(c.body, js)
		if code != c.code || !strings.Contains(out, c.msg) {
			t.Errorf("%s: %d %s", c.name, code, out)
		}
	}
	if n := e.blobs(); n != 2 { // the one gist made above: a file and its render
		t.Errorf("%d blobs after refused gists", n)
	}
	// Exactly 50 files is fine.
	if code, out := post(gf{"files": many[:items.MaxGistFiles]}, js); code != 201 {
		t.Errorf("50 files: %d %s", code, out)
	}
	// Names: sanitized, blocked extensions refused, generated with the language's extension.
	it := e.gist("ann", gf{"files": []gf{{"name": "../etc/passwd\u202e", "content": "x"}, {"lang": "go", "content": "package x"}, {"content": "y"}}})
	if fl := files(it); str(fl[0], "name") != "..etcpasswd" || str(fl[1], "name") != "file2.go" || str(fl[2], "name") != "file3.txt" {
		t.Errorf("names: %v", it["files"])
	}
	e.setting("uploads.blocked_extensions", []string{"exe"})
	if code, _ := post(gf{"files": []gf{{"name": "a.txt", "content": "x"}, {"name": "b.EXE", "content": "y"}}}, js); code != 415 {
		t.Errorf("blocked extension: %d", code)
	}
	// Naming original takes the first file's name; without one, random.
	if s := str(e.gist("ann", gf{"naming": "original", "files": []gf{{"name": "Report Q3.md", "content": "# x"}, {"content": "y"}}}), "slug"); s != "Report-Q3" {
		t.Errorf("original naming: %s", s)
	}
	if s := str(e.gist("ann", gf{"naming": "original", "files": []gf{{"content": "x"}, {"name": "b.md", "content": "y"}}}), "slug"); len(s) != items.DefaultRandomLength {
		t.Errorf("original naming without a first name: %s", s)
	}
	// Size: together at most the paste cap or the upload limit.
	before := e.blobs()
	e.setting("uploads.max_bytes", 1000)
	if code, out := post(gf{"files": []gf{{"content": strings.Repeat("a", 600)}, {"content": strings.Repeat("b", 600)}}}, js); code != 413 || !strings.Contains(out, "over the 1 KB limit together") {
		t.Errorf("over the limit together: %d %s", code, out)
	}
	e.setting("uploads.max_bytes", 1<<30)
	// Quota: all or nothing, and no blob is left behind.
	used, _ := e.usage(1)
	e.exec(`UPDATE users SET quota_bytes = ? WHERE id = 1`, used+100)
	if code, out := post(gf{"files": []gf{{"content": strings.Repeat("a", 60)}, {"content": strings.Repeat("b", 60)}}}, js); code != 413 || !strings.Contains(out, "quota") {
		t.Errorf("over quota: %d %s", code, out)
	}
	if n := e.blobs(); n != before {
		t.Errorf("blobs after refused gists: %d, want %d", n, before)
	}
	if u, r := e.usage(1); u != used || r != 0 {
		t.Errorf("usage after refused gists: %d %d", u, r)
	}
	// Content that fits but whose renders do not: kept without renders.
	small := e.gist("ann", gf{"files": []gf{{"name": "a.go", "content": strings.Repeat("x", 40)}, {"name": "b.go", "content": strings.Repeat("y", 40)}}})
	if g := e.item(str(small, "slug")); g.RenderSize != 0 || g.Size != 80 {
		t.Errorf("renders over quota: %+v", g)
	}
	if u, _ := e.usage(1); u != e.charged(1) {
		t.Errorf("used %d, charged %d", u, e.charged(1))
	}
	if _, page := e.do("GET", "/"+str(small, "slug"), nil); strings.Count(page, "Too large to preview") != 2 {
		t.Error("files without renders are not shown as too large to preview")
	}
}

func TestGistAccess(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	body := func(opts gf) gf {
		opts["files"] = []gf{{"name": "secret-name.go", "content": goSrc}, {"name": "b.txt", "content": "bee"}}
		return opts
	}
	priv := str(e.gist("ann", body(gf{"visibility": "private"})), "slug")
	pw := str(e.gist("ann", body(gf{"password": "hunter2-long"})), "slug")
	exp := str(e.gist("ann", body(gf{"expires": "1h"})), "slug")
	e.exec(`UPDATE items SET expires_at = ? WHERE slug = ?`, db.Millis(time.Now().Add(-time.Second)), exp)

	type who struct {
		name string
		opt  reqOpt
	}
	anon := who{"anonymous", func(*http.Request) {}}
	other := who{"bob", as("bob")}
	owner := who{"ann", as("ann")}
	check := func(slug string, w who, wantPage, wantContent int) {
		t.Helper()
		if res, _ := e.do("GET", "/"+slug, nil, w.opt); res.StatusCode != wantPage {
			t.Errorf("%s page as %s: %d want %d", slug, w.name, res.StatusCode, wantPage)
		}
		for _, sub := range []string{"/raw?f=0", "/download?f=1", "/download"} {
			if res, _ := e.do("GET", "/"+slug+sub, nil, w.opt, header("Sec-Fetch-Dest", "empty")); res.StatusCode != wantContent {
				t.Errorf("%s%s as %s: %d want %d", slug, sub, w.name, res.StatusCode, wantContent)
			}
		}
	}
	check(priv, anon, 404, 404)
	check(priv, other, 404, 404)
	check(priv, owner, 200, 200)
	check(exp, anon, 404, 404)
	check(exp, owner, 404, 404)
	check(pw, anon, 200, 403) // 200 is the unlock form
	check(pw, other, 200, 403)
	check(pw, owner, 200, 200)
	if _, page := e.do("GET", "/"+pw, nil); !strings.Contains(page, "Password required") || strings.Contains(page, "secret-name") || strings.Contains(page, "og:description") {
		t.Error("unlock page must ask for the password and not name the gist or its files")
	}
	for _, sub := range []string{"/raw?f=0", "/download"} {
		if res, _ := e.do("GET", "/"+pw+sub, nil, header("Sec-Fetch-Dest", "document")); res.StatusCode != 303 || res.Header.Get("Location") != "/"+pw {
			t.Errorf("%s navigation: %d %s", sub, res.StatusCode, res.Header.Get("Location"))
		}
	}
	// An out-of-range file answers 403 too until unlocked: the file count is not revealed.
	if res, _ := e.do("GET", "/"+pw+"/raw?f=9", nil); res.StatusCode != 403 {
		t.Errorf("locked, out of range: %d", res.StatusCode)
	}
	form := strings.NewReader(url.Values{"password": {"hunter2-long"}}.Encode())
	res, _ := e.do("POST", "/unlock/"+pw, form, header("Content-Type", "application/x-www-form-urlencoded"))
	c := unlockCookie(t, res)
	if c == nil {
		t.Fatalf("unlock: %d", res.StatusCode)
	}
	if res, body := e.do("GET", "/"+pw+"/raw?f=1", nil, cookie(c)); res.StatusCode != 200 || body != "bee" {
		t.Errorf("raw after unlock: %d %q", res.StatusCode, body)
	}
	if res, body := e.do("GET", "/"+pw+"/download", nil, cookie(c)); res.StatusCode != 200 || len(unzip(t, body)) != 2 {
		t.Errorf("zip after unlock: %d", res.StatusCode)
	}
	if res, page := e.do("GET", "/"+pw, nil, cookie(c)); res.StatusCode != 200 || !strings.Contains(page, "secret-name.go") {
		t.Errorf("page after unlock: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/"+pw+"/raw?f=9", nil, cookie(c)); res.StatusCode != 404 {
		t.Errorf("unlocked, out of range: %d", res.StatusCode)
	}
}

func TestGistEmbedAndStats(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.gist("ann", gf{"files": []gf{{"name": "a.go", "content": goSrc}, {"name": "b.txt", "content": "bee"}}})
	slug := str(it, "slug")
	_, body := e.do("GET", "/"+slug, nil, header("User-Agent", uaDiscord))
	h, _, _ := strings.Cut(body, "</head>")
	for _, want := range []string{`<meta property="og:title" content="a.go">`, `<meta property="og:description" content="2 files">`,
		`<meta property="og:url" content="` + e.s.URL + "/" + slug + `">`} {
		if !strings.Contains(h, want) {
			t.Errorf("embed lacks %s", want)
		}
	}
	if strings.Contains(h, "og:image") {
		t.Error("gist embed has an image")
	}
	priv := e.gist("ann", gf{"visibility": "private", "files": []gf{{"name": "hidden.go", "content": goSrc}}})
	if _, body := e.do("GET", "/"+str(priv, "slug"), nil, as("ann"), header("User-Agent", uaDiscord)); strings.Contains(body, "og:description") || strings.Contains(body, `og:title" content="hidden.go"`) {
		t.Error("private gist embed names the gist")
	}
	// A view of the page, and downloads of a file and of the zip.
	e.do("GET", "/"+slug, nil, header("Sec-Fetch-Dest", "document"))
	e.do("GET", "/"+slug+"/download?f=0", nil)
	e.do("GET", "/"+slug+"/download", nil)
	if err := e.svc.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	if g := e.item(slug); g.Views != 1 || g.Downloads != 2 {
		t.Errorf("views %d downloads %d", g.Views, g.Downloads)
	}
}

func TestGistRawOrigin(t *testing.T) {
	e := newEnv(t, testutil.Env("RAW_URL", "https://raw.test"))
	e.user("ann", 1, true)
	fl := []gf{{"name": "a.go", "content": goSrc}, {"name": "b.txt", "content": "bee"}}
	pub := e.gist("ann", gf{"files": fl})
	priv := e.gist("ann", gf{"files": fl, "visibility": "private"})
	ps := str(pub, "slug")
	if str(files(pub)[1], "raw_url") != "https://raw.test/"+ps+"/raw?f=1" || str(pub, "download_url") != "https://raw.test/"+ps+"/download" {
		t.Errorf("public URLs: %v", pub)
	}
	if !strings.HasPrefix(str(files(priv)[1], "raw_url"), e.s.URL+"/") {
		t.Errorf("private raw_url must stay on the app origin: %v", priv)
	}
	res, _ := e.do("GET", "/"+ps+"/raw?f=1", nil)
	if res.StatusCode != 302 || res.Header.Get("Location") != "https://raw.test/"+ps+"/raw?f=1" {
		t.Errorf("app origin raw: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, body := e.do("GET", "/"+ps+"/raw?f=1", nil, host("raw.test")); res.StatusCode != 200 || body != "bee" || !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("raw origin: %d %q", res.StatusCode, body)
	}
	if res, body := e.do("GET", "/"+ps+"/download", nil, host("raw.test")); res.StatusCode != 200 || len(unzip(t, body)) != 2 {
		t.Errorf("zip on the raw origin: %d", res.StatusCode)
	}
	for _, p := range []string{"/raw", "/raw?f=2", "/thumb"} {
		if res, _ := e.do("GET", "/"+ps+p, nil, host("raw.test")); res.StatusCode != 404 {
			t.Errorf("raw origin %s: %d", p, res.StatusCode)
		}
	}
	// Private: signed URLs, which cover every file of the gist.
	slug := str(priv, "slug")
	if res, _ := e.do("GET", "/"+slug+"/raw?f=1", nil, host("raw.test")); res.StatusCode != 404 {
		t.Errorf("unsigned private on raw origin: %d", res.StatusCode)
	}
	res, _ = e.do("GET", "/"+slug+"/raw?f=1", nil, as("ann"))
	loc := res.Header.Get("Location")
	if res.StatusCode != 302 || !strings.HasPrefix(loc, "https://raw.test/"+slug+"/raw?f=1&e=") {
		t.Fatalf("owner redirect: %d %s", res.StatusCode, loc)
	}
	signed := path(loc)
	if res, body := e.do("GET", signed, nil, host("raw.test")); res.StatusCode != 200 || body != "bee" {
		t.Errorf("signed raw: %d %q", res.StatusCode, body)
	}
	other := strings.Replace(signed, "f=1", "f=0", 1)
	if res, body := e.do("GET", other, nil, host("raw.test")); res.StatusCode != 200 || body != goSrc {
		t.Errorf("signature with another file: %d %q", res.StatusCode, body)
	}
	if res, _ := e.do("GET", signed[:len(signed)-3]+"abc", nil, host("raw.test")); res.StatusCode != 404 {
		t.Errorf("tampered signature: %d", res.StatusCode)
	}
	zipURL := strings.Replace(strings.Replace(signed, "/raw?", "/download?", 1), "f=1&", "", 1)
	if res, body := e.do("GET", zipURL, nil, host("raw.test")); res.StatusCode != 200 || len(unzip(t, body)) != 2 {
		t.Errorf("signed zip %s: %d", zipURL, res.StatusCode)
	}
	if _, page := e.do("GET", "/"+slug, nil, as("ann")); !strings.Contains(page, "https://raw.test/"+slug+"/raw?f=0&amp;e=") ||
		!strings.Contains(page, "https://raw.test/"+slug+"/download?e=") {
		t.Error("owner page without signed raw-origin URLs")
	}
}

// Every way an item is deleted removes a gist's files and renders.
func TestGistDeleteRemovesBlobs(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	mk := func(user string, extra gf) map[string]any {
		t.Helper()
		extra["files"] = threeFiles()
		it := e.gist(user, extra)
		if n := e.blobs(); n != 6 {
			t.Fatalf("%d blobs after creating, want 6", n)
		}
		return it
	}
	gone := func(how string, it map[string]any) {
		t.Helper()
		if n := e.blobs(); n != 0 {
			t.Errorf("%s: %d blobs left", how, n)
		}
		var rows int
		_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM gist_files WHERE item_id = ?`, num(it, "id")).Scan(&rows)
		if rows != 0 {
			t.Errorf("%s: %d gist_files rows left", how, rows)
		}
		if used, reserved := e.usage(1); used != 0 || reserved != 0 {
			t.Errorf("%s: usage %d %d", how, used, reserved)
		}
	}

	it := mk("ann", gf{})
	if res, _ := e.doJSON("DELETE", itemPath(num(it, "id")), nil, as("ann")); res.StatusCode != 204 {
		t.Fatalf("API delete: %d", res.StatusCode)
	}
	gone("Delete", it)

	it = mk("ann", gf{})
	g := e.item(str(it, "slug")) // as dashboard and admin read it: no files loaded
	if g.Files != nil {
		t.Fatal("Get loads files")
	}
	if err := e.s.App.DB.Tx(e.ctx, func(tx *sql.Tx) error { return e.svc.DeleteTx(e.ctx, tx, g) }); err != nil {
		t.Fatal(err)
	}
	e.svc.DeleteBlobs(e.ctx, g)
	gone("DeleteTx", it)

	it = mk("ann", gf{"expires": "1h"})
	e.exec(`UPDATE items SET expires_at = ? WHERE id = ?`, db.Millis(time.Now().Add(-time.Minute)), num(it, "id"))
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	gone("Sweep", it)

	it = mk("ann", gf{})
	if err := e.svc.DeleteOwnedBy(e.ctx, 1); err != nil {
		t.Fatal(err)
	}
	gone("DeleteOwnedBy", it)

	it = mk("ann", gf{})
	_, token, _ := strings.Cut(str(it, "deletion_url"), "#")
	if res, _ := e.doJSON("POST", "/api/v1/delete", map[string]any{"token": token}); res.StatusCode != 200 {
		t.Fatalf("token delete: %d", res.StatusCode)
	}
	gone("token", it)
}

func TestGistSearchAndPatch(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.gist("ann", gf{"files": threeFiles()})
	e.put("ann", "other.txt", "nothing here", "")
	search := func(q string) int64 {
		t.Helper()
		_, list := e.doJSON("GET", "/api/v1/items?q="+url.QueryEscape(q), nil, as("ann"))
		return num(list, "total")
	}
	for _, q := range []string{"file2.py", "notes.md", "quokka", "main"} {
		if n := search(q); n != 1 {
			t.Errorf("search %q: %d", q, n)
		}
	}
	res, got := e.doJSON("PATCH", itemPath(num(it, "id")), map[string]any{"title": "Renamed", "visibility": "private"}, as("ann"))
	if res.StatusCode != 200 || str(got, "name") != "Renamed" || str(got, "visibility") != "private" || len(files(got)) != 3 {
		t.Fatalf("patch: %d %v", res.StatusCode, got)
	}
	for _, q := range []string{"Renamed", "file2.py", "quokka"} {
		if n := search(q); n != 1 {
			t.Errorf("search %q after patch: %d", q, n)
		}
	}
	// File contents are not editable.
	if res, _ := e.doJSON("PATCH", itemPath(num(it, "id")), map[string]any{"files": []gf{{"content": "x"}}}, as("ann")); res.StatusCode != 400 {
		t.Errorf("patch files: %d", res.StatusCode)
	}
	counts, err := e.svc.TypeCounts(e.ctx, 1, items.Filter{})
	if err != nil || counts["gist"] != 1 || counts["text"] != 1 {
		t.Errorf("type counts: %v %v", counts, err)
	}
}
