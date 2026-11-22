package accounts_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/oidctest"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func ssoServer(t *testing.T, opts ...testutil.Option) (*testutil.Server, *oidctest.Provider) {
	t.Helper()
	p := oidctest.New(t)
	base := []testutil.Option{
		testutil.OIDCClient(p.Client()),
		testutil.Env("OIDC_ISSUER", p.URL),
		testutil.Env("OIDC_CLIENT_ID", p.ClientID),
		testutil.Env("OIDC_CLIENT_SECRET", p.ClientSecret),
		testutil.Env("OIDC_NAME", "Gitea"),
		testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1,::1"),
	}
	return newServer(t, append(base, opts...)...), p
}

// ssoFlow follows start -> provider -> callback and returns the callback's answer.
func (b *browser) ssoFlow(start string) (*response, string) {
	b.t.Helper()
	res, _ := b.get(start)
	if res.StatusCode != http.StatusFound {
		b.t.Fatalf("start: %d", res.StatusCode)
	}
	authz := res.Header.Get("Location")
	q := mustParse(b.t, authz).Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
		b.t.Fatalf("authorize request lacks PKCE/nonce/state: %s", authz)
	}
	if scope := q.Get("scope"); scope != "openid profile email groups" {
		b.t.Errorf("scope %q", scope)
	}
	res, _ = b.getURL(authz)
	if res.StatusCode != http.StatusFound {
		b.t.Fatalf("authorize: %d", res.StatusCode)
	}
	return b.getURL(res.Header.Get("Location"))
}

// authorize runs start and the provider step only, returning the callback URL.
func (b *browser) authorize(start string) string {
	b.t.Helper()
	res, _ := b.get(start)
	res, _ = b.getURL(res.Header.Get("Location"))
	return res.Header.Get("Location")
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func identities(t *testing.T, s *testutil.Server, username string) []accounts.Identity {
	t.Helper()
	u, err := s.App.Accounts.Store().UserByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("%s: %v", username, err)
	}
	ids, err := s.App.Accounts.Store().Identities(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestSSOProvisionAndReturn(t *testing.T) {
	s, p := ssoServer(t)
	p.SetUser(oidctest.User{Subject: "42", PreferredUsername: "gina", Email: "gina@example.com", EmailVerified: true, Groups: []string{"staff"}})
	b := newBrowser(t, s)

	if _, body := b.get("/login"); !strings.Contains(body, "Continue with Gitea") {
		t.Fatal("no SSO button")
	}
	res, _ := b.ssoFlow("/auth/oidc/start?next=/settings/security")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/security" {
		t.Fatalf("callback: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if b.whoami() != "gina" {
		t.Fatal("not signed in as the provisioned user")
	}
	if b.cookie("__Host-snap_oidc") != "" {
		t.Error("state cookie not cleared")
	}
	ids := identities(t, s, "gina")
	if len(ids) != 1 || ids[0].Provider != p.URL || ids[0].Subject != "42" || strings.Join(ids[0].Groups, ",") != "staff" {
		t.Fatalf("identity %+v", ids)
	}
	u, _ := s.App.Accounts.Store().UserByUsername(context.Background(), "gina")
	if u.Email != "gina@example.com" || u.EmailVerifiedAt == nil || u.HasPassword() || u.Role != auth.RoleUser {
		t.Errorf("provisioned user %+v", u)
	}

	// Same subject again: same account, even if the provider username changed.
	p.SetUser(oidctest.User{Subject: "42", PreferredUsername: "gina-renamed", Email: "gina@example.com", EmailVerified: true})
	b2 := newBrowser(t, s)
	if res, _ := b2.ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("second sign-in: %d", res.StatusCode)
	}
	if b2.whoami() != "gina" {
		t.Error("second sign-in did not reach the same account")
	}
	if n, _ := s.App.Accounts.Store().CountUsers(context.Background()); n != 1 {
		t.Errorf("%d users", n)
	}
	if ids := identities(t, s, "gina"); ids[0].Username != "gina-renamed" || len(ids[0].Groups) != 0 {
		t.Errorf("identity not refreshed: %+v", ids[0])
	}

	// The SSO-only account cannot be unlinked (it would have no way in).
	_, body := b2.get("/settings/security")
	if strings.Contains(body, "/unlink") {
		t.Error("unlink offered for the only sign-in method")
	}
	if res, _ := b2.post("/settings/security/identities/"+itoa(ids[0].ID)+"/unlink", nil); res.StatusCode != http.StatusConflict {
		t.Errorf("unlink last method: %d", res.StatusCode)
	}
	// After setting a password it can.
	if res, _ := b2.post("/settings/security/password", url.Values{"password": {"a password for gina"}}); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("set password: %d", res.StatusCode)
	}
	if res, _ := b2.post("/settings/security/identities/"+itoa(ids[0].ID)+"/unlink", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("unlink: %d", res.StatusCode)
	}
	if countAudit(t, s, "sso.unlink") != 1 {
		t.Error("unlink not audited")
	}
}

