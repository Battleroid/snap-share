package redteam_test

// Regression tests for the second factor (passkeys and 2FA plan review):
// the second step can't be skipped, a reset or an SSO link doesn't go
// around it, codes work once, guesses are counted per account across
// challenges, and a session that must set up 2FA first is anonymous on
// public routes.

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238
	"encoding/binary"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/oidctest"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/testutil"
)

const mfaPassword = "the owner's long password"

// totpAt is the RFC 6238 code of secret at t (HMAC-SHA1, 6 digits, 30 s).
func totpAt(secret []byte, t time.Time) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(t.Unix()/30))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// wrongTOTP is a code secret gives for no step near now.
func wrongTOTP(secret []byte) string {
	for i := 0; ; i++ {
		c := fmt.Sprintf("%06d", i)
		if c != totpAt(secret, time.Now().Add(-30*time.Second)) && c != totpAt(secret, time.Now()) && c != totpAt(secret, time.Now().Add(30*time.Second)) {
			return c
		}
	}
}

// mfaUser makes a user with a password, an authenticator app and recovery codes.
func (e *env) mfaUser(name string) (*accounts.User, []byte, []string) {
	e.t.Helper()
	u := e.user(name, auth.RoleUser)
	e.setPassword(u, mfaPassword)
	secret := []byte(fmt.Sprintf("%-20s", name+" secret"))
	sealer, err := auth.NewSealer("snap totp secret v1", []byte(strings.Repeat("t", 32))) // testutil's SNAP_SECRET_KEY
	if err != nil {
		e.t.Fatal(err)
	}
	sealed, err := sealer.Seal(u.ID, secret)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.s.App.DB.W.Exec(`INSERT INTO user_totp (user_id, secret, created_at) VALUES (?, ?, 0)`, u.ID, sealed); err != nil {
		e.t.Fatal(err)
	}
	codes, err := e.s.App.Accounts.Store().ReplaceRecoveryCodes(e.ctx, u.ID, accounts.AuditEntry{})
	if err != nil {
		e.t.Fatal(err)
	}
	return u, secret, codes
}

func (e *env) form(path string, form url.Values, opts ...opt) (*resp, string) {
	e.t.Helper()
	return e.do("POST", path, strings.NewReader(form.Encode()), append([]opt{header("Content-Type", "application/x-www-form-urlencoded")}, opts...)...)
}

