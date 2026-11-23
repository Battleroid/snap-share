package accounts_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/accounts/webauthntest"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// Passkeys need a hostname (WebAuthn refuses IP addresses).
const passkeyHost = "snap.example.com"

func newPasskeyServer(t *testing.T, opts ...testutil.Option) *testutil.Server {
	t.Helper()
	return newServer(t, append([]testutil.Option{testutil.Host(passkeyHost)}, opts...)...)
}

func newAuthenticator() *webauthntest.Authenticator {
	return webauthntest.New("https://" + passkeyHost)
}

// ceremony posts a form the way webauthn.js does and returns the raw JSON.
func (b *browser) ceremony(path string, form url.Values) (int, []byte) {
	b.t.Helper()
	res, body := b.post(path, form, func(r *http.Request) { r.Header.Set("Accept", "application/json") })
	return res.StatusCode, []byte(body)
}

// finish posts a credential as JSON (sign-in and second step) and decodes the answer.
func (b *browser) finish(path string, cred []byte) (int, map[string]string) {
	b.t.Helper()
	req := b.s.Request(b.t, "POST", path, bytes.NewReader(cred))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, body := b.do(req)
	var out map[string]string
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		b.t.Fatalf("%s: not JSON (%d): %s", path, res.StatusCode, body)
	}
	return res.StatusCode, out
}

// addPasskey registers a passkey from a in Settings, with the confirm-it's-you
// fields in form, and returns the finish response and page.
func (b *browser) addPasskey(a *webauthntest.Authenticator, form url.Values) (*response, string) {
	b.t.Helper()
	status, opts := b.ceremony("/settings/security/passkeys/begin", form)
	if status != http.StatusOK {
		b.t.Fatalf("passkeys/begin: %d %s", status, opts)
	}
	cred, _, err := a.Create(opts)
	if err != nil {
		b.t.Fatalf("create: %v", err)
	}
	return b.post("/settings/security/passkeys/finish", url.Values{"credential": {string(cred)}})
}

func (b *browser) mustAddPasskey(a *webauthntest.Authenticator, form url.Values) string {
	b.t.Helper()
	res, body := b.addPasskey(a, form)
	if res.StatusCode != http.StatusSeeOther && res.StatusCode != http.StatusOK {
		b.t.Fatalf("passkeys/finish: %d %s", res.StatusCode, body)
	}
	return body
}

// passkeyLogin signs in with a passkey from a, as the sign-in button does.
func (b *browser) passkeyLogin(a *webauthntest.Authenticator, next string) (int, map[string]string) {
	b.t.Helper()
	status, opts := b.ceremony("/login/passkey/begin", url.Values{"next": {next}})
	if status != http.StatusOK {
		var out map[string]string
		_ = json.Unmarshal(opts, &out)
		return status, out
	}
	cred, err := a.Get(opts)
	if err != nil {
		b.t.Fatalf("get: %v", err)
	}
	return b.finish("/login/passkey/finish", cred)
}

// mfaPasskey runs the passkey second step for this browser's mfa_login.
func (b *browser) mfaPasskey(a *webauthntest.Authenticator) (int, map[string]string) {
	b.t.Helper()
	status, opts := b.ceremony("/login/2fa/passkey/begin", nil)
	if status != http.StatusOK {
		b.t.Fatalf("2fa/passkey/begin: %d %s", status, opts)
	}
	cred, err := a.Get(opts)
	if err != nil {
		b.t.Fatalf("get: %v", err)
	}
	return b.finish("/login/2fa/passkey/finish", cred)
}

// proof makes a passkey assertion for confirmOwner, as the proof button does.
func (b *browser) proof(a *webauthntest.Authenticator) string {
	b.t.Helper()
	status, opts := b.ceremony("/settings/security/reauth/passkey/begin", nil)
	if status != http.StatusOK {
		b.t.Fatalf("reauth/passkey/begin: %d %s", status, opts)
	}
	cred, err := a.Get(opts)
	if err != nil {
		b.t.Fatalf("get: %v", err)
	}
	return string(cred)
}

