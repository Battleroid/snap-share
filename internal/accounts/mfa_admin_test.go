package accounts_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestAdminResetMFA(t *testing.T) {
	e := newMFAEnv(t, testutil.WithSMTP())
	root := createUser(t, e.s, "root", "root@example.com", auth.RoleAdmin)
	bob := createUser(t, e.s, "bob", "bob@example.com", auth.RoleUser)
	ra, bb := newBrowser(t, e.s), newBrowser(t, e.s)
	ra.mustLogin("root")
	bb.mustLogin("bob")
	e.setupTOTP(bb, nil)
	rootSecret, _ := e.setupTOTP(ra, nil)
	path := fmt.Sprintf("/admin/users/%d/mfa-reset", bob.ID)

	if _, body := ra.get(fmt.Sprintf("/admin/users/%d", bob.ID)); !strings.Contains(body, "passkeys 0 · app on · codes 10") || !strings.Contains(body, `action="`+path+`"`) {
		t.Error("user page lacks the 2FA status or the reset")
	}
	if _, body := ra.get("/admin/users"); !strings.Contains(body, "passkeys 0 · app on · codes 10") {
		t.Error("users table lacks the 2FA status")
	}
	// The admin's own factor is needed, not just their password.
	if res, _ := ra.post(path, url.Values{"current": {pw}}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reset with the admin's password alone: %d", res.StatusCode)
	}
	if res, _ := ra.post(path, url.Values{"current": {"wrong password"}, "code": {e.code(rootSecret)}}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reset with a wrong password: %d", res.StatusCode)
	}
	if !factorsOf(t, e.s, bob.ID).TOTP || bb.whoami() != "bob" {
		t.Fatal("a refused reset changed something")
	}
	// Not their own (that's Settings).
	if res, _ := ra.post(fmt.Sprintf("/admin/users/%d/mfa-reset", root.ID), url.Values{"current": {pw}, "code": {e.code(rootSecret)}}); res.StatusCode != http.StatusConflict {
		t.Errorf("reset own: %d", res.StatusCode)
	}
	// Not with an API key, even one with the admin scope.
	tok, _, err := e.s.App.Keys.Store().Create(context.Background(), root.ID, keys.NewKey{Name: "adm", Scopes: []auth.Scope{auth.ScopeAdmin}}, accounts.AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := regBearer(t, e.s, tok, "POST", path, ""); code != http.StatusForbidden {
		t.Errorf("reset with an admin key: %d", code)
	}
	if !factorsOf(t, e.s, bob.ID).TOTP {
		t.Fatal("reset without the admin's proof")
	}

	res, _ := ra.post(path, url.Values{"current": {pw}, "code": {e.code(rootSecret)}, "back": {fmt.Sprintf("/admin/users/%d", bob.ID)}})
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "done=mfa_reset") {
		t.Fatalf("reset: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if f := factorsOf(t, e.s, bob.ID); f.Any() || f.RecoveryLeft != 0 {
		t.Errorf("bob's factors after the reset: %+v", f)
	}
	if bb.whoami() != "" || sessionCount(t, e.s, bob.ID) != 0 {
		t.Error("bob's sessions survived the reset")
	}
	if countAudit(t, e.s, "user.mfa_reset") != 1 {
		t.Error("reset not audited")
	}
	waitMail(t, e.s, "bob@example.com", accounts.SecurityMFAReset)
	if res := newBrowser(t, e.s).login("bob", pw); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/" {
		t.Errorf("bob's password sign-in after the reset: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestAdminKeyNeedsConfirmWith2FA(t *testing.T) {
	e := newMFAEnv(t)
	root := createUser(t, e.s, "root", "root@example.com", auth.RoleAdmin)
	b := newBrowser(t, e.s)
	b.mustLogin("root")
	// Without 2FA nothing extra is asked.
	if res, _ := b.post("/settings/keys", url.Values{"name": {"before"}, "scope": {"admin"}, "expires": {"never"}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("admin key without 2FA: %d", res.StatusCode)
	}
	secret, _ := e.setupTOTP(b, nil)
	if _, body := b.get("/settings/keys/new"); !strings.Contains(body, `name="current"`) || !strings.Contains(body, `name="code"`) {
		t.Error("the form lacks confirm it's you")
	}
	post := func(scope string, proof url.Values) int {
		form := url.Values{"name": {"k"}, "scope": {scope}, "expires": {"never"}}
		for k, v := range proof {
			form[k] = v
		}
		res, _ := b.post("/settings/keys", form)
		return res.StatusCode
	}
	if code := post("admin", nil); code != http.StatusUnprocessableEntity {
		t.Errorf("admin key without proof: %d", code)
	}
	if code := post("admin", url.Values{"current": {pw}}); code != http.StatusUnprocessableEntity {
		t.Errorf("admin key with the password alone: %d", code)
	}
	if code := post("items:read", nil); code != http.StatusCreated {
		t.Errorf("other key: %d", code)
	}
	if code := post("admin", url.Values{"current": {pw}, "code": {e.code(secret)}}); code != http.StatusCreated {
		t.Errorf("admin key with password and code: %d", code)
	}
	if code, _ := b.regJSON("POST", "/api/v1/keys", `{"name":"api","scopes":["admin"]}`); code != http.StatusForbidden {
		t.Errorf("admin key through the API: %d", code)
	}
	if code, _ := b.regJSON("POST", "/api/v1/keys", `{"name":"api","scopes":["items:read"]}`); code != http.StatusCreated {
		t.Errorf("other key through the API: %d", code)
	}
	var n int
	_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = ? AND scopes LIKE '%admin%'`, root.ID).Scan(&n)
	if n != 2 {
		t.Errorf("%d admin keys, want 2", n)
	}
}