func TestSSOGiteaIssuerWithSlash(t *testing.T) {
	p := oidctest.New(t)
	p.IssuerSlash = true
	s := newServer(t, testutil.OIDCClient(p.Client()), testutil.Env("OIDC_ISSUER", p.URL+"/"),
		testutil.Env("OIDC_CLIENT_ID", p.ClientID), testutil.Env("OIDC_CLIENT_SECRET", p.ClientSecret))
	b := newBrowser(t, s)
	if res, _ := b.ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("callback: %d", res.StatusCode)
	}
	if b.whoami() != "ann" {
		t.Error("not signed in")
	}
}

func TestSSOLinkFromSettings(t *testing.T) {
	s, p := ssoServer(t)
	createUser(t, s, "ann", "ann@example.com", auth.RoleUser)
	createUser(t, s, "bob", "", auth.RoleUser)
	b := newBrowser(t, s)
	b.mustLogin("ann")
	p.SetUser(oidctest.User{Subject: "7", PreferredUsername: "annie", Email: "other@example.com", EmailVerified: true})

	_, body := b.get("/settings/security")
	if !strings.Contains(body, "/auth/oidc/start?link=1") {
		t.Fatal("no link button")
	}
	res, _ := b.ssoFlow("/auth/oidc/start?link=1")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/security?saved=linked" {
		t.Fatalf("link: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if ids := identities(t, s, "ann"); len(ids) != 1 || ids[0].Subject != "7" {
		t.Fatalf("identities %+v", ids)
	}
	if countAudit(t, s, "sso.link") != 1 {
		t.Error("link not audited")
	}
	// Signing in with it now reaches ann; no new account is created.
	c := newBrowser(t, s)
	c.ssoFlow("/auth/oidc/start")
	if c.whoami() != "ann" {
		t.Error("linked identity does not sign in as ann")
	}
	// The same provider account cannot be linked to bob too.
	d := newBrowser(t, s)
	d.mustLogin("bob")
	if res, body := d.ssoFlow("/auth/oidc/start?link=1"); res.StatusCode != http.StatusConflict || !strings.Contains(body, "linked to another user") {
		t.Errorf("link taken identity: %d", res.StatusCode)
	}
	// Linking needs a signed-in user.
	if res, _ := newBrowser(t, s).get("/auth/oidc/start?link=1"); res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Errorf("anonymous link: %d", res.StatusCode)
	}
}