func passkeyIDs(t *testing.T, s *testutil.Server, uid int64) []int64 {
	t.Helper()
	rows, err := s.App.DB.R.Query(`SELECT id FROM webauthn_credentials WHERE user_id = ? ORDER BY id`, uid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func passkeyNames(t *testing.T, s *testutil.Server) string {
	t.Helper()
	rows, err := s.App.DB.R.Query(`SELECT name FROM webauthn_credentials ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(names, ",")
}

var codeRE = regexp.MustCompile(`\b[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4}\b`)

func TestPasskeyRegisterAndSignIn(t *testing.T) {
	s := newPasskeyServer(t, testutil.WithSMTP())
	ann := createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	a := newAuthenticator()

	// Adding needs the password.
	if status, body := b.ceremony("/settings/security/passkeys/begin", url.Values{"current": {"wrong password!"}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("begin with a wrong password: %d %s", status, body)
	}
	page := b.mustAddPasskey(a, url.Values{"current": {pw}})
	if got := len(codeRE.FindAllString(page, -1)); got != 10 {
		t.Fatalf("first passkey: %d recovery codes on the page, want 10", got)
	}
	if n := countAudit(t, s, "mfa.passkey_added"); n != 1 {
		t.Fatalf("mfa.passkey_added audited %d times", n)
	}
	waitMail(t, s, "ann@example.com", accounts.SecurityPasskeyAdded)
	var handle []byte
	_ = s.App.DB.R.QueryRow(`SELECT webauthn_id FROM users WHERE id = ?`, ann.ID).Scan(&handle)
	if len(handle) != 32 {
		t.Fatalf("user handle is %d bytes", len(handle))
	}
	if _, body := b.get("/settings/security"); !strings.Contains(body, `<td class="pb-table__name">Passkey</td>`) {
		t.Fatal("the security page doesn't list the passkey")
	}

	// A second registration excludes the first credential, so the same
	// authenticator refuses (a browser's InvalidStateError).
	status, opts := b.ceremony("/settings/security/passkeys/begin", url.Values{"current": {pw}, "passkey": {b.proof(a)}})
	if status != http.StatusOK {
		t.Fatalf("second begin: %d %s", status, opts)
	}
	if !strings.Contains(string(opts), `"excludeCredentials":[{"type":"public-key","id":"`+base64.RawURLEncoding.EncodeToString(a.Credentials[0].ID)) {
		t.Fatalf("excludeCredentials doesn't list the passkey: %s", opts)
	}
	if _, _, err := a.Create(opts); !errors.Is(err, webauthntest.ErrExcluded) {
		t.Fatalf("create with the same authenticator: %v", err)
	}

	// Signed out, the passkey alone signs in (it verified the user). Only a
	// local next is followed.
	other := newBrowser(t, s)
	status2, out := other.passkeyLogin(a, "/settings/security")
	if status2 != http.StatusOK || out["redirect"] != "/settings/security" {
		t.Fatalf("passkey sign-in: %d %v", status2, out)
	}
	if other.whoami() != "ann" {
		t.Fatal("not signed in after a passkey sign-in")
	}
	if m := lastLoginMethod(t, s); m != "passkey" {
		t.Fatalf("audit method %q, want passkey", m)
	}
	if _, out := newBrowser(t, s).passkeyLogin(a, "//evil.example/x"); out["redirect"] != "/" {
		t.Fatalf("next //evil.example: redirect %q", out["redirect"])
	}
	var used sql.NullInt64
	_ = s.App.DB.R.QueryRow(`SELECT last_used_at FROM webauthn_credentials`).Scan(&used)
	if !used.Valid {
		t.Fatal("last_used_at not set")
	}
}

func TestPasskeyUserVerification(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	a := newAuthenticator()
	b.mustAddPasskey(a, url.Values{"current": {pw}})

	// The options ask for it...
	if _, opts := newBrowser(t, s).ceremony("/login/passkey/begin", nil); !strings.Contains(string(opts), `"userVerification":"required"`) {
		t.Fatalf("passkey sign-in options: %s", opts)
	}
	// ...and an assertion without it is refused: alone, a passkey has to be two factors.
	a.UserVerified = false
	c := newBrowser(t, s)
	if status, out := c.passkeyLogin(a, "/"); status != http.StatusUnauthorized {
		t.Fatalf("sign-in without user verification: %d %v", status, out)
	}
	if c.whoami() != "" {
		t.Fatal("signed in without user verification")
	}
	if countAudit(t, s, "auth.login_failed") != 1 {
		t.Fatal("the refused sign-in isn't audited")
	}

	// After the password, presence is enough (PIN-less security keys).
	if res := c.login("ann", pw); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login/2fa" {
		t.Fatalf("password with a passkey: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if c.whoami() != "" {
		t.Fatal("the password alone signed in")
	}
	_, opts := c.ceremony("/login/2fa/passkey/begin", nil)
	if !strings.Contains(string(opts), `"userVerification":"preferred"`) || !strings.Contains(string(opts), `"allowCredentials"`) {
		t.Fatalf("second-step options: %s", opts)
	}
	if status, out := c.mfaPasskey(a); status != http.StatusOK || out["redirect"] != "/" {
		t.Fatalf("second step: %d %v", status, out)
	}
	if c.whoami() != "ann" {
		t.Fatal("not signed in after the second step")
	}
	if m := lastLoginMethod(t, s); m != "password+key" {
		t.Fatalf("audit method %q, want password+key", m)
	}
}

func TestPasskeyOriginsAndRPID(t *testing.T) {
	s := newPasskeyServer(t, testutil.Env("RAW_URL", "https://raw.example.com"))
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	good := newAuthenticator()
	b.mustAddPasskey(good, url.Values{"current": {pw}})

	for _, tc := range []struct {
		name string
		mut  func(a *webauthntest.Authenticator)
	}{
		{"another origin", func(a *webauthntest.Authenticator) { a.Origin = "https://snap.example.com.evil.example" }},
		{"the raw origin", func(a *webauthntest.Authenticator) { a.Origin = "https://raw.example.com" }},
		{"http", func(a *webauthntest.Authenticator) { a.Origin = "http://snap.example.com" }},
		{"another RP ID", func(a *webauthntest.Authenticator) { a.RPID = "example.com" }},
		{"a cross-origin frame", func(a *webauthntest.Authenticator) { a.CrossOrigin = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Signing in with the registered credential.
			a := newAuthenticator()
			a.Use = good.Credentials[0]
			tc.mut(a)
			c := newBrowser(t, s)
			if status, out := c.passkeyLogin(a, "/"); status != http.StatusUnauthorized {
				t.Errorf("sign-in: %d %v", status, out)
			}
			if c.whoami() != "" {
				t.Error("signed in")
			}
			// Registering a new one.
			fresh := newAuthenticator()
			tc.mut(fresh)
			if res, _ := b.addPasskey(fresh, url.Values{"current": {pw}, "passkey": {b.proof(good)}}); res.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("registration: %d", res.StatusCode)
			}
		})
	}
	if n := len(passkeyIDs(t, s, 1)); n != 1 {
		t.Fatalf("%d passkeys after refused registrations", n)
	}
	// The real origin still works after all that.
	if status, _ := newBrowser(t, s).passkeyLogin(good, "/"); status != http.StatusOK {
		t.Fatalf("good sign-in: %d", status)
	}
}

func TestPasskeyCounter(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	a := newAuthenticator()
	b.mustAddPasskey(a, url.Values{"current": {pw}})
	cred := a.Credentials[0]

	for i := range 2 {
		if status, out := newBrowser(t, s).passkeyLogin(a, "/"); status != http.StatusOK {
			t.Fatalf("sign-in %d: %d %v", i, status, out)
		}
	}
	// A copy of the key whose counter is level with, then behind, the stored 2.
	for _, last := range []uint32{1, 0} {
		cred.SignCount = last // Get adds one
		c := newBrowser(t, s)
		status, out := c.passkeyLogin(a, "/")
		if status != http.StatusUnauthorized || !strings.Contains(out["error"], "copied") {
			t.Fatalf("counter %d against 2: %d %v", last+1, status, out)
		}
		if c.whoami() != "" {
			t.Fatal("a cloned key signed in")
		}
	}
	if n := countAudit(t, s, "auth.passkey_clone"); n != 2 {
		t.Fatalf("auth.passkey_clone audited %d times, want 2", n)
	}
	var stored int64
	_ = s.App.DB.R.QueryRow(`SELECT sign_count FROM webauthn_credentials`).Scan(&stored)
	if stored != 2 {
		t.Fatalf("stored counter %d, want 2", stored)
	}
	// The real key, ahead again, works; the second step checks the counter too.
	cred.SignCount = 5
	if status, _ := newBrowser(t, s).passkeyLogin(a, "/"); status != http.StatusOK {
		t.Fatalf("counter ahead: %d", status)
	}
	cred.SignCount = 1
	c := newBrowser(t, s)
	c.login("ann", pw)
	if status, out := c.mfaPasskey(a); status != http.StatusUnprocessableEntity || !strings.Contains(out["error"], "copied") {
		t.Fatalf("second step with a cloned key: %d %v", status, out)
	}
	if c.whoami() != "" {
		t.Fatal("a cloned key passed the second step")
	}
}

func TestPasskeyBackupFlags(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	synced := newAuthenticator()
	synced.BackupEligible, synced.BackupState, synced.Counting = true, true, false
	b.mustAddPasskey(synced, url.Values{"current": {pw}})

	var be, bs bool
	var count int64
	var transports string
	_ = s.App.DB.R.QueryRow(`SELECT backup_eligible, backup_state, sign_count, transports FROM webauthn_credentials`).Scan(&be, &bs, &count, &transports)
	if !be || !bs || count != 0 || transports != `["internal"]` {
		t.Fatalf("stored: be=%v bs=%v count=%d transports=%s", be, bs, count, transports)
	}
	if _, body := b.get("/settings/security"); !strings.Contains(body, `<span class="pb-icon" aria-hidden="true"></span>synced</span>`) {
		t.Fatal("no synced badge")
	}
	// A synced passkey reports 0 every time: sign-ins in a row work.
	for i := range 2 {
		if status, out := newBrowser(t, s).passkeyLogin(synced, "/"); status != http.StatusOK {
			t.Fatalf("synced sign-in %d: %d %v", i, status, out)
		}
	}

	// A key that is neither discoverable nor synced is a security key.
	key := newAuthenticator()
	key.ResidentKey = false
	for range 2 {
		b.mustAddPasskey(key, url.Values{"current": {pw}, "passkey": {b.proof(synced)}})
		key.Credentials = nil // the next one is another key
	}
	if n := passkeyNames(t, s); n != "Passkey,Security key,Security key 2" {
		t.Fatalf("names %q", n)
	}

	// The backup state may change, and is kept.
	synced.Credentials[0].BackupState = false
	if status, _ := newBrowser(t, s).passkeyLogin(synced, "/"); status != http.StatusOK {
		t.Fatalf("after the backup state changed: %d", status)
	}
	_ = s.App.DB.R.QueryRow(`SELECT backup_state FROM webauthn_credentials ORDER BY id LIMIT 1`).Scan(&bs)
	if bs {
		t.Fatal("backup_state not updated")
	}
	// Backup eligibility can't.
	synced.Credentials[0].BackupEligible = false
	if status, _ := newBrowser(t, s).passkeyLogin(synced, "/"); status != http.StatusUnauthorized {
		t.Fatalf("backup eligibility flipped: %d", status)
	}
}

func TestPasskeySecondStepOwnCredentialsOnly(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	createUser(t, s, "bob", "bob@example.com", auth.RoleUser)
	ba, bb := newBrowser(t, s), newBrowser(t, s)
	ba.mustLogin("ann")
	bb.mustLogin("bob")
	annKey, annKey2, bobKey := newAuthenticator(), newAuthenticator(), newAuthenticator()
	ba.mustAddPasskey(annKey, url.Values{"current": {pw}})
	ba.mustAddPasskey(annKey2, url.Values{"current": {pw}, "passkey": {ba.proof(annKey)}})
	bb.mustAddPasskey(bobKey, url.Values{"current": {pw}})

	c := newBrowser(t, s)
	c.login("ann", pw)
	// Bob's passkey at Ann's second step (a client that ignores allowCredentials).
	bobKey.IgnoreLists = true
	if status, out := c.mfaPasskey(bobKey); status != http.StatusUnprocessableEntity {
		t.Fatalf("bob's passkey for ann: %d %v", status, out)
	}
	// A passkey removed after the step began.
	status, opts := c.ceremony("/login/2fa/passkey/begin", nil)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	assertion, err := annKey2.Get(opts)
	if err != nil {
		t.Fatal(err)
	}
	ids := passkeyIDs(t, s, 1)
	if _, err := s.App.DB.W.Exec(`DELETE FROM webauthn_credentials WHERE id = ?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if status, out := c.finish("/login/2fa/passkey/finish", assertion); status != http.StatusUnprocessableEntity {
		t.Fatalf("deleted passkey: %d %v", status, out)
	}
	if c.whoami() != "" {
		t.Fatal("signed in with someone else's or a deleted passkey")
	}
	// Failed passkeys count as attempts on the challenge, which then ends.
	var attempts int
	_ = s.App.DB.R.QueryRow(`SELECT attempts FROM auth_challenges WHERE kind = 'mfa_login'`).Scan(&attempts)
	if attempts != 2 {
		t.Fatalf("attempts %d, want 2", attempts)
	}
	for range 3 {
		if status, _ := c.mfaPasskey(bobKey); status != http.StatusUnprocessableEntity {
			t.Fatalf("failed passkey: %d", status)
		}
	}
	if status, out := c.mfaPasskey(bobKey); status != http.StatusUnauthorized || out["redirect"] != "/login" {
		t.Fatalf("after 5 failed passkeys: %d %v", status, out)
	}
	if status, _ := c.ceremony("/login/2fa/passkey/begin", nil); status != http.StatusUnauthorized {
		t.Fatalf("the challenge is still there: %d", status)
	}
	// They spent nothing else: the password opens the step again, and Ann's
	// own passkey finishes it.
	if res := c.login("ann", pw); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("password after failed passkeys: %d", res.StatusCode)
	}
	if status, out := c.mfaPasskey(annKey); status != http.StatusOK {
		t.Fatalf("ann's passkey: %d %v", status, out)
	}
	if c.whoami() != "ann" {
		t.Fatal("not signed in")
	}
}

func TestPasskeyChallengeSingleUseAndBound(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	a := newAuthenticator()
	a.Counting = false // so only the challenge can stop a replay
	b.mustAddPasskey(a, url.Values{"current": {pw}})

	// Sign-in: an assertion works once, in the browser that began it.
	c := newBrowser(t, s)
	_, opts := c.ceremony("/login/passkey/begin", nil)
	assertion, _ := a.Get(opts)
	if status, _ := c.finish("/login/passkey/finish", assertion); status != http.StatusOK {
		t.Fatalf("first finish: %d", status)
	}
	d := newBrowser(t, s)
	_, _ = d.ceremony("/login/passkey/begin", nil) // a challenge of its own
	for i, x := range []*browser{c, d, newBrowser(t, s)} {
		if status, _ := x.finish("/login/passkey/finish", assertion); status == http.StatusOK {
			t.Fatalf("browser %d: a sign-in assertion worked twice", i)
		}
	}

	// e's challenge, signed: f can't finish it with its own cookie, and
	// trying doesn't spoil it for e.
	e, f := newBrowser(t, s), newBrowser(t, s)
	_, eOpts := e.ceremony("/login/passkey/begin", nil)
	_, _ = f.ceremony("/login/passkey/begin", nil)
	forE, _ := a.Get(eOpts)
	if status, _ := f.finish("/login/passkey/finish", forE); status == http.StatusOK {
		t.Fatal("finished another browser's sign-in")
	}
	if f.whoami() != "" {
		t.Fatal("f is signed in")
	}
	if status, _ := e.finish("/login/passkey/finish", forE); status != http.StatusOK {
		t.Fatalf("e's own finish: %d", status)
	}

	// Registration: a finish works once, and only with the browser's challenge.
	_, regOpts := b.ceremony("/settings/security/passkeys/begin", url.Values{"current": {pw}, "passkey": {b.proof(a)}})
	other := newAuthenticator()
	cred, _, err := other.Create(regOpts)
	if err != nil {
		t.Fatal(err)
	}
	g := newBrowser(t, s)
	g.login("ann", pw)
	g.mfaPasskey(a)
	if res, _ := g.post("/settings/security/passkeys/finish", url.Values{"credential": {string(cred)}}); res.StatusCode != http.StatusConflict {
		t.Fatalf("registration finished in another browser: %d", res.StatusCode)
	}
	if res, _ := b.post("/settings/security/passkeys/finish", url.Values{"credential": {string(cred)}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("registration finish: %d", res.StatusCode)
	}
	if res, _ := b.post("/settings/security/passkeys/finish", url.Values{"credential": {string(cred)}}); res.StatusCode != http.StatusConflict {
		t.Fatalf("registration finished twice: %d", res.StatusCode)
	}
	if n := len(passkeyIDs(t, s, 1)); n != 2 {
		t.Fatalf("%d passkeys, want 2", n)
	}
}

func TestPasskeyCrossOriginRefused(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	a := newAuthenticator()
	b.mustAddPasskey(a, url.Values{"current": {pw}})
	var before int
	_ = s.App.DB.R.QueryRow(`SELECT count(*) FROM auth_challenges`).Scan(&before)

	crossSite := func(r *http.Request) {
		r.Header.Set("Accept", "application/json")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "https://evil.example")
	}
	for _, path := range []string{
		"/login/passkey/begin", "/login/passkey/finish",
		"/login/2fa/passkey/begin", "/login/2fa/passkey/finish",
		"/settings/security/passkeys/begin", "/settings/security/passkeys/finish",
		"/settings/security/reauth/passkey/begin",
		fmt.Sprintf("/settings/security/passkeys/%d/rename", passkeyIDs(t, s, 1)[0]),
		fmt.Sprintf("/settings/security/passkeys/%d/remove", passkeyIDs(t, s, 1)[0]),
	} {
		if res, _ := b.post(path, url.Values{"current": {pw}, "name": {"x"}}, crossSite); res.StatusCode != http.StatusForbidden {
			t.Errorf("cross-site POST %s: %d, want 403", path, res.StatusCode)
		}
	}
	var after int
	_ = s.App.DB.R.QueryRow(`SELECT count(*) FROM auth_challenges`).Scan(&after)
	if after != before {
		t.Fatalf("cross-site requests made %d challenges", after-before)
	}
	if n := passkeyNames(t, s); n != "Passkey" {
		t.Fatalf("passkeys after cross-site requests: %q", n)
	}
}

func TestPasskeyFirstEndsOtherSessions(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	here, there := newBrowser(t, s), newBrowser(t, s)
	here.mustLogin("ann")
	there.mustLogin("ann")
	a := newAuthenticator()

	page := here.mustAddPasskey(a, url.Values{"current": {pw}})
	if !strings.Contains(page, "Your other sessions were signed out.") || len(codeRE.FindAllString(page, -1)) != 10 {
		t.Fatal("the first passkey's page lacks the notice or the codes")
	}
	if here.whoami() != "ann" {
		t.Fatal("this browser was signed out")
	}
	if there.whoami() != "" {
		t.Fatal("the other session survived the first factor")
	}
	// The recovery codes work, once (they stand in for the passkey).
	code := codeRE.FindString(page)
	ok, err := s.App.Accounts.Store().UseRecoveryCode(context.Background(), 1, code)
	if err != nil || !ok {
		t.Fatalf("shown code: %v %v", ok, err)
	}

	// The second passkey changes neither: no codes, no sign-outs.
	there.login("ann", pw)
	there.mfaPasskey(a)
	res, body := here.addPasskey(newAuthenticator(), url.Values{"current": {pw}, "passkey": {here.proof(a)}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/security?saved=passkey" {
		t.Fatalf("second passkey: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if codeRE.MatchString(body) {
		t.Fatal("codes shown again")
	}
	if there.whoami() != "ann" {
		t.Fatal("the second passkey ended the other session")
	}
	f, _ := s.App.Accounts.Store().Factors(context.Background(), 1)
	if f.Passkeys != 2 || f.RecoveryLeft != 9 {
		t.Fatalf("factors %+v", f)
	}
}

func TestPasskeySetupAndLastFactor(t *testing.T) {
	s := newPasskeyServer(t, testutil.WithSMTP())
	if err := accounts.Require2FA.Set(context.Background(), s.App.Settings, accounts.Require2FAEveryone); err != nil {
		t.Fatal(err)
	}
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	createUser(t, s, "bob", "bob@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	if res, _ := b.get("/settings/profile"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("a session that needs setup reached the profile: %d", res.StatusCode)
	}
	// Adding a passkey is allowed during setup, and ends it.
	a := newAuthenticator()
	page := b.mustAddPasskey(a, url.Values{"current": {pw}})
	if !strings.Contains(page, `aria-label="Main"`) || strings.Contains(page, "Set up two-factor authentication to continue.") {
		t.Fatal("the page after the first passkey still looks like setup")
	}
	if b.whoami() != "ann" {
		t.Fatal("the session still needs setup")
	}

	// The only factor can't go while the policy covers the user.
	id := passkeyIDs(t, s, 1)[0]
	remove := func(id int64, form url.Values) (*response, string) {
		return b.post(fmt.Sprintf("/settings/security/passkeys/%d/remove", id), form)
	}
	res, body := remove(id, url.Values{"current": {pw}, "passkey": {b.proof(a)}})
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "Two-factor authentication is required on this site") {
		t.Fatalf("removing the last factor: %d", res.StatusCode)
	}
	if len(passkeyIDs(t, s, 1)) != 1 {
		t.Fatal("the last passkey was removed")
	}
	// Removing needs the password and a factor.
	second := newAuthenticator()
	b.mustAddPasskey(second, url.Values{"current": {pw}, "passkey": {b.proof(a)}})
	for _, form := range []url.Values{{}, {"current": {pw}}, {"current": {"wrong password!"}, "passkey": {b.proof(second)}}} {
		if res, _ := remove(id, form); res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("remove with %v: %d, want 422", form, res.StatusCode)
		}
	}
	// Someone else's passkey is not found.
	bb := newBrowser(t, s)
	bb.mustLogin("bob")
	bb.mustAddPasskey(newAuthenticator(), url.Values{"current": {pw}})
	bobs := passkeyIDs(t, s, 2)[0]
	if res, _ := remove(bobs, url.Values{"current": {pw}, "passkey": {b.proof(second)}}); res.StatusCode != http.StatusNotFound {
		t.Fatalf("removing bob's passkey: %d", res.StatusCode)
	}
	// With another factor left, it goes.
	if res, _ := remove(id, url.Values{"current": {pw}, "passkey": {b.proof(second)}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("remove: %d", res.StatusCode)
	}
	if ids := passkeyIDs(t, s, 1); len(ids) != 1 || ids[0] == id {
		t.Fatalf("passkeys left: %v", ids)
	}
	if countAudit(t, s, "mfa.passkey_removed") != 1 {
		t.Fatal("removal not audited")
	}
	waitMail(t, s, "ann@example.com", accounts.SecurityPasskeyRemoved)
	if _, body := b.get("/settings/security?saved=passkey_removed"); !strings.Contains(body, "Passkey removed.") {
		t.Fatal("no notice after removing")
	}
}

func TestPasskeyProof(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	a := newAuthenticator()
	b.mustAddPasskey(a, url.Values{"current": {pw}})
	begin := func(form url.Values) (int, string) {
		status, body := b.ceremony("/settings/security/passkeys/begin", form)
		var out map[string]string
		_ = json.Unmarshal(body, &out)
		return status, out["error"]
	}

	// With a factor, the password alone isn't enough.
	if status, msg := begin(url.Values{"current": {pw}}); status != http.StatusUnprocessableEntity || !strings.Contains(msg, "use a passkey") {
		t.Fatalf("password only: %d %q", status, msg)
	}
	if _, opts := b.ceremony("/settings/security/reauth/passkey/begin", nil); !strings.Contains(string(opts), `"userVerification":"required"`) {
		t.Fatalf("proof options: %s", opts)
	}
	// A proof without user verification is refused.
	a.UserVerified = false
	if status, _ := begin(url.Values{"current": {pw}, "passkey": {b.proof(a)}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("proof without user verification: %d", status)
	}
	a.UserVerified = true
	// A proof works once.
	proof := b.proof(a)
	if status, msg := begin(url.Values{"current": {pw}, "passkey": {proof}}); status != http.StatusOK {
		t.Fatalf("proof: %d %q", status, msg)
	}
	if status, _ := begin(url.Values{"current": {pw}, "passkey": {proof}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("proof used twice: %d", status)
	}
	// A proof made in another browser doesn't count here.
	other := newBrowser(t, s)
	other.login("ann", pw)
	other.mfaPasskey(a)
	_, _ = b.ceremony("/settings/security/reauth/passkey/begin", nil) // b has a challenge of its own
	if status, _ := begin(url.Values{"current": {pw}, "passkey": {other.proof(a)}}); status != http.StatusUnprocessableEntity {
		t.Fatalf("another browser's proof: %d", status)
	}
	// A proof still needs the password with it.
	if status, msg := begin(url.Values{"passkey": {b.proof(a)}}); status != http.StatusUnprocessableEntity || !strings.Contains(msg, "current password") {
		t.Fatalf("proof without the password: %d %q", status, msg)
	}
	// Someone without a passkey gets no options.
	createUser(t, s, "bob", "bob@example.com", auth.RoleUser)
	bb := newBrowser(t, s)
	bb.mustLogin("bob")
	if status, _ := bb.ceremony("/settings/security/reauth/passkey/begin", nil); status != http.StatusConflict {
		t.Fatalf("proof options for a user without passkeys: %d", status)
	}
}

func TestPasskeyRename(t *testing.T) {
	s := newPasskeyServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	createUser(t, s, "bob", "bob@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	b.mustAddPasskey(newAuthenticator(), url.Values{"current": {pw}})
	id := passkeyIDs(t, s, 1)[0]
	rename := func(b *browser, id int64, name string) int {
		res, _ := b.post(fmt.Sprintf("/settings/security/passkeys/%d/rename", id), url.Values{"name": {name}})
		return res.StatusCode
	}
	if c := rename(b, id, "  YubiKey 5C  "); c != http.StatusSeeOther {
		t.Fatalf("rename: %d", c)
	}
	if n := passkeyNames(t, s); n != "YubiKey 5C" {
		t.Fatalf("name %q", n)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("é", 65), "tab\there", "rtl" + string(rune(0x202e)) + "override"} {
		if c := rename(b, id, bad); c != http.StatusUnprocessableEntity {
			t.Errorf("rename to %q: %d", bad, c)
		}
	}
	if c := rename(b, id, strings.Repeat("é", 64)); c != http.StatusSeeOther {
		t.Fatalf("64 characters: %d", c)
	}
	bb := newBrowser(t, s)
	bb.mustLogin("bob")
	if c := rename(bb, id, "mine now"); c != http.StatusNotFound {
		t.Fatalf("renaming someone else's passkey: %d", c)
	}
	if countAudit(t, s, "mfa.passkey_renamed") != 2 {
		t.Fatal("renames not audited")
	}
}

func TestPasskeySignInRefused(t *testing.T) {
	s := newPasskeyServer(t)
	ann := createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	a := newAuthenticator()
	b.mustAddPasskey(a, url.Values{"current": {pw}})
	ctx := context.Background()

	// A disabled account.
	if err := s.App.Accounts.Store().SetStatus(ctx, ann.ID, accounts.StatusDisabled, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	c := newBrowser(t, s)
	if status, out := c.passkeyLogin(a, "/"); status != http.StatusForbidden || out["error"] != "This account is disabled." {
		t.Fatalf("disabled: %d %v", status, out)
	}
	if c.whoami() != "" || c.cookie(sessionCookie) != "" {
		t.Fatal("a disabled account signed in")
	}
	if err := s.App.Accounts.Store().SetStatus(ctx, ann.ID, accounts.StatusActive, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}

	// Password sign-in off: passkeys don't sign in on their own either.
	_, opts := c.ceremony("/login/passkey/begin", nil)
	assertion, _ := a.Get(opts)
	if err := accounts.PasswordLogin.Set(ctx, s.App.Settings, false); err != nil {
		t.Fatal(err)
	}
	if status, _ := c.ceremony("/login/passkey/begin", nil); status != http.StatusForbidden {
		t.Fatalf("begin with password sign-in off: %d", status)
	}
	if status, _ := c.finish("/login/passkey/finish", assertion); status != http.StatusForbidden {
		t.Fatalf("finish with password sign-in off: %d", status)
	}
	if c.whoami() != "" {
		t.Fatal("signed in with password sign-in off")
	}
	if _, body := c.get("/login"); strings.Contains(body, `data-webauthn="login"`) {
		t.Fatal("the sign-in page offers a passkey with password sign-in off")
	}
	_ = accounts.PasswordLogin.Set(ctx, s.App.Settings, true)
	if _, body := c.get("/login"); !strings.Contains(body, `data-webauthn="login"`) || !strings.Contains(body, `autocomplete="username webauthn"`) {
		t.Fatal("the sign-in page has no passkey button or autofill")
	}
}

func TestPasskeyBeginLimit(t *testing.T) {
	s := newPasskeyServer(t)
	b := newBrowser(t, s)
	for i := range 30 {
		if status, _ := b.ceremony("/login/passkey/begin", nil); status != http.StatusOK {
			t.Fatalf("begin %d: %d", i, status)
		}
	}
	if status, _ := b.ceremony("/login/passkey/begin", nil); status != http.StatusTooManyRequests {
		t.Fatalf("begin 31: %d, want 429", status)
	}
	// Its own budget: the password one is untouched.
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b.mustLogin("ann")
}

// On an IP address (or a single-label host) passkeys can't work: the
// ceremonies say so instead of failing with a server error.
func TestPasskeysNeedADomain(t *testing.T) {
	s := newServer(t) // https://127.0.0.1:port
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	status, body := b.ceremony("/login/passkey/begin", nil)
	if status != http.StatusServiceUnavailable || !strings.Contains(string(body), "Passkeys don't work on this site") {
		t.Fatalf("passkey sign-in on an IP address: %d %s", status, body)
	}
	b.mustLogin("ann")
	if status, _ := b.ceremony("/settings/security/passkeys/begin", url.Values{"current": {pw}}); status != http.StatusServiceUnavailable {
		t.Fatalf("adding a passkey on an IP address: %d", status)
	}
}
