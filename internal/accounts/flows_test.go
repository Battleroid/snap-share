package accounts_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

const sessionCookie = "__Host-snap_session"

func TestLoginLogout(t *testing.T) {
	s := newServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)

	for _, c := range []struct{ login, password string }{{"ann", "wrong password!"}, {"nobody", pw}, {"ann", ""}} {
		res, body := b.post("/login", url.Values{"login": {c.login}, "password": {c.password}})
		if res.StatusCode == http.StatusSeeOther || !strings.Contains(body, "Wrong username or password.") {
			t.Errorf("%q/%q: %d", c.login, c.password, res.StatusCode)
		}
	}
	if b.cookie(sessionCookie) != "" {
		t.Fatal("cookie set by a failed login")
	}

	res, _ := b.post("/login", url.Values{"login": {"ann"}, "password": {pw}, "next": {"/settings/security"}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/security" {
		t.Fatalf("login: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	var set *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			set = c
		}
	}
	if set == nil || !set.Secure || !set.HttpOnly || set.SameSite != http.SameSiteLaxMode || set.Path != "/" || set.Domain != "" {
		t.Fatalf("session cookie attributes: %+v", set)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Error("login response is cacheable")
	}
	if b.whoami() != "ann" {
		t.Fatal("not signed in")
	}
	first := b.cookie(sessionCookie)

	// Email works as the login too, and every sign-in gets a new session id.
	if res := b.login("ANN@example.com", pw); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("email login: %d", res.StatusCode)
	}
	second := b.cookie(sessionCookie)
	if second == first || second == "" {
		t.Fatal("session id not rotated on login")
	}
	old := newBrowser(t, s)
	u, _ := url.Parse(s.URL)
	old.c.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: first, Path: "/"}})
	if old.whoami() != "" {
		t.Error("the replaced session still works")
	}

	// Unsafe next values fall back to /.
	for _, next := range []string{"https://evil.example/", "//evil.example", "/\\evil"} {
		res, _ := b.post("/login", url.Values{"login": {"ann"}, "password": {pw}, "next": {next}})
		if loc := res.Header.Get("Location"); loc != "/" {
			t.Errorf("next %q redirected to %q", next, loc)
		}
	}

	res, _ = b.post("/logout", nil)
	if res.StatusCode != http.StatusSeeOther || b.cookie(sessionCookie) != "" {
		t.Fatalf("logout: %d, cookie %q", res.StatusCode, b.cookie(sessionCookie))
	}
	if b.whoami() != "" {
		t.Error("still signed in after logout")
	}
	if n := countAudit(t, s, "auth.login"); n < 2 {
		t.Errorf("login audit rows: %d", n)
	}
	if n := countAudit(t, s, "auth.login_failed"); n != 2 { // the empty password never reaches a check
		t.Errorf("failed login audit rows: %d", n)
	}
}

func TestCrossSitePostsAreRefused(t *testing.T) {
	s := newServer(t)
	createUser(t, s, "ann", "", auth.RoleUser)
	b := newBrowser(t, s)
	crossSite := func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "https://evil.example")
	}
	if res, _ := b.post("/login", url.Values{"login": {"ann"}, "password": {pw}}, crossSite); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site login: %d", res.StatusCode)
	}
	if b.cookie(sessionCookie) != "" {
		t.Fatal("cross-site login signed in")
	}
	b.mustLogin("ann")
	if res, _ := b.post("/logout", nil, crossSite); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site logout: %d", res.StatusCode)
	}
	if res, _ := b.post("/settings/security/signout-all", nil, crossSite); res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site sign out everywhere: %d", res.StatusCode)
	}
	if b.whoami() != "ann" {
		t.Error("cross-site POST ended the session")
	}
}

