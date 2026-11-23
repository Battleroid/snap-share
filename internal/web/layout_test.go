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
		`<span class="pb-mono">86%</span>`, `class="pb-meter app-quota__meter" data-status="warn" data-vars="--value:86%"`,
		"8.6 GB of 10 GB · 1.4 GB free · files up to 1 GB", `href="/settings/profile#usage"`)
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
	if i, j := strings.Index(body, "/static/vendor/patchbay/pb-theme-boot.js"), strings.Index(body, "patchbay.css"); i < 0 || i > j {
		t.Error("pb-theme-boot.js must load before the CSS")
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

// page.action and row.actions: form actions with hidden fields and a
// confirm, extra attributes, data-row-action items and skipped entries.
func TestPartialActions(t *testing.T) {
	assets, err := NewAssets()
	if err != nil {
		t.Fatal(err)
	}
	rd, err := NewRenderer(assets, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	run := func(name string, data any) string {
		t.Helper()
		var b strings.Builder
		if err := rd.pages["core/kit"].ExecuteTemplate(&b, name, data); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	d := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	head := run("page.head", d("title", "ann", "note", "you", "meta", []any{"1 unread"}, "metaAttrs", d("data-unread-label", ""),
		"actions", []any{
			d("label", "Reset password", "post", "/admin/users/1/reset", "fields", []any{d("name", "back", "value", "/admin/users/1")}),
			false,
			d("label", "Disable user", "post", "/admin/users/1/status", "name", "status", "value", "disabled",
				"confirm", d("title", "Disable ann?", "action", "Disable user")),
		}))
	for _, want := range []string{
		`<span class="pb-person__meta app-title-note">you</span>`, `<p class="pb-pagehead__meta"><span data-unread-label="">1 unread</span></p>`,
		`<form method="post" action="/admin/users/1/reset"><input type="hidden" name="back" value="/admin/users/1"><button type="submit" class="pb-btn">`,
		`name="status" value="disabled" data-confirm-title="Disable ann?" data-confirm-label="Disable user" data-confirm-danger>`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("page.head lacks %q:\n%s", want, head)
		}
	}
	if strings.Count(head, "<form") != 2 {
		t.Errorf("false actions must be skipped:\n%s", head)
	}

	open := run("page.head.open", d("title", "Stats"))
	if !strings.HasSuffix(strings.TrimSpace(open), `<div class="pb-pagehead__actions">`) || run("page.head.close", nil) != "</div>\n</header>" {
		t.Errorf("page.head.open/close: %q", open)
	}

	row := run("row.actions", d("id", "row-7", "name", "a.png", "inline", d("label", "Copy link to a.png", "copy", "https://x/a"),
		"items", []any{
			d("label", "Timeline", "href", "/t", "attrs", d("hx-get", "/t", "hx-target", "#dlg", "data-dialog-open", "dlg", "rel", "noreferrer")),
			d("label", "Edit", "action", "edit"),
			false,
			d("label", "Burn", "post", "/burn", "fields", []any{d("name", "ref", "value", "7")}, "confirm", d("title", "Burn?")),
		}))
	for _, want := range []string{
		`data-copy-value="https://x/a" aria-label="Copy link to a.png"`, `id="row-7-menu"`,
		`<a class="pb-menu__item" role="menuitem" href="/t" data-dialog-open="dlg" hx-get="/t" hx-target="#dlg" rel="noreferrer">`,
		`data-row-action="edit"`, `<form method="post" action="/burn"><input type="hidden" name="ref" value="7">`,
		`<hr class="pb-menu__sep">`,
	} {
		if !strings.Contains(row, want) {
			t.Errorf("row.actions lacks %q:\n%s", want, row)
		}
	}
	// Kit Menu roles: a named role=menu, and every item (links, buttons,
	// form and confirm buttons) a menuitem.
	if !strings.Contains(row, `aria-haspopup="menu" aria-label="Actions for a.png"`) ||
		!strings.Contains(row, `role="menu" id="row-7-menu" popover data-align="end" aria-label="Actions for a.png"`) {
		t.Errorf("row.actions menu lacks its role or name:\n%s", row)
	}
	if n := strings.Count(row, `role="menuitem"`); n != 3 {
		t.Errorf("row.actions: %d menuitems, want 3:\n%s", n, row)
	}
	post := run("row.actions", d("id", "r", "name", "x", "items", []any{d("label", "Copy", "copy", "v"), d("label", "Send", "post", "/s")}))
	if n := strings.Count(post, `role="menuitem"`); n != 2 {
		t.Errorf("copy and post items: %d menuitems, want 2:\n%s", n, post)
	}
	if strings.Contains(row, "ZgotmplZ") {
		t.Errorf("an attribute was refused:\n%s", row)
	}
}
