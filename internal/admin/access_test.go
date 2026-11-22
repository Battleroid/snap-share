package admin_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/testutil"
	"github.com/Battleroid/snap-share/internal/web"
)

var wildcard = regexp.MustCompile(`\{[^}]+\}`)

// adminRoutes lists every registered /admin and /api/v1/admin route.
func adminRoutes(e *env) []web.Route {
	var out []web.Route
	for _, rt := range e.s.App.Router.Routes() {
		_, path, _ := strings.Cut(rt.Pattern, " ")
		if path == "/admin" || strings.HasPrefix(path, "/admin/") || strings.HasPrefix(path, "/api/v1/admin/") {
			out = append(out, rt)
		}
	}
	return out
}

// Every admin route is web.Admin(), and nobody else gets past the router:
// not a signed-in user, not their API key, not an admin's key without the
// admin scope.
func TestEveryAdminRouteIsAdminOnly(t *testing.T) {
	e := newEnv(t)
	routes := adminRoutes(e)
	if len(routes) < 25 {
		t.Fatalf("only %d admin routes; did they move?", len(routes))
	}
	for _, rt := range routes {
		if rt.Access != web.AccessAdmin {
			t.Errorf("%s: access %v, want web.Admin()", rt.Pattern, rt.Access)
		}
		method, path, _ := strings.Cut(rt.Pattern, " ")
		path = wildcard.ReplaceAllString(path, "1")
		for _, who := range []opt{as("ann"), bearer("ann-key"), bearer("root-nokey")} {
			res, body := e.do(method, path, strings.NewReader("{}"), who, header("Content-Type", "application/json"))
			if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Admins only") {
				t.Errorf("%s: %d %.80s", rt.Pattern, res.StatusCode, body)
			}
		}
		res, _ := e.do(method, path, nil)
		if want := map[bool]int{true: http.StatusSeeOther, false: http.StatusUnauthorized}[method == "GET" && !strings.HasPrefix(path, "/api/")]; res.StatusCode != want {
			t.Errorf("%s anonymous: %d want %d", rt.Pattern, res.StatusCode, want)
		}
	}
}

func TestAdminPagesRender(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/admin", "/admin/users", "/admin/users/new", "/admin/invites", "/admin/content", "/admin/secrets", "/admin/settings", "/admin/audit"} {
		for _, who := range []opt{as("root"), bearer("root-key")} {
			res, body := e.get(path, who)
			if res.StatusCode != http.StatusOK || !strings.Contains(body, `aria-label="Admin"`) || !strings.Contains(body, `aria-current="page"`) {
				t.Errorf("%s: %d", path, res.StatusCode)
			}
			if res.Header.Get("Cache-Control") != "no-store" {
				t.Errorf("%s: cache-control %q", path, res.Header.Get("Cache-Control"))
			}
		}
	}
	if res, _ := e.get("/admin/users/999", as("root")); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown user: %d", res.StatusCode)
	}
}

func TestAdminCIDRs(t *testing.T) {
	e := newEnv(t, testutil.Env("ADMIN_CIDRS", "10.0.0.0/8"))
	if res, body := e.get("/admin", as("root")); res.StatusCode != http.StatusForbidden || !strings.Contains(body, "not available from this network") {
		t.Errorf("page from outside: %d", res.StatusCode)
	}
	if res, _ := e.json("GET", "/api/v1/admin/users", nil, bearer("root-key")); res.StatusCode != http.StatusForbidden {
		t.Errorf("API from outside: %d", res.StatusCode)
	}
	// Other pages are not affected.
	if res, _ := e.get("/dashboard", as("root")); res.StatusCode != http.StatusOK {
		t.Errorf("dashboard: %d", res.StatusCode)
	}

	inside := newEnv(t, testutil.Env("ADMIN_CIDRS", "127.0.0.0/8,::1/128"))
	if res, _ := inside.get("/admin", as("root")); res.StatusCode != http.StatusOK {
		t.Errorf("page from inside: %d", res.StatusCode)
	}
}

// Cookie-authenticated admin POSTs are refused cross-site.
func TestAdminCSRF(t *testing.T) {
	e := newEnv(t)
	for path, form := range map[string]url.Values{
		"/admin/users":                               {"username": {"mallory"}},
		"/admin/settings/apply":                      {"key": {"secrets.anonymous"}, "value": {"true"}},
		"/admin/settings":                            {"keys": {"secrets.anonymous"}, "secrets.anonymous": {"on"}},
		"/admin/secrets/burn":                        {"ref": {"AAAAAAAAAAAAAAAAAAAAAA"}},
		"/admin/users/" + itoa(e.ann.ID) + "/delete": nil,
	} {
		res, body := e.post(path, form, as("root"), crossSite())
		if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Cross-origin") {
			t.Errorf("%s cross-site: %d", path, res.StatusCode)
		}
	}
	if _, err := e.s.App.Accounts.Store().UserByUsername(e.ctx, "mallory"); err == nil {
		t.Error("cross-site create went through")
	}
	if _, err := e.s.App.Accounts.Store().User(e.ctx, e.ann.ID); err != nil {
		t.Error("cross-site delete went through")
	}
}