func TestLoginRateLimits(t *testing.T) {
	t.Run("per name", func(t *testing.T) {
		s := newServer(t)
		createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
		b := newBrowser(t, s)
		for i := range 5 {
			// Case and spaces don't make a new budget.
			login := []string{"ann", "Ann", " ANN ", "ann", "aNn"}[i]
			if res := b.login(login, "wrong password!"); res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("attempt %d: %d", i, res.StatusCode)
			}
		}
		// Limited before the password is checked: even the right one is refused.
		res, body := b.post("/login", url.Values{"login": {"ann"}, "password": {pw}})
		if res.StatusCode != http.StatusTooManyRequests || !strings.Contains(body, "Too many attempts") {
			t.Fatalf("after 5 failures: %d", res.StatusCode)
		}
		// The address has its own budget, so a locked username says nothing
		// about which address is ann's.
		if res := b.login("ann@example.com", pw); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("email after the username was locked: %d", res.StatusCode)
		}
	})
	t.Run("per address", func(t *testing.T) {
		s := newServer(t)
		b := newBrowser(t, s)
		for i := range 10 {
			if res := b.login("user"+string(rune('a'+i)), "wrong password!"); res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("attempt %d: %d", i, res.StatusCode)
			}
		}
		if res := b.login("someone", "wrong password!"); res.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("11th attempt from one address: %d", res.StatusCode)
		}
	})
	t.Run("exempt address", func(t *testing.T) {
		s := newServer(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1,::1"))
		b := newBrowser(t, s)
		for i := range 12 {
			if res := b.login("user"+string(rune('a'+i)), "wrong password!"); res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("attempt %d: %d", i, res.StatusCode)
			}
		}
	})
}

func TestPasswordLoginOff(t *testing.T) {
	s := newServer(t)
	createUser(t, s, "ann", "", auth.RoleUser)
	if err := accounts.PasswordLogin.Set(context.Background(), s.App.Settings, false); err != nil {
		t.Fatal(err)
	}
	b := newBrowser(t, s)
	if res := b.login("ann", pw); res.StatusCode != http.StatusForbidden {
		t.Errorf("password login while off: %d", res.StatusCode)
	}
	_, body := b.get("/login")
	if strings.Contains(body, `action="/login"`) {
		t.Error("login form shown while password sign-in is off")
	}
}

