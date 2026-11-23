package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
)

func TestNavKey(t *testing.T) {
	cases := map[string]string{
		"/":                               "new",
		"/new/secret":                     "new",
		"/dashboard":                      "items",
		"/dashboard?type=image":           "items",
		"/dashboard?type=album":           "albums",
		"/dashboard/secrets":              "secrets",
		"/dashboard/secrets/abc/timeline": "secrets",
		"/dashboard/stats":                "stats",
		"/dashboard/items/7/stats":        "stats",
		"/dashboard/items/7/events.csv":   "stats",
		"/dashboard/items/7/delete":       "items",
		"/notifications":                  "notifications",
		"/settings":                       "settings/profile",
		"/settings/profile":               "settings/profile",
		"/settings/security/totp/confirm": "settings/security",
		"/settings/keys/new":              "settings/keys",
		"/settings/keys/3/revoke":         "settings/keys",
		"/settings/defaults":              "settings/defaults",
		"/settings/integrations":          "settings/integrations",
		"/settings/notifications":         "settings/notifications",
		"/admin":                          "admin/overview",
		"/admin/users/12/role":            "admin/users",
		"/admin/users/new":                "admin/users",
		"/admin/secrets/burn":             "admin/secrets",
		"/admin/audit":                    "admin/audit",
		"/_kit":                           "admin/kit",
		"/a1b2c3":                         "",
		"/dashboardx":                     "",
		"/administrator":                  "",
	}
	for in, want := range cases {
		u, _ := url.Parse(in)
		if got := NavKey(u); got != want {
			t.Errorf("NavKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLayoutFor(t *testing.T) {
	user := &auth.Principal{UserID: 1}
	cases := []struct {
		page string
		p    *auth.Principal
		want string
	}{
		{"auth/login", nil, LayoutAuth},
		{"auth/mfa", user, LayoutAuth},
		{"share/unlock", user, LayoutAuth},
		{"secrets/open", nil, LayoutAuth},
		{"share/x", nil, LayoutAuth},
		{"core/error", user, LayoutAuth},
		{"share/item", user, LayoutSite},
		{"share/gist", nil, LayoutSite},
		{"albums/album", user, LayoutSite},
		{"dashboard/items", user, LayoutApp},
		{"keys/list", user, LayoutApp},
		{"core/home", nil, LayoutSite},
	}
	for _, c := range cases {
		if got := layoutFor(c.page, c.p); got != c.want {
			t.Errorf("layoutFor(%q, %v) = %q, want %q", c.page, c.p != nil, got, c.want)
		}
	}
}

// The chrome carries the hooks that negative tests elsewhere look for
// (mfa_test, redteam: "signed in as"; aria-label="Main"; pb-bell), so
// those negatives fail if the hooks are renamed instead of passing.
func TestLayoutChrome(t *testing.T) {
	assets, err := NewAssets()
	if err != nil {
		t.Fatal(err)
	}
	rd, err := NewRenderer(assets, func(*http.Request) Site { return Site{Name: "snap"} }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	rd.SetBell(func(context.Context, int64) int { return 3 })
	// User 1 has a quota (86% used: warn); everyone else is unlimited.
	rd.SetQuota(func(_ context.Context, id int64) (Quota, bool) {
		return Quota{Used: 8_600_000_000, Total: 10_000_000_000, MaxFile: 1_000_000_000}, id == 1
	})
	render := func(page, path string, p *auth.Principal) string {
		t.Helper()
		r := httptest.NewRequest("GET", "https://snap.test"+path, nil)
		if p != nil {
			r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		}
		w := httptest.NewRecorder()
		rd.Page(w, r, http.StatusOK, page, Page{Title: "Test", Data: map[string]any{}})
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", page, path, w.Code, w.Body)
		}
		return w.Body.String()
	}
	has := func(t *testing.T, name, body string, want bool, needles ...string) {
		t.Helper()
		for _, n := range needles {
			if strings.Contains(body, n) != want {
				t.Errorf("%s: contains %q = %v, want %v", name, n, !want, want)
			}
		}
	}

	user := &auth.Principal{UserID: 1, Username: "ann"}
	admin := &auth.Principal{UserID: 2, Username: "root", Role: auth.RoleAdmin}
	setup := &auth.Principal{UserID: 3, Username: "sam", NeedsSetup: true}

	body := render("core/home", "/", user)
	has(t, "signed in", body, true, `class="pb-shell"`, `class="pb-shell__head"`, `aria-label="Main"`, "signed in as", `aria-label="Signed in as ann"`,
		`class="pb-bell__count">3</span>`, "/static/js/inbox.", `href="/" aria-current="page">`, `action="/logout"`, `href="/settings/profile"`, "data-theme-switch",
		`href="/dashboard?type=album"`, `href="/dashboard/stats"`)
	has(t, "signed in", body, false, `aria-label="Admin"`, `href="/_kit"`)
	// The quota ring sits before the bell.
	has(t, "quota", body, true, `aria-label="Storage: 8.6 GB of 10 GB used" data-level="warn"`, `stroke-dasharray="43.23 50.27"`,
		"86%", "1.4 GB", "1 GB", `href="/settings/profile#usage"`)
	if strings.Index(body, "app-quota") > strings.Index(body, "pb-bell") {
		t.Error("the quota ring must come before the bell")
	}

	body = render("core/home", "/admin/users/7", admin)
	has(t, "admin", body, true, `aria-label="Admin"`, `href="/admin/users" aria-current="page">`, `href="/_kit"`)
	has(t, "no quota", body, false, "app-quota")

	// A session held at the setup: the header and Sign out, no sidebar, no bell, no Settings.
	body = render("core/home", "/settings/security", setup)
	has(t, "setup", body, true, `class="pb-shell app-shell--bare"`, `aria-label="Signed in as sam"`, `action="/logout"`)
	setup.UserID = 1 // even with a quota
	body = render("core/home", "/settings/security", setup)
	has(t, "setup", body, false, `aria-label="Main"`, "pb-bell", "data-nav-toggle", `href="/settings/profile"`, "app-quota")

	// Signed out: the site header with Sign in, no account or bell.
	body = render("core/home", "/somewhere", nil)
	has(t, "signed out", body, true, `class="pb-sitehead"`, `href="/login">Sign in`)
	has(t, "signed out", body, false, "signed in as", "pb-bell", "/static/js/inbox.", `aria-label="Main"`, "pb-shell")

	// The auth frame carries no chrome at all.
	body = render("auth/login", "/login", nil)
	has(t, "auth", body, true, `class="pb-auth"`)
	has(t, "auth", body, false, "pb-shell", "pb-sitehead", "signed in as", "pb-bell")

	// Error pages use the auth frame even when signed in.
	body = render("core/error", "/x", user)
	has(t, "error page", body, false, "pb-shell", `aria-label="Main"`, "pb-bell", "/static/js/inbox.", "app-quota")

	// The theme boots before the CSS paints, as a classic script.
	if i, j := strings.Index(body, "/static/js/theme-boot.js"), strings.Index(body, "patchbay.css"); i < 0 || i > j {
		t.Error("theme-boot.js must load before the CSS")
	}
}

func TestAvatar(t *testing.T) {
	// The kit's pbAvatar: two initials, lower case; the color from a hash of the name.
	for name, want := range map[string]Avatar{
		"riley":       {"ri", 1},
		"Ann Lee":     {"al", avatar("Ann Lee").Color},
		"jo.smith":    {"js", avatar("jo.smith").Color},
		"":            {"?", 0},
		"émile":       {"ém", avatar("émile").Color},
		"x":           {"x", avatar("x").Color},
		"ann@example": {"ae", avatar("ann@example").Color},
	} {
		if got := avatar(name); got != want {
			t.Errorf("avatar(%q) = %+v, want %+v", name, got, want)
		}
	}
}
