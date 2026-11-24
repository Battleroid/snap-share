package accounts_test

// POST /reset/check lets the reset page say "expired or used" before anyone
// types a password. Redteam: it must be no token oracle beyond what POST
// /reset already is (a bad token is a 404 there before the password is
// read), must say nothing about the account, and is rate limited.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestResetCheckIsNoOracle(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	b := newBrowser(t, s)
	b.post("/reset/request", url.Values{"email": {"ann@example.com"}})
	token := lastLink(t, s, "ann@example.com", "/reset")

	t.Run("a live token", func(t *testing.T) {
		res, body := b.post("/reset/check", url.Values{"token": {token}}, func(r *http.Request) { r.Header.Set("Accept", "application/json") })
		if res.StatusCode != http.StatusOK {
			t.Fatalf("live token: %d %s", res.StatusCode, body)
		}
		var out map[string]string
		if err := json.Unmarshal([]byte(body), &out); err != nil || len(out) != 1 || out["valid"] != "1" {
			t.Errorf("the answer carries more than valid: %s", body)
		}
		if strings.Contains(body, "ann") {
			t.Errorf("the answer names the account: %s", body)
		}
		if len(res.Cookies()) != 0 {
			t.Errorf("a check set cookies: %v", res.Cookies())
		}
		// The check changed nothing: the token still resets.
		if res, _ := b.post("/reset", url.Values{"token": {token}, "password": {"short"}}); res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("token after a check: %d", res.StatusCode)
		}
	})

	t.Run("every dead token answers alike", func(t *testing.T) {
		if res, _ := b.post("/reset", url.Values{"token": {token}, "password": {"a brand new password"}}); res.StatusCode != http.StatusSeeOther {
			t.Fatalf("reset: %d", res.StatusCode)
		}
		used := token
		unknown := strings.Repeat("A", len(token))
		var first string
		for _, tok := range []string{used, unknown, "x", "", strings.Repeat("z", 500)} {
			res, body := b.post("/reset/check", url.Values{"token": {tok}}, func(r *http.Request) { r.Header.Set("Accept", "application/json") })
			if res.StatusCode != http.StatusNotFound {
				t.Errorf("token %.8q: %d, want 404", tok, res.StatusCode)
			}
			if first == "" {
				first = body
			} else if body != first {
				t.Errorf("token %.8q: a different answer:\n%s\nvs\n%s", tok, body, first)
			}
			if strings.Contains(body, "ann") || strings.Contains(body, "example.com") {
				t.Errorf("a dead token's answer names the account: %s", body)
			}
		}
	})
}

func TestResetCheckIsRateLimited(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	b := newBrowser(t, s)
	for i := range 10 {
		if res, _ := b.post("/reset/check", url.Values{"token": {"x"}}); res.StatusCode != http.StatusNotFound {
			t.Fatalf("check %d: %d", i, res.StatusCode)
		}
	}
	if res, _ := b.post("/reset/check", url.Values{"token": {"x"}}); res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Errorf("11th check in an hour: %d", res.StatusCode)
	}
}

func TestResetPageChecksItsLink(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	_, body := newBrowser(t, s).get("/reset")
	for _, want := range []string{`data-check="/reset/check"`, `data-state="failed"`, "Request a new link", "other sessions end"} {
		if !strings.Contains(body, want) {
			t.Errorf("reset page lacks %q", want)
		}
	}
	for _, r := range s.App.Router.Routes() {
		if strings.HasSuffix(r.Pattern, " /reset/check") && !r.Redact {
			t.Errorf("%s is not redacted in logs", r.Pattern)
		}
	}
}
