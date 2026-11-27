package items_test

import (
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestRawHeadersPerRiskyType(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	cases := []struct {
		name, content, stored, served string
		sandbox                       bool
	}{
		{"page.html", "<!DOCTYPE html><html><body><script>alert(document.cookie)</script></body></html>", "text/html", "text/plain; charset=utf-8", true},
		{"page.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></html>`, "text/html", "text/plain; charset=utf-8", true},
		{"app.js", "navigator.serviceWorker.register('/x.js')", "text/javascript", "text/plain; charset=utf-8", true},
		{"mod.mjs", "export default 1", "text/javascript", "text/plain; charset=utf-8", true},
		{"style.css", "body{background:url(//evil)}", "text/css", "text/plain; charset=utf-8", true},
		{"data.json", `{"a":1}`, "application/json", "text/plain; charset=utf-8", true},
		{"feed.xml", `<?xml version="1.0"?><x/>`, "text/xml", "text/plain; charset=utf-8", true},
		{"logo.svg", `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script></svg>`, "image/svg+xml", "image/svg+xml", true},
		{"doc.pdf", "%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF", "application/pdf", "application/pdf", false},
		{"pic.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "image/png", "image/png", true},
		{"tool.wasm", "\x00asm\x01\x00\x00\x00", "application/wasm", "text/plain; charset=utf-8", true},
		{"a.exe", "MZ\x90\x00\x03\x00\x00\x00", "application/octet-stream", "application/octet-stream", true},
	}
	for _, c := range cases {
		it := e.put("ann", c.name, c.content, "")
		if str(it, "type") != c.stored {
			t.Errorf("%s: stored %s want %s", c.name, str(it, "type"), c.stored)
		}
		slug := str(it, "slug")
		for _, p := range []string{"/" + slug + "/raw", path(str(it, "raw_url")), "/" + slug + ".html", "/" + slug + "/download"} {
			res, body := e.do("GET", p, nil)
			h := res.Header
			if res.StatusCode != 200 || body != c.content {
				t.Errorf("%s %s: %d", c.name, p, res.StatusCode)
				continue
			}
			if got := h.Get("Content-Type"); got != c.served {
				t.Errorf("%s %s: Content-Type %q want %q", c.name, p, got, c.served)
			}
			csp := h.Get("Content-Security-Policy")
			if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") {
				t.Errorf("%s %s: CSP %q", c.name, p, csp)
			}
			dl := strings.HasSuffix(p, "/download")
			if sb := strings.HasPrefix(csp, "sandbox"); sb != (c.sandbox || dl) {
				t.Errorf("%s %s: sandbox=%v in %q", c.name, p, sb, csp)
			}
			if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Robots-Tag") != "noindex" {
				t.Errorf("%s: missing nosniff/noindex: %v", c.name, h)
			}
			disp := h.Get("Content-Disposition")
			want := "inline; "
			if dl {
				want = "attachment; "
			}
			if !strings.HasPrefix(disp, want) || !strings.Contains(disp, "filename*=UTF-8''"+url.PathEscape(c.name)) {
				t.Errorf("%s %s: disposition %q", c.name, p, disp)
			}
			if h.Get("ETag") != `"`+sha(c.content)+`"` {
				t.Errorf("%s: ETag %q", c.name, h.Get("ETag"))
			}
		}
	}
	// PDFs: no iframe without a raw origin; Open (new tab) and Download instead.
	pdf := e.put("ann", "spec.pdf", "%PDF-1.4\n", "")
	_, page := e.do("GET", "/"+str(pdf, "slug"), nil)
	if strings.Contains(page, "<iframe") || !strings.Contains(page, `target="_blank"`) || !strings.Contains(page, "Download") {
		t.Error("pdf share page without raw origin")
	}
	// Range requests and conditional GETs.
	img := e.put("ann", "r.bin", "0123456789", "")
	res, body := e.do("GET", "/"+str(img, "slug")+"/raw", nil, header("Range", "bytes=2-4"))
	if res.StatusCode != 206 || body != "234" {
		t.Errorf("range: %d %q", res.StatusCode, body)
	}
	if res, _ := e.do("GET", "/"+str(img, "slug")+"/raw", nil, header("If-None-Match", `"`+sha("0123456789")+`"`)); res.StatusCode != 304 {
		t.Errorf("If-None-Match: %d", res.StatusCode)
	}
	// Thumb and preview exist only once the media package sets them.
	for _, sub := range []string{"/thumb", "/preview"} {
		if res, _ := e.do("GET", "/"+str(img, "slug")+sub, nil); res.StatusCode != 404 {
			t.Errorf("%s without a key: %d", sub, res.StatusCode)
		}
	}
}

func TestDispatch(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.put("ann", "a.txt", "hello", "slug=Hello_World-1")
	cases := map[string]int{
		"/Hello_World-1":          200,
		"/hello_world-1":          200,
		"/HELLO_WORLD-1.txt":      200,
		"/Hello_World-1.anything": 200,
		"/Hello_World-1/raw":      200,
		"/Hello_World-1/download": 200,
		"/Hello_World-1/thumb":    404,
		"/Hello_World-1/preview":  404,
		"/Hello_World-1/unlock":   404,
		"/Hello_World-1/raw/x":    404,
		"/Hello_World-1/":         404,
		"/Hello_World-1.tar.gz":   404,
		"/Hello_World-1.":         404,
		"/Hello_World-2":          404,
		"/s/raw":                  200, // the secrets reveal page (same static page for any id), never the slug dispatcher
		"/S/raw":                  404,
		"/api":                    404,
		"/API/raw":                404,
		"/login/raw":              404,
		"/robots":                 404,
		"/x/raw":                  404,
		"/.well-known/x":          404,
		"/%2e%2e/etc":             404,
	}
	for p, want := range cases {
		if res, _ := e.do("GET", p, nil); res.StatusCode != want {
			t.Errorf("GET %s: %d want %d", p, res.StatusCode, want)
		}
	}
	if res, _ := e.do("HEAD", "/"+str(it, "slug")+"/raw", nil); res.StatusCode != 200 || res.Header.Get("Content-Length") != "5" {
		t.Errorf("HEAD raw: %d %v", res.StatusCode, res.Header)
	}
	if res, _ := e.do("POST", "/"+str(it, "slug"), nil); res.StatusCode != 404 && res.StatusCode != 405 {
		t.Errorf("POST share page: %d", res.StatusCode)
	}
	// Literal routes still win over the dispatcher.
	if res, _ := e.do("GET", "/healthz", nil); res.StatusCode != 200 {
		t.Errorf("healthz: %d", res.StatusCode)
	}
}

func TestPrivateExpiredAndPasswordChecks(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	priv := str(e.put("ann", "p.txt", "private", "visibility=private"), "slug")
	exp := str(e.put("ann", "e.txt", "expired", "expires=1h"), "slug")
	e.exec(`UPDATE items SET expires_at = ? WHERE slug = ?`, db.Millis(time.Now().Add(-time.Second)), exp)
	pw := str(e.put("ann", "s.txt", "secret", "", header("X-Snap-Password", "hunter2-long")), "slug")

	type who struct {
		name string
		opt  reqOpt
	}
	anon := who{"anonymous", func(*http.Request) {}}
	other := who{"bob", as("bob")}
	owner := who{"ann", as("ann")}
	check := func(slug string, w who, wantPage, wantRaw int) {
		t.Helper()
		if res, _ := e.do("GET", "/"+slug, nil, w.opt); res.StatusCode != wantPage {
			t.Errorf("%s page as %s: %d want %d", slug, w.name, res.StatusCode, wantPage)
		}
		for _, sub := range []string{"/raw", "/download", ".txt"} {
			if res, _ := e.do("GET", "/"+slug+sub, nil, w.opt, header("Sec-Fetch-Dest", "empty")); res.StatusCode != wantRaw {
				t.Errorf("%s%s as %s: %d want %d", slug, sub, w.name, res.StatusCode, wantRaw)
			}
		}
	}
	check(priv, anon, 404, 404)
	check(priv, other, 404, 404)
	check(priv, owner, 200, 200)
	check(exp, anon, 404, 404)
	check(exp, owner, 404, 404) // expiry applies to the owner too, before the sweeper runs
	check(pw, anon, 200, 403)   // 200 is the unlock form
	check(pw, other, 200, 403)
	check(pw, owner, 200, 200)
	if _, page := e.do("GET", "/"+pw, nil); !strings.Contains(page, "Password required") || strings.Contains(page, "s.txt") {
		t.Error("unlock page must ask for the password and not name the item")
	}
	// Opening a protected raw link in a tab goes to the unlock page.
	if res, _ := e.do("GET", "/"+pw+"/raw", nil, header("Sec-Fetch-Dest", "document")); res.StatusCode != 303 || res.Header.Get("Location") != "/"+pw {
		t.Errorf("raw navigation: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// Deleting and API access of expired items.
	res, body := e.doJSON("GET", "/api/v1/items", nil, as("ann"))
	if res.StatusCode != 200 || num(body, "total") != 3 {
		t.Errorf("list: %v", body)
	}
}

func unlockCookie(t *testing.T, res *resp) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == "__Host-snap_unlock" {
			return c
		}
	}
	return nil
}

func TestUnlockCookie(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	a := str(e.put("ann", "a.txt", "aaa", "", header("X-Snap-Password", "pw-a-long")), "slug")
	b := str(e.put("ann", "b.txt", "bbb", "", header("X-Snap-Password", "pw-b-long")), "slug")
	form := func(pw, next string) *strings.Reader {
		return strings.NewReader(url.Values{"password": {pw}, "next": {next}}.Encode())
	}
	ct := header("Content-Type", "application/x-www-form-urlencoded")
	if res, page := e.do("POST", "/unlock/"+a, form("wrong", ""), ct); res.StatusCode != 403 || !strings.Contains(page, "Wrong password.") || unlockCookie(t, res) != nil {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	res, _ := e.do("POST", "/unlock/"+a, form("pw-a-long", "/"+a+"/raw"), ct)
	c := unlockCookie(t, res)
	if res.StatusCode != 303 || res.Header.Get("Location") != "/"+a+"/raw" || c == nil {
		t.Fatalf("unlock: %d %s %v", res.StatusCode, res.Header.Get("Location"), c)
	}
	if !c.HttpOnly || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode || c.MaxAge <= 0 {
		t.Errorf("cookie attributes: %+v", c)
	}
	if res, body := e.do("GET", "/"+a+"/raw", nil, cookie(c)); res.StatusCode != 200 || body != "aaa" {
		t.Errorf("raw after unlock: %d", res.StatusCode)
	}
	if res, page := e.do("GET", "/"+a, nil, cookie(c)); res.StatusCode != 200 || strings.Contains(page, "Password required") {
		t.Errorf("page after unlock: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/"+b+"/raw", nil, cookie(c)); res.StatusCode != 403 {
		t.Errorf("unlocking a must not unlock b: %d", res.StatusCode)
	}
	// An open redirect through next is refused.
	if res, _ := e.do("POST", "/unlock/"+b, form("pw-b-long", "https://evil.example/"), ct); res.Header.Get("Location") != "/"+b {
		t.Errorf("next: %s", res.Header.Get("Location"))
	}
	// Tampering with the cookie invalidates it.
	val := []byte(c.Value)
	for _, i := range []int{0, len(val) / 2, len(val) - 1} {
		bad := append([]byte(nil), val...)
		bad[i] ^= 0x01
		if bad[i] == '.' || val[i] == '.' {
			continue
		}
		forged := &http.Cookie{Name: c.Name, Value: string(bad)}
		if res, _ := e.do("GET", "/"+a+"/raw", nil, cookie(forged)); res.StatusCode != 403 {
			t.Errorf("tampered cookie (byte %d): %d", i, res.StatusCode)
		}
	}
	// A password change invalidates earlier unlocks.
	it := e.item(a)
	if res, _ := e.doJSON("PATCH", itemPath(it.ID), map[string]any{"password": "new-password"}, as("ann")); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := e.do("GET", "/"+a+"/raw", nil, cookie(c)); res.StatusCode != 403 {
		t.Errorf("unlock survived a password change: %d", res.StatusCode)
	}
	// Clearing the password opens it to anyone with the link.
	if res, _ := e.doJSON("PATCH", itemPath(it.ID), map[string]any{"password": nil}, as("ann")); res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if res, _ := e.do("GET", "/"+a+"/raw", nil); res.StatusCode != 200 {
		t.Errorf("after clearing the password: %d", res.StatusCode)
	}
	// Unlock attempts are rate limited per IP and item.
	limited := false
	for range 8 {
		if res, _ := e.do("POST", "/unlock/"+b, form("nope", ""), ct); res.StatusCode == 429 {
			limited = true
		}
	}
	if !limited {
		t.Error("unlock attempts not rate limited")
	}
}

func TestLinks(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	res, it := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://Example.com:443/a?b=1", "slug": "ex"}, as("ann"))
	if res.StatusCode != 201 || str(it, "target_url") != "https://example.com/a?b=1" || str(it, "url") != e.s.URL+"/ex" {
		t.Fatalf("create: %d %v", res.StatusCode, it)
	}
	if _, ok := it["raw_url"]; ok {
		t.Error("links have no raw URL")
	}
	for range 3 {
		res, _ := e.do("GET", "/ex", nil)
		if res.StatusCode != 302 || res.Header.Get("Location") != "https://example.com/a?b=1" || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("redirect: %d %v", res.StatusCode, res.Header)
		}
	}
	// The owner's own clicks and bots do not count.
	_, _ = e.do("GET", "/ex", nil, as("ann"))
	_, _ = e.do("GET", "/ex", nil, header("User-Agent", "Mozilla/5.0 (compatible; Discordbot/2.0)"))
	if res, _ := e.do("GET", "/ex/raw", nil); res.StatusCode != 404 {
		t.Errorf("link raw: %d", res.StatusCode)
	}
	var views int64
	_ = e.s.App.DB.R.QueryRow(`SELECT views FROM items WHERE slug = 'ex'`).Scan(&views)
	if views != 0 {
		t.Errorf("views written per request: %d", views)
	}
	if err := e.svc.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	link := e.item("ex")
	if link.Views != 3 || link.FirstOpened == nil || link.OpenedCount != 3 {
		t.Errorf("after flush: views %d opened %v", link.Views, link.FirstOpened)
	}
	bad := []string{"javascript:alert(1)", "JAVASCRIPT:alert(1)", " javascript:alert(1)", "data:text/html;base64,PHNjcmlwdD4=",
		"ftp://example.com/", "//evil.example/", "/local", "http://", "https://user:pass@example.com/", "file:///etc/passwd",
		"https://example.com/" + strings.Repeat("x", 2100)}
	for _, u := range bad {
		if res, body := e.doJSON("POST", "/api/v1/links", map[string]any{"url": u}, as("ann")); res.StatusCode != 400 {
			t.Errorf("%.40q accepted: %d %v", u, res.StatusCode, body)
		}
	}
	// curl -d url=... with a bare URL back.
	res, body := e.do("POST", "/api/v1/links?format=url", strings.NewReader("url="+url.QueryEscape("http://example.org/x")), as("ann"),
		header("Content-Type", "application/x-www-form-urlencoded"))
	if res.StatusCode != 201 || !strings.HasPrefix(body, e.s.URL+"/") {
		t.Errorf("form link: %d %q", res.StatusCode, body)
	}
	// Password-protected links show the unlock page first.
	res, pl := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://example.net/", "password": "pw-123456"}, as("ann"))
	if res.StatusCode != 201 {
		t.Fatal(res.StatusCode)
	}
	slug := str(pl, "slug")
	if res, page := e.do("GET", "/"+slug, nil); res.StatusCode != 200 || !strings.Contains(page, "Password required") {
		t.Errorf("protected link: %d", res.StatusCode)
	}
	res, _ = e.do("POST", "/unlock/"+slug, strings.NewReader("password=pw-123456"), header("Content-Type", "application/x-www-form-urlencoded"))
	if res, _ := e.do("GET", "/"+slug, nil, cookie(unlockCookie(t, res))); res.StatusCode != 302 || res.Header.Get("Location") != "https://example.net/" {
		t.Errorf("unlocked link: %d", res.StatusCode)
	}
}

func TestPastes(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	res, it := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "package main\n\nfunc main() {}\n", "lang": "go", "title": "Example"}, as("ann"))
	if res.StatusCode != 201 || str(it, "kind") != "paste" || str(it, "name") != "Example" {
		t.Fatalf("json paste: %d %v", res.StatusCode, it)
	}
	_, page := e.do("GET", path(str(it, "url")), nil)
	if !strings.Contains(page, `<span class="tok-k">package</span>`) || !strings.Contains(page, "pb-code__gutter") {
		t.Error("paste not highlighted")
	}
	if _, raw := e.do("GET", path(str(it, "raw_url")), nil); raw != "package main\n\nfunc main() {}\n" {
		t.Errorf("raw %q", raw)
	}
	// curl --data-binary @file -H 'Content-Type: text/plain'
	res, body := e.do("POST", "/api/v1/pastes?name=script.py&format=url", strings.NewReader("def f():\n    return 1\n"), as("ann"), header("Content-Type", "text/plain"))
	if res.StatusCode != 201 || !strings.HasPrefix(body, e.s.URL) {
		t.Fatalf("text paste: %d %s", res.StatusCode, body)
	}
	_, page = e.do("GET", path(strings.TrimSpace(body)), nil)
	if !strings.Contains(page, `<span class="tok-k">def</span>`) {
		t.Error("language from name not used")
	}
	// No language and no name: plain text, no guessing.
	_, plain := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "def f(): pass"}, as("ann"))
	if str(plain, "lang") != "plaintext" {
		t.Errorf("lang %q", str(plain, "lang"))
	}
	// Markdown: Rendered | Source.
	_, md := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "# Hello\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1))", "name": "notes.md"}, as("ann"))
	_, page = e.do("GET", path(str(md, "url")), nil)
	if !strings.Contains(page, `id="user-content-hello"`) || !strings.Contains(page, "data-md-toggle") || strings.Contains(page, "<script>alert") || strings.Contains(page, `href="javascript:`) {
		t.Error("markdown paste")
	}
	if res, _ := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "  \n"}, as("ann")); res.StatusCode != 400 {
		t.Errorf("empty paste: %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/api/v1/pastes", strings.NewReader("content=hi+there&lang=bash"), as("ann"), header("Content-Type", "application/x-www-form-urlencoded")); res.StatusCode != 201 {
		t.Errorf("form paste: %d", res.StatusCode)
	}
	e.setting("uploads.max_bytes", 1000)
	if res, _ := e.do("POST", "/api/v1/pastes", strings.NewReader(strings.Repeat("x", 2000)), as("ann"), header("Content-Type", "text/plain")); res.StatusCode != 413 {
		t.Errorf("large paste: %d", res.StatusCode)
	}
	// Large text files are shown with a raw link instead of highlighting.
	e.setting("uploads.max_bytes", 2<<20)
	big := e.put("ann", "big.log", strings.Repeat("line\n", 120_000), "")
	if _, page := e.do("GET", path(str(big, "url")), nil); !strings.Contains(page, "Too large to preview") {
		t.Error("large text page")
	}
}

