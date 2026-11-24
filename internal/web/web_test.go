package web

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
)

var (
	inlineScriptRe = regexp.MustCompile(`(?i)<script(\s[^>]*)?>\s*[^<\s]`)
	// The one inline script allowed is a data block the kit reads, such as the
	// time-series chart's JSON: <script type="application/json"> whose body is
	// a single template action. It never runs, so the CSP does not touch it.
	jsonBlockRe = regexp.MustCompile(`<script type="application/json">\{\{[^{}]+\}\}</script>`)
)

// inlineScript reports whether a template line carries an inline script
// other than a JSON data block.
func inlineScript(line string) bool {
	return inlineScriptRe.MatchString(jsonBlockRe.ReplaceAllString(line, ""))
}

func TestInlineScriptRule(t *testing.T) {
	for line, want := range map[string]bool{
		`<script type="application/json">{{.chart.Data}}</script>`:          false,
		`  <script type="application/json">{{ .x }}</script>`:               false,
		`<script type="application/json">{"a":1}</script>`:                  true,
		`<script type="application/json">{{.a}}{{.b}}</script>`:             true,
		`<script type="application/json">x{{.a}}</script>`:                  true,
		`<script type="text/javascript">{{.a}}</script>`:                    true,
		`<script>{{.a}}</script>`:                                           true,
		`<script>alert(1)</script>`:                                         true,
		`<script type="application/json">{{.a}}</script><script>x</script>`: true,
		`<script src="/static/js/app.js" type="module"></script>`:           false,
	} {
		if got := inlineScript(line); got != want {
			t.Errorf("inlineScript(%q) = %v, want %v", line, got, want)
		}
	}
}

