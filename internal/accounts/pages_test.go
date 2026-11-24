package accounts_test

// What the auth pages put on the page: server errors on the field they are
// about, the compact result for every end state, one primary per card.

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestWrongLoginErrorsOnTheFields(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	res, body := newBrowser(t, s).post("/login", url.Values{"login": {"ann"}, "password": {"nope nope nope"}})
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	// Both fields are invalid (the page never says which part was wrong); the
	// sentence is on the password field, not in a banner above the form.
	if n := strings.Count(body, `aria-invalid="true"`); n != 2 {
		t.Errorf("%d invalid controls, want 2", n)
	}
	for _, want := range []string{`id="login-password-error"`, `aria-describedby="login-password-error"`, "Wrong username or password.", "data-focus-error", `value="ann"`} {
		if !strings.Contains(body, want) {
			t.Errorf("wrong-login page lacks %q", want)
		}
	}
	if strings.Count(body, "Wrong username or password.") != 1 {
		t.Error("the wrong-login error is also a banner")
	}
	// A fresh page has nothing invalid and focuses the username.
	_, fresh := newBrowser(t, s).get("/login")
	if strings.Contains(fresh, "aria-invalid") || strings.Count(fresh, " autofocus") != 1 {
		t.Error("fresh sign-in page: invalid controls, or no single autofocus")
	}
	// The password form owns the one primary.
	if n := strings.Count(fresh, "pb-btn--primary"); n != 1 {
		t.Errorf("%d primary buttons on sign-in", n)
	}
}

func TestRegisterErrorsOnTheirFields(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	_ = accounts.SignupMode.Set(t.Context(), s.App.Settings, accounts.SignupOpen)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	_, body := newBrowser(t, s).post("/register", url.Values{"username": {"ann"}, "email": {"new@example.com"}, "password": {"short"}})
	for _, want := range []string{"That username is taken.", "pb-strength"} {
		if !strings.Contains(body, want) {
			t.Errorf("register page lacks %q", want)
		}
	}
	if !regexp.MustCompile(`id="f-username-[0-9]+-error"`).MatchString(body) {
		t.Error("the username error has no id for aria-describedby")
	}
	if n := strings.Count(body, `aria-invalid="true"`); n != 2 {
		t.Errorf("%d invalid controls, want username and password", n)
	}
	if n := strings.Count(body, " autofocus"); n != 2 {
		t.Errorf("%d autofocus attributes, want one per invalid field (the first wins)", n)
	}
}

func TestEndStatesAreCompactResults(t *testing.T) {
	s := newServer(t, testutil.WithSMTP())
	b := newBrowser(t, s)
	for _, p := range []string{"/verify", "/invite", "/reset"} {
		_, body := b.get(p)
		if !strings.Contains(body, "pb-result--compact") || strings.Contains(body, "app-auth-result") || strings.Contains(body, "app-result-page") {
			t.Errorf("%s: end states are not the kit's compact Result", p)
		}
		if strings.Contains(body, `class="pb-notice" data-status="down" role="alert"`) {
			t.Errorf("%s: a failure is still a notice", p)
		}
	}
	// The error page too, with its way out.
	res, body := b.get("/no-such-page-at-all")
	if res.StatusCode != http.StatusNotFound || !strings.Contains(body, "pb-result pb-result--compact") || !strings.Contains(body, "Go to ") {
		t.Errorf("error page: %d, no compact result with a way out", res.StatusCode)
	}
}
