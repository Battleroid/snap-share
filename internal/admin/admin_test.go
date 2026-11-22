package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func adminServer(t *testing.T, opts ...testutil.Option) *testutil.Server {
	t.Helper()
	s := testutil.NewServer(t, opts...)
	u, err := s.App.Accounts.Store().CreateUser(context.Background(),
		accounts.NewUser{Username: "root", Email: "root@example.com", EmailVerified: true, Role: auth.RoleAdmin}, accounts.AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	s.Auth.Add("root", auth.Principal{UserID: u.ID, Role: auth.RoleAdmin, SessionID: "x"})
	s.Auth.Add("ann", auth.Principal{UserID: 99})
	return s
}

// response keeps what the tests check; testutil.Server.Do already closed the body.
type response struct {
	StatusCode int
	Header     http.Header
}

func do(t *testing.T, s *testutil.Server, req *http.Request) (*response, string) {
	t.Helper()
	res, body := s.Do(t, req) //nolint:bodyclose // Do reads and closes the body
	return &response{res.StatusCode, res.Header}, body
}

func post(t *testing.T, s *testutil.Server, as, path string, form url.Values) (*response, string) {
	t.Helper()
	req := s.Request(t, "POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	authtest.Session(req, as)
	return do(t, s, req)
}

func get(t *testing.T, s *testutil.Server, as, path string) (*response, string) {
	t.Helper()
	req := s.Request(t, "GET", path, nil)
	if as != "" {
		authtest.Session(req, as)
	}
	return do(t, s, req)
}

func audits(t *testing.T, s *testutil.Server, action string) int {
	var n int
	_ = s.App.DB.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n)
	return n
}

func TestAccess(t *testing.T) {
	s := adminServer(t)
	for as, want := range map[string]int{"": 303, "ann": 403, "root": 200} {
		if res, _ := get(t, s, as, "/admin/settings"); res.StatusCode != want {
			t.Errorf("%q: %d want %d", as, res.StatusCode, want)
		}
	}
	if res, _ := get(t, s, "root", "/admin"); res.StatusCode != 303 || res.Header.Get("Location") != "/admin/settings" {
		t.Errorf("/admin: %d", res.StatusCode)
	}
}

func TestSaveSettings(t *testing.T) {
	s := adminServer(t)
	res, body := get(t, s, "root", "/admin/settings")
	if res.StatusCode != 200 || !strings.Contains(body, `value="invite" checked`) || !strings.Contains(body, "Email is off.") {
		t.Fatalf("page: %d", res.StatusCode)
	}

	// Open sign-up needs SMTP; turning password sign-in off needs SSO.
	if res, body := post(t, s, "root", "/admin/settings", url.Values{"site_name": {"x"}, "signup_mode": {"open"}, "password_login": {"on"}}); res.StatusCode != 422 || !strings.Contains(body, "needs SMTP") {
		t.Errorf("open without SMTP: %d", res.StatusCode)
	}
	if res, body := post(t, s, "root", "/admin/settings", url.Values{"site_name": {"x"}, "signup_mode": {"invite"}}); res.StatusCode != 422 || !strings.Contains(body, "nobody could sign in") {
		t.Errorf("password off without SSO: %d", res.StatusCode)
	}
	if res, _ := post(t, s, "root", "/admin/settings", url.Values{"site_name": {""}, "signup_mode": {"invite"}, "password_login": {"on"}}); res.StatusCode != 422 {
		t.Errorf("empty site name: %d", res.StatusCode)
	}

	res, _ = post(t, s, "root", "/admin/settings", url.Values{"site_name": {"Drop box"}, "signup_mode": {"closed"}, "password_login": {"on"}})
	if res.StatusCode != 303 {
		t.Fatalf("save: %d", res.StatusCode)
	}
	if app.SiteName.Get(s.App.Settings) != "Drop box" || accounts.SignupMode.Get(s.App.Settings) != "closed" {
		t.Error("settings not saved")
	}
	if n := audits(t, s, "settings.change"); n != 2 {
		t.Errorf("settings.change rows: %d", n)
	}
	if _, body := get(t, s, "root", "/admin/settings"); !strings.Contains(body, "invite links do not work") || !strings.Contains(body, "<title>Settings · Drop box</title>") {
		t.Error("closed-mode notice or new site name missing")
	}
}

func TestTestEmail(t *testing.T) {
	s := adminServer(t, testutil.WithSMTP())
	_, body := get(t, s, "root", "/admin/settings")
	if !strings.Contains(body, "Send test email") || !strings.Contains(body, `value="root@example.com"`) {
		t.Fatal("no test email form")
	}
	if res, body := post(t, s, "root", "/admin/settings/test-email", url.Values{"to": {"ops@example.com"}}); res.StatusCode != 200 || !strings.Contains(body, "Test email sent to ops@example.com.") {
		t.Fatalf("send: %d", res.StatusCode)
	}
	sent := s.Mail.Sent()
	if len(sent) != 1 || sent[0].To[0] != "ops@example.com" || !strings.Contains(sent[0].Subject, "Test email") {
		t.Fatalf("sent %+v", sent)
	}
	if res, _ := post(t, s, "root", "/admin/settings/test-email", url.Values{"to": {"nope"}}); res.StatusCode != 422 {
		t.Errorf("bad address: %d", res.StatusCode)
	}
}

func TestTestEmailWithoutSMTP(t *testing.T) {
	s := adminServer(t)
	if res, body := post(t, s, "root", "/admin/settings/test-email", url.Values{"to": {"ops@example.com"}}); res.StatusCode != 502 || !strings.Contains(body, "not configured") {
		t.Errorf("send without SMTP: %d", res.StatusCode)
	}
}

func TestInvites(t *testing.T) {
	s := adminServer(t)
	res, body := post(t, s, "root", "/admin/invites", url.Values{"uses": {"0"}, "expires": {"7d"}})
	if res.StatusCode != 422 || !strings.Contains(body, "from 1 to 1000") {
		t.Errorf("bad uses: %d", res.StatusCode)
	}
	if res, _ := post(t, s, "root", "/admin/invites", url.Values{"uses": {"1"}, "expires": {"forever"}}); res.StatusCode != 422 {
		t.Errorf("bad expiry: %d", res.StatusCode)
	}
	res, body = post(t, s, "root", "/admin/invites", url.Values{"uses": {"5"}, "expires": {"30d"}, "role": {"admin"}, "email": {"x@example.com"}})
	if res.StatusCode != 201 || !strings.Contains(body, "/invite#t=") || !strings.Contains(body, "Copy the link now") {
		t.Fatalf("create: %d", res.StatusCode)
	}
	// Without SMTP the invite is not emailed, only shown.
	if strings.Contains(body, "and sent to") || len(s.Mail.Sent()) != 0 {
		t.Error("invite emailed without SMTP")
	}
	list, _ := s.App.Accounts.Store().ListInvites(context.Background(), 5)
	if len(list) != 1 || list[0].MaxUses != 5 || list[0].Role != auth.RoleAdmin || list[0].CreatedByName != "root" {
		t.Fatalf("stored %+v", list)
	}
	_, page := get(t, s, "root", "/admin/settings")
	if !strings.Contains(page, "x@example.com") || !strings.Contains(page, "0 / 5") || strings.Contains(page, "/invite#t=") {
		t.Error("invite list wrong, or the link is shown again")
	}
	if res, _ := post(t, s, "ann", "/admin/invites", url.Values{"uses": {"1"}, "expires": {"1d"}}); res.StatusCode != 403 {
		t.Errorf("non-admin create: %d", res.StatusCode)
	}
	if res, _ := post(t, s, "root", "/admin/invites/999/revoke", nil); res.StatusCode != 404 {
		t.Errorf("revoke unknown: %d", res.StatusCode)
	}
}
