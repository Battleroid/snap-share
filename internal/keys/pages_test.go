package keys_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

var tokenRE = regexp.MustCompile(`value="(snap_[0-9A-Za-z_]+)"`)

func form(v url.Values) (*strings.Reader, opt) {
	return strings.NewReader(v.Encode()), header("Content-Type", "application/x-www-form-urlencoded")
}

func TestKeyPages(t *testing.T) {
	e := newEnv(t)
	ann := e.user("ann", auth.RoleUser)
	sess := withCookie(e.session(ann))

	res, body := e.do("GET", "/settings/keys", nil, sess)
	if res.StatusCode != 200 || !strings.Contains(body, `<h2 class="pb-empty__title">No keys yet</h2>`) || !strings.Contains(body, `href="/settings/keys/new"`) {
		t.Fatalf("empty list: %d", res.StatusCode)
	}
	res, body = e.do("GET", "/settings/keys/new", nil, sess)
	if res.StatusCode != 200 {
		t.Fatalf("new: %d", res.StatusCode)
	}
	for _, sc := range []string{"items:read", "items:write", "items:delete"} {
		if !strings.Contains(body, `value="`+sc+`" checked`) {
			t.Errorf("%s not preselected", sc)
		}
	}
	if strings.Contains(body, `value="admin"`) || !strings.Contains(body, `value="account:read">`) {
		t.Error("scope choices do not follow the role")
	}
	if !strings.Contains(body, "<summary>Restrictions</summary>") {
		t.Error("no Restrictions disclosure")
	}

	// Errors keep what was entered.
	b, ct := form(url.Values{"name": {"laptop"}, "expires": {"date"}, "allowed_cidrs": {"10.0.0.0/8\nbogus"}})
	res, body = e.do("POST", "/settings/keys", b, sess, ct)
	if res.StatusCode != 400 || !strings.Contains(body, "Pick the date") {
		t.Fatalf("missing date: %d", res.StatusCode)
	}
	b, ct = form(url.Values{"name": {"laptop"}, "expires": {"7d"}, "allowed_cidrs": {"10.0.0.0/8\nbogus"}})
	res, body = e.do("POST", "/settings/keys", b, sess, ct)
	if res.StatusCode != 400 || !strings.Contains(html.UnescapeString(body), `"bogus" is not an address or CIDR.`) ||
		!strings.Contains(body, "Pick at least one scope.") || !strings.Contains(body, `value="laptop"`) {
		t.Fatalf("invalid: %d", res.StatusCode)
	}

	// Create: the token is shown once, on a no-store page.
	b, ct = form(url.Values{
		"name": {"laptop"}, "scope": {"items:write", "account:read"}, "expires": {"date"}, "expires_on": {"2099-01-31"},
		"allowed_cidrs": {"127.0.0.1\n::1"}, "visibility": {"private"}, "max_expiry": {"7d"}, "max_upload_mb": {"2.5"},
	})
	res, body = e.do("POST", "/settings/keys", b, sess, ct)
	if res.StatusCode != http.StatusCreated || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d", res.StatusCode)
	}
	m := tokenRE.FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, "Copy it now; it can&#39;t be shown again.") {
		t.Fatalf("token not shown: %s", body)
	}
	token := m[1]
	list, _ := e.s.App.Keys.Store().List(e.ctx, ann.ID)
	if len(list) != 1 {
		t.Fatalf("keys: %d", len(list))
	}
	k := list[0]
	if k.Name != "laptop" || k.ExpiresAt == nil || k.ExpiresAt.Format("2006-01-02") != "2099-01-31" || len(k.CIDRs) != 2 ||
		(k.Constraints != keys.Constraints{Visibility: "private", MaxExpirySeconds: 7 * 86400, MaxUploadBytes: 2_500_000}) {
		t.Fatalf("stored %+v", k)
	}
	if res, _ := e.do("GET", "/api/v1/me", nil, bearer(token)); res.StatusCode != 200 {
		t.Fatalf("new key: %d", res.StatusCode)
	}
	if err := e.s.App.Keys.Flush(e.ctx); err != nil {
		t.Fatal(err)
	}

	// The list shows the prefix, scopes, expiry and last use.
	_, body = e.do("GET", "/settings/keys", nil, sess)
	for _, want := range []string{k.Prefix() + "…", `<span class="pb-pill">items:write</span>`, `<span class="pb-pill">account:read</span>`,
		"2099-01-31 23:59", "127.0.0.1", `data-status="ok"`, "Revoke laptop?"} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(body, token) {
		t.Error("list shows the token")
	}

	// Revoke (the dialog posts here).
	res, _ = e.do("POST", "/settings/keys/"+itoa(k.ID)+"/revoke", nil, sess)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/keys?done=revoked" {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/api/v1/me", nil, bearer(token)); res.StatusCode != 401 {
		t.Errorf("revoked key: %d", res.StatusCode)
	}
	_, body = e.do("GET", "/settings/keys?done=revoked", nil, sess)
	if !strings.Contains(body, "Key revoked.") || !strings.Contains(body, `data-status="down"`) || strings.Contains(body, "Revoke laptop?") {
		t.Error("list after revoke")
	}

	// Cross-site form posts are refused; key principals cannot use the pages.
	b, ct = form(url.Values{"name": {"evil"}, "scope": {"items:read"}})
	if res, _ := e.do("POST", "/settings/keys", b, sess, ct, crossSite()); res.StatusCode != 403 {
		t.Errorf("cross-site create: %d", res.StatusCode)
	}
	all := e.mint(ann, keys.NewKey{Scopes: auth.AllScopes[:8]})
	for _, p := range []string{"/settings/keys", "/settings/keys/new", "/settings/integrations"} {
		if res, _ := e.do("GET", p, nil, bearer(all)); res.StatusCode != 403 {
			t.Errorf("%s with a key: %d", p, res.StatusCode)
		}
	}
	b, ct = form(url.Values{"name": {"x"}, "scope": {"items:read"}})
	if res, _ := e.do("POST", "/settings/keys", b, ct, bearer(all)); res.StatusCode != 403 {
		t.Errorf("create with a key: %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/settings/integrations/sharex/files", nil, bearer(all)); res.StatusCode != 403 {
		t.Errorf("sharex with a key: %d", res.StatusCode)
	}
	// Anonymous visitors are sent to sign in.
	if res, _ := e.do("GET", "/settings/keys", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("anonymous: %d", res.StatusCode)
	}
}

func TestAdminSeesAdminScope(t *testing.T) {
	e := newEnv(t)
	root := e.user("root", auth.RoleAdmin)
	_, body := e.do("GET", "/settings/keys/new", nil, withCookie(e.session(root)))
	if !strings.Contains(body, `value="admin">`) {
		t.Error("admins cannot pick the admin scope")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