func TestSSONeverMergesLocalAccounts(t *testing.T) {
	t.Run("same username", func(t *testing.T) {
		s, p := ssoServer(t)
		createUser(t, s, "ann", "", auth.RoleUser)
		p.SetUser(oidctest.User{Subject: "9", PreferredUsername: "ann", Email: "ann@sso.example", EmailVerified: true})
		b := newBrowser(t, s)
		b.ssoFlow("/auth/oidc/start")
		if got := b.whoami(); got != "ann-2" {
			t.Fatalf("signed in as %q", got)
		}
		if ids := identities(t, s, "ann"); len(ids) != 0 {
			t.Error("local account got an identity")
		}
	})
	t.Run("same verified email, linking off (default)", func(t *testing.T) {
		s, p := ssoServer(t)
		createUser(t, s, "local", "shared@example.com", auth.RoleUser)
		p.SetUser(oidctest.User{Subject: "10", PreferredUsername: "remote", Email: "shared@example.com", EmailVerified: true})
		b := newBrowser(t, s)
		b.ssoFlow("/auth/oidc/start")
		if got := b.whoami(); got != "remote" {
			t.Fatalf("signed in as %q", got)
		}
		if ids := identities(t, s, "local"); len(ids) != 0 {
			t.Error("linked by email although SNAP_OIDC_LINK_BY_EMAIL is off")
		}
		u, _ := s.App.Accounts.Store().UserByUsername(context.Background(), "remote")
		if u.Email != "" {
			t.Errorf("new account took an address in use: %q", u.Email)
		}
	})
	t.Run("same verified email, linking on", func(t *testing.T) {
		s, p := ssoServer(t, testutil.Env("OIDC_LINK_BY_EMAIL", "true"))
		createUser(t, s, "local", "shared@example.com", auth.RoleUser)
		p.SetUser(oidctest.User{Subject: "10", PreferredUsername: "remote", Email: "shared@example.com", EmailVerified: true})
		b := newBrowser(t, s)
		b.ssoFlow("/auth/oidc/start")
		if got := b.whoami(); got != "local" {
			t.Fatalf("signed in as %q", got)
		}
		// An unverified provider address never links.
		p.SetUser(oidctest.User{Subject: "11", PreferredUsername: "third", Email: "shared@example.com", EmailVerified: false})
		c := newBrowser(t, s)
		c.ssoFlow("/auth/oidc/start")
		if got := c.whoami(); got != "third" {
			t.Errorf("unverified email linked: signed in as %q", got)
		}
	})
}

func TestSSOAllowedGroups(t *testing.T) {
	s, p := ssoServer(t, testutil.Env("OIDC_ALLOWED_GROUPS", "family,friends:core"))
	p.SetUser(oidctest.User{Subject: "1", PreferredUsername: "out", Groups: []string{"friends"}})
	b := newBrowser(t, s)
	res, body := b.ssoFlow("/auth/oidc/start")
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "not in a group") {
		t.Fatalf("outsider: %d", res.StatusCode)
	}
	if n, _ := s.App.Accounts.Store().CountUsers(context.Background()); n != 0 {
		t.Error("outsider provisioned")
	}
	p.SetUser(oidctest.User{Subject: "2", PreferredUsername: "in", Groups: []string{"friends:core"}})
	if res, _ := b.ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("member: %d", res.StatusCode)
	}
}

