package items_test

// Regression tests for the serving-path findings of the security review.
// Each started as a proof of concept that passed while the bug existed; it
// now passes only with the fix.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// An API key of the owner that holds no items:read (secrets:read,
// account:read or the ShareX items:write) is a visitor on the public
// routes: private items are 404, password items need the password, it gets
// no signed raw URLs and no owner's album view. A key with items:read keeps
// the owner's view, as the API gives it.
func TestRedteamKeyWithoutItemScopesReadsPrivateContent(t *testing.T) {
	e := newEnv(t, testutil.Env("RAW_URL", "https://raw.test"))
	e.user("ann", 1, true)
	keys := map[string][]auth.Scope{
		"ann-secrets": {auth.ScopeSecretsRead},
		"ann-account": {auth.ScopeAccountRead},
		"ann-sharex":  {auth.ScopeItemsWrite},
		"ann-reader":  {auth.ScopeItemsRead},
	}
	keyID := int64(1000)
	for name, sc := range keys {
		keyID++
		e.s.Auth.Add(name, auth.Principal{UserID: 1, EmailVerified: true, KeyID: keyID, Scopes: sc})
	}

	priv := e.put("ann", "diary.txt", "private diary", "visibility=private")
	locked := e.put("ann", "taxes.txt", "locked contents", "", header("X-Snap-Password", "hunter2-long"))
	hidden := e.put("ann", "hidden.txt", "private album member", "visibility=private")
	e.exec(`UPDATE items SET thumb_key = blob_key, preview_key = blob_key`)
	res, al := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "trip", "items": []int64{num(hidden, "id")}}, as("ann"))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create album: %d %v", res.StatusCode, al)
	}

	for name := range keys {
		owner := name == "ann-reader"
		for _, m := range []string{"", "/raw", ".txt", "/download", "/thumb", "/preview"} {
			res, body := e.do("GET", "/"+str(priv, "slug")+m, nil, bearer(name))
			loc := res.Header.Get("Location")
			if owner {
				if m != "" && (res.StatusCode != http.StatusFound || !strings.Contains(loc, "?e=")) {
					t.Errorf("%s %s: %d %q, want a signed redirect", name, m, res.StatusCode, loc)
				}
				continue
			}
			if res.StatusCode != http.StatusNotFound || strings.Contains(loc, "?e=") || strings.Contains(body, "private diary") {
				t.Errorf("%s GET private%s: %d %q", name, m, res.StatusCode, loc)
			}
		}
		for _, m := range []string{"/raw", ".txt", "/download"} {
			res, body := e.do("GET", "/"+str(locked, "slug")+m, nil, bearer(name))
			loc := res.Header.Get("Location")
			if owner {
				if res.StatusCode != http.StatusFound || !strings.Contains(loc, "?e=") {
					t.Errorf("%s locked%s: %d %q, want a signed redirect", name, m, res.StatusCode, loc)
				}
				continue
			}
			if res.StatusCode != http.StatusForbidden || strings.Contains(loc, "?e=") || strings.Contains(body, "locked contents") {
				t.Errorf("%s GET locked%s: %d %q", name, m, res.StatusCode, loc)
			}
		}
		if res, page := e.do("GET", "/"+str(locked, "slug"), nil, bearer(name)); !owner && !strings.Contains(page, `name="password"`) {
			t.Errorf("%s locked page: %d without the unlock form", name, res.StatusCode)
		}
		// The unlock route treats the key as a visitor too: private is 404.
		form := strings.NewReader(url.Values{"password": {"x"}}.Encode())
		if res, _ := e.do("POST", "/unlock/"+str(priv, "slug"), form, bearer(name), header("Content-Type", "application/x-www-form-urlencoded")); !owner && res.StatusCode != http.StatusNotFound {
			t.Errorf("%s unlock private: %d", name, res.StatusCode)
		}
		// Album page: no owner view, no private member.
		_, page := e.do("GET", "/"+str(al, "slug"), nil, bearer(name))
		if leaks := strings.Contains(page, str(hidden, "slug")) || strings.Contains(page, "js/album.js"); leaks != owner {
			t.Errorf("%s album page: owner view %v", name, leaks)
		}
		// The composer at / lists recent items: a key gets the signed-out page.
		if _, home := e.do("GET", "/", nil, bearer(name)); strings.Contains(home, str(priv, "slug")) || strings.Contains(home, str(locked, "slug")) {
			t.Errorf("%s GET / lists private slugs", name)
		}
	}
}

