package accounts_test

// Regression tests for the security review's accounts, SSO, admin and email
// findings. Each began as a proof of concept that passed while the bug
// existed; these pass only with the fix in place.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/oidctest"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// regJSON sends a same-origin JSON request with the browser's cookies.
func (b *browser) regJSON(method, path, body string) (int, string) {
	b.t.Helper()
	req := b.s.Request(b.t, method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, out := b.do(req)
	return res.StatusCode, out
}

// regBearer sends a request with only an API key (no cookies).
func regBearer(t *testing.T, s *testutil.Server, token, method, path, body string) (int, string) {
	t.Helper()
	req := s.Request(t, method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, out := s.Do(t, req) //nolint:bodyclose // Do reads and closes the body
	return res.StatusCode, out
}

// regMintKey creates an API key through the session-only keys API.
func regMintKey(t *testing.T, b *browser, scopes ...string) string {
	t.Helper()
	sc, _ := json.Marshal(scopes)
	code, body := b.regJSON("POST", "/api/v1/keys", `{"name":"backup","expires":"never","scopes":`+string(sc)+`}`)
	if code != http.StatusCreated {
		t.Fatalf("create key: %d %s", code, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Token == "" {
		t.Fatalf("key response: %s", body)
	}
	return out.Token
}

func regActiveAdmins(t *testing.T, s *testutil.Server) int {
	t.Helper()
	var n int
	if err := s.App.DB.R.QueryRow(`SELECT count(*) FROM users WHERE role = 'admin' AND status = 'active'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// keyWorks reports whether an API key still authenticates.
func keyWorks(t *testing.T, s *testutil.Server, token string) bool {
	t.Helper()
	code, _ := regBearer(t, s, token, "GET", "/api/v1/me", "")
	return code == http.StatusOK
}

// M4: a key minted by someone who held a session for a moment ends with the
// owner's recovery steps: a password reset, sign out everywhere, a password
// change (unless the owner unticks "Also revoke API keys"), and an admin
// disabling the account.
func TestKeysEndWithRecovery(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	u := createUser(t, s, "vic", "vic@example.com", auth.RoleUser)
	createUser(t, s, "root", "root@example.com", auth.RoleAdmin)
	attacker := newBrowser(t, s)
	attacker.mustLogin("vic")
	owner := newBrowser(t, s)

	t.Run("password reset", func(t *testing.T) {
		token := regMintKey(t, attacker, "items:read", "account:read")
		if res, _ := owner.post("/reset/request", url.Values{"email": {"vic@example.com"}}); res.StatusCode != http.StatusOK {
			t.Fatalf("reset request: %d", res.StatusCode)
		}
		tok := lastLink(t, s, "vic@example.com", "/reset")
		if res, _ := owner.post("/reset", url.Values{"token": {tok}, "password": {"brand new password 1"}}); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("reset: %d", res.StatusCode)
		}
		if keyWorks(t, s, token) {
			t.Fatal("key survived the password reset")
		}
		if attacker.whoami() != "" {
			t.Fatal("attacker session survived the reset")
		}
	})
	t.Run("sign out everywhere", func(t *testing.T) {
		attacker.login("vic", "brand new password 1")
		token := regMintKey(t, attacker, "account:read")
		if res, _ := owner.post("/settings/security/signout-all", nil); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("sign out everywhere: %d", res.StatusCode)
		}
		if keyWorks(t, s, token) {
			t.Fatal("key survived sign out everywhere")
		}
	})
	t.Run("password change", func(t *testing.T) {
		owner.login("vic", "brand new password 1")
		kept := regMintKey(t, owner, "account:read")
		if res, _ := owner.post("/settings/security/password", url.Values{"current": {"brand new password 1"}, "password": {"brand new password 2"}}); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("change without revoking: %d", res.StatusCode)
		}
		if !keyWorks(t, s, kept) {
			t.Fatal("unticked box revoked the keys")
		}
		_, page := owner.get("/settings/security")
		if !strings.Contains(page, `name="revoke_keys" value="1" checked`) {
			t.Error(`"Also revoke API keys" is not offered checked`)
		}
		res, _ := owner.post("/settings/security/password", url.Values{"current": {"brand new password 2"}, "password": {"brand new password 3"}, "revoke_keys": {"1"}})
		if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "password_keys") {
			t.Fatalf("change and revoke: %d", res.StatusCode)
		}
		if keyWorks(t, s, kept) {
			t.Fatal("key survived the password change with revoke_keys")
		}
	})
	t.Run("disabled by an admin", func(t *testing.T) {
		token := regMintKey(t, owner, "account:read")
		root := newBrowser(t, s)
		root.mustLogin("root")
		if res, _ := root.post(fmt.Sprintf("/admin/users/%d/status", u.ID), url.Values{"status": {"disabled"}}); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("disable: %d", res.StatusCode)
		}
		if res, _ := root.post(fmt.Sprintf("/admin/users/%d/status", u.ID), url.Values{"status": {"active"}}); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("enable: %d", res.StatusCode)
		}
		if keyWorks(t, s, token) {
			t.Fatal("key came back when the account was enabled again")
		}
	})
	if n := countAudit(t, s, "key.revoke_all"); n != 4 {
		t.Errorf("key.revoke_all audited %d times, want 4", n)
	}
}

// L3: an admin key cannot turn itself into a browser admin session: it never
// gets set-password or invite links, and can't give the admin role.
func TestAdminKeyCannotMintBrowserSession(t *testing.T) {
	t.Run("no mail", func(t *testing.T) {
		s := newServer(t)
		createUser(t, s, "root", "", auth.RoleAdmin)
		b := newBrowser(t, s)
		b.mustLogin("root")
		key := regMintKey(t, b, "admin")
		for _, body := range []string{`{"username":"shadow","role":"admin"}`, `{"username":"shadow"}`} {
			code, out := regBearer(t, s, key, "POST", "/api/v1/admin/users", body)
			if code != http.StatusForbidden || strings.Contains(out, "#t=") {
				t.Fatalf("create %s via key: %d %s", body, code, out)
			}
		}
		if _, err := s.App.Accounts.Store().UserByUsername(context.Background(), "shadow"); err == nil {
			t.Fatal("account created although its link could not be given")
		}
		// An existing account: no reset link either, from the API or the pages.
		bob := createUser(t, s, "bob", "", auth.RoleUser)
		if code, out := regBearer(t, s, key, "POST", fmt.Sprintf("/api/v1/admin/users/%d/reset", bob.ID), ""); code != http.StatusForbidden || strings.Contains(out, "#t=") {
			t.Fatalf("reset via key: %d %s", code, out)
		}
		req := s.Request(t, "POST", fmt.Sprintf("/admin/users/%d/reset", bob.ID), nil)
		req.Header.Set("Authorization", "Bearer "+key)
		if res, out := s.Do(t, req); res.StatusCode != http.StatusForbidden || strings.Contains(out, "#t=") { //nolint:bodyclose // Do reads and closes the body
			t.Fatalf("reset page via key: %d", res.StatusCode)
		}
		// Nor the admin role, nor an invite link.
		if code, _ := regBearer(t, s, key, "PATCH", fmt.Sprintf("/api/v1/admin/users/%d", bob.ID), `{"role":"admin"}`); code != http.StatusForbidden {
			t.Fatalf("promote via key: %d", code)
		}
		req = s.Request(t, "POST", "/admin/invites", strings.NewReader(url.Values{"uses": {"1"}, "expires": {"1d"}, "role": {"user"}}.Encode()))
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if res, out := s.Do(t, req); res.StatusCode != http.StatusForbidden || strings.Contains(out, "#t=") { //nolint:bodyclose // Do reads and closes the body
			t.Fatalf("invite via key: %d", res.StatusCode)
		}
		// The browser session still gets links.
		code, out := b.regJSON("POST", "/api/v1/admin/users", `{"username":"withlink"}`)
		if code != http.StatusCreated || !strings.Contains(out, "#t=") {
			t.Fatalf("create via session: %d %s", code, out)
		}
	})
	t.Run("with mail", func(t *testing.T) {
		s := newServer(t, testutil.WithSMTP())
		createUser(t, s, "root", "root@example.com", auth.RoleAdmin)
		b := newBrowser(t, s)
		b.mustLogin("root")
		key := regMintKey(t, b, "admin")
		code, out := regBearer(t, s, key, "POST", "/api/v1/admin/users", `{"username":"newbie","email":"newbie@example.com"}`)
		if code != http.StatusCreated || strings.Contains(out, "#t=") || !strings.Contains(out, `"emailed":true`) {
			t.Fatalf("create via key with mail: %d %s", code, out)
		}
		lastLink(t, s, "newbie@example.com", "/reset")
	})
}

// L4: the SSO role mapping never demotes the last active admin.
func TestSSOMappingKeepsLastAdmin(t *testing.T) {
	s, p := ssoServer(t, testutil.Env("OIDC_ADMIN_GROUPS", "snap-admins"))
	createUser(t, s, "root", "root@example.com", auth.RoleAdmin)
	b := newBrowser(t, s)
	b.mustLogin("root")
	p.SetUser(oidctest.User{Subject: "9", PreferredUsername: "root", Email: "root@example.com", EmailVerified: true, Groups: []string{"staff"}})
	if res, _ := b.ssoLink(); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("link: %d", res.StatusCode)
	}
	b2 := newBrowser(t, s)
	if res, _ := b2.ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sso sign-in: %d", res.StatusCode)
	}
	if n := regActiveAdmins(t, s); n != 1 {
		t.Fatalf("%d active admins after the SSO sign-in, want 1", n)
	}
	if res, _ := b2.get("/admin"); res.StatusCode != http.StatusOK {
		t.Fatalf("the kept admin on /admin: %d", res.StatusCode)
	}
}

// L5: removal at the provider ends admin rights: a refused SSO sign-in
// demotes and ends the sessions and admin keys, and SSO-mapped admin rights
// on a session last a day from the last SSO sign-in.
func TestIdPRemovalEndsAdminAccess(t *testing.T) {
	s, p := ssoServer(t, testutil.Env("OIDC_ADMIN_GROUPS", "snap-admins"), testutil.Env("OIDC_ALLOWED_GROUPS", "staff"))
	createUser(t, s, "root", "root@example.com", auth.RoleAdmin) // so the mapping is not the only admin
	p.SetUser(oidctest.User{Subject: "5", PreferredUsername: "boss", Email: "boss@example.com", EmailVerified: true, Groups: []string{"staff", "snap-admins"}})
	b := newBrowser(t, s)
	if res, _ := b.ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sso: %d", res.StatusCode)
	}
	key := regMintKey(t, b, "admin")
	userKey := regMintKey(t, b, "account:read")
	if code, _ := regBearer(t, s, key, "GET", "/api/v1/admin/users", ""); code != http.StatusOK {
		t.Fatalf("admin key before: %d", code)
	}

	// The provider removes boss from every group: SSO sign-in is refused, and
	// that ends what boss still holds.
	p.SetUser(oidctest.User{Subject: "5", PreferredUsername: "boss", Email: "boss@example.com", EmailVerified: true, Groups: []string{}})
	if res, _ := newBrowser(t, s).ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("sso after removal: %d", res.StatusCode)
	}
	if code, _ := regBearer(t, s, key, "GET", "/api/v1/admin/users", ""); code == http.StatusOK {
		t.Fatal("admin key kept admin rights")
	}
	if res, _ := b.get("/admin"); res.StatusCode == http.StatusOK {
		t.Fatal("session kept admin rights")
	}
	if b.whoami() != "" {
		t.Fatal("session survived the refused sign-in")
	}
	if !keyWorks(t, s, userKey) {
		t.Error("a key without the admin scope was revoked too")
	}
}

func TestSSOAdminSessionsNeedRecentSSO(t *testing.T) {
	s, p := ssoServer(t, testutil.Env("OIDC_ADMIN_GROUPS", "snap-admins"))
	createUser(t, s, "root", "root@example.com", auth.RoleAdmin)
	p.SetUser(oidctest.User{Subject: "6", PreferredUsername: "op", Email: "op@example.com", EmailVerified: true, Groups: []string{"snap-admins"}})
	b := newBrowser(t, s)
	b.ssoFlow("/auth/oidc/start")
	if res, _ := b.get("/admin"); res.StatusCode != http.StatusOK {
		t.Fatalf("fresh SSO admin: %d", res.StatusCode)
	}
	// A day later the session still works, but not as an admin.
	if _, err := s.App.DB.W.Exec(`UPDATE sessions SET sso_at = sso_at - ? WHERE sso_at IS NOT NULL`, (accounts.SSOAdminMaxAge + time.Minute).Milliseconds()); err != nil {
		t.Fatal(err)
	}
	if res, _ := b.get("/admin"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("day-old SSO admin session on /admin: %d", res.StatusCode)
	}
	if b.whoami() != "op" {
		t.Fatal("day-old session signed out")
	}
	// Signing in with SSO again re-reads the mapping and restores it.
	b.ssoFlow("/auth/oidc/start")
	if res, _ := b.get("/admin"); res.StatusCode != http.StatusOK {
		t.Fatalf("after a fresh SSO sign-in: %d", res.StatusCode)
	}
	// A local admin without an identity is not affected.
	r := newBrowser(t, s)
	r.mustLogin("root")
	if res, _ := r.get("/admin"); res.StatusCode != http.StatusOK {
		t.Fatalf("local admin: %d", res.StatusCode)
	}
}

// M5: account pre-hijacking. Opening a verify link verifies nothing until
// Confirm is clicked, and SSO never links by email to an account with a
// password, even a verified one.
func TestPreHijackViaVerifyAndLinkByEmail(t *testing.T) {
	s, p := ssoServer(t, testutil.WithSMTP(), testutil.Env("OIDC_LINK_BY_EMAIL", "true"))
	if err := accounts.SignupMode.Set(context.Background(), s.App.Settings, accounts.SignupOpen); err != nil {
		t.Fatal(err)
	}
	attacker := newBrowser(t, s)
	res, body := attacker.post("/register", url.Values{"username": {"mallory"}, "email": {"victim@corp.example"}, "password": {pw}})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("register: %d %s", res.StatusCode, body)
	}
	tok := lastLink(t, s, "victim@corp.example", "/verify")
	// The mail scanner opens the page: the page only asks.
	scanner := newBrowser(t, s)
	if _, page := scanner.get("/verify"); !strings.Contains(page, "data-confirm") || !strings.Contains(page, `data-mode="confirm"`) {
		t.Fatal("verify page has no Confirm step")
	}
	if code, out := scanner.postJSON("/verify", url.Values{"token": {tok}}); code != http.StatusOK || out["confirm"] != "1" || out["username"] != "mallory" {
		t.Fatalf("check: %d %v", code, out)
	}
	u, _ := s.App.Accounts.Store().UserByUsername(context.Background(), "mallory")
	if u.EmailVerifiedAt != nil {
		t.Fatal("opening the link verified the address")
	}
	// Even a careless click on Confirm does not hand over the victim's SSO.
	if code, _ := scanner.postJSON("/verify", url.Values{"token": {tok}, "confirm": {"1"}}); code != http.StatusOK {
		t.Fatalf("confirm: %d", code)
	}
	p.SetUser(oidctest.User{Subject: "777", PreferredUsername: "victim", Email: "victim@corp.example", EmailVerified: true})
	victim := newBrowser(t, s)
	res, _ = victim.ssoFlow("/auth/oidc/start")
	if who := victim.whoami(); who == "mallory" {
		t.Fatal("the victim's SSO identity was linked to the attacker's account")
	}
	if res.StatusCode != http.StatusConflict {
		t.Errorf("victim sso: %d, want 409", res.StatusCode)
	}
	if ids := identities(t, s, "mallory"); len(ids) != 0 {
		t.Errorf("mallory got identities: %+v", ids)
	}
}

// L6: the profile form says nothing about which addresses have accounts,
// and spends the mail limit before looking.
func TestProfileDoesNotEnumerateEmails(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	createUser(t, s, "victim", "secret.person@example.org", auth.RoleUser)
	createUser(t, s, "mallory", "m@example.net", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("mallory")
	probe := func(addr string) (int, string) {
		res, body := b.post("/settings/profile", url.Values{"username": {"mallory"}, "email": {addr}, "current": {pw}})
		return res.StatusCode, res.Header.Get("Location") + body
	}
	taken, _ := probe("secret.person@example.org")
	free, _ := probe("nobody@example.org")
	if taken != free || taken != http.StatusSeeOther {
		t.Fatalf("taken %d, free %d", taken, free)
	}
	for range 2 {
		probe("nobody2@example.org")
	}
	// The limit is spent: both answer alike, without a hint.
	ct, bt := probe("secret.person@example.org")
	cf, bf := probe("nobody3@example.org")
	if ct != cf || strings.Contains(bt, "another account") || strings.Contains(bf, "another account") || !strings.Contains(bt, "Too many emails") {
		t.Fatalf("after the limit: taken %d, free %d", ct, cf)
	}
}

// L7: "Send link" mail carries no text the sender chose (title, link target).
func TestSendLinkHasNoSenderText(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	createUser(t, s, "IT-Helpdesk", "mallory@example.net", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("IT-Helpdesk")
	title := "your mailbox is full. Keep your mail: sign in at https://login.evil.example/owa within 24 hours"
	code, body := b.regJSON("POST", "/api/v1/links", `{"url":"https://login.evil.example/owa","title":"`+title+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("link: %d %s", code, body)
	}
	var it struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal([]byte(body), &it)
	code, body = b.regJSON("POST", "/api/v1/items/bulk", fmt.Sprintf(`{"ids":[%d],"action":"send_email","params":{"to":["ceo@victim.example"]}}`, it.ID))
	if code != http.StatusOK {
		t.Fatalf("send: %d %s", code, body)
	}
	var got string
	for _, m := range s.Mail.Sent() {
		if len(m.To) == 1 && m.To[0] == "ceo@victim.example" {
			got = "Subject: " + m.Subject + "\n\n" + m.Text + "\n" + m.HTML
		}
	}
	if got == "" {
		t.Fatal("no mail")
	}
	for _, bad := range []string{"login.evil.example", "mailbox is full", "24 hours"} {
		if strings.Contains(got, bad) {
			t.Errorf("mail carries %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "IT-Helpdesk shared a link with you on") || !strings.Contains(got, "link: ") || !strings.Contains(got, "/"+it.Slug) {
		t.Errorf("mail:\n%s", got)
	}
}

// L8: changing the password rotates this browser's session, so a stolen
// copy of its cookie stops working.
func TestPasswordChangeRotatesSession(t *testing.T) {
	s := newServer(t)
	createUser(t, s, "vic", "vic@example.com", auth.RoleUser)
	owner := newBrowser(t, s)
	owner.mustLogin("vic")
	stolen := owner.cookie("__Host-snap_session")

	thief := newBrowser(t, s)
	u, _ := url.Parse(s.URL)
	thief.c.Jar.SetCookies(u, []*http.Cookie{{Name: "__Host-snap_session", Value: stolen, Path: "/", Secure: true}})
	if thief.whoami() != "vic" {
		t.Fatal("setup: stolen cookie does not work")
	}
	if res, _ := owner.post("/settings/security/password", url.Values{"current": {pw}, "password": {"a different long password"}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("change password: %d", res.StatusCode)
	}
	if owner.cookie("__Host-snap_session") == stolen {
		t.Fatal("session id not rotated")
	}
	if thief.whoami() != "" {
		t.Fatal("stolen cookie still works")
	}
	if owner.whoami() != "vic" {
		t.Fatal("owner signed out by the change")
	}
}

// L9: sensitive changes need the current password or, for an account
// without one, a sign-in from the last 10 minutes.
func TestSSOOnlyAccountNeedsRecentSignInForPassword(t *testing.T) {
	s, p := ssoServer(t)
	p.SetUser(oidctest.User{Subject: "31", PreferredUsername: "sso-only", Email: "sso@example.com", EmailVerified: true, Groups: []string{"staff"}})
	b := newBrowser(t, s)
	if res, _ := b.ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sso: %d", res.StatusCode)
	}
	// An old session (a stolen cookie, an unlocked laptop) can't add a password.
	if _, err := s.App.DB.W.Exec(`UPDATE sessions SET auth_at = auth_at - ?`, (accounts.ReauthWindow + time.Minute).Milliseconds()); err != nil {
		t.Fatal(err)
	}
	res, body := b.post("/settings/security/password", url.Values{"password": {"attacker chosen password"}})
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Sign in with Gitea again") {
		t.Fatalf("set password from an old session: %d", res.StatusCode)
	}
	if res := newBrowser(t, s).login("sso-only", "attacker chosen password"); res.StatusCode == http.StatusSeeOther {
		t.Fatal("the password was set")
	}
	// Right after an SSO sign-in it works.
	b.ssoFlow("/auth/oidc/start")
	if res, _ := b.post("/settings/security/password", url.Values{"password": {"owner chosen password"}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("set password after a fresh sign-in: %d", res.StatusCode)
	}
}

func TestUnlinkNeedsCurrentPassword(t *testing.T) {
	s, p := ssoServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	p.SetUser(oidctest.User{Subject: "7", PreferredUsername: "annie", Email: "other@example.com", EmailVerified: true})
	if res, _ := b.ssoLink(); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("link: %d", res.StatusCode)
	}
	id := identities(t, s, "ann")[0].ID
	path := fmt.Sprintf("/settings/security/identities/%d/unlink", id)
	if res, _ := b.post(path, nil); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unlink without the password: %d", res.StatusCode)
	}
	if res, _ := b.post(path, url.Values{"current": {"wrong password!"}}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unlink with a wrong password: %d", res.StatusCode)
	}
	if len(identities(t, s, "ann")) != 1 {
		t.Fatal("unlinked without the password")
	}
	if res, _ := b.post(path, url.Values{"current": {pw}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("unlink: %d", res.StatusCode)
	}
	if len(identities(t, s, "ann")) != 0 {
		t.Fatal("not unlinked")
	}
}

// L10: invites stop working once their creator is disabled or no longer an
// admin.
func TestInvitesOfDisabledOrDemotedAdminsStopWorking(t *testing.T) {
	for _, change := range []string{"disabled", "demoted"} {
		t.Run(change, func(t *testing.T) {
			s := newServer(t)
			ctx := context.Background()
			rogue := createUser(t, s, "rogue", "rogue@example.com", auth.RoleAdmin)
			createUser(t, s, "root", "root@example.com", auth.RoleAdmin)
			raw, _, err := s.App.Accounts.Store().CreateInvite(ctx, accounts.NewInvite{
				CreatedBy: rogue.ID, Role: auth.RoleAdmin, MaxUses: 1000, TTL: accounts.MaxInviteTTL,
			}, accounts.AuditEntry{ActorUserID: rogue.ID})
			if err != nil {
				t.Fatal(err)
			}
			// While the creator is an active admin, the invite works.
			ok := newBrowser(t, s)
			if code, out := ok.postJSON("/invite", url.Values{"token": {raw}}); code != http.StatusOK {
				t.Fatalf("invite: %d %v", code, out)
			}
			st := s.App.Accounts.Store()
			if change == "disabled" {
				err = st.SetStatusChecked(ctx, rogue.ID, accounts.StatusDisabled, accounts.AuditEntry{})
			} else {
				err = st.SetRoleChecked(ctx, rogue.ID, auth.RoleUser, accounts.AuditEntry{})
			}
			if err != nil {
				t.Fatal(err)
			}
			b := newBrowser(t, s)
			if code, _ := b.postJSON("/invite", url.Values{"token": {raw}}); code != http.StatusNotFound {
				t.Fatalf("invite of a %s admin: %d", change, code)
			}
			// A browser that accepted it earlier can't register with it now.
			if res, _ := ok.post("/register", url.Values{"username": {"rogue2"}, "password": {pw}}); res.StatusCode == http.StatusSeeOther {
				t.Fatal("registered through the invite")
			}
			if _, err := st.UserByUsername(ctx, "rogue2"); err == nil {
				t.Fatal("account created")
			}
		})
	}
}

var _ = regexp.MustCompile