func TestSSOAdminMapping(t *testing.T) {
	t.Run("groups, from userinfo", func(t *testing.T) {
		s, p := ssoServer(t, testutil.Env("OIDC_ADMIN_GROUPS", "ops"))
		p.SetUser(oidctest.User{Subject: "5", PreferredUsername: "op", Groups: []string{"ops"}})
		b := newBrowser(t, s)
		b.ssoFlow("/auth/oidc/start")
		if res, _ := b.get("/admin/settings"); res.StatusCode != http.StatusOK {
			t.Fatalf("promoted user on admin page: %d", res.StatusCode)
		}
		// Removed from the group: demoted at the next sign-in.
		p.SetUser(oidctest.User{Subject: "5", PreferredUsername: "op", Groups: []string{"dev"}})
		c := newBrowser(t, s)
		c.ssoFlow("/auth/oidc/start")
		if res, _ := c.get("/admin/settings"); res.StatusCode != http.StatusForbidden {
			t.Errorf("demoted user on admin page: %d", res.StatusCode)
		}
		if countAudit(t, s, "user.role_change") != 1 {
			t.Errorf("demotion audited %d times", countAudit(t, s, "user.role_change"))
		}
	})
	t.Run("subjects, groups in the ID token", func(t *testing.T) {
		s, p := ssoServer(t, testutil.Env("OIDC_ADMIN_SUBS", "1,77"))
		p.GroupsInIDToken = true
		p.SetUser(oidctest.User{Subject: "77", PreferredUsername: "boss"})
		b := newBrowser(t, s)
		b.ssoFlow("/auth/oidc/start")
		u, _ := s.App.Accounts.Store().UserByUsername(context.Background(), "boss")
		if u.Role != auth.RoleAdmin {
			t.Fatalf("role %s", u.Role)
		}
	})
	t.Run("no mapping leaves roles alone", func(t *testing.T) {
		s, p := ssoServer(t)
		p.SetUser(oidctest.User{Subject: "8", PreferredUsername: "kept"})
		b := newBrowser(t, s)
		b.ssoFlow("/auth/oidc/start")
		u, _ := s.App.Accounts.Store().UserByUsername(context.Background(), "kept")
		_ = s.App.Accounts.Store().SetRole(context.Background(), u.ID, auth.RoleAdmin, accounts.CLIActor())
		newBrowser(t, s).ssoFlow("/auth/oidc/start")
		u, _ = s.App.Accounts.Store().UserByUsername(context.Background(), "kept")
		if u.Role != auth.RoleAdmin {
			t.Error("role changed without a mapping")
		}
	})
}

func TestSSOProvisioningNeedsAutoProvisionOrInvite(t *testing.T) {
	s, p := ssoServer(t, testutil.Env("OIDC_AUTO_PROVISION", "false"))
	p.SetUser(oidctest.User{Subject: "3", PreferredUsername: "carol"})
	b := newBrowser(t, s)
	res, body := b.ssoFlow("/auth/oidc/start")
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Ask an admin for an invite") {
		t.Fatalf("without invite: %d", res.StatusCode)
	}
	raw, _, _ := s.App.Accounts.Store().CreateInvite(context.Background(), accounts.NewInvite{MaxUses: 1, TTL: time.Hour, Role: auth.RoleAdmin}, accounts.CLIActor())
	if code, _ := b.postJSON("/invite", url.Values{"token": {raw}}); code != http.StatusOK {
		t.Fatalf("accept invite: %d", code)
	}
	if _, body := b.get("/register"); !strings.Contains(body, "Continue with Gitea") {
		t.Error("invite page offers no SSO")
	}
	if res, _ := b.ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("with invite: %d", res.StatusCode)
	}
	u, err := s.App.Accounts.Store().UserByUsername(context.Background(), "carol")
	if err != nil || u.Role != auth.RoleAdmin {
		t.Fatalf("invited SSO user %+v %v", u, err)
	}
	invites, _ := s.App.Accounts.Store().ListInvites(context.Background(), 1)
	if invites[0].Uses != 1 {
		t.Errorf("invite uses %d", invites[0].Uses)
	}
	if b.cookie("__Host-snap_invite") != "" {
		t.Error("invite cookie kept after use")
	}
}

