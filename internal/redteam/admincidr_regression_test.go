package redteam_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// TestAdminCIDRsBypassedByStatsPage (regression, L2): with SNAP_ADMIN_CIDRS
// set, the item stats page gives an admin the totals of another user's
// item only from inside those networks, as /admin does, and the totals
// view never shows the item's name or link.
func TestAdminCIDRsBypassedByStatsPage(t *testing.T) {
	for _, tc := range []struct {
		cidrs  string
		inside bool
	}{
		{"10.0.0.0/8", false},
		{"127.0.0.0/8,::1/128", true},
	} {
		e := newEnv(t, testutil.Env("ADMIN_CIDRS", tc.cidrs))
		root, bob := e.user("root", auth.RoleAdmin), e.user("bob", auth.RoleUser)
		cr, cb := e.session(root), e.session(bob)
		m := e.paste(cb, map[string]any{"content": "bob's unlisted notes", "title": "Bob tax return 2026"})
		slug := m["slug"].(string)
		path := fmt.Sprintf("/dashboard/items/%d/stats", int64(m["id"].(float64)))

		if res, _ := e.do("GET", "/admin", nil, withCookie(cr)); (res.StatusCode == http.StatusForbidden) == tc.inside {
			t.Fatalf("%s: /admin: %d", tc.cidrs, res.StatusCode)
		}
		res, body := e.do("GET", path, nil, withCookie(cr))
		switch {
		case !tc.inside && res.StatusCode != http.StatusNotFound:
			t.Errorf("%s: stats page from outside ADMIN_CIDRS: %d, want 404", tc.cidrs, res.StatusCode)
		case tc.inside && res.StatusCode != http.StatusOK:
			t.Errorf("%s: stats page from inside ADMIN_CIDRS: %d, want 200", tc.cidrs, res.StatusCode)
		}
		if strings.Contains(body, slug) || strings.Contains(body, "Bob tax return 2026") {
			t.Errorf("%s: the admin's stats page shows bob's item name or link", tc.cidrs)
		}
		// The owner still sees both.
		if res, body := e.do("GET", path, nil, withCookie(cb)); res.StatusCode != http.StatusOK || !strings.Contains(body, slug) || !strings.Contains(body, "Bob tax return 2026") {
			t.Errorf("%s: owner's stats page: %d", tc.cidrs, res.StatusCode)
		}
	}
}
