package items_test

// Red-team checks that HELD (these pass because the code is correct): every
// serving path refuses private, password-protected and expired items to
// strangers without leaking validators, signed raw URLs are bound to the
// item and its protection, and risky types stay sandboxed in every mode.

import (
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestRedteamHoldsServingAccess(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	priv := e.put("ann", "p.txt", "private", "visibility=private")
	pw := e.put("ann", "w.txt", "locked", "", header("X-Snap-Password", "pw-123456"))
	exp := e.put("ann", "e.txt", "expired", "")
	e.exec(`UPDATE items SET expires_at = 1 WHERE id = ?`, num(exp, "id"))
	// Pretend the media package made derived files for all three.
	for _, it := range []map[string]any{priv, pw, exp} {
		e.exec(`UPDATE items SET thumb_key = blob_key, preview_key = blob_key WHERE id = ?`, num(it, "id"))
	}
	modes := []string{"", "/raw", ".txt", ".html", "/download", "/thumb", "/preview"}
	reqs := []struct {
		name string
		opts []reqOpt
	}{
		{"anon", nil},
		{"bob", []reqOpt{as("bob")}},
		{"bob-key", []reqOpt{bearer("bob-key")}},
		{"range", []reqOpt{header("Range", "bytes=0-1")}},
		{"inm", []reqOpt{header("If-None-Match", `"`+sha("locked")+`"`)}},
	}
	for _, it := range []map[string]any{priv, pw, exp} {
		for _, m := range modes {
			for _, rq := range reqs {
				for _, method := range []string{"GET", "HEAD"} {
					p := "/" + str(it, "slug") + m
					res, body := e.do(method, p, nil, rq.opts...)
					if res.StatusCode == 200 && m == "" && it["password"] == true && (method == "HEAD" || strings.Contains(body, "Password required")) {
						continue // the unlock page
					}
					if res.StatusCode == 200 || res.StatusCode == 206 || res.StatusCode == 304 {
						t.Errorf("%s %s as %s: %d", method, p, rq.name, res.StatusCode)
					}
					if res.Header.Get("ETag") != "" || strings.Contains(body, "locked") || strings.Contains(body, "private") && m != "" {
						t.Errorf("%s %s as %s: leaks ETag or content", method, p, rq.name)
					}
				}
			}
		}
	}
}

func TestRedteamHoldsSignedRawURLs(t *testing.T) {
	e := newEnv(t, testutil.Env("RAW_URL", "https://raw.test"))
	e.user("ann", 1, true)
	a := e.put("ann", "a.txt", "aaa", "visibility=private")
	b := e.put("ann", "b.txt", "bbb", "visibility=private")
	res, _ := e.do("GET", "/"+str(a, "slug")+"/raw", nil, as("ann"))
	signed := path(res.Header.Get("Location"))
	q := signed[strings.IndexByte(signed, '?'):]
	// Replay on another item: refused.
	if res, _ := e.do("GET", "/"+str(b, "slug")+"/raw"+q, nil, host("raw.test")); res.StatusCode != 404 {
		t.Errorf("signature replayed on another item: %d", res.StatusCode)
	}
	// Changing visibility or the password invalidates it.
	e.do("PATCH", itemPath(num(a, "id")), strings.NewReader(`{"visibility":"unlisted","password":"x-123456"}`), as("ann"), jsonBody())
	if res, _ := e.do("GET", signed, nil, host("raw.test")); res.StatusCode != 404 {
		t.Errorf("signature survived a password change: %d", res.StatusCode)
	}
	// Expiry far in the future is refused.
	if res, _ := e.do("GET", "/"+str(a, "slug")+"/raw?e=99999999999&s=AAAA", nil, host("raw.test")); res.StatusCode != 404 {
		t.Errorf("far expiry: %d", res.StatusCode)
	}
}

func TestRedteamHoldsRiskyTypesEveryMode(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	cases := map[string]string{
		"a.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"b.html":  `<script>alert(1)</script><svg>`, // sniffed as SVG
		"c.xhtml": "plain words",                    // extension-only XHTML
		"d.xsl":   `<?xml version="1.0"?><xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"/>`,
		"e.js":    "self.addEventListener('fetch',e=>{})",
		"f.pdf":   "%PDF-1.4\n<html><script>alert(1)</script></html>",
		"g.htm":   "<!doctype html><script>alert(1)</script>",
	}
	for name, body := range cases {
		it := e.put("ann", name, body, "")
		for _, m := range []string{"/raw", ".js", ".html", ".svg", "/download"} {
			res, _ := e.do("GET", "/"+str(it, "slug")+m, nil)
			ct, csp := res.Header.Get("Content-Type"), res.Header.Get("Content-Security-Policy")
			if strings.Contains(ct, "html") || strings.Contains(ct, "javascript") || strings.Contains(ct, "xml") && ct != "image/svg+xml" {
				t.Errorf("%s%s: Content-Type %q", name, m, ct)
			}
			if ct != "application/pdf" && !strings.HasPrefix(csp, "sandbox") {
				t.Errorf("%s%s: no sandbox (%q, %q)", name, m, ct, csp)
			}
			if ct == "application/pdf" && (strings.Contains(csp, "script-src") || !strings.Contains(csp, "default-src 'none'")) {
				t.Errorf("%s%s: pdf CSP %q", name, m, csp)
			}
			if res.Header.Get("Service-Worker-Allowed") != "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("%s%s: headers %v", name, m, res.Header)
			}
		}
	}
}