func TestSSORejectsForgedResponses(t *testing.T) {
	s, p := ssoServer(t)
	t.Run("state mismatch", func(t *testing.T) {
		b := newBrowser(t, s)
		cb := mustParse(t, b.authorize("/auth/oidc/start"))
		q := cb.Query()
		q.Set("state", "forged")
		cb.RawQuery = q.Encode()
		if res, _ := b.getURL(cb.String()); res.StatusCode != http.StatusBadRequest {
			t.Errorf("forged state: %d", res.StatusCode)
		}
	})
	t.Run("no state cookie", func(t *testing.T) {
		b := newBrowser(t, s)
		cb := b.authorize("/auth/oidc/start")
		if res, _ := newBrowser(t, s).getURL(cb); res.StatusCode != http.StatusBadRequest {
			t.Errorf("callback in another browser: %d", res.StatusCode)
		}
	})
	t.Run("tampered state cookie", func(t *testing.T) {
		b := newBrowser(t, s)
		cb := b.authorize("/auth/oidc/start")
		u, _ := url.Parse(s.URL)
		v := b.cookie("__Host-snap_oidc")
		b.c.Jar.SetCookies(u, []*http.Cookie{{Name: "__Host-snap_oidc", Value: "x" + v[1:], Path: "/", Secure: true}})
		if res, _ := b.getURL(cb); res.StatusCode != http.StatusBadRequest {
			t.Errorf("tampered cookie: %d", res.StatusCode)
		}
	})
	t.Run("nonce mismatch", func(t *testing.T) {
		p.NonceOverride = "replayed-nonce"
		defer func() { p.NonceOverride = "" }()
		b := newBrowser(t, s)
		if res, _ := b.ssoFlow("/auth/oidc/start"); res.StatusCode != http.StatusBadGateway {
			t.Errorf("wrong nonce: %d", res.StatusCode)
		}
		if b.whoami() != "" {
			t.Error("signed in with a wrong nonce")
		}
	})
	t.Run("code from another flow (PKCE)", func(t *testing.T) {
		// The code is bound to flow 1's challenge; flow 2's state cookie
		// carries another verifier, so the provider refuses the exchange.
		b := newBrowser(t, s)
		cb1 := mustParse(t, b.authorize("/auth/oidc/start"))
		cb2 := mustParse(t, b.authorize("/auth/oidc/start")) // replaces the state cookie
		q := cb2.Query()
		q.Set("code", cb1.Query().Get("code"))
		cb2.RawQuery = q.Encode()
		before := p.Tokens
		if res, _ := b.getURL(cb2.String()); res.StatusCode != http.StatusBadGateway {
			t.Errorf("mismatched verifier: %d", res.StatusCode)
		}
		if p.Tokens != before || b.whoami() != "" {
			t.Error("exchange succeeded without the matching verifier")
		}
	})
	t.Run("provider error", func(t *testing.T) {
		b := newBrowser(t, s)
		cb := mustParse(t, b.authorize("/auth/oidc/start"))
		q := cb.Query()
		q.Del("code")
		q.Set("error", "access_denied")
		cb.RawQuery = q.Encode()
		if res, body := b.getURL(cb.String()); res.StatusCode != http.StatusForbidden || !strings.Contains(body, "cancelled") {
			t.Errorf("access denied: %d", res.StatusCode)
		}
	})
}

func TestSSOProviderDown(t *testing.T) {
	// The server boots with an unreachable issuer and answers 503 until it is back.
	p := oidctest.New(t)
	s := newServer(t, testutil.OIDCClient(p.Client()), testutil.Env("OIDC_ISSUER", "https://127.0.0.1:1"),
		testutil.Env("OIDC_CLIENT_ID", "x"), testutil.Env("OIDC_CLIENT_SECRET", "y"))
	b := newBrowser(t, s)
	if res, body := b.get("/auth/oidc/start"); res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "not reachable") {
		t.Errorf("start with the provider down: %d", res.StatusCode)
	}
	if res, _ := b.get("/login"); res.StatusCode != http.StatusOK {
		t.Errorf("login page: %d", res.StatusCode)
	}
}

func TestSSODisabled(t *testing.T) {
	s := newServer(t)
	b := newBrowser(t, s)
	for _, path := range []string{"/auth/oidc/start", "/auth/oidc/callback?code=x&state=y"} {
		if res, _ := b.get(path); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s without SSO: %d", path, res.StatusCode)
		}
	}
}