func TestSignupModes(t *testing.T) {
	register := func(b *browser, name, email string) (*response, string) {
		return b.post("/register", url.Values{"username": {name}, "email": {email}, "password": {pw}})
	}
	t.Run("invite only by default", func(t *testing.T) {
		s := newServer(t, testutil.WithSMTP())
		b := newBrowser(t, s)
		if res, _ := b.get("/register"); res.StatusCode != http.StatusForbidden {
			t.Errorf("GET /register: %d", res.StatusCode)
		}
		if res, _ := register(b, "eve", "eve@example.com"); res.StatusCode != http.StatusForbidden {
			t.Errorf("POST /register: %d", res.StatusCode)
		}
		if _, body := b.get("/login"); strings.Contains(body, `href="/register"`) {
			t.Error("sign-up link shown")
		}
	})
	t.Run("open needs smtp", func(t *testing.T) {
		s := newServer(t)
		_ = accounts.SignupMode.Set(context.Background(), s.App.Settings, accounts.SignupOpen)
		b := newBrowser(t, s)
		if res, _ := register(b, "eve", "eve@example.com"); res.StatusCode != http.StatusForbidden {
			t.Errorf("open sign-up without SMTP: %d", res.StatusCode)
		}
	})
	t.Run("open", func(t *testing.T) {
		s := newServer(t, testutil.WithSMTP(), testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1,::1"))
		_ = accounts.SignupMode.Set(context.Background(), s.App.Settings, accounts.SignupOpen)
		createUser(t, s, "taken", "taken@example.com", auth.RoleUser)
		b := newBrowser(t, s)
		if _, body := b.get("/login"); !strings.Contains(body, `href="/register"`) {
			t.Error("no sign-up link")
		}
		for _, c := range []struct{ name, email, field string }{
			{"taken", "eve@example.com", "That username is taken."},
			{"eve", "taken@example.com", "That address belongs to another account."},
			{"eve", "", "Enter your email address."},
			{"-eve", "eve@example.com", "Use 1–32 letters"},
		} {
			res, body := register(b, c.name, c.email)
			if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, c.field) {
				t.Errorf("%s/%s: %d", c.name, c.email, res.StatusCode)
			}
		}
		if res, body := b.post("/register", url.Values{"username": {"eve"}, "email": {"eve@example.com"}, "password": {"short"}}); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "at least 10") {
			t.Errorf("short password: %d", res.StatusCode)
		}

		res, _ := register(b, "eve", "eve@example.com")
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("register: %d", res.StatusCode)
		}
		// Signed in, but unverified: the principal says so and the profile shows a notice.
		_, body := b.get("/settings/profile")
		if !strings.Contains(body, "Verify eve@example.com") {
			t.Error("no verification notice")
		}
		p, _, _ := s.App.Accounts.Authenticate(b.requestWithCookies("/"))
		if p == nil || p.EmailVerified {
			t.Fatalf("principal %+v", p)
		}

		token := lastLink(t, s, "eve@example.com", "/verify")
		v := newBrowser(t, s) // the link may open in another browser
		// Opening it (a mail scanner does) verifies nothing.
		if code, out := v.postJSON("/verify", url.Values{"token": {token}}); code != http.StatusOK || out["confirm"] != "1" || out["username"] != "eve" || out["email"] != "eve@example.com" {
			t.Fatalf("check: %d %v", code, out)
		}
		if p, _, _ := s.App.Accounts.Authenticate(b.requestWithCookies("/")); p == nil || p.EmailVerified {
			t.Fatal("verified without Confirm")
		}
		if code, out := v.postJSON("/verify", url.Values{"token": {token}, "confirm": {"1"}}); code != http.StatusOK || !strings.Contains(out["message"], "verified") {
			t.Fatalf("verify: %d %v", code, out)
		}
		for _, confirm := range []string{"", "1"} {
			if code, out := v.postJSON("/verify", url.Values{"token": {token}, "confirm": {confirm}}); code != http.StatusNotFound || out["error"] == "" {
				t.Errorf("reused verify link (confirm=%q): %d %v", confirm, code, out)
			}
		}
		p, _, _ = s.App.Accounts.Authenticate(b.requestWithCookies("/"))
		if p == nil || !p.EmailVerified {
			t.Fatalf("still unverified: %+v", p)
		}
	})
	t.Run("rate limited", func(t *testing.T) {
		s := newServer(t, testutil.WithSMTP())
		_ = accounts.SignupMode.Set(context.Background(), s.App.Settings, accounts.SignupOpen)
		b := newBrowser(t, s)
		for i := range 5 {
			if res, _ := register(b, "-bad", "x@example.com"); res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("attempt %d: %d", i, res.StatusCode)
			}
		}
		if res, _ := register(b, "eve", "eve@example.com"); res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
			t.Errorf("6th sign-up in an hour: %d", res.StatusCode)
		}
		for range 5 {
			b.post("/reset/request", url.Values{"email": {"x@example.com"}})
		}
		if res, _ := b.post("/reset/request", url.Values{"email": {"x@example.com"}}); res.StatusCode != http.StatusTooManyRequests {
			t.Errorf("6th reset request: %d", res.StatusCode)
		}
		for range 10 { // two requests per link (check, Confirm)
			b.postJSON("/verify", url.Values{"token": {"x"}})
		}
		if code, _ := b.postJSON("/verify", url.Values{"token": {"x"}}); code != http.StatusTooManyRequests {
			t.Errorf("11th verify: %d", code)
		}
		for range 5 {
			b.postJSON("/invite", url.Values{"token": {"x"}})
		}
		if code, _ := b.postJSON("/invite", url.Values{"token": {"x"}}); code != http.StatusTooManyRequests {
			t.Errorf("6th invite check: %d", code)
		}
	})
	t.Run("closed refuses invites", func(t *testing.T) {
		s := newServer(t)
		raw, _, err := s.App.Accounts.Store().CreateInvite(context.Background(), accounts.NewInvite{MaxUses: 1, TTL: time.Hour}, accounts.CLIActor())
		if err != nil {
			t.Fatal(err)
		}
		_ = accounts.SignupMode.Set(context.Background(), s.App.Settings, accounts.SignupClosed)
		b := newBrowser(t, s)
		if code, _ := b.postJSON("/invite", url.Values{"token": {raw}}); code != http.StatusForbidden {
			t.Errorf("invite while closed: %d", code)
		}
	})
}

