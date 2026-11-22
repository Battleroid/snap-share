package redteam_test

// Regression test: API keys used to survive "sign out everywhere".

import (
	"net/http"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

func TestSignOutEverywhereEndsKeys(t *testing.T) {
	e := newEnv(t)
	vic := e.user("vic", auth.RoleUser)
	tok := e.mint(vic, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAccountRead}})
	if res, m := e.doJSON("GET", "/api/v1/me", nil, bearer(tok)); res.StatusCode != http.StatusOK || m["username"] != "vic" {
		t.Fatalf("key before: %d %v", res.StatusCode, m)
	}
	if res, _ := e.do("POST", "/settings/security/signout-all", nil, withCookie(e.session(vic))); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign out everywhere: %d", res.StatusCode)
	}
	if res, _ := e.doJSON("GET", "/api/v1/me", nil, bearer(tok)); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("key after sign out everywhere: %d, want 401", res.StatusCode)
	}
}