func setCookie(res *resp, name string) *http.Cookie {
	for _, c := range (&http.Response{Header: res.Header}).Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

const (
	sessionCookie   = "__Host-snap_session"
	challengeCookie = "__Host-snap_auth_mfa_login"
)

// passwordStep signs in with the password and returns the second step's
// challenge cookie. No session may come with it.
func (e *env) passwordStep(login string) *http.Cookie {
	e.t.Helper()
	res, _ := e.form("/login", url.Values{"login": {login}, "password": {mfaPassword}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login/2fa" {
		e.t.Fatalf("password step: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if setCookie(res, sessionCookie) != nil {
		e.t.Fatal("the password alone set a session cookie")
	}
	c := setCookie(res, challengeCookie)
	if c == nil {
		e.t.Fatal("no challenge cookie")
	}
	return c
}

// secondStep sends a code to the second step; it returns the answer and the
// session cookie it set, if any.
func (e *env) secondStep(ch *http.Cookie, kind, code string) (*resp, *http.Cookie) {
	e.t.Helper()
	res, _ := e.form("/login/2fa/code", url.Values{"kind": {kind}, "code": {code}}, withCookie(ch))
	return res, setCookie(res, sessionCookie)
}

func (e *env) signedIn(c *http.Cookie) bool {
	e.t.Helper()
	res, _ := e.do("GET", "/settings/profile", nil, withCookie(c))
	return res.StatusCode == http.StatusOK
}

func TestSecondStepCannotBeSkipped(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	u, _, _ := e.mfaUser("ann")
	ch := e.passwordStep("ann")
	// The challenge cookie opens nothing, and its token is no session token.
	for _, c := range []*http.Cookie{ch, {Name: sessionCookie, Value: ch.Value}} {
		for _, path := range []string{"/settings/profile", "/dashboard", "/settings/keys"} {
			if res, _ := e.do("GET", path, nil, withCookie(c)); res.StatusCode == http.StatusOK {
				t.Errorf("%s with %s: 200", path, c.Name)
			}
		}
		if res, _ := e.doJSON("GET", "/api/v1/me", nil, withCookie(c)); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("/api/v1/me with %s: %d", c.Name, res.StatusCode)
		}
	}
	// Nor does any answer to the second step that isn't a right code.
	for _, f := range []url.Values{
		{}, {"kind": {"totp"}}, {"kind": {"recovery"}, "code": {""}}, {"kind": {"passkey"}, "code": {"x"}},
		{"kind": {"totp"}, "code": {"0000000"}}, {"kind": {"recovery"}, "code": {"0000-0000-0000-0000"}},
	} {
		res, _ := e.form("/login/2fa/code", f, withCookie(ch))
		if s := setCookie(res, sessionCookie); s != nil || res.StatusCode == http.StatusSeeOther && res.Header.Get("Location") == "/" {
			t.Errorf("%v: signed in (%d)", f, res.StatusCode)
		}
	}
	var logins int
	_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'auth.login' AND target = ?`, fmt.Sprintf("user:%d", u.ID)).Scan(&logins)
	if logins != 0 {
		t.Errorf("%d sign-ins without the second factor", logins)
	}
}

func TestResetDoesNotBypassSecondFactor(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	u, secret, _ := e.mfaUser("ann")
	sess := e.session(u)
	key := e.mint(u, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAccountRead}})
	link, err := e.s.App.Accounts.ResetLink(e.ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, token, _ := strings.Cut(link, "#t=")
	const newPW = "the attacker's new password"

	// Someone with the mailbox but not the factor sets a new password.
	res, _ := e.form("/reset", url.Values{"token": {token}, "password": {newPW}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login/2fa" || setCookie(res, sessionCookie) != nil {
		t.Fatalf("reset: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	ch := setCookie(res, challengeCookie)
	for range 3 {
		if _, s := e.secondStep(ch, "totp", wrongTOTP(secret)); s != nil {
			t.Fatal("signed in with a wrong code")
		}
	}
	// Nothing changed: the session, the key and the old password still work.
	if !e.signedIn(sess) {
		t.Error("the owner's session ended")
	}
	if res, _ := e.doJSON("GET", "/api/v1/me", nil, bearer(key)); res.StatusCode != http.StatusOK {
		t.Errorf("the owner's key: %d", res.StatusCode)
	}
	e.passwordStep("ann")
	if res, _ := e.form("/login", url.Values{"login": {"ann"}, "password": {newPW}}); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("the new password works: %d", res.StatusCode)
	}
	if n := countRows(t, e, `SELECT count(*) FROM audit_log WHERE action IN ('user.password_reset', 'key.revoke_all')`); n != 0 {
		t.Errorf("%d reset rows written", n)
	}
}

func countRows(t *testing.T, e *env, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.s.App.DB.R.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSSOLinkNeedsSecondFactor(t *testing.T) {
	p := oidctest.New(t)
	e := newEnv(t, testutil.OIDCClient(p.Client()), testutil.Env("OIDC_ISSUER", p.URL),
		testutil.Env("OIDC_CLIENT_ID", p.ClientID), testutil.Env("OIDC_CLIENT_SECRET", p.ClientSecret),
		testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	u, secret, _ := e.mfaUser("ann")
	stolen := e.session(u) // a fresh session: a recent sign-in doesn't stand in for the factor
	p.SetUser(oidctest.User{Subject: "666", PreferredUsername: "mallory"})
	for _, f := range []url.Values{
		{"link": {"1"}},
		{"link": {"1"}, "current": {mfaPassword}},
		{"link": {"1"}, "current": {mfaPassword}, "code": {wrongTOTP(secret)}},
	} {
		res, _ := e.form("/auth/oidc/start", f, withCookie(stolen))
		if res.StatusCode != http.StatusUnprocessableEntity || setCookie(res, "__Host-snap_oidc") != nil {
			t.Errorf("link with %v: %d %q", f, res.StatusCode, res.Header.Get("Location"))
		}
	}
	// With the factor it starts (the gate is not a blanket refusal).
	res, _ := e.form("/auth/oidc/start", url.Values{"link": {"1"}, "current": {mfaPassword}, "code": {totpAt(secret, time.Now())}}, withCookie(stolen))
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), p.URL) {
		t.Errorf("link with the factor: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestSecondFactorCodesWorkOnce(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	_, secret, codes := e.mfaUser("ann")
	code := totpAt(secret, time.Now())
	if _, s := e.secondStep(e.passwordStep("ann"), "totp", code); s == nil {
		t.Fatal("first use of a code")
	}
	if _, s := e.secondStep(e.passwordStep("ann"), "totp", code); s != nil {
		t.Error("a code worked twice")
	}
	if _, s := e.secondStep(e.passwordStep("ann"), "totp", totpAt(secret, time.Now().Add(-30*time.Second))); s != nil {
		t.Error("an older code worked after a newer one")
	}
	if _, s := e.secondStep(e.passwordStep("ann"), "recovery", codes[0]); s == nil {
		t.Fatal("first use of a recovery code")
	}
	if _, s := e.secondStep(e.passwordStep("ann"), "recovery", strings.ToUpper(codes[0])); s != nil {
		t.Error("a recovery code worked twice")
	}
}

func TestCodeGuessesSpanChallenges(t *testing.T) {
	e := newEnv(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	_, secret, _ := e.mfaUser("ann")
	wrong := wrongTOTP(secret)
	// A fresh challenge per guess (the password is known) gets no more.
	for i := range 5 {
		if res, s := e.secondStep(e.passwordStep("ann"), "totp", wrong); s != nil || res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d", i+1, res.StatusCode)
		}
	}
	if res, s := e.secondStep(e.passwordStep("ann"), "totp", totpAt(secret, time.Now())); s != nil || res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th code on a new challenge: %d", res.StatusCode)
	}
	// Re-authentication draws on the same budget.
	u, _ := e.s.App.Accounts.Store().UserByUsername(context.Background(), "ann")
	res, _ := e.form("/settings/security/password", url.Values{"current": {mfaPassword}, "password": {"another long password"}, "code": {totpAt(secret, time.Now())}}, withCookie(e.session(u)))
	if res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("re-authentication after 5 wrong codes: %d", res.StatusCode)
	}
}

func TestSetupSessionIsAnonymousOnPublicRoutes(t *testing.T) {
	e := newEnv(t)
	u := e.user("ann", auth.RoleUser)
	sess := e.session(u)
	key := e.mint(u, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAccountRead}})
	slug := e.paste(sess, map[string]any{"content": "ann's private notes", "visibility": "private"})["slug"].(string)
	if err := accounts.Require2FA.Set(e.ctx, e.s.App.Settings, accounts.Require2FAEveryone); err != nil {
		t.Fatal(err)
	}
	if res, body := e.do("GET", "/"+slug, nil, withCookie(sess)); res.StatusCode != http.StatusNotFound || strings.Contains(body, "private notes") {
		t.Errorf("own private item during setup: %d", res.StatusCode)
	}
	if _, body := e.do("GET", "/", nil, withCookie(sess)); strings.Contains(body, "signed in as") {
		t.Error("the home page acts for a session in setup")
	}
	if res, _ := e.doJSON("POST", "/api/v1/secrets", map[string]any{"kind": "text", "size": 1024}, withCookie(sess)); res.StatusCode == http.StatusCreated {
		t.Error("a session in setup created a secret")
	}
	if res, m := e.doJSON("GET", "/api/v1/me", nil, withCookie(sess)); res.StatusCode != http.StatusForbidden {
		t.Errorf("api during setup: %d %v", res.StatusCode, m)
	}
	// API keys are not covered (they can't make sessions or links).
	if res, _ := e.doJSON("GET", "/api/v1/me", nil, bearer(key)); res.StatusCode != http.StatusOK {
		t.Errorf("key during setup: %d", res.StatusCode)
	}
}
