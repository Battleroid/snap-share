package accounts_test

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/oidctest"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// totpT0 is where the tests fix the clock TOTP codes are checked against.
var totpT0 = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// mfaEnv is a server whose TOTP clock the test moves along, one step per
// code, so every code is fresh and none falls out of the window.
type mfaEnv struct {
	t  *testing.T
	s  *testutil.Server
	mu sync.Mutex
	at time.Time
}

func newMFAEnv(t *testing.T, opts ...testutil.Option) *mfaEnv {
	t.Helper()
	s := newServer(t, append([]testutil.Option{testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1,::1")}, opts...)...)
	return mfaEnvFor(t, s)
}

func mfaEnvFor(t *testing.T, s *testutil.Server) *mfaEnv {
	t.Helper()
	if err := local.MinFreeBytes.Set(context.Background(), s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	e := &mfaEnv{t: t, s: s, at: totpT0}
	s.App.Accounts.SetTOTPTime(e.at)
	return e
}

// code returns secret's code for a step no code has used yet, and moves the
// server's TOTP clock there.
func (e *mfaEnv) code(secret []byte) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.at = e.at.Add(30 * time.Second)
	e.s.App.Accounts.SetTOTPTime(e.at)
	return accounts.TOTPCode(secret, e.at)
}

// now returns secret's code for the clock's current step (already used if
// code just returned it).
func (e *mfaEnv) now(secret []byte) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return accounts.TOTPCode(secret, e.at)
}

// wrongCode is a code secret gives for no step in the window.
func (e *mfaEnv) wrongCode(secret []byte) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := 0; ; i++ {
		c := fmt.Sprintf("%06d", i)
		ok := true
		for d := -1; d <= 1; d++ {
			if accounts.TOTPCode(secret, e.at.Add(time.Duration(d)*30*time.Second)) == c {
				ok = false
			}
		}
		if ok {
			return c
		}
	}
}

func (e *mfaEnv) user(name string, role auth.Role) *accounts.User {
	return createUser(e.t, e.s, name, name+"@example.com", role)
}

var (
	secretRE = regexp.MustCompile(`value="([A-Z2-7]+)" id="totp-secret"`)
	codesRE  = regexp.MustCompile(`(?s)id="recovery-codes">(.*?)</ul>`)
)

// setupTOTP turns on an authenticator app for b's user through Settings:
// the current password plus proof (a factor the user has already). It
// returns the secret and the recovery codes shown (none when the user had
// a factor before).
func (e *mfaEnv) setupTOTP(b *browser, proof url.Values) ([]byte, []string) {
	e.t.Helper()
	form := url.Values{"current": {pw}}
	for k, v := range proof {
		form[k] = v
	}
	res, body := b.post("/settings/security/totp", form)
	if res.StatusCode != http.StatusOK {
		e.t.Fatalf("begin setup: %d", res.StatusCode)
	}
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Referrer-Policy") != "no-referrer" {
		e.t.Errorf("setup page headers: %v", res.Header)
	}
	if !strings.Contains(body, `src="data:image/png;base64,`) {
		e.t.Error("setup page has no QR code")
	}
	m := secretRE.FindStringSubmatch(body)
	if m == nil {
		e.t.Fatal("no secret on the setup page")
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(m[1])
	if err != nil || len(secret) != 20 {
		e.t.Fatalf("secret %q: %v", m[1], err)
	}
	res, body = b.post("/settings/security/totp/confirm", url.Values{"code": {e.code(secret)}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Authenticator app set up.") {
		e.t.Fatalf("confirm setup: %d", res.StatusCode)
	}
	return secret, recoveryCodes(body)
}

func recoveryCodes(body string) []string {
	m := codesRE.FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	return regexp.MustCompile(`[0-9a-z]{4}(?:-[0-9a-z]{4}){3}`).FindAllString(m[1], -1)
}

// loginCode signs b in with the password, then sends code of kind ("totp"
// or "recovery") to the second step.
func (b *browser) loginCode(login, kind, code string) (*response, string) {
	b.t.Helper()
	b.passwordStep(login)
	return b.secondStep(kind, code)
}

// passwordStep signs in with the password, which must lead to the second step.
func (b *browser) passwordStep(login string) {
	b.t.Helper()
	if res := b.login(login, pw); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login/2fa" {
		b.t.Fatalf("password step: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func (b *browser) secondStep(kind, code string) (*response, string) {
	b.t.Helper()
	return b.post("/login/2fa/code", url.Values{"kind": {kind}, "code": {code}})
}

// lastLoginMethod is the method of the newest auth.login audit entry.
func lastLoginMethod(t *testing.T, s *testutil.Server) string {
	t.Helper()
	var details string
	if err := s.App.DB.R.QueryRow(`SELECT details FROM audit_log WHERE action = 'auth.login' ORDER BY id DESC LIMIT 1`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal([]byte(details), &d)
	return d.Method
}

func sessionCount(t *testing.T, s *testutil.Server, uid int64) int {
	t.Helper()
	var n int
	if err := s.App.DB.R.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func factorsOf(t *testing.T, s *testutil.Server, uid int64) accounts.Factors {
	t.Helper()
	f, err := s.App.Accounts.Store().Factors(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// waitMail waits for a message to to whose text contains want.
func waitMail(t *testing.T, s *testutil.Server, to, want string) {
	t.Helper()
	for range 100 {
		for _, m := range s.Mail.Sent() {
			if len(m.To) > 0 && m.To[0] == to && strings.Contains(m.Text, want) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no mail to %s with %q", to, want)
}

func TestTOTPAndRecoverySignIn(t *testing.T) {
	e := newMFAEnv(t, testutil.WithSMTP())
	createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, e.s)
	b.mustLogin("ann")
	secret, codes := e.setupTOTP(b, nil)
	if len(codes) != 10 {
		t.Fatalf("%d recovery codes shown", len(codes))
	}
	waitMail(t, e.s, "ann@example.com", accounts.SecurityTOTPAdded)

	// The password alone opens only the second step.
	c := newBrowser(t, e.s)
	c.passwordStep("ann")
	if c.whoami() != "" {
		t.Fatal("signed in after the password alone")
	}
	_, body := c.get("/login/2fa")
	if !strings.Contains(body, `name="kind" value="totp"`) || !strings.Contains(body, `name="kind" value="recovery"`) {
		t.Fatal("second step lacks the code forms")
	}
	if res, _ := c.secondStep("totp", e.wrongCode(secret)); res.StatusCode != http.StatusUnauthorized || c.whoami() != "" {
		t.Fatalf("wrong code: %d", res.StatusCode)
	}
	if countAudit(t, e.s, "auth.login_failed") != 1 {
		t.Error("wrong code not audited")
	}
	code := e.code(secret)
	if res, _ := c.secondStep("totp", code[:3]+" "+code[3:]); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Fatalf("totp: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if c.whoami() != "ann" || lastLoginMethod(t, e.s) != "password+totp" {
		t.Fatalf("after totp: %q, method %q", c.whoami(), lastLoginMethod(t, e.s))
	}
	// The same code doesn't work twice.
	d := newBrowser(t, e.s)
	if res, _ := d.loginCode("ann", "totp", code); res.StatusCode != http.StatusUnauthorized || d.whoami() != "" {
		t.Fatalf("reused totp code: %d", res.StatusCode)
	}

	// A recovery code works once, in any case and without the hyphens.
	r := newBrowser(t, e.s)
	if res, _ := r.loginCode("ann", "recovery", strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("recovery: %d", res.StatusCode)
	}
	if r.whoami() != "ann" || lastLoginMethod(t, e.s) != "password+recovery" {
		t.Fatalf("after recovery: method %q", lastLoginMethod(t, e.s))
	}
	waitMail(t, e.s, "ann@example.com", "9 recovery codes left")
	if f := factorsOf(t, e.s, createdID(t, e.s, "ann")); f.RecoveryLeft != 9 {
		t.Errorf("%d codes left", f.RecoveryLeft)
	}
	r2 := newBrowser(t, e.s)
	if res, _ := r2.loginCode("ann", "recovery", codes[0]); res.StatusCode != http.StatusUnauthorized || r2.whoami() != "" {
		t.Fatalf("reused recovery code: %d", res.StatusCode)
	}
	// Unknown kinds and empty codes are refused.
	if res, _ := r2.secondStep("sms", "123456"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown kind: %d", res.StatusCode)
	}
	if res, _ := r2.secondStep("totp", ""); res.StatusCode != http.StatusBadRequest {
		t.Errorf("empty code: %d", res.StatusCode)
	}
	// Without a challenge there is nothing to finish.
	if res, _ := newBrowser(t, e.s).secondStep("totp", e.code(secret)); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Errorf("no challenge: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func createdID(t *testing.T, s *testutil.Server, name string) int64 {
	t.Helper()
	u, err := s.App.Accounts.Store().UserByUsername(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// An mfa_login challenge is not a session anywhere.
func TestSecondStepIsAnonymous(t *testing.T) {
	e := newMFAEnv(t)
	createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, e.s)
	b.mustLogin("ann")
	code, out := b.regJSON("POST", "/api/v1/pastes", `{"content":"ann's notes","visibility":"private"}`)
	if code != http.StatusCreated {
		t.Fatalf("paste: %d %s", code, out)
	}
	var paste struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal([]byte(out), &paste)
	e.setupTOTP(b, nil)

	c := newBrowser(t, e.s)
	c.passwordStep("ann")
	if _, body := c.get("/"); strings.Contains(body, `aria-label="Signed in as `) {
		t.Error("home page acts for the user")
	}
	if res, _ := c.get("/dashboard"); res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Errorf("dashboard: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := c.get("/" + paste.Slug); res.StatusCode != http.StatusNotFound {
		t.Errorf("private item: %d", res.StatusCode)
	}
	if code, _ := c.regJSON("POST", "/api/v1/secrets", `{"kind":"text","size":1024}`); code != http.StatusUnauthorized {
		t.Errorf("new secret: %d", code)
	}
	if code, _ := c.regJSON("GET", "/api/v1/me", ""); code != http.StatusUnauthorized {
		t.Errorf("/api/v1/me: %d", code)
	}
	// The challenge token is no session token either.
	tok := c.cookie("__Host-snap_auth_mfa_login")
	if tok == "" {
		t.Fatal("no challenge cookie")
	}
	d := newBrowser(t, e.s)
	req := e.s.Request(t, "GET", "/settings/profile", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-snap_session", Value: tok})
	if res, _ := d.do(req); res.StatusCode == http.StatusOK {
		t.Error("the challenge token works as a session")
	}
}

func TestWrongCodesEndTheChallenge(t *testing.T) {
	e := newMFAEnv(t)
	createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, e.s)
	b.mustLogin("ann")
	secret, _ := e.setupTOTP(b, nil)

	c := newBrowser(t, e.s)
	c.passwordStep("ann")
	wrong := e.wrongCode(secret)
	for i := range 4 {
		res, body := c.secondStep("totp", wrong)
		if res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "That code didn&#39;t work.") {
			t.Fatalf("wrong code %d: %d", i+1, res.StatusCode)
		}
		if i == 0 { // a wrong code is an error on the code field, not a banner above the form
			if !strings.Contains(body, `id="mfa-code-error"`) || !strings.Contains(body, `aria-describedby="mfa-code-error"`) ||
				strings.Count(body, `aria-invalid="true"`) != 7 || !strings.Contains(body, " data-invalid") || strings.Contains(body, `class="pb-notice"`) ||
				!strings.Contains(body, `data-name="code"`) {
				t.Error("the wrong-code error is not on the field (input, six boxes, message)")
			}
		}
	}
	if res, body := c.secondStep("totp", wrong); res.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Too many wrong codes") {
		t.Fatalf("5th wrong code: %d", res.StatusCode)
	}
	// The challenge is gone: even the right code doesn't finish it.
	if res, _ := c.secondStep("totp", e.code(secret)); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" || c.whoami() != "" {
		t.Fatalf("after 5 wrong codes: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	// The account's code budget outlasts the challenge: a new one (the
	// password is right) gets no more guesses for now.
	c.passwordStep("ann")
	if res, _ := c.secondStep("totp", e.code(secret)); res.StatusCode != http.StatusTooManyRequests || c.whoami() != "" {
		t.Fatalf("new challenge after 5 wrong codes: %d", res.StatusCode)
	}
}

func TestSecondStepIsSingleUse(t *testing.T) {
	e := newMFAEnv(t)
	t.Run("parallel finishes of one challenge", func(t *testing.T) {
		u := e.user("ann", auth.RoleUser)
		b := newBrowser(t, e.s)
		b.mustLogin("ann")
		_, codes := e.setupTOTP(b, nil)
		c := newBrowser(t, e.s)
		c.passwordStep("ann")
		tok := c.cookie("__Host-snap_auth_mfa_login")
		before := sessionCount(t, e.s, u.ID)
		var (
			wg    sync.WaitGroup
			mu    sync.Mutex
			wins  int
			start = make(chan struct{})
		)
		// Every request carries the challenge cookie (no jar to clear it).
		plain := &http.Client{Transport: c.c.Transport, CheckRedirect: c.c.CheckRedirect}
		for i := range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := e.s.Request(t, "POST", "/login/2fa/code", strings.NewReader(url.Values{"kind": {"recovery"}, "code": {codes[i]}}.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.AddCookie(&http.Cookie{Name: "__Host-snap_auth_mfa_login", Value: tok})
				<-start
				res, err := plain.Do(req)
				if err != nil {
					return
				}
				_ = res.Body.Close()
				if res.StatusCode == http.StatusSeeOther && res.Header.Get("Location") == "/" {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		close(start)
		wg.Wait()
		if wins != 1 || sessionCount(t, e.s, u.ID) != before+1 {
			t.Fatalf("%d finishes, %d new sessions; want 1", wins, sessionCount(t, e.s, u.ID)-before)
		}
	})
	race := func(t *testing.T, name string, kind func(secret []byte, codes []string) (string, string)) {
		u := e.user(name, auth.RoleUser)
		b := newBrowser(t, e.s)
		b.mustLogin(name)
		secret, codes := e.setupTOTP(b, nil)
		k, code := kind(secret, codes)
		before := sessionCount(t, e.s, u.ID)
		var browsers []*browser
		for range 5 {
			c := newBrowser(t, e.s)
			c.passwordStep(name)
			browsers = append(browsers, c)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, c := range browsers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := e.s.Request(t, "POST", "/login/2fa/code", strings.NewReader(url.Values{"kind": {k}, "code": {code}}.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				<-start
				if res, err := c.c.Do(req); err == nil {
					_ = res.Body.Close()
				}
			}()
		}
		close(start)
		wg.Wait()
		if n := sessionCount(t, e.s, u.ID) - before; n != 1 {
			t.Fatalf("one %s code gave %d sessions", k, n)
		}
	}
	t.Run("one totp step from parallel challenges", func(t *testing.T) {
		race(t, "bob", func(secret []byte, _ []string) (string, string) { return "totp", e.code(secret) })
	})
	t.Run("one recovery code from parallel challenges", func(t *testing.T) {
		race(t, "cat", func(_ []byte, codes []string) (string, string) { return "recovery", codes[0] })
	})
}

func TestPasswordResetWith2FA(t *testing.T) {
	e := newMFAEnv(t, testutil.WithSMTP())
	u := createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	a := newBrowser(t, e.s)
	a.mustLogin("ann")
	secret, _ := e.setupTOTP(a, nil)
	key, _, err := e.s.App.Keys.Store().Create(context.Background(), u.ID, keys.NewKey{Name: "laptop", Scopes: []auth.Scope{auth.ScopeAccountRead}}, accounts.AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	keyWorks := func() bool {
		code, _ := regBearer(t, e.s, key, "GET", "/api/v1/me", "")
		return code == http.StatusOK
	}
	const newPW = "a brand new password"
	untouched := func(when string) {
		t.Helper()
		if a.whoami() != "ann" {
			t.Errorf("%s: the existing session ended", when)
		}
		if !keyWorks() {
			t.Errorf("%s: the API key was revoked", when)
		}
		c := newBrowser(t, e.s)
		c.passwordStep("ann") // the old password still leads to the second step
		if res := newBrowser(t, e.s).login("ann", newPW); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: the new password works: %d", when, res.StatusCode)
		}
	}

	b := newBrowser(t, e.s)
	b.post("/reset/request", url.Values{"email": {"ann@example.com"}})
	token := lastLink(t, e.s, "ann@example.com", "/reset")
	if res, _ := b.post("/reset", url.Values{"token": {token}, "password": {newPW}}); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login/2fa" {
		t.Fatalf("reset: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if _, body := b.get("/login/2fa"); !strings.Contains(body, `value="totp"`) {
		t.Fatal("reset's second step lacks the code form")
	}
	untouched("abandoned")
	if res, _ := b.secondStep("totp", e.wrongCode(secret)); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", res.StatusCode)
	}
	untouched("wrong code")

	if res, _ := b.secondStep("totp", e.code(secret)); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("right code: %d", res.StatusCode)
	}
	if b.whoami() != "ann" || lastLoginMethod(t, e.s) != "reset+totp" {
		t.Fatalf("after reset: %q, method %q", b.whoami(), lastLoginMethod(t, e.s))
	}
	if a.whoami() != "" {
		t.Error("the old session survived the reset")
	}
	if keyWorks() {
		t.Error("the API key survived the reset")
	}
	if res := newBrowser(t, e.s).login("ann", pw); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("old password after the reset: %d", res.StatusCode)
	}
	if res := newBrowser(t, e.s).login("ann", newPW); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login/2fa" {
		t.Errorf("new password: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	waitMail(t, e.s, "ann@example.com", accounts.SecurityPasswordReset)
	// The link is used up.
	if res, _ := newBrowser(t, e.s).post("/reset", url.Values{"token": {token}, "password": {"yet another password"}}); res.StatusCode != http.StatusNotFound {
		t.Errorf("reused link: %d", res.StatusCode)
	}
}

func TestSetupOnlySession(t *testing.T) {
	s, _ := ssoServer(t)
	e := mfaEnvFor(t, s)
	ctx := context.Background()
	u := createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	code, out := b.regJSON("POST", "/api/v1/pastes", `{"content":"ann's notes","visibility":"private"}`)
	if code != http.StatusCreated {
		t.Fatalf("paste: %d %s", code, out)
	}
	var paste struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal([]byte(out), &paste)
	if err := accounts.Require2FA.Set(ctx, s.App.Settings, accounts.Require2FAEveryone); err != nil {
		t.Fatal(err)
	}
	// The policy applies at once, to existing sessions too.
	if res, _ := b.get("/" + paste.Slug); res.StatusCode != http.StatusNotFound {
		t.Errorf("own private item during setup: %d", res.StatusCode)
	}
	if _, body := b.get("/"); strings.Contains(body, `aria-label="Signed in as `) {
		t.Error("home page acts for the user during setup")
	}
	if code, _ := b.regJSON("POST", "/api/v1/secrets", `{"kind":"text","size":1024}`); code == http.StatusCreated {
		t.Error("created a secret during setup")
	}
	if res, _ := b.get("/dashboard"); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/security#two-factor" {
		t.Errorf("dashboard: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := b.get("/settings/profile"); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/security#two-factor" {
		t.Errorf("profile: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := b.do(func() *http.Request {
		req := s.Request(t, "GET", "/dashboard", nil)
		req.Header.Set("HX-Request", "true")
		return req
	}()); res.StatusCode != http.StatusNoContent || res.Header.Get("HX-Redirect") != "/settings/security#two-factor" {
		t.Errorf("htmx: %d %q", res.StatusCode, res.Header.Get("HX-Redirect"))
	}
	if code, body := b.regJSON("GET", "/api/v1/me", ""); code != http.StatusForbidden || !strings.Contains(body, "Set up two-factor authentication first.") {
		t.Errorf("api: %d %s", code, body)
	}
	if res, _ := b.ssoLink(); res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Errorf("sso link during setup: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if b.cookie("__Host-snap_oidc") != "" {
		t.Error("sso link started during setup")
	}
	res, body := b.get("/settings/security")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Set up two-factor authentication to continue.") {
		t.Fatalf("security page during setup: %d", res.StatusCode)
	}
	if strings.Contains(body, `id="sso-link"`) || strings.Contains(body, `aria-label="Main"`) {
		t.Error("the security page offers the SSO link or the nav during setup")
	}
	// A password sign-in lands on the setup, not on a page that would show
	// the user as signed out; signing out works.
	c := newBrowser(t, s)
	if res := c.login("ann", pw); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/security#two-factor" {
		t.Errorf("sign-in during setup: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := c.post("/logout", nil); res.StatusCode != http.StatusSeeOther || c.cookie("__Host-snap_session") != "" {
		t.Errorf("logout during setup: %d", res.StatusCode)
	}

	// Setting up an authenticator app ends the setup, on this page already.
	form := url.Values{"current": {pw}}
	res, body = b.post("/settings/security/totp", form)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("begin setup: %d", res.StatusCode)
	}
	m := secretRE.FindStringSubmatch(body)
	secret, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(m[1])
	res, body = b.post("/settings/security/totp/confirm", url.Values{"code": {e.code(secret)}})
	if res.StatusCode != http.StatusOK || len(recoveryCodes(body)) != 10 {
		t.Fatalf("confirm: %d", res.StatusCode)
	}
	if strings.Contains(body, "Set up two-factor authentication to continue.") || !strings.Contains(body, `aria-label="Main"`) {
		t.Error("the page still shows the setup")
	}
	if res, _ := b.get("/dashboard"); res.StatusCode != http.StatusOK {
		t.Errorf("dashboard after setup: %d", res.StatusCode)
	}
	if res, _ := b.get("/" + paste.Slug); res.StatusCode != http.StatusOK {
		t.Errorf("own private item after setup: %d", res.StatusCode)
	}
	if code, _ := b.regJSON("GET", "/api/v1/me", ""); code != http.StatusOK {
		t.Errorf("api after setup: %d", code)
	}
	if !factorsOf(t, s, u.ID).TOTP {
		t.Error("no authenticator app stored")
	}
}

func TestFirstFactorEndsOtherSessions(t *testing.T) {
	e := newMFAEnv(t)
	createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	b, c := newBrowser(t, e.s), newBrowser(t, e.s)
	b.mustLogin("ann")
	c.mustLogin("ann")
	secret, _ := e.setupTOTP(b, nil)
	if b.whoami() != "ann" || c.whoami() != "" {
		t.Fatalf("after the first factor: this browser %q, the other %q", b.whoami(), c.whoami())
	}
	// A second setup (replacing the app) needs the first app and ends nothing.
	c.loginCode("ann", "totp", e.code(secret))
	if _, codes := e.setupTOTP(b, url.Values{"code": {e.code(secret)}}); codes != nil {
		t.Error("replacing the app made new recovery codes")
	}
	if c.whoami() != "ann" {
		t.Error("replacing the app ended another session")
	}
}

func TestSetupNeverReplacesAWorkingSecret(t *testing.T) {
	e := newMFAEnv(t)
	createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, e.s)
	b.mustLogin("ann")
	secret, _ := e.setupTOTP(b, nil)
	// Start a new setup but don't confirm it: the old app keeps working.
	if res, _ := b.post("/settings/security/totp", url.Values{"current": {pw}, "code": {e.code(secret)}}); res.StatusCode != http.StatusOK {
		t.Fatalf("begin: %d", res.StatusCode)
	}
	if res, body := b.post("/settings/security/totp/confirm", url.Values{"code": {"000000"}}); res.StatusCode != http.StatusUnprocessableEntity || !secretRE.MatchString(body) {
		t.Fatalf("wrong confirm code: %d", res.StatusCode)
	}
	c := newBrowser(t, e.s)
	if res, _ := c.loginCode("ann", "totp", e.code(secret)); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("old app after an unconfirmed setup: %d", res.StatusCode)
	}
	// Another browser can't confirm this browser's setup.
	if res, _ := c.post("/settings/security/totp/confirm", url.Values{"code": {e.code(secret)}}); res.StatusCode != http.StatusConflict {
		t.Errorf("confirm from another browser: %d", res.StatusCode)
	}
}

func TestRemoveTOTP(t *testing.T) {
	e := newMFAEnv(t, testutil.WithSMTP())
	u := createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, e.s)
	b.mustLogin("ann")
	secret, _ := e.setupTOTP(b, nil)
	if err := accounts.Require2FA.Set(context.Background(), e.s.App.Settings, accounts.Require2FAEveryone); err != nil {
		t.Fatal(err)
	}
	if res, body := b.post("/settings/security/totp/remove", url.Values{"current": {pw}, "code": {e.code(secret)}}); res.StatusCode != http.StatusConflict || !strings.Contains(body, "required on this site") {
		t.Fatalf("remove the last factor under the policy: %d", res.StatusCode)
	}
	if !factorsOf(t, e.s, u.ID).TOTP {
		t.Fatal("removed under the policy")
	}
	if err := accounts.Require2FA.Set(context.Background(), e.s.App.Settings, accounts.Require2FAOff); err != nil {
		t.Fatal(err)
	}
	if res, _ := b.post("/settings/security/totp/remove", url.Values{"current": {pw}}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("remove without a code: %d", res.StatusCode)
	}
	if res, _ := b.post("/settings/security/totp/remove", url.Values{"current": {pw}, "code": {e.code(secret)}}); res.StatusCode != http.StatusOK {
		t.Fatalf("remove: %d", res.StatusCode)
	}
	if f := factorsOf(t, e.s, u.ID); f.Any() || f.RecoveryLeft != 0 {
		t.Errorf("factors after removal: %+v", f)
	}
	if countAudit(t, e.s, "mfa.totp_removed") != 1 || countAudit(t, e.s, "mfa.totp_enabled") != 1 {
		t.Error("not audited")
	}
	waitMail(t, e.s, "ann@example.com", accounts.SecurityTOTPRemoved)
	if res := newBrowser(t, e.s).login("ann", pw); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Errorf("password sign-in after removal: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestNewRecoveryCodes(t *testing.T) {
	e := newMFAEnv(t, testutil.WithSMTP())
	createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, e.s)
	b.mustLogin("ann")
	if res, _ := b.post("/settings/security/recovery", url.Values{"current": {pw}}); res.StatusCode != http.StatusConflict {
		t.Errorf("codes without a factor: %d", res.StatusCode)
	}
	secret, old := e.setupTOTP(b, nil)
	res, body := b.post("/settings/security/recovery", url.Values{"current": {pw}, "code": {old[0]}})
	fresh := recoveryCodes(body)
	if res.StatusCode != http.StatusOK || len(fresh) != 10 {
		t.Fatalf("new codes: %d, %d codes", res.StatusCode, len(fresh))
	}
	waitMail(t, e.s, "ann@example.com", accounts.SecurityRecoveryReplaced)
	if _, body := b.get("/settings/security"); recoveryCodes(body) != nil || !strings.Contains(body, "10 left") {
		t.Error("codes shown again, or the count is wrong")
	}
	if res, _ := newBrowser(t, e.s).loginCode("ann", "recovery", old[1]); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("old code after new ones: %d", res.StatusCode)
	}
	if res, _ := newBrowser(t, e.s).loginCode("ann", "recovery", fresh[0]); res.StatusCode != http.StatusSeeOther {
		t.Errorf("new code: %d", res.StatusCode)
	}
	_ = secret
}

func TestConfirmOwnerWith2FA(t *testing.T) {
	e := newMFAEnv(t)
	createUser(t, e.s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, e.s)
	b.mustLogin("ann")
	secret, codes := e.setupTOTP(b, nil)
	if _, body := b.get("/settings/security"); !strings.Contains(body, `name="code"`) {
		t.Error("the password form has no code field")
	}
	change := func(proof url.Values) int {
		form := url.Values{"current": {pw}, "password": {pw}}
		for k, v := range proof {
			form[k] = v
		}
		res, _ := b.post("/settings/security/password", form)
		return res.StatusCode
	}
	if code := change(nil); code != http.StatusUnprocessableEntity {
		t.Errorf("password alone: %d", code)
	}
	if code := change(url.Values{"code": {e.wrongCode(secret)}}); code != http.StatusUnprocessableEntity {
		t.Errorf("wrong code: %d", code)
	}
	if code := change(url.Values{"current": {"not the password"}, "code": {e.code(secret)}}); code != http.StatusUnprocessableEntity {
		t.Errorf("wrong password with a code: %d", code)
	}
	if code := change(url.Values{"code": {e.code(secret)}}); code != http.StatusSeeOther {
		t.Errorf("password and code: %d", code)
	}
	if code := change(url.Values{"code": {e.now(secret)}}); code != http.StatusUnprocessableEntity {
		t.Errorf("the same code again: %d", code)
	}
	if code := change(url.Values{"code": {codes[0]}}); code != http.StatusSeeOther {
		t.Errorf("password and recovery code: %d", code)
	}
}

func TestLinkByEmailRefusesAccountsWithAFactor(t *testing.T) {
	s, p := ssoServer(t, testutil.Env("OIDC_LINK_BY_EMAIL", "true"))
	u, err := s.App.Accounts.Store().CreateUser(context.Background(), accounts.NewUser{
		Username: "local", Email: "shared@example.com", EmailVerified: true, Role: auth.RoleUser,
	}, accounts.AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.App.DB.W.Exec(`INSERT INTO user_totp (user_id, secret, created_at) VALUES (?, x'00', 0)`, u.ID); err != nil {
		t.Fatal(err)
	}
	p.SetUser(oidctest.User{Subject: "10", PreferredUsername: "remote", Email: "shared@example.com", EmailVerified: true})
	res, body := newBrowser(t, s).ssoFlow("/auth/oidc/start")
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "Sign in to it") {
		t.Fatalf("link by email into an account with a factor: %d", res.StatusCode)
	}
	if ids := identities(t, s, "local"); len(ids) != 0 {
		t.Fatal("linked")
	}
}

func TestSSOLinkNeedsTheFactor(t *testing.T) {
	s, p := ssoServer(t)
	e := mfaEnvFor(t, s)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	secret, _ := e.setupTOTP(b, nil)
	p.SetUser(oidctest.User{Subject: "7", PreferredUsername: "annie", Email: "other@example.com", EmailVerified: true})
	if res, _ := b.ssoLink(); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("link with the password alone: %d", res.StatusCode)
	}
	if res, _ := b.ssoLinkWith(url.Values{"current": {pw}, "code": {e.code(secret)}}); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/security?saved=linked" {
		t.Fatalf("link with password and code: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if len(identities(t, s, "ann")) != 1 {
		t.Fatal("not linked")
	}
}

func TestTOTPKeyRotation(t *testing.T) {
	oldKey, newKey := strings.Repeat("o", 32), strings.Repeat("n", 32)
	seal := func(t *testing.T, s *testutil.Server, uid int64, secret []byte) {
		t.Helper()
		old, err := auth.NewSealer("snap totp secret v1", []byte(oldKey))
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := old.Seal(uid, secret)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.App.DB.W.Exec(`INSERT INTO user_totp (user_id, secret, created_at) VALUES (?, ?, 0)`, uid, sealed); err != nil {
			t.Fatal(err)
		}
	}
	secret := []byte("12345678901234567890")

	t.Run("with the previous key", func(t *testing.T) {
		e := newMFAEnv(t, testutil.Env("SECRET_KEY", newKey), testutil.Env("SECRET_KEY_PREVIOUS", oldKey))
		u := e.user("ann", auth.RoleUser)
		seal(t, e.s, u.ID, secret)
		b := newBrowser(t, e.s)
		if res, _ := b.loginCode("ann", "totp", e.code(secret)); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("code under the previous key: %d", res.StatusCode)
		}
		var sealed []byte
		if err := e.s.App.DB.R.QueryRow(`SELECT secret FROM user_totp WHERE user_id = ?`, u.ID).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		cur, _ := auth.NewSealer("snap totp secret v1", []byte(newKey))
		if got, current, err := cur.Open(u.ID, sealed); err != nil || !current || string(got) != string(secret) {
			t.Fatalf("not sealed again under the current key: %v", err)
		}
	})
	t.Run("without it", func(t *testing.T) {
		e := newMFAEnv(t, testutil.Env("SECRET_KEY", newKey))
		u := e.user("ann", auth.RoleUser)
		seal(t, e.s, u.ID, secret)
		codes, err := e.s.App.Accounts.Store().ReplaceRecoveryCodes(context.Background(), u.ID, accounts.AuditEntry{})
		if err != nil {
			t.Fatal(err)
		}
		b := newBrowser(t, e.s)
		b.passwordStep("ann")
		res, body := b.get("/login/2fa")
		if res.StatusCode != http.StatusOK || strings.Contains(body, `value="totp"`) || !strings.Contains(body, `value="recovery"`) {
			t.Fatalf("second step with an unreadable secret: %d", res.StatusCode)
		}
		if res, _ := b.secondStep("totp", e.code(secret)); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("code from an unreadable secret: %d", res.StatusCode)
		}
		if res, _ := b.secondStep("recovery", codes[0]); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("recovery code: %d", res.StatusCode)
		}
		if _, body := b.get("/settings/security"); !strings.Contains(body, "Unreadable") || !strings.Contains(body, `>Recovery code</label>`) {
			t.Error("security page doesn't say the app needs setting up again")
		}
	})
}