// requestWithCookies builds a request carrying the browser's cookies (to call
// the Authenticator directly).
func (b *browser) requestWithCookies(path string) *http.Request {
	req := b.s.Request(b.t, "GET", path, nil)
	u, _ := url.Parse(b.s.URL)
	for _, c := range b.c.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	return req
}

func TestInviteFlow(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	createUser(t, s, "root", "root@example.com", auth.RoleAdmin)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	admin := newBrowser(t, s)
	admin.mustLogin("root")

	// Regular users cannot reach the admin page.
	user := newBrowser(t, s)
	user.mustLogin("ann")
	if res, _ := user.get("/admin/settings"); res.StatusCode != http.StatusForbidden {
		t.Errorf("user on admin page: %d", res.StatusCode)
	}

	res, body := admin.post("/admin/invites", url.Values{"email": {"new@example.com"}, "uses": {"1"}, "expires": {"7d"}, "role": {"user"}})
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "Invite created and sent to new@example.com") {
		t.Fatalf("create invite: %d", res.StatusCode)
	}
	if countAudit(t, s, "invite.create") != 1 {
		t.Error("invite creation not audited")
	}
	token := lastLink(t, s, "new@example.com", "/invite")
	if !strings.Contains(body, token) {
		t.Error("link not shown to the admin")
	}

	guest := newBrowser(t, s)
	if res, body := guest.get("/invite"); res.StatusCode != http.StatusOK || !strings.Contains(body, "token-page") {
		t.Fatalf("invite page: %d", res.StatusCode)
	}
	code, out := guest.postJSON("/invite", url.Values{"token": {token}})
	if code != http.StatusOK || out["redirect"] != "/register" {
		t.Fatalf("accept invite: %d %v", code, out)
	}
	res, body = guest.get("/register")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "invited") || !strings.Contains(body, `value="new@example.com"`) {
		t.Fatalf("register page with invite: %d", res.StatusCode)
	}
	res, _ = guest.post("/register", url.Values{"username": {"newbie"}, "email": {"new@example.com"}, "password": {pw}})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("register with invite: %d", res.StatusCode)
	}
	if guest.whoami() != "newbie" {
		t.Fatal("not signed in after registering")
	}
	u, err := s.App.Accounts.Store().UserByUsername(context.Background(), "newbie")
	if err != nil || u.EmailVerifiedAt == nil || u.Email != "new@example.com" {
		t.Fatalf("invited address should be trusted: %+v %v", u, err)
	}

	// Used up.
	other := newBrowser(t, s)
	if code, _ := other.postJSON("/invite", url.Values{"token": {token}}); code != http.StatusNotFound {
		t.Errorf("used invite accepted: %d", code)
	}
	if res, _ := other.get("/register"); res.StatusCode != http.StatusForbidden {
		t.Errorf("register without invite: %d", res.StatusCode)
	}

	// A second invite, revoked before use.
	_, body = admin.post("/admin/invites", url.Values{"uses": {"3"}, "expires": {"1d"}, "role": {"admin"}})
	m := linkRE.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no link on the page")
	}
	invites, _ := s.App.Accounts.Store().ListInvites(context.Background(), 10)
	if res, _ := admin.post("/admin/invites/"+itoa(invites[0].ID)+"/revoke", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if code, _ := other.postJSON("/invite", url.Values{"token": {m[1]}}); code != http.StatusNotFound {
		t.Errorf("revoked invite accepted: %d", code)
	}
	if countAudit(t, s, "invite.revoke") != 1 {
		t.Error("revoke not audited")
	}

	// An invitee who picks another address gets it only after confirming it.
	raw, _, _ := s.App.Accounts.Store().CreateInvite(context.Background(), accounts.NewInvite{MaxUses: 1, TTL: time.Hour, Email: "given@example.com"}, accounts.CLIActor())
	g2 := newBrowser(t, s)
	g2.postJSON("/invite", url.Values{"token": {raw}})
	if res, _ := g2.post("/register", url.Values{"username": {"chooser"}, "email": {"mine@example.com"}, "password": {pw}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("register: %d", res.StatusCode)
	}
	u, _ = s.App.Accounts.Store().UserByUsername(context.Background(), "chooser")
	if u.Email != "" || !u.EmailVerified() {
		t.Fatalf("unconfirmed address applied: %+v", u)
	}
	confirm := lastLink(t, s, "mine@example.com", "/verify")
	if code, _ := g2.postJSON("/verify", url.Values{"token": {confirm}, "confirm": {"1"}}); code != http.StatusOK {
		t.Fatalf("confirm: %d", code)
	}
	u, _ = s.App.Accounts.Store().UserByUsername(context.Background(), "chooser")
	if u.Email != "mine@example.com" || u.EmailVerifiedAt == nil {
		t.Fatalf("confirmed address not applied: %+v", u)
	}
}

func TestInviteWithoutSMTP(t *testing.T) {
	s := newServer(t)
	raw, _, _ := s.App.Accounts.Store().CreateInvite(context.Background(), accounts.NewInvite{MaxUses: 1, TTL: time.Hour, Email: "x@example.com", Role: auth.RoleAdmin}, accounts.CLIActor())
	b := newBrowser(t, s)
	b.postJSON("/invite", url.Values{"token": {raw}})
	res, body := b.get("/register")
	if res.StatusCode != http.StatusOK || strings.Contains(body, `name="email"`) {
		t.Fatalf("register page: %d (email field shown without SMTP: %v)", res.StatusCode, strings.Contains(body, `name="email"`))
	}
	if res, _ := b.post("/register", url.Values{"username": {"x"}, "password": {pw}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("register: %d", res.StatusCode)
	}
	u, _ := s.App.Accounts.Store().UserByUsername(context.Background(), "x")
	if u.Role != auth.RoleAdmin || u.Email != "x@example.com" || !u.EmailVerified() {
		t.Errorf("invited user %+v", u)
	}
	if len(s.Mail.Sent()) != 0 {
		t.Error("mail sent without SMTP")
	}
}

func TestResetFlow(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	other := newBrowser(t, s)
	other.mustLogin("ann")

	b := newBrowser(t, s)
	res, body := b.post("/reset/request", url.Values{"email": {"nobody@example.com"}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Check your inbox") {
		t.Fatalf("unknown address: %d", res.StatusCode)
	}
	res, body2 := b.post("/reset/request", url.Values{"email": {"ann@example.com"}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body2, "Check your inbox") {
		t.Fatalf("request: %d", res.StatusCode)
	}
	token := lastLink(t, s, "ann@example.com", "/reset")
	if n := len(s.Mail.Sent()); n != 1 {
		t.Fatalf("%d messages; the unknown address must get none", n)
	}

	if res, body := b.post("/reset", url.Values{"token": {token}, "password": {"short"}}); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, token) {
		t.Fatalf("short password: %d", res.StatusCode)
	}
	res, _ = b.post("/reset", url.Values{"token": {token}, "password": {"a brand new password"}})
	if res.StatusCode != http.StatusSeeOther || b.whoami() != "ann" {
		t.Fatalf("reset: %d", res.StatusCode)
	}
	if res, _ := b.post("/reset", url.Values{"token": {token}, "password": {"another new password"}}); res.StatusCode != http.StatusNotFound {
		t.Errorf("reused reset link: %d", res.StatusCode)
	}
	if other.whoami() != "" {
		t.Error("other session survived a reset")
	}
	if res := newBrowser(t, s).login("ann", pw); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("old password: %d", res.StatusCode)
	}
	if res := newBrowser(t, s).login("ann", "a brand new password"); res.StatusCode != http.StatusSeeOther {
		t.Errorf("new password: %d", res.StatusCode)
	}
}

func TestTokenPagesHeaders(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	b := newBrowser(t, s)
	for _, p := range []string{"/verify", "/reset", "/invite"} {
		res, body := b.get(p)
		if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: %d %q %q", p, res.StatusCode, res.Header.Get("Cache-Control"), res.Header.Get("Referrer-Policy"))
		}
		if !strings.Contains(body, "/static/js/token-page") || !strings.Contains(body, "data-token-page") {
			t.Errorf("%s: token-page.js not loaded", p)
		}
	}
	for _, r := range s.App.Router.Routes() {
		for _, p := range []string{"/verify", "/reset", "/invite", "/auth/oidc/callback"} {
			if strings.HasSuffix(r.Pattern, " "+p) && !r.Redact {
				t.Errorf("%s is not redacted in logs", r.Pattern)
			}
		}
	}
}

func TestPasswordChangeAndSessions(t *testing.T) {
	s := newServer(t)
	createUser(t, s, "ann", "", auth.RoleUser)
	a, b, c := newBrowser(t, s), newBrowser(t, s), newBrowser(t, s)
	a.mustLogin("ann")
	b.mustLogin("ann")
	c.mustLogin("ann")

	if res, body := a.post("/settings/security/password", url.Values{"current": {"not it at all"}, "password": {"next password 1"}}); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "not your current password") {
		t.Fatalf("wrong current password: %d", res.StatusCode)
	}
	if res, _ := a.post("/settings/security/password", url.Values{"current": {pw}, "password": {"next password 1"}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("change: %d", res.StatusCode)
	}
	if a.whoami() != "ann" || b.whoami() != "" || c.whoami() != "" {
		t.Fatal("password change must keep only the current session")
	}
	if countAudit(t, s, "user.password_change") != 1 {
		t.Error("password change not audited")
	}

	// Session list and revoking one.
	b.login("ann", "next password 1")
	_, body := a.get("/settings/security")
	if strings.Count(body, "/revoke\"") != 2 || !strings.Contains(body, "this browser") {
		t.Fatalf("session list:\n%s", body)
	}
	sessions, _ := s.App.Accounts.Sessions().List(context.Background(), 1)
	var bID string
	pa, _, _ := s.App.Accounts.Authenticate(a.requestWithCookies("/"))
	for _, se := range sessions {
		if se.ID != pa.SessionID {
			bID = se.ID
		}
	}
	if res, _ := a.post("/settings/security/sessions/"+bID+"/revoke", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if b.whoami() != "" || a.whoami() != "ann" {
		t.Error("revoke ended the wrong session")
	}
	// Another user's session id cannot be revoked.
	if res, _ := a.post("/settings/security/sessions/"+strings.Repeat("ab", 32)+"/revoke", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown session: %d", res.StatusCode)
	}

	// Sign out everywhere includes this browser.
	b.login("ann", "next password 1")
	if res, _ := a.post("/settings/security/signout-all", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign out everywhere: %d", res.StatusCode)
	}
	if a.whoami() != "" || b.whoami() != "" {
		t.Error("sessions survived sign out everywhere")
	}
	if countAudit(t, s, "auth.logout_everywhere") != 1 {
		t.Error("not audited")
	}
}

func TestDisabledUserLosesSession(t *testing.T) {
	s := newServer(t)
	u := createUser(t, s, "ann", "", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	if err := s.App.Accounts.Store().SetStatus(context.Background(), u.ID, accounts.StatusDisabled, accounts.CLIActor()); err != nil {
		t.Fatal(err)
	}
	if b.whoami() != "" {
		t.Error("disabled user still signed in")
	}
	res, body := b.post("/login", url.Values{"login": {"ann"}, "password": {pw}})
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "disabled") {
		t.Errorf("disabled login: %d", res.StatusCode)
	}
	// A wrong password does not reveal that the account is disabled.
	if _, body := b.post("/login", url.Values{"login": {"ann"}, "password": {"wrong password!"}}); strings.Contains(body, "disabled") {
		t.Error("disabled state leaked without the password")
	}
}

func TestProfile(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	createUser(t, s, "bob", "bob@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")

	if res, body := b.post("/settings/profile", url.Values{"username": {"bob"}, "email": {"ann@example.com"}}); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "taken") {
		t.Errorf("taken username: %d", res.StatusCode)
	}
	if res, _ := b.post("/settings/profile", url.Values{"username": {"annie"}, "email": {"ann@example.com"}}); res.StatusCode != http.StatusSeeOther || b.whoami() != "annie" {
		t.Fatalf("rename: %d", res.StatusCode)
	}
	// Changing the address needs the current password.
	if res, body := b.post("/settings/profile", url.Values{"username": {"annie"}, "email": {"annie@example.com"}}); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Enter your current password") {
		t.Fatalf("email change without the password: %d", res.StatusCode)
	}
	if res, body := b.post("/settings/profile", url.Values{"username": {"annie"}, "email": {"annie@example.com"}, "current": {"not the password"}}); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "not your current password") {
		t.Fatalf("email change with a wrong password: %d", res.StatusCode)
	}
	// A new address waits for confirmation; the verified one stays.
	if res, _ := b.post("/settings/profile", url.Values{"username": {"annie"}, "email": {"annie@example.com"}, "current": {pw}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("email change: %d", res.StatusCode)
	}
	_, body := b.get("/settings/profile")
	if !strings.Contains(body, "Confirm annie@example.com") || !strings.Contains(body, `value="ann@example.com"`) {
		t.Error("pending change not shown, or address replaced early")
	}
	token := lastLink(t, s, "annie@example.com", "/verify")
	// Opening the link only shows what it confirms.
	code, out := b.postJSON("/verify", url.Values{"token": {token}})
	if code != http.StatusOK || out["confirm"] != "1" || out["email"] != "annie@example.com" || out["username"] != "annie" {
		t.Fatalf("check: %d %v", code, out)
	}
	code, out = b.postJSON("/verify", url.Values{"token": {token}, "confirm": {"1"}})
	if code != http.StatusOK || out["next"] != "/settings/profile" {
		t.Fatalf("confirm: %d %v", code, out)
	}
	if _, body := b.get("/settings/profile"); !strings.Contains(body, `value="annie@example.com"`) {
		t.Error("new address not applied")
	}
	// Another account's address: the same answer as any other, and no mail.
	before := len(s.Mail.Sent())
	if res, _ := b.post("/settings/profile", url.Values{"username": {"annie"}, "email": {"bob@example.com"}, "current": {pw}}); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/profile?saved=email" {
		t.Errorf("taken email: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	time.Sleep(100 * time.Millisecond)
	for _, m := range s.Mail.Sent()[before:] {
		if m.To[0] == "bob@example.com" {
			t.Error("confirmation mailed to another account's address")
		}
	}
}

func TestAPIKeysCannotUseAccountPages(t *testing.T) {
	s := newServer(t)
	createUser(t, s, "ann", "", auth.RoleUser)
	s.App.Accounts.SetBearerVerifier(verifierFunc(func(r *http.Request, token string) (*auth.Principal, error) {
		switch token {
		case "good":
			return &auth.Principal{UserID: 1, Username: "ann", Role: auth.RoleUser, KeyID: 7, Scopes: []auth.Scope{auth.ScopeAccountRead}}, nil
		case "net":
			return nil, auth.ErrForbiddenNetwork
		}
		return nil, errors.New("unknown key")
	}))
	b := newBrowser(t, s)
	b.mustLogin("ann")
	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	// A bearer request never falls back to the cookie.
	req := b.requestWithCookies("/settings/profile")
	req.Header.Set("Authorization", "Bearer nope")
	if res, _ := b.do(req); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad bearer with a valid cookie: %d", res.StatusCode)
	}
	req = b.requestWithCookies("/settings/profile")
	req.Header.Set("Authorization", "Basic YW5uOnB3")
	if res, _ := b.do(req); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("basic auth: %d", res.StatusCode)
	}
	if res, body := b.post("/settings/profile", url.Values{"username": {"mallory"}}, bearer("net")); res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "not allowed from this address") {
		t.Errorf("forbidden network: %d", res.StatusCode)
	}
	if res, _ := b.post("/settings/profile", url.Values{"username": {"mallory"}}, bearer("good")); res.StatusCode != http.StatusForbidden {
		t.Errorf("API key changing the profile: %d", res.StatusCode)
	}
	if b.whoami() != "ann" {
		t.Error("profile changed through an API key")
	}
}

type verifierFunc func(r *http.Request, token string) (*auth.Principal, error)

func (f verifierFunc) VerifyBearer(r *http.Request, token string) (*auth.Principal, error) {
	return f(r, token)
}

func TestNoVerifierRejectsAllBearers(t *testing.T) {
	s := newServer(t)
	b := newBrowser(t, s)
	req := s.Request(t, "GET", "/healthz", nil)
	req.Header.Set("Authorization", "Bearer snap_abc")
	if res, _ := b.do(req); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("bearer without a verifier: %d", res.StatusCode)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