func TestDeletionByToken(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.put("ann", "del.txt", "bye", "")
	u := str(it, "deletion_url")
	pre, token, ok := strings.Cut(u, "#")
	if !ok || pre != e.s.URL+"/x" || len(token) < 40 {
		t.Fatalf("deletion url %q", u)
	}
	if res, page := e.do("GET", "/x", nil); res.StatusCode != 200 || !strings.Contains(page, "token-x.js") || res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("/x page: %d", res.StatusCode)
	}
	res, body := e.doJSON("POST", "/api/v1/delete/lookup", map[string]any{"token": token})
	if res.StatusCode != 200 || str(body, "name") != "del.txt" {
		t.Fatalf("lookup: %d %v", res.StatusCode, body)
	}
	if res, _ := e.doJSON("POST", "/api/v1/delete", map[string]any{"token": token + "x"}); res.StatusCode != 404 {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	e.exec(`INSERT INTO item_stats_daily (item_id, day, views) VALUES (?, 0, 3)`, num(it, "id"))
	if res, _ := e.doJSON("POST", "/api/v1/delete", map[string]any{"token": token}); res.StatusCode != 200 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	var statRows int
	_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM item_stats_daily WHERE item_id = ?`, num(it, "id")).Scan(&statRows)
	if statRows != 0 {
		t.Error("stats rows outlive the item")
	}
	if res, _ := e.do("GET", "/"+str(it, "slug"), nil); res.StatusCode != 404 {
		t.Errorf("after delete: %d", res.StatusCode)
	}
	if res, _ := e.doJSON("POST", "/api/v1/delete/lookup", map[string]any{"token": token}); res.StatusCode != 404 {
		t.Errorf("lookup after delete: %d", res.StatusCode)
	}
	if used, _ := e.usage(1); used != 0 {
		t.Errorf("used %d", used)
	}
}

func TestSweeper(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	del := e.put("ann", "d.txt", "delete me", "expires=1h")
	hide := e.put("ann", "h.txt", "hide me", "expires=1h&expiry_action=private")
	keep := e.put("ann", "k.txt", "keep", "expires=7d")
	past := db.Millis(time.Now().Add(-time.Minute))
	e.exec(`UPDATE items SET expires_at = ? WHERE slug IN (?, ?)`, past, str(del, "slug"), str(hide, "slug"))
	delItem := e.item(str(del, "slug"))
	blobPath, _ := e.s.App.Files.Path(delItem.BlobKey)
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetBySlug(e.ctx, str(del, "slug")); err == nil {
		t.Error("expired item not deleted")
	}
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Errorf("blob kept: %v", err)
	}
	h := e.item(str(hide, "slug"))
	if h.Visibility != "private" || h.ExpiresAt != nil {
		t.Errorf("private action: %s %v", h.Visibility, h.ExpiresAt)
	}
	if res, _ := e.do("GET", "/"+h.Slug, nil); res.StatusCode != 404 {
		t.Errorf("hidden item public: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/"+h.Slug, nil, as("ann")); res.StatusCode != 200 {
		t.Errorf("hidden item for owner: %d", res.StatusCode)
	}
	if e.item(str(keep, "slug")).ExpiresAt == nil {
		t.Error("unexpired item touched")
	}
	if used, _ := e.usage(1); used != e.charged(1) || used <= int64(len("hide me")+len("keep")) {
		t.Errorf("used %d", used)
	}
}

func TestIDOR(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	it := e.put("ann", "a.txt", "ann's", "visibility=private")
	id := num(it, "id")
	_, up := e.initUpload("ann", "u.txt", 10, nil)
	upID := str(up, "id")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", itemPath(id), nil},
		{"PATCH", itemPath(id), map[string]any{"title": "pwned"}},
		{"PATCH", itemPath(id), map[string]any{"visibility": "unlisted"}},
		{"DELETE", itemPath(id), nil},
		{"GET", "/api/v1/uploads/" + upID, nil},
		{"HEAD", "/api/v1/uploads/" + upID, nil},
		{"POST", "/api/v1/uploads/" + upID + "/complete", nil},
		{"DELETE", "/api/v1/uploads/" + upID, nil},
	} {
		for _, opt := range []reqOpt{as("bob"), bearer("bob-key")} {
			res, _ := e.doJSON(c.method, c.path, c.body, opt)
			if res.StatusCode != 404 {
				t.Errorf("bob %s %s: %d", c.method, c.path, res.StatusCode)
			}
		}
	}
	if res, _ := e.putPart("bob", upID, 0, "x"); res.StatusCode != 404 {
		t.Errorf("bob PUT part: %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/dashboard/items/"+itemPath(id)[len("/api/v1/items/"):]+"/delete", nil, as("bob")); res.StatusCode != 404 {
		t.Errorf("bob dashboard delete: %d", res.StatusCode)
	}
	for _, p := range []string{"/" + str(it, "slug"), "/" + str(it, "slug") + "/raw", "/" + str(it, "slug") + "/download"} {
		if res, _ := e.do("GET", p, nil, as("bob")); res.StatusCode != 404 {
			t.Errorf("bob GET %s: %d", p, res.StatusCode)
		}
	}
	_, list := e.doJSON("GET", "/api/v1/items", nil, as("bob"))
	if num(list, "total") != 0 {
		t.Errorf("bob sees %v", list)
	}
	// Ann's item and upload are untouched.
	if got := e.item(str(it, "slug")); got.Title != "" || got.Visibility != "private" {
		t.Errorf("item changed: %+v", got)
	}
	if res, _ := e.do("HEAD", "/api/v1/uploads/"+upID, nil, as("ann")); res.StatusCode != 200 {
		t.Errorf("ann's upload: %d", res.StatusCode)
	}
	if res, _ := e.doJSON("GET", itemPath(id), nil, as("ann")); res.StatusCode != 200 {
		t.Errorf("ann's item: %d", res.StatusCode)
	}
}

func TestVerificationAndScopes(t *testing.T) {
	e := newEnv(t)
	e.user("new", 1, false)
	e.user("ro", 2, true, auth.ScopeItemsRead)
	e.user("rw", 3, true, auth.ScopeItemsWrite)
	creates := []struct {
		method, path, ct, body string
	}{
		{"POST", "/api/v1/uploads", "application/json", `{"name":"a.txt","size":1}`},
		{"PUT", "/api/v1/upload/a.txt", "", "a"},
		{"POST", "/api/v1/pastes", "text/plain", "a"},
		{"POST", "/api/v1/links", "application/json", `{"url":"https://example.com"}`},
	}
	for _, c := range creates {
		res, body := e.do(c.method, c.path, strings.NewReader(c.body), as("new"), header("Content-Type", c.ct))
		if res.StatusCode != 403 || !strings.Contains(body, "Verify your email address first.") {
			t.Errorf("unverified %s: %d %s", c.path, res.StatusCode, body)
		}
		if res, _ := e.do(c.method, c.path, strings.NewReader(c.body), bearer("ro-key"), header("Content-Type", c.ct)); res.StatusCode != 403 {
			t.Errorf("read-only key %s: %d", c.path, res.StatusCode)
		}
		if res, _ := e.do(c.method, c.path, strings.NewReader(c.body), header("Content-Type", c.ct)); res.StatusCode != 401 {
			t.Errorf("anonymous %s: %d", c.path, res.StatusCode)
		}
		if res, b := e.do(c.method, c.path, strings.NewReader(c.body), bearer("rw-key"), header("Content-Type", c.ct)); res.StatusCode != 201 {
			t.Errorf("write key %s: %d %s", c.path, res.StatusCode, b)
		}
	}
	_, list := e.doJSON("GET", "/api/v1/items", nil, bearer("rw-key"))
	if list["error"] == nil {
		t.Error("write-only key listed items")
	}
	_, mine := e.doJSON("GET", "/api/v1/items", nil, as("rw"))
	items, _ := mine["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("rw items: %v", mine)
	}
	id := num(items[0].(map[string]any), "id")
	if res, _ := e.doJSON("DELETE", itemPath(id), nil, bearer("rw-key")); res.StatusCode != 403 {
		t.Errorf("delete without items:delete: %d", res.StatusCode)
	}
	// Cookie-authenticated API writes need same-origin.
	req := e.s.Request(t, "POST", "/api/v1/pastes", strings.NewReader("x"))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Content-Type", "text/plain")
	as("rw")(req)
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("cross-site cookie POST: %d", res.StatusCode)
	}
}

func TestPatchAndList(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	a := e.put("ann", "a.txt", "a", "")
	b := e.put("ann", "b.txt", "b", "slug=taken")
	id := num(a, "id")
	res, got := e.doJSON("PATCH", itemPath(id), map[string]any{"title": "Renamed", "slug": "fresh", "expires": "1d", "visibility": "private"}, as("ann"))
	if res.StatusCode != 200 || str(got, "slug") != "fresh" || str(got, "title") != "Renamed" || got["expires_at"] == nil || str(got, "visibility") != "private" {
		t.Fatalf("patch: %d %v", res.StatusCode, got)
	}
	if res, _ := e.do("GET", "/"+str(a, "slug"), nil, as("ann")); res.StatusCode != 404 {
		t.Errorf("old slug still works: %d", res.StatusCode)
	}
	for body, want := range map[string]int{
		`{"slug":"TAKEN"}`: 409, `{"slug":"admin"}`: 400, `{"slug":"a b"}`: 400, `{"expires":"soon"}`: 400,
		`{"visibility":"public"}`: 400, `{"password":5}`: 400, `{"unknown":1}`: 400,
	} {
		if res, _ := e.do("PATCH", itemPath(id), strings.NewReader(body), as("ann"), jsonBody()); res.StatusCode != want {
			t.Errorf("patch %s: %d want %d", body, res.StatusCode, want)
		}
	}
	if res, got := e.doJSON("PATCH", itemPath(id), map[string]any{"expires": "never"}, as("ann")); res.StatusCode != 200 || got["expires_at"] != nil {
		t.Errorf("expires never: %v", got)
	}
	_ = b
	for i := range 5 {
		e.put("ann", "n.txt", strings.Repeat("n", i+1), "")
	}
	_, page := e.doJSON("GET", "/api/v1/items?limit=3&offset=0", nil, as("ann"))
	list, _ := page["items"].([]any)
	if num(page, "total") != 7 || len(list) != 3 || num(list[0].(map[string]any), "size") != 5 {
		t.Errorf("list: %v", page)
	}
	if res, _ := e.doJSON("DELETE", itemPath(id), nil, as("ann")); res.StatusCode != 204 {
		t.Errorf("delete: %d", res.StatusCode)
	}
	if res, _ := e.doJSON("GET", itemPath(id), nil, as("ann")); res.StatusCode != 404 {
		t.Errorf("get deleted: %d", res.StatusCode)
	}
}

// A file's filename is what it downloads as. A rename is cleaned like an
// upload's name, passes the blocked extensions, keeps the stored type, and
// applies to files only.
func TestPatchFilename(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.setting("uploads.blocked_extensions", []string{"exe"})
	f := e.put("ann", "bundle.zip", "PK\x03\x04", "")
	id := num(f, "id")
	res, got := e.doJSON("PATCH", itemPath(id), map[string]any{"filename": " ../release v2.zip "}, as("ann"))
	if res.StatusCode != 200 || str(got, "filename") != "..release v2.zip" || str(got, "type") != str(f, "type") {
		t.Fatalf("rename: %d %v", res.StatusCode, got)
	}
	if res, _ := e.do("GET", "/"+str(f, "slug")+"/download", nil, as("ann")); res.StatusCode != 200 ||
		!strings.Contains(res.Header.Get("Content-Disposition"), `filename="..release v2.zip"`) {
		t.Errorf("download after rename: %d %q", res.StatusCode, res.Header.Get("Content-Disposition"))
	}
	if _, page := e.doJSON("GET", "/api/v1/items?q=release", nil, as("ann")); num(page, "total") != 1 {
		t.Errorf("search by new name: %v", page)
	}
	for body, want := range map[string]int{`{"filename":"  "}`: 400, `{"filename":"/"}`: 400, `{"filename":"setup.EXE"}`: 415} {
		if res, _ := e.do("PATCH", itemPath(id), strings.NewReader(body), as("ann"), jsonBody()); res.StatusCode != want {
			t.Errorf("patch %s: %d want %d", body, res.StatusCode, want)
		}
	}
	_, link := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://example.com/"}, as("ann"))
	if res, _ := e.doJSON("PATCH", itemPath(num(link, "id")), map[string]any{"filename": "x.txt"}, as("ann")); res.StatusCode != 400 {
		t.Errorf("rename a link: %d", res.StatusCode)
	}
}

// A rename is held to the same checks as every other edit: a key needs
// items:write and, without items:read, may rename only what it uploaded;
// another user's file is not found; and a new extension never changes how
// the file is served.
func TestPatchFilenameGuards(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	e.user("ro", 3, true, auth.ScopeItemsRead)
	e.user("wo", 4, true, auth.ScopeItemsWrite)
	rename := func(id int64, name string, opt reqOpt) int {
		res, _ := e.doJSON("PATCH", itemPath(id), map[string]any{"filename": name}, opt)
		return res.StatusCode
	}
	f := e.put("ann", "notes.txt", "hello", "")
	id := num(f, "id")
	if code := rename(id, "mine.txt", as("bob")); code != 404 {
		t.Errorf("bob renames ann's file: %d, want 404", code)
	}
	ro := num(e.put("ro", "a.txt", "a", ""), "id")
	if code := rename(ro, "b.txt", bearer("ro-key")); code != 403 {
		t.Errorf("read-only key renames: %d, want 403", code)
	}
	wo := num(e.put("wo", "a.txt", "a", ""), "id")
	if code := rename(wo, "b.txt", bearer("wo-key")); code != 404 {
		t.Errorf("write-only key renames a file it did not upload: %d, want 404", code)
	}
	if it := e.item(str(f, "slug")); it.Filename != "notes.txt" {
		t.Fatalf("filename changed by a refused rename: %q", it.Filename)
	}
	// Text renamed to .html stays text, at the new and the old raw URL.
	if code := rename(id, "notes.html", as("ann")); code != 200 {
		t.Fatalf("rename to .html: %d", code)
	}
	for _, p := range []string{"/" + str(f, "slug") + ".html", "/" + str(f, "slug") + ".txt", "/" + str(f, "slug") + "/raw"} {
		res, _ := e.do("GET", p, nil)
		if ct := res.Header.Get("Content-Type"); res.StatusCode != 200 || !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("GET %s after rename: %d %q, want text/plain", p, res.StatusCode, ct)
		}
	}
	// The cleaned name is what is compared: the same name with spaces is no change.
	before := e.item(str(f, "slug")).UpdatedAt
	if code := rename(id, "  notes.html ", as("ann")); code != 200 || !e.item(str(f, "slug")).UpdatedAt.Equal(before) {
		t.Errorf("same name with spaces: %d, updated %v -> %v", code, before, e.item(str(f, "slug")).UpdatedAt)
	}
	// A non-ASCII name has an ASCII fallback and the RFC 5987 form.
	if code := rename(id, "résumé.txt", as("ann")); code != 200 {
		t.Fatalf("rename résumé: %d", code)
	}
	res, _ := e.do("GET", "/"+str(f, "slug")+"/download", nil)
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="r_sum_.txt"`) || !strings.Contains(cd, `filename*=UTF-8''r%C3%A9sum%C3%A9.txt`) {
		t.Errorf("Content-Disposition: %q", cd)
	}
	// Albums are not files.
	_, al := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "A", "items": []int64{id}}, as("ann"))
	if res, _ := e.doJSON("PATCH", "/api/v1/albums/"+strconv.FormatInt(num(al, "id"), 10), map[string]any{"filename": "x.zip"}, as("ann")); res.StatusCode != 400 {
		t.Errorf("rename an album's filename: %d, want 400", res.StatusCode)
	}
}