// Templates must not carry inline styles or event handlers: the CSP forbids both.
func TestTemplatesHaveNoInlineStyleOrHandlers(t *testing.T) {
	bad := regexp.MustCompile(`(?i)<[^>]*\s(style|on[a-z]+)\s*=`)
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
			if inlineScript(line) {
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
	inlineStyle := regexp.MustCompile(`\sstyle\s*=\s*["']`)
	for _, dir := range []string{"static/js", "static/vendor/patchbay"} {
		_ = fs.WalkDir(staticFS, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || (!strings.HasSuffix(p, ".js") && !strings.HasSuffix(p, ".mjs")) {
				return err
			}
			b, _ := fs.ReadFile(staticFS, p)
			if re.Match(b) {
				t.Errorf("%s uses eval/new Function, which the CSP blocks", p)
			}
			// Markup the kit's scripts build must carry no style attributes either.
			if strings.HasPrefix(p, "static/vendor/") && inlineStyle.Match(b) {
				t.Errorf("%s writes a style attribute, which the CSP blocks", p)
			}
			return nil
		})
	}
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
	secret := get("/s/abc").Get("Content-Security-Policy")
	if !strings.Contains(secret, "'wasm-unsafe-eval'") {
		t.Error("secret route must allow wasm")
	}
	// The kit's MediaPlayer plays same-origin media and blob: previews
	// (composer uploads, decrypted secret files): both policies allow them.
	for name, p := range map[string]string{"app": csp, "secret": secret} {
		if !strings.Contains(p, "media-src 'self' blob:") || !strings.Contains(p, "font-src 'self'") {
			t.Errorf("%s CSP lacks media or font sources: %s", name, p)
		}
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

// app.css is page layout on kit tokens: no selector names a .pb- class
// (put an app- class on the element instead), no raw px but 1px and 2px
// in declarations (at-rule preludes such as @media may use rem), and no
// hex colors.
func TestAppCSSIsLayoutOnly(t *testing.T) {
	b, err := fs.ReadFile(staticFS, "static/css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(b), "")
	px := regexp.MustCompile(`(^|[^\w.-])(\d*\.?\d+)px\b`)
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)
	// Innermost blocks are rules: selectors, then declarations. The text
	// before a rule may still hold an enclosing at-rule's prelude.
	rule := regexp.MustCompile(`([^{}]*)\{([^{}]*)\}`)
	n := 0
	for _, m := range rule.FindAllStringSubmatch(css, -1) {
		n++
		sel, decl := strings.TrimSpace(m[1]), m[2]
		if strings.HasPrefix(sel, "@") {
			t.Errorf("app.css: unparsed at-rule %q", sel)
			continue
		}
		if strings.Contains(sel, ".pb-") {
			t.Errorf("app.css: selector %q names a kit class; give the element an app- class", sel)
		}
		for _, p := range px.FindAllStringSubmatch(decl, -1) {
			if p[2] != "1" && p[2] != "2" {
				t.Errorf("app.css: %q uses %spx; use a token (var(--space-*), var(--size-*)) or rem", sel, p[2])
			}
		}
		if h := hex.FindString(decl); h != "" {
			t.Errorf("app.css: %q uses the color %s; use a color token", sel, h)
		}
	}
	// Selectors inside @media blocks: the regexp above sees "@media (…) {"
	// only as the text between rules, so check those preludes on their own.
	for _, m := range regexp.MustCompile(`@[a-z-]+[^{;]*\{`).FindAllString(css, -1) {
		if strings.Contains(m, ".pb-") {
			t.Errorf("app.css: at-rule %q names a kit class", m)
		}
	}
	if n < 20 {
		t.Fatalf("app.css: parsed only %d rules; is the file there?", n)
	}
}

// pendingKitControls lists the templates that still break a kit control
// rule (TestTemplatesUseKitControls), each with its exact violation count.
// It may only shrink: a family converting a page removes its entry, and
// TestPendingKitControlsOnlyShrink fails while an entry is higher than
// the template's actual count. Run that test with -v to print the counts.
var pendingKitControls = map[string]int{}

// The checker itself: each rule catches its case and passes the kit's.
func TestKitControlRules(t *testing.T) {
	bad := map[string]string{
		"video":        `<video controls src="x"></video>`,
		"video in fig": `<figure class="pb-player" data-player><video controls></video></figure>`,
		"date":         `<span class="pb-input"><input type="date"></span>`,
		"color":        `<span class="pb-input"><input type="color"></span>`,
		"bare text":    `<input name="q">`,
		"bare area":    `<label><textarea></textarea></label>`,
		"bare select":  `<select name="s"></select>`,
		"checkbox":     `<input type="checkbox" name="c">`,
		"table":        `<table class="app-x"></table>`,
		"dialog":       `<dialog id="d"></dialog>`,
		"pre":          `<pre><code>x</code></pre>`,
		"button":       `<button type="button" class="app-x">x</button>`,
		"closed wrap":  `<span class="pb-input"></span><input name="q">`,
	}
	for what, src := range bad {
		if v := kitControlViolations("x.html", src); len(v) != 1 {
			t.Errorf("%s: %d violations, want 1: %v", what, len(v), v)
		}
	}
	good := []string{
		`<figure class="pb-player" data-player aria-label="v"><video playsinline></video></figure>`,
		`<video class="app-preview" muted></video>`,
		`<span class="pb-input"><input name="q"><button type="button" class="pb-input__btn">x</button></span>`,
		`<span class="pb-input pb-datefield"><input type="text" name="d"></span>`,
		`<input type="hidden" name="h"><input type="file" name="f">`,
		`<span class="pb-select"><select name="s"></select></span>`,
		`<input type="checkbox" class="pb-check"><input type="checkbox" role="switch" class="pb-switch">`,
		`<div class="pb-seg" role="radiogroup"><label><input type="radio" name="v"></label></div>`,
		`<div class="pb-code"><div class="pb-code__body"><pre class="pb-code__hl">x</pre></div></div>`,
		`<div class="pb-prose"><pre><code>x</code></pre></div>`,
		`<table class="pb-table"></table><dialog class="pb-dialog"></dialog>`,
		`<button class="{{$c}}">x</button>`,
		`{{/* <button>in a comment</button> */}}`,
		`<div class="pb-otp" data-otp><input inputmode="numeric" maxlength="1"></div>`,
	}
	for _, src := range good {
		if v := kitControlViolations("x.html", src); len(v) != 0 {
			t.Errorf("%s: %v", src, v)
		}
	}
	if v := kitControlViolations("share/player.html", `<video controls></video>`); len(v) != 0 {
		t.Errorf("player is exempt: %v", v)
	}
}

// Every control and content type uses its kit element (plan section 5b):
// MediaPlayer for video and audio, the kit pickers instead of native
// date/time/color inputs, .pb-input / .pb-select around fields, kit
// classes on checkboxes, radios, tables, dialogs and buttons, and code in
// a CodeArea or Prose. Templates not yet converted are in
// pendingKitControls.
func TestTemplatesUseKitControls(t *testing.T) {
	counts, lines := kitControlCounts(t)
	for name, n := range counts {
		if allowed := pendingKitControls[name]; n > allowed {
			t.Errorf("%s: %d kit control violations, %d allowed:\n  %s", name, n, allowed, strings.Join(lines[name], "\n  "))
		}
	}
}

func TestPendingKitControlsOnlyShrink(t *testing.T) {
	counts, _ := kitControlCounts(t)
	for name, allowed := range pendingKitControls {
		if n := counts[name]; n < allowed {
			if n == 0 {
				t.Errorf("pendingKitControls: %s passes now; remove its entry", name)
			} else {
				t.Errorf("pendingKitControls: %s has %d violations; lower its entry from %d to %d", name, n, allowed, n)
			}
		}
	}
	if t.Failed() || testing.Verbose() {
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "\t%q: %d,\n", k, counts[k])
		}
		t.Logf("actual counts:\n%s", b.String())
	}
}

