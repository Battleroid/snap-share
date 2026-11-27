package accounts_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/oidctest"
	"github.com/Battleroid/snap-share/internal/testutil"
)

const lapsedTitle = "Admin access paused"

// setSSOAt moves the SSO sign-in of every session of user back to ago.
func setSSOAt(t *testing.T, s *testutil.Server, user int64, ago time.Duration) {
	t.Helper()
	if _, err := s.App.DB.W.Exec(`UPDATE sessions SET sso_at = ? WHERE user_id = ? AND sso_at IS NOT NULL`, time.Now().Add(-ago).UnixMilli(), user); err != nil {
		t.Fatal(err)
	}
}

func lapseRows(t *testing.T, s *testutil.Server, user int64) int {
	t.Helper()
	var n int
	if err := s.App.DB.R.QueryRow(`SELECT count(*) FROM inbox WHERE user_id = ? AND kind = 'admin_lapsed' AND subject_type = 'account' AND subject_id = CAST(? AS TEXT)`,
		user, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func userID(t *testing.T, s *testutil.Server, name string) int64 {
	t.Helper()
	u, err := s.App.Accounts.Store().UserByUsername(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// An SSO-mapped admin whose sign-in is more than a day old acts as a user,
// and is told so: a notice on every app page and on admin URLs, both with
// the SSO sign-in that comes back to where they were, and one inbox row
// per lapse. A fresh SSO sign-in restores everything.
func TestSSOAdminLapseIsExplained(t *testing.T) {
	s, p := ssoServer(t, testutil.Env("OIDC_ADMIN_GROUPS", "snap-admins"))
	createUser(t, s, "root", "root@example.com", auth.RoleAdmin)
	p.SetUser(oidctest.User{Subject: "6", PreferredUsername: "op", Email: "op@example.com", EmailVerified: true, Groups: []string{"snap-admins"}})
	b := newBrowser(t, s)
	b.ssoFlow("/auth/oidc/start")
	op := userID(t, s, "op")
	if _, body := b.get("/dashboard"); strings.Contains(body, lapsedTitle) || strings.Contains(body, "Admin access ends") {
		t.Fatal("fresh SSO admin sees a notice")
	}
	if _, body := b.get("/settings/notifications"); !strings.Contains(body, "Your admin access pauses") {
		t.Fatal("no grid row for the lapse")
	}

	// The last hour: still an admin, with a heads-up.
	setSSOAt(t, s, op, accounts.SSOAdminMaxAge-30*time.Minute)
	if res, _ := b.get("/admin"); res.StatusCode != http.StatusOK {
		t.Fatalf("admin in the last hour: %d", res.StatusCode)
	}
	if _, body := b.get("/dashboard"); !strings.Contains(body, "Admin access ends in 30 min — sign in with Gitea again to extend.") ||
		!strings.Contains(body, `data-status="info"`) {
		t.Fatal("no heads-up in the last hour")
	}

	// Past the limit.
	setSSOAt(t, s, op, accounts.SSOAdminMaxAge+time.Minute)
	res, body := b.get("/dashboard?tab=files")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, lapsedTitle) ||
		!strings.Contains(body, "Your Gitea sign-in is more than 24 hours old; sign in with Gitea again to restore admin features.") ||
		!strings.Contains(strings.ToLower(body), `href="/auth/oidc/start?next=%2fdashboard%3ftab%3dfiles"`) {
		t.Fatalf("dashboard of a lapsed admin: %d", res.StatusCode)
	}
	if strings.Contains(body, `data-dismiss`) || strings.Contains(body, `aria-label="Admin"`) {
		t.Fatal("the notice can be dismissed, or the admin nav is still shown")
	}
	// Admin URLs: still a 403, now with the reason and the way back.
	res, body = b.get("/admin/users?q=x")
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, lapsedTitle) || !strings.Contains(body, "more than 24 hours old") ||
		!strings.Contains(body, `href="/auth/oidc/start?next=%2Fadmin%2Fusers%3Fq%3Dx"`) || !strings.Contains(body, "Sign in with Gitea") {
		t.Fatalf("admin page of a lapsed admin: %d %s", res.StatusCode, body)
	}
	req := s.Request(t, "GET", "/admin/users", nil)
	req.Header.Set("Accept", "application/json")
	if res, body := b.do(req); res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Admin access paused") {
		t.Fatalf("admin JSON of a lapsed admin: %d %s", res.StatusCode, body)
	}
	// One inbox row, however many requests.
	for range 3 {
		b.get("/dashboard")
	}
	if n := lapseRows(t, s, op); n != 1 {
		t.Fatalf("%d admin_lapsed rows", n)
	}
	// The button's next comes back, and only ever to a path here.
	if res, _ := b.ssoFlow("/auth/oidc/start?next=%2Fadmin%2Fusers%3Fq%3Dx"); res.Header.Get("Location") != "/admin/users?q=x" {
		t.Fatalf("after the SSO sign-in: %q", res.Header.Get("Location"))
	}
	if res, _ := b.get("/admin"); res.StatusCode != http.StatusOK {
		t.Fatalf("after a fresh SSO sign-in: %d", res.StatusCode)
	}
	if _, body := b.get("/dashboard"); strings.Contains(body, lapsedTitle) {
		t.Fatal("notice after a fresh SSO sign-in")
	}
	for _, next := range []string{"https://evil.example/", "//evil.example/", "/\\evil.example"} {
		c := newBrowser(t, s)
		if res, _ := c.ssoFlow("/auth/oidc/start?next=" + next); res.Header.Get("Location") != "/" {
			t.Fatalf("next %q went to %q", next, res.Header.Get("Location"))
		}
	}
	// The next lapse (of the new sign-in) is news again.
	setSSOAt(t, s, op, accounts.SSOAdminMaxAge+time.Minute)
	b.get("/dashboard")
	if n := lapseRows(t, s, op); n != 2 {
		t.Fatalf("%d admin_lapsed rows after a second lapse", n)
	}

	// A session of a linked admin that never signed in with SSO: the
	// notice, worded for it, and no inbox row (nothing lapsed).
	if _, err := s.App.DB.W.Exec(`UPDATE sessions SET sso_at = NULL WHERE user_id = ?`, op); err != nil {
		t.Fatal(err)
	}
	if _, body := b.get("/dashboard"); !strings.Contains(body, "Admin features need a recent Gitea sign-in; sign in with Gitea to use them.") {
		t.Fatal("no notice for a session without SSO")
	}
	if n := lapseRows(t, s, op); n != 2 {
		t.Fatalf("%d admin_lapsed rows after a session without SSO", n)
	}

	// A password admin without an identity is not affected.
	r := newBrowser(t, s)
	r.mustLogin("root")
	if res, body := r.get("/dashboard"); res.StatusCode != http.StatusOK || strings.Contains(body, lapsedTitle) {
		t.Fatalf("password admin: %d", res.StatusCode)
	}
	if res, _ := r.get("/admin"); res.StatusCode != http.StatusOK {
		t.Fatalf("password admin on /admin: %d", res.StatusCode)
	}
	if n := lapseRows(t, s, userID(t, s, "root")); n != 0 {
		t.Fatalf("password admin has %d admin_lapsed rows", n)
	}
}

// Without the SSO role mapping, an admin's SSO session never lapses: no
// notice, no inbox row, and no grid row for it.
func TestNoAdminLapseWithoutRoleMapping(t *testing.T) {
	s, p := ssoServer(t)
	p.SetUser(oidctest.User{Subject: "8", PreferredUsername: "kept"})
	newBrowser(t, s).ssoFlow("/auth/oidc/start")
	kept := userID(t, s, "kept")
	if err := s.App.Accounts.Store().SetRole(context.Background(), kept, auth.RoleAdmin, accounts.CLIActor()); err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, s)
	b.ssoFlow("/auth/oidc/start")
	setSSOAt(t, s, kept, accounts.SSOAdminMaxAge+time.Hour)
	if res, _ := b.get("/admin"); res.StatusCode != http.StatusOK {
		t.Fatalf("admin without a mapping: %d", res.StatusCode)
	}
	if _, body := b.get("/dashboard"); strings.Contains(body, lapsedTitle) || strings.Contains(body, "Admin access ends") {
		t.Fatal("notice without a mapping")
	}
	if n := lapseRows(t, s, kept); n != 0 {
		t.Fatalf("%d admin_lapsed rows without a mapping", n)
	}
	if _, body := b.get("/settings/notifications"); strings.Contains(body, "Your admin access pauses") {
		t.Fatal("grid row for an event that cannot happen")
	}
}