// Item unlocks: new passwords are at least 8 characters (existing shorter
// ones still unlock), each item has a failure budget shared by every
// address, and one address has a budget across items.
func TestRedteamUnlockLimitIsPerAddressAndSlug(t *testing.T) {
	e := newEnv(t, testutil.Env("TRUSTED_PROXIES", "127.0.0.1/32,::1/128"))
	e.user("ann", 1, true)
	ct := header("Content-Type", "application/x-www-form-urlencoded")
	form := func(pw string) *strings.Reader { return strings.NewReader(url.Values{"password": {pw}}.Encode()) }

	// Short passwords are refused on create and on PATCH.
	if res, body := e.do("PUT", "/api/v1/upload/weak.txt", strings.NewReader("secret"), as("ann"), header("X-Snap-Password", "7")); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("one-character password: %d %s", res.StatusCode, body)
	}
	if res, _ := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "x", "password": "1234567"}, as("ann")); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("seven-character password: %d", res.StatusCode)
	}
	it := e.put("ann", "ok.txt", "secret", "", header("X-Snap-Password", "correct-pw-1"))
	if res, _ := e.doJSON("PATCH", itemPath(num(it, "id")), map[string]any{"password": "short"}, as("ann")); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH short password: %d", res.StatusCode)
	}

	// An older, shorter password still unlocks.
	old := e.put("ann", "old.txt", "old secret", "", header("X-Snap-Password", "placeholder-pw"))
	h, err := auth.HashPassword(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE items SET password_hash = ? WHERE id = ?`, h, num(old, "id"))
	if res, _ := e.do("POST", "/unlock/"+str(old, "slug"), form("7"), ct, header("X-Forwarded-For", "198.51.100.1")); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("legacy short password: %d", res.StatusCode)
	}

	// 20 wrong guesses at one item from 20 /64s spend its budget: the 21st
	// attempt, from a new address and with the right password, is refused.
	slug := str(it, "slug")
	for i := range 20 {
		xff := header("X-Forwarded-For", fmt.Sprintf("2001:db8:0:%x::1", i))
		if res, _ := e.do("POST", "/unlock/"+slug, form(fmt.Sprintf("guess%d", i)), ct, xff); res.StatusCode != http.StatusForbidden {
			t.Fatalf("guess %d: %d, want 403", i, res.StatusCode)
		}
	}
	res, _ := e.do("POST", "/unlock/"+slug, form("correct-pw-1"), ct, header("X-Forwarded-For", "2001:db8:0:ff::1"))
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("after 20 failures: %d (Retry-After %q), want 429", res.StatusCode, res.Header.Get("Retry-After"))
	}

	// One address, many items: at most 20 hashed attempts a minute.
	var slugs []string
	for i := range 6 {
		slugs = append(slugs, str(e.put("ann", fmt.Sprintf("p%d.txt", i), "x", "", header("X-Snap-Password", "pw-123456")), "slug"))
	}
	hashed := 0
	for _, s := range slugs {
		for range 5 {
			if res, _ := e.do("POST", "/unlock/"+s, form("wrong"), ct, header("X-Forwarded-For", "198.51.100.7")); res.StatusCode == http.StatusForbidden {
				hashed++
			}
		}
	}
	if hashed > 20 {
		t.Fatalf("one address got %d hashed attempts in a minute, want at most 20", hashed)
	}
}

// Well-known root files (domain verification, ads.txt, security.txt,
// sitemaps…) cannot be claimed as slugs, under any extension or case, and
// the dispatcher answers 404 for them even if an item already has the name.
func TestRedteamRootVerificationFilesClaimable(t *testing.T) {
	// Many 404s from one address: keep the slug miss budget out of the way.
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.0/8,::1/128"))
	e.user("mallory", 9, true)
	cases := []struct{ slug, ext string }{
		{"google1a2b3c4d5e6f7a8b", "html"},
		{"BingSiteAuth", "xml"},
		{"keybase", "txt"},
		{"ads", "txt"},
		{"app-ads", "txt"},
		{"security", "txt"},
		{"sitemap", "xml"},
		{"sitemap_index", "xml"},
		{"humans", "txt"},
		{"crossdomain", "xml"},
		{"yandex_0123abcd", "html"},
		{"Security", "TXT"},
	}
	for _, c := range cases {
		res, body := e.do("PUT", "/api/v1/upload/"+c.slug+"."+c.ext+"?slug="+c.slug, strings.NewReader("claim"), as("mallory"))
		if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "reserved") {
			t.Errorf("claim %s: %d %s", c.slug, res.StatusCode, body)
		}
	}
	if res, _ := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://evil.example/", "slug": "apple-app-site-association"}, as("mallory")); res.StatusCode != http.StatusBadRequest {
		t.Errorf("claim apple-app-site-association: %d", res.StatusCode)
	}
	// An item that already holds such a name (made before it was reserved).
	it := e.put("mallory", "x.txt", "evil-ads.example, pub-1, DIRECT", "")
	for _, c := range cases {
		e.exec(`UPDATE items SET slug = ? WHERE id = ?`, c.slug, num(it, "id"))
		for _, p := range []string{"/" + c.slug + "." + c.ext, "/" + c.slug, "/" + c.slug + "/raw"} {
			if res, body := e.do("GET", p, nil, header("User-Agent", "Googlebot/2.1")); res.StatusCode != http.StatusNotFound || strings.Contains(body, "evil-ads") {
				t.Errorf("GET %s: %d", p, res.StatusCode)
			}
		}
	}
	// Names that only resemble them stay available.
	for _, ok := range []string{"googley", "adsl", "securityteam", "keybased"} {
		if items.IsReserved(ok) {
			t.Errorf("%s is reserved", ok)
		}
	}
}

// The raw origin serves raw content only, always anonymously: every app
// page redirects to the public origin, other methods get 404, credentials
// sent to it are ignored and it never sets a cookie.
func TestRedteamRawOriginServesTheApp(t *testing.T) {
	e := newEnv(t, testutil.Env("RAW_URL", "https://raw.test"))
	e.user("ann", 1, true)
	pub := e.put("ann", "pub.txt", "public words", "")
	priv := e.put("ann", "priv.txt", "private words", "visibility=private")

	for _, p := range []string{"/login", "/s", "/reset", "/dashboard", "/settings/defaults", "/api/v1/items", "/", "/static/js/upload.js", "/" + str(pub, "slug")} {
		res, _ := e.do("GET", p+"?q=1", nil, host("raw.test"), as("ann"))
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != e.s.App.Cfg.Origin()+p+"?q=1" {
			t.Errorf("GET %s on the raw origin: %d %q", p, res.StatusCode, res.Header.Get("Location"))
		}
		if len(res.Cookies()) != 0 {
			t.Errorf("GET %s on the raw origin set a cookie", p)
		}
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		req := e.s.Request(t, m, "/login", strings.NewReader("login=x&password=y"))
		req.Host = "raw.test"
		req.Header.Set("Origin", "https://raw.test")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s /login on the raw origin: %d", m, res.StatusCode)
		}
	}
	// Raw content still works there; credentials do not.
	if res, body := e.do("GET", "/"+str(pub, "slug")+".txt", nil, host("raw.test")); res.StatusCode != http.StatusOK || body != "public words" {
		t.Errorf("raw content on the raw origin: %d %q", res.StatusCode, body)
	}
	for _, o := range []reqOpt{as("ann"), bearer("ann-key")} {
		if res, _ := e.do("GET", "/"+str(priv, "slug")+"/raw", nil, host("raw.test"), o); res.StatusCode != http.StatusNotFound {
			t.Errorf("private raw content with credentials on the raw origin: %d", res.StatusCode)
		}
	}
	if res, _ := e.do("GET", "/"+str(pub, "slug")+"/raw", nil, host("raw.test"), header("Authorization", "Bearer not-a-key")); res.StatusCode != http.StatusOK {
		t.Errorf("a bad Authorization header on the raw origin: %d, want it ignored", res.StatusCode)
	}
}

// A deleted, expired or renamed item's slug stays with its owner for the
// cooldown: nobody else can take it, the owner can, and the hold ends.
func TestRedteamSlugTakeoverAfterDeleteOrExpiry(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("mallory", 9, true)
	link := func(user, slug, target string) int {
		res, _ := e.doJSON("POST", "/api/v1/links", map[string]any{"url": target, "slug": slug}, as(user))
		return res.StatusCode
	}
	// Deleted.
	if code := link("ann", "meetup", "https://ann.example/meetup"); code != http.StatusCreated {
		t.Fatalf("ann creates /meetup: %d", code)
	}
	if res, _ := e.do("DELETE", itemPath(e.item("meetup").ID), nil, as("ann")); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if code := link("mallory", "meetup", "https://phish.example/login"); code != http.StatusConflict {
		t.Errorf("mallory takes ann's deleted /meetup: %d, want 409", code)
	}
	if code := link("ann", "meetup", "https://ann.example/meetup2"); code != http.StatusCreated {
		t.Errorf("ann takes back /meetup: %d", code)
	}
	// Expired and swept.
	if code := link("ann", "q3-report", "https://ann.example/q3"); code != http.StatusCreated {
		t.Fatal(code)
	}
	e.exec(`UPDATE items SET expires_at = 1 WHERE slug = 'q3-report'`)
	if err := e.svc.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if code := link("mallory", "Q3-Report", "https://phish.example/q3"); code != http.StatusConflict {
		t.Errorf("mallory takes ann's expired /q3-report: %d, want 409", code)
	}
	// Renamed: the old name is held too, for PATCH as for creation.
	res, h := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://ann.example/h", "slug": "handbook"}, as("ann"))
	if res.StatusCode != http.StatusCreated {
		t.Fatal(res.StatusCode)
	}
	if res, _ := e.doJSON("PATCH", itemPath(num(h, "id")), map[string]any{"slug": "handbook-v2"}, as("ann")); res.StatusCode != http.StatusOK {
		t.Fatalf("rename: %d", res.StatusCode)
	}
	res, m := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://phish.example/"}, as("mallory"))
	if res.StatusCode != http.StatusCreated {
		t.Fatal(res.StatusCode)
	}
	if res, _ := e.doJSON("PATCH", itemPath(num(m, "id")), map[string]any{"slug": "handbook"}, as("mallory")); res.StatusCode != http.StatusConflict {
		t.Errorf("mallory renames to ann's old /handbook: %d, want 409", res.StatusCode)
	}
	// Past the cooldown the name is free, and the sweep drops the hold.
	e.exec(`UPDATE released_slugs SET released_at = 1`)
	if code := link("mallory", "q3-report", "https://mallory.example/q3"); code != http.StatusCreated {
		t.Errorf("after the cooldown: %d", code)
	}
	if err := e.svc.SweepOrphans(e.ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.s.App.DB.R.QueryRow(`SELECT count(*) FROM released_slugs`).Scan(&n); err != nil || n != 0 {
		t.Errorf("released slugs after the sweep: %d %v", n, err)
	}
}

// Misses in the public slug namespace spend a per-address budget: the 31st
// in a minute gets 429 with Retry-After. Hits never spend it and still work.
func TestSlugMissesAreRateLimited(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.put("ann", "hit.txt", "hello", "")
	for i := range items.MissesPerMinute {
		p := fmt.Sprintf("/nosuch%04d", i)
		switch i % 3 {
		case 1:
			p += "/raw"
		case 2:
			p += ".png"
		}
		if res, _ := e.do("GET", p, nil); res.StatusCode != http.StatusNotFound {
			t.Fatalf("miss %d (%s): %d", i, p, res.StatusCode)
		}
		if res, _ := e.do("GET", "/"+str(it, "slug"), nil); res.StatusCode != http.StatusOK {
			t.Fatalf("hit after miss %d: %d", i, res.StatusCode)
		}
	}
	res, _ := e.do("GET", "/nosuch-more", nil)
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("miss %d: %d (Retry-After %q), want 429", items.MissesPerMinute+1, res.StatusCode, res.Header.Get("Retry-After"))
	}
	if res, body := e.do("GET", "/"+str(it, "slug")+"/raw", nil); res.StatusCode != http.StatusOK || body != "hello" {
		t.Fatalf("hit after the budget is spent: %d", res.StatusCode)
	}
}

// Markdown on a share page can point <img> tags at our own /download and
// raw URLs; those loads are not counted as downloads or views.
func TestSelfEmbeddedLoadsAreNotCounted(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.put("ann", "pic.txt", "words", "")
	slug := str(it, "slug")
	img := []reqOpt{header("Sec-Fetch-Dest", "image"), header("Sec-Fetch-Mode", "no-cors"), header("Sec-Fetch-Site", "same-origin"),
		header("Referer", e.s.URL+"/other")}
	for _, p := range []string{"/" + slug + "/download", "/" + slug + "/raw", "/" + slug} {
		if res, _ := e.do("GET", p, nil, img...); res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s as an image: %d", p, res.StatusCode)
		}
	}
	if err := e.svc.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.item(slug); got.Downloads != 0 || got.Views != 0 {
		t.Fatalf("self-embedded loads counted: %d views, %d downloads", got.Views, got.Downloads)
	}
	// A real download still counts.
	if res, _ := e.do("GET", "/"+slug+"/download", nil, header("Sec-Fetch-Dest", "document"), header("Sec-Fetch-Mode", "navigate")); res.StatusCode != http.StatusOK {
		t.Fatal(res.StatusCode)
	}
	if err := e.svc.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.item(slug); got.Downloads != 1 {
		t.Fatalf("download not counted: %d", got.Downloads)
	}
}

// A name removed by someone other than its owner (an admin's moderation),
// or with the owner's account, is held for nobody: not even an account
// that later gets the same user id can take it.
func TestFreedSlugHeldForNobodyAfterModeration(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	for _, slug := range []string{"phish-page", "gone-user"} {
		if res, _ := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://ann.example/", "slug": slug}, as("ann")); res.StatusCode != http.StatusCreated {
			t.Fatal(res.StatusCode)
		}
	}
	admin := auth.WithPrincipal(e.ctx, &auth.Principal{UserID: 99, Role: auth.RoleAdmin})
	if err := e.svc.Delete(admin, e.item("phish-page").ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteOwnedBy(e.ctx, 1); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"phish-page", "gone-user"} {
		if res, _ := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://ann.example/again", "slug": slug}, as("ann")); res.StatusCode != http.StatusConflict {
			t.Errorf("owner retakes /%s: %d, want 409", slug, res.StatusCode)
		}
	}
}
