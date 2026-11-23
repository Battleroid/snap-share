package redteam_test

// Passkey (WebAuthn) attacks on sign-in, as inverted PoCs: each passes
// while the server refuses the attack.

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts/webauthntest"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

const rtHost = "snap.example.com"

// pkBrowser is one cookie jar, for ceremonies that keep their state in a cookie.
type pkBrowser struct {
	t *testing.T
	e *env
	c *http.Client
}

func (e *env) browser() *pkBrowser { return &pkBrowser{t: e.t, e: e, c: e.s.Client()} }

func (b *pkBrowser) send(path, ctype string, body []byte, accept bool) (int, []byte) {
	b.t.Helper()
	req := b.e.s.Request(b.t, "POST", path, bytes.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	if accept {
		req.Header.Set("Accept", "application/json")
	}
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func (b *pkBrowser) form(path string, form url.Values) (int, []byte) {
	return b.send(path, "application/x-www-form-urlencoded", []byte(form.Encode()), true)
}

func (b *pkBrowser) json(path string, body []byte) int {
	status, _ := b.send(path, "application/json", body, true)
	return status
}

func (b *pkBrowser) login(name, password string) int {
	status, _ := b.send("/login", "application/x-www-form-urlencoded", []byte(url.Values{"login": {name}, "password": {password}}.Encode()), false)
	return status
}

func (b *pkBrowser) signedIn() bool {
	b.t.Helper()
	res, err := b.c.Do(b.e.s.Request(b.t, "GET", "/settings/profile", nil))
	if err != nil {
		b.t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// options begins a ceremony and returns its options.
func (b *pkBrowser) options(path string, form url.Values) []byte {
	b.t.Helper()
	status, opts := b.form(path, form)
	if status != http.StatusOK {
		b.t.Fatalf("%s: %d %s", path, status, opts)
	}
	return opts
}

// passkeyVictim makes a user with a password and one passkey on a.
func (e *env) passkeyVictim(name string, a *webauthntest.Authenticator) *pkBrowser {
	e.t.Helper()
	u := e.user(name, auth.RoleUser)
	e.setPassword(u, "victim-password-42")
	b := e.browser()
	if c := b.login(name, "victim-password-42"); c != http.StatusSeeOther {
		e.t.Fatalf("victim sign-in: %d", c)
	}
	cred, _, err := a.Create(b.options("/settings/security/passkeys/begin", url.Values{"current": {"victim-password-42"}}))
	if err != nil {
		e.t.Fatal(err)
	}
	if status, body := b.form("/settings/security/passkeys/finish", url.Values{"credential": {string(cred)}}); status != http.StatusOK {
		e.t.Fatalf("passkey finish: %d %s", status, body)
	}
	return b
}

// A security key without a PIN (presence only), or a stolen phone whose
// lock was bypassed, can't sign in without the password, nor stand in for
// one in a sensitive change: a passkey alone must verify its user.
func TestPasskeyUserVerificationBypass(t *testing.T) {
	e := newEnv(t, testutil.Host(rtHost))
	key := webauthntest.New("https://" + rtHost)
	victim := e.passkeyVictim("victim", key)
	key.UserVerified = false

	attacker := e.browser()
	assertion, err := key.Get(attacker.options("/login/passkey/begin", nil))
	if err != nil {
		t.Fatal(err)
	}
	if status := attacker.json("/login/passkey/finish", assertion); status == http.StatusOK || attacker.signedIn() {
		t.Fatalf("a presence-only assertion signed in without the password: %d", status)
	}
	// The second step's options allow presence only, but its challenge is
	// bound to an mfa_login (the password) and can't finish a passkey sign-in.
	attacker.login("victim", "victim-password-42")
	mfaOpts := attacker.options("/login/2fa/passkey/begin", nil)
	other := e.browser()
	other.options("/login/passkey/begin", nil)
	forMFA, _ := key.Get(mfaOpts)
	if status := other.json("/login/passkey/finish", forMFA); status == http.StatusOK || other.signedIn() {
		t.Fatalf("a second-step assertion finished a passkey sign-in: %d", status)
	}
	// As proof for a change, presence alone is not enough either.
	proof, _ := key.Get(victim.options("/settings/security/reauth/passkey/begin", nil))
	if status, _ := victim.form("/settings/security/passkeys/begin", url.Values{"current": {"victim-password-42"}, "passkey": {string(proof)}}); status == http.StatusOK {
		t.Fatal("a presence-only proof passed confirmOwner")
	}
}

// A captured assertion can't be used again: not in the browser that made
// it, not with a fresh challenge, not at the second step after the password.
func TestPasskeyAssertionReplay(t *testing.T) {
	e := newEnv(t, testutil.Host(rtHost))
	key := webauthntest.New("https://" + rtHost)
	key.Counting = false // a synced passkey: the counter can't catch a replay
	e.passkeyVictim("victim", key)

	b := e.browser()
	assertion, _ := key.Get(b.options("/login/passkey/begin", nil))
	if status := b.json("/login/passkey/finish", assertion); status != http.StatusOK {
		t.Fatalf("the victim's own sign-in: %d", status)
	}
	attacker := e.browser()
	for range 2 {
		if status := b.json("/login/passkey/finish", assertion); status == http.StatusOK {
			t.Fatal("replayed in the same browser")
		}
		attacker.options("/login/passkey/begin", nil)
		if status := attacker.json("/login/passkey/finish", assertion); status == http.StatusOK || attacker.signedIn() {
			t.Fatal("replayed with a fresh challenge")
		}
	}

	// Second step: the victim finishes theirs; an attacker with the password
	// replays that assertion on a new one.
	v := e.browser()
	v.login("victim", "victim-password-42")
	step, _ := key.Get(v.options("/login/2fa/passkey/begin", nil))
	if status := v.json("/login/2fa/passkey/finish", step); status != http.StatusOK {
		t.Fatalf("the victim's second step: %d", status)
	}
	if status := v.json("/login/2fa/passkey/finish", step); status == http.StatusOK {
		t.Fatal("the second step finished twice")
	}
	attacker.login("victim", "victim-password-42")
	attacker.options("/login/2fa/passkey/begin", nil)
	if status := attacker.json("/login/2fa/passkey/finish", step); status == http.StatusOK || attacker.signedIn() {
		t.Fatal("a replayed assertion passed the second step")
	}
}

// An assertion made for someone else's challenge doesn't finish yours:
// not a sign-in begun in another browser, not a second step begun by the
// victim, and not one a phishing page relayed from its own origin.
func TestPasskeyFinishSomeoneElsesChallenge(t *testing.T) {
	e := newEnv(t, testutil.Host(rtHost))
	key := webauthntest.New("https://" + rtHost)
	e.passkeyVictim("victim", key)

	// The victim begins a sign-in; the attacker gets the assertion (a
	// malicious extension, a shoulder) and sends it with their own cookie.
	victim, attacker := e.browser(), e.browser()
	forVictim, _ := key.Get(victim.options("/login/passkey/begin", nil))
	attacker.options("/login/passkey/begin", nil)
	if status := attacker.json("/login/passkey/finish", forVictim); status == http.StatusOK || attacker.signedIn() {
		t.Fatal("finished the victim's sign-in with the attacker's cookie")
	}
	if status := e.browser().json("/login/passkey/finish", forVictim); status == http.StatusOK {
		t.Fatal("finished without any challenge cookie")
	}

	// The attacker knows the password and has a second step of their own;
	// the victim's second-step assertion doesn't finish it.
	victim.login("victim", "victim-password-42")
	forVictimStep, _ := key.Get(victim.options("/login/2fa/passkey/begin", nil))
	attacker.login("victim", "victim-password-42")
	attacker.options("/login/2fa/passkey/begin", nil)
	if status := attacker.json("/login/2fa/passkey/finish", forVictimStep); status == http.StatusOK || attacker.signedIn() {
		t.Fatal("finished the attacker's second step with the victim's assertion")
	}

	// A phishing site relays the server's options and the victim's
	// authenticator signs them for the phishing origin (rpId as sent).
	phished := webauthntest.New("https://snap-example.com.evil.example")
	phished.Use = key.Credentials[0]
	relay := e.browser()
	forPhish, _ := phished.Get(relay.options("/login/passkey/begin", nil))
	if status := relay.json("/login/passkey/finish", forPhish); status == http.StatusOK || relay.signedIn() {
		t.Fatal("a relayed (phished) assertion signed in")
	}
	// Even with the attacker's RP ID hashed in.
	phished.RPID = "evil.example"
	forPhish, _ = phished.Get(relay.options("/login/passkey/begin", nil))
	if status := relay.json("/login/passkey/finish", forPhish); status == http.StatusOK || relay.signedIn() {
		t.Fatal("an assertion for another RP ID signed in")
	}
	var logins int
	_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'auth.login'`).Scan(&logins)
	if logins != 1 { // the victim's password sign-in before the passkey was added
		t.Fatalf("%d sign-ins, want 1", logins)
	}
}