func TestPages(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.put("ann", "photo.png", "\x89PNG\r\n\x1a\n", "")
	res, page := e.do("GET", "/", nil, as("ann"))
	if res.StatusCode != 200 || !strings.Contains(page, `class="pb-drop app-drop-tall"`) || !strings.Contains(page, `role="tablist"`) ||
		!strings.Contains(page, `data-dropset`) || !strings.Contains(page, `id="composer-options"`) || !strings.Contains(page, `name="naming" value="original"`) || !strings.Contains(page, "photo.png") || !strings.Contains(page, "upload.js") {
		t.Errorf("composer: %d", res.StatusCode)
	}
	if res, page := e.do("GET", "/"+str(it, "slug"), nil); res.StatusCode != 200 || !strings.Contains(page, "pb-media__frame--contain") ||
		!strings.Contains(page, `<img src="`+str(it, "raw_url")) || res.Header.Get("X-Robots-Tag") != "noindex" {
		t.Errorf("image share page: %d", res.StatusCode)
	}
	res, page = e.do("GET", "/dashboard", nil, as("ann"))
	if res.StatusCode != 200 || !strings.Contains(page, "photo.png") || !strings.Contains(page, "not opened") {
		t.Errorf("dashboard: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/dashboard", nil); res.StatusCode != 303 {
		t.Errorf("anonymous dashboard: %d", res.StatusCode)
	}
	// Opened badge once a visitor has looked.
	_, _ = e.do("GET", "/"+str(it, "slug"), nil)
	_ = e.svc.FlushCounters(e.ctx)
	if _, page := e.do("GET", "/dashboard", nil, as("ann")); !strings.Contains(page, `title="first opened `) {
		t.Error("opened badge")
	}
	res, _ = e.do("POST", "/dashboard/items/"+itemPath(num(it, "id"))[len("/api/v1/items/"):]+"/delete", strings.NewReader("page=1"), as("ann"),
		header("Content-Type", "application/x-www-form-urlencoded"))
	if res.StatusCode != 303 || res.Header.Get("Location") != "/dashboard?page=1" {
		t.Errorf("dashboard delete: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := e.do("GET", "/"+str(it, "slug"), nil); res.StatusCode != 404 {
		t.Errorf("after dashboard delete: %d", res.StatusCode)
	}

	// Defaults: stored in users.prefs without clobbering other keys.
	e.exec(`UPDATE users SET prefs = '{"notify":"first"}' WHERE id = 1`)
	if res, page := e.do("GET", "/settings/defaults", nil, as("ann")); res.StatusCode != 200 || !strings.Contains(page, "Copy direct link") {
		t.Errorf("defaults page: %d", res.StatusCode)
	}
	form := url.Values{"naming": {"words"}, "random_length": {"12"}, "visibility": {"private"}, "expiry": {"7d"}, "direct_link": {"1"}, "embed_color": {"#1f3fd6"}, "embed_title": {"Mine"}}
	res, _ = e.do("POST", "/settings/defaults", strings.NewReader(form.Encode()), as("ann"), header("Content-Type", "application/x-www-form-urlencoded"))
	if res.StatusCode != 303 {
		t.Fatalf("save defaults: %d", res.StatusCode)
	}
	var prefs string
	_ = e.s.App.DB.R.QueryRow(`SELECT prefs FROM users WHERE id = 1`).Scan(&prefs)
	if !strings.Contains(prefs, `"notify":"first"`) || !strings.Contains(prefs, `"naming":"words"`) || !strings.Contains(prefs, `"direct_link":true`) {
		t.Errorf("prefs %s", prefs)
	}
	n := e.put("ann", "after.png", "\x89PNG\r\n\x1a\n", "")
	if strings.Count(str(n, "slug"), "-") < 2 || str(n, "visibility") != "private" || n["expires_at"] == nil {
		t.Errorf("defaults not applied: %v", n)
	}
	// Direct link applies to unprotected files only.
	u := e.put("ann", "pub.png", "\x89PNG\r\n\x1a\n", "visibility=unlisted")
	if str(u, "url") != str(u, "raw_url") || str(u, "page_url") == str(u, "url") {
		t.Errorf("direct link: %v", u)
	}
	form.Set("random_length", "4")
	if res, _ := e.do("POST", "/settings/defaults", strings.NewReader(form.Encode()), as("ann"), header("Content-Type", "application/x-www-form-urlencoded")); res.StatusCode != 400 {
		t.Errorf("bad length: %d", res.StatusCode)
	}
	// A signed-in principal without a users row still gets a page.
	e.s.Auth.Add("ghost", auth.Principal{UserID: 99, EmailVerified: true})
	if res, _ := e.do("GET", "/", nil, as("ghost")); res.StatusCode != 200 {
		t.Errorf("composer without a user row: %d", res.StatusCode)
	}
}

func TestRawOrigin(t *testing.T) {
	e := newEnv(t, testutil.Env("RAW_URL", "https://raw.test"))
	e.user("ann", 1, true)
	pub := e.put("ann", "pub.txt", "public", "")
	priv := e.put("ann", "priv.txt", "private", "visibility=private")
	pdf := e.put("ann", "doc.pdf", "%PDF-1.4\n", "")
	if !strings.HasPrefix(str(pub, "raw_url"), "https://raw.test/") {
		t.Errorf("raw_url %s", str(pub, "raw_url"))
	}
	if !strings.HasPrefix(str(priv, "raw_url"), e.s.URL+"/") {
		t.Errorf("private raw_url must stay on the app origin: %s", str(priv, "raw_url"))
	}
	// App origin redirects raw requests to the raw origin.
	res, _ := e.do("GET", "/"+str(pub, "slug")+"/raw", nil)
	if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), "https://raw.test/"+str(pub, "slug")) {
		t.Errorf("app origin raw: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, body := e.do("GET", "/"+str(pub, "slug")+".txt", nil, host("raw.test")); res.StatusCode != 200 || body != "public" || !strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("raw origin: %d %q", res.StatusCode, body)
	}
	// Pages are never served on the raw origin.
	if res, _ := e.do("GET", "/"+str(pub, "slug"), nil, host("raw.test")); res.StatusCode != 302 || res.Header.Get("Location") != e.s.URL+"/"+str(pub, "slug") {
		t.Errorf("page on raw origin: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// Private: only with a signature, which the owner gets via the app origin.
	slug := str(priv, "slug")
	if res, _ := e.do("GET", "/"+slug+"/raw", nil, host("raw.test")); res.StatusCode != 404 {
		t.Errorf("unsigned private on raw origin: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/"+slug+"/raw", nil); res.StatusCode != 404 {
		t.Errorf("anonymous private on app origin: %d", res.StatusCode)
	}
	res, _ = e.do("GET", "/"+slug+"/raw", nil, as("ann"))
	loc := res.Header.Get("Location")
	if res.StatusCode != 302 || !strings.HasPrefix(loc, "https://raw.test/"+slug+".txt?e=") {
		t.Fatalf("owner redirect: %d %s", res.StatusCode, loc)
	}
	signed := path(loc)
	if res, body := e.do("GET", signed, nil, host("raw.test")); res.StatusCode != 200 || body != "private" {
		t.Errorf("signed raw: %d %q", res.StatusCode, body)
	}
	if res, _ := e.do("GET", signed[:len(signed)-3]+"abc", nil, host("raw.test")); res.StatusCode != 404 {
		t.Errorf("tampered signature: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/"+str(pub, "slug")+"/raw?"+strings.SplitN(signed, "?", 2)[1], nil, host("raw.test")); res.StatusCode != 200 {
		t.Errorf("public item ignores signatures: %d", res.StatusCode)
	}
	// The owner's share page embeds signed raw URLs.
	if _, page := e.do("GET", "/"+slug, nil, as("ann")); !strings.Contains(page, "https://raw.test/"+slug+".txt?e=") {
		t.Error("share page without a signed raw URL")
	}
	// PDFs are framed from the raw origin, without the sandbox.
	if _, page := e.do("GET", "/"+str(pdf, "slug"), nil); !strings.Contains(page, `<iframe src="https://raw.test/`) {
		t.Error("pdf iframe with raw origin")
	}
	res, _ = e.do("GET", "/"+str(pdf, "slug")+".pdf", nil, host("raw.test"))
	csp := res.Header.Get("Content-Security-Policy")
	if strings.Contains(csp, "sandbox") || !strings.Contains(csp, "frame-ancestors "+e.s.URL) || res.Header.Get("X-Frame-Options") != "" {
		t.Errorf("pdf on raw origin: %s / XFO %q", csp, res.Header.Get("X-Frame-Options"))
	}
	if res, _ := e.do("GET", "/"+str(pdf, "slug")+".pdf", nil); res.StatusCode != 302 {
		t.Errorf("pdf on app origin: %d", res.StatusCode)
	}
}

// The sweep reports each expiry once through OnExpired (here the app's
// notify inbox): the deleted item as an account row, the hidden one as an
// item row. ExpiringWithin lists what expires in the window.
func TestSweeperReportsExpiries(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	del := e.put("ann", "d.txt", "delete me", "expires=1h")
	hide := e.put("ann", "h.txt", "hide me", "expires=1h&expiry_action=private")
	e.put("ann", "k.txt", "keep", "expires=7d")
	soon, err := e.svc.ExpiringWithin(e.ctx, 24*time.Hour)
	if err != nil || len(soon) != 2 {
		t.Fatalf("expiring within a day: %d %v", len(soon), err)
	}
	past := db.Millis(time.Now().Add(-time.Minute))
	e.exec(`UPDATE items SET expires_at = ? WHERE slug IN (?, ?)`, past, str(del, "slug"), str(hide, "slug"))
	for range 2 {
		if err := e.svc.Sweep(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := e.s.App.DB.R.Query(`SELECT subject_type, title, body FROM inbox WHERE user_id = 1 AND kind = 'item_expired' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var typ, title, body string
		if err := rows.Scan(&typ, &title, &body); err != nil {
			t.Fatal(err)
		}
		got = append(got, typ+" · "+title+" · "+body)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "\n") != "account · d.txt expired · deleted\nitem · h.txt expired · now private" {
		t.Fatalf("inbox:\n%s", strings.Join(got, "\n"))
	}
}

// An upload that takes storage past 80% of the quota lands in the owner's
// inbox (onItemCreated → notify), once.
func TestUploadReportsQuota(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.exec(`UPDATE users SET quota_bytes = 20 WHERE id = 1`)
	e.put("ann", "a.bin", "0123456789abcdef", "")
	e.put("ann", "b.bin", "x", "")
	var n int
	var title string
	if err := e.s.App.DB.R.QueryRow(`SELECT count(*), coalesce(max(title), '') FROM inbox WHERE user_id = 1 AND kind = 'quota'`).Scan(&n, &title); err != nil {
		t.Fatal(err)
	}
	if n != 1 || title != "Storage 80% used" {
		t.Fatalf("quota rows: %d %q", n, title)
	}
}

// The first-run welcome is on the composer only while the user has no items.
func TestComposerWelcome(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	res, page := e.do("GET", "/", nil, as("ann"))
	if res.StatusCode != 200 || !strings.Contains(page, `data-welcome`) || !strings.Contains(page, "js/welcome.js") ||
		!strings.Contains(page, `href="/settings/integrations"`) || !strings.Contains(page, "keyboard shortcuts") {
		t.Errorf("no welcome for a user without items: %d", res.StatusCode)
	}
	e.put("ann", "photo.png", "\x89PNG\r\n\x1a\n", "")
	if _, page = e.do("GET", "/", nil, as("ann")); strings.Contains(page, `data-welcome`) || strings.Contains(page, "js/welcome.js") {
		t.Error("welcome shown to a user with items")
	}
}