func kitControlCounts(t *testing.T) (map[string]int, map[string][]string) {
	t.Helper()
	counts, lines := map[string]int{}, map[string][]string{}
	err := fs.WalkDir(templateFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := fs.ReadFile(templateFS, p)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(p, "templates/")
		if v := kitControlViolations(name, string(b)); len(v) > 0 {
			counts[name], lines[name] = len(v), v
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return counts, lines
}

var (
	tmplComment = regexp.MustCompile(`(?s)\{\{-?\s*/\*.*?\*/\s*-?\}\}`)
	tmplAction  = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
)

// kitControlViolations checks one template file against the kit control
// rules and returns a line per violation. Each template action becomes
// the word "tmpl" (both branches of an if stay), so a class or type the
// template computes reads as "tmpl"; each {{define}} is checked on its
// own, since its container may be in another template.
func kitControlViolations(name, src string) []string {
	src = tmplComment.ReplaceAllString(src, "")
	var out []string
	for _, chunk := range strings.Split(src, "{{define") {
		out = append(out, kitControlChunk(name, tmplAction.ReplaceAllString(chunk, " tmpl "))...)
	}
	return out
}

func kitControlChunk(name, src string) []string {
	type el struct {
		tag   string
		class []string
	}
	var stack []el
	var out []string
	hasClass := func(cs []string, want ...string) bool {
		for _, c := range cs {
			for _, w := range want {
				if c == w {
					return true
				}
			}
		}
		return false
	}
	inside := func(tag string, want ...string) bool {
		for _, e := range stack {
			if (tag == "" || e.tag == tag) && hasClass(e.class, want...) {
				return true
			}
		}
		return false
	}
	void := map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}
	textLike := map[string]bool{"": true, "text": true, "email": true, "password": true, "search": true, "url": true, "tel": true, "number": true}
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out
		}
		tok := z.Token()
		if tt == html.EndTagToken {
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].tag == tok.Data {
					stack = stack[:i]
					break
				}
			}
			continue
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		attr := map[string]string{}
		for _, a := range tok.Attr {
			attr[a.Key] = a.Val
		}
		class := strings.Fields(attr["class"])
		bad := func(msg string) {
			out = append(out, fmt.Sprintf("%s: <%s class=%q>: %s", name, tok.Data, attr["class"], msg))
		}
		switch tok.Data {
		case "video", "audio":
			_, controls := attr["controls"]
			// Exempt: the scriptless chat player and the hover teasers.
			if name != "share/player.html" && !hasClass(class, "app-preview") && (controls || !inside("figure", "pb-player")) {
				bad("use the kit MediaPlayer: figure.pb-player[data-player], no native controls")
			}
		case "input":
			typ := strings.ToLower(strings.TrimSpace(attr["type"]))
			if strings.Contains(typ, "tmpl") {
				typ = "" // computed by the template: hold it to the text rules
			}
			switch {
			case typ == "date" || typ == "time" || typ == "datetime-local" || typ == "color":
				bad("use the kit DatePicker, TimePicker or ColorPicker")
			case textLike[typ] && !inside("", "pb-input", "pb-otp", "pb-code"):
				bad("text-like input outside .pb-input")
			case (typ == "checkbox" || typ == "radio") && !hasClass(class, "pb-check", "pb-switch", "pb-radio") && !inside("", "pb-seg"):
				bad("checkbox or radio without pb-check, pb-switch or pb-radio, outside a pb-seg")
			}
		case "textarea":
			if !inside("", "pb-input", "pb-code") {
				bad("textarea outside .pb-input or .pb-code")
			}
		case "select":
			if !inside("", "pb-select") {
				bad("select outside .pb-select")
			}
		case "table":
			if !hasClass(class, "pb-table") {
				bad("table without pb-table")
			}
		case "dialog":
			if !hasClass(class, "pb-dialog", "pb-lightbox", "pb-cmd") {
				bad("dialog without pb-dialog")
			}
		case "pre":
			if !hasClass(class, "pb-code", "pb-prose", "pb-code__gutter", "pb-code__hl") && !inside("", "pb-code", "pb-prose") {
				bad("pre outside .pb-code or .pb-prose")
			}
		case "button":
			kit := false // a kit class, or one the template computes
			for _, c := range class {
				kit = kit || strings.HasPrefix(c, "pb-") || c == "tmpl"
			}
			if !kit {
				bad("button without a kit class")
			}
		}
		if tt == html.StartTagToken && !void[tok.Data] {
			stack = append(stack, el{tok.Data, class})
		}
	}
}
