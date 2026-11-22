package accounts

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/web"
)

const (
	oidcStateTTL   = 10 * time.Minute
	discoveryRetry = 5 * time.Second
	maxGroups      = 500
)

// oidcFlow is the SSO client: lazy provider discovery and the signed state cookie.
type oidcFlow struct {
	cfg      config.OIDC
	issuer   string // as configured (no trailing slash); identities are stored under it
	redirect string
	client   *http.Client
	keys     [][]byte // state MAC keys, current first
	now      func() time.Time

	mu       sync.Mutex
	provider *oidc.Provider
	lastTry  time.Time
	lastErr  error
}

func newOIDCFlow(cfg *config.Config, client *http.Client) (*oidcFlow, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	f := &oidcFlow{
		cfg:      cfg.OIDC,
		issuer:   strings.TrimRight(cfg.OIDC.Issuer, "/"),
		redirect: cfg.Origin() + "/auth/oidc/callback",
		client:   client,
		now:      time.Now,
	}
	for _, secret := range [][]byte{cfg.SecretKey, cfg.SecretKeyPrevious} {
		if len(secret) == 0 {
			continue
		}
		k, err := hkdf.Key(sha256.New, secret, nil, "snap oidc state v1", 32)
		if err != nil {
			return nil, err
		}
		f.keys = append(f.keys, k)
	}
	return f, nil
}

func (f *oidcFlow) ctx(ctx context.Context) context.Context { return oidc.ClientContext(ctx, f.client) }

// discover returns the provider, fetching its metadata on first use. Failures
// are retried on a later request, so the server starts while the issuer is down.
func (f *oidcFlow) discover(ctx context.Context) (*oidc.Provider, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.provider != nil {
		return f.provider, nil
	}
	if f.lastErr != nil && f.now().Sub(f.lastTry) < discoveryRetry {
		return nil, f.lastErr
	}
	f.lastTry = f.now()
	dctx, cancel := context.WithTimeout(f.ctx(ctx), 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(dctx, f.issuer)
	var mismatch *oidc.IssuerMismatchError
	if errors.As(err, &mismatch) && strings.TrimRight(mismatch.Discovered, "/") == f.issuer {
		// Gitea advertises its issuer with a trailing slash; accept that spelling.
		p, err = oidc.NewProvider(dctx, mismatch.Discovered)
	}
	if err != nil {
		f.lastErr = err
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	f.provider, f.lastErr = p, nil
	return p, nil
}

func (f *oidcFlow) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     f.cfg.ClientID,
		ClientSecret: f.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  f.redirect,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
	}
}

// oidcState travels in the HMAC-signed __Host-snap_oidc cookie between start and callback.
type oidcState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"r,omitempty"`
	LinkUID  int64  `json:"l,omitempty"` // linking from Settings: the signed-in user
	Expires  int64  `json:"e"`           // unix seconds
}

func (f *oidcFlow) mac(key []byte, payload string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("oidc-state|" + payload))
	return m.Sum(nil)
}

func (f *oidcFlow) seal(st oidcState) string {
	b, _ := json.Marshal(st)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + base64.RawURLEncoding.EncodeToString(f.mac(f.keys[0], payload))
}

var errBadState = errors.New("invalid oidc state")

func (f *oidcFlow) open(v string) (*oidcState, error) {
	payload, sig, ok := strings.Cut(v, ".")
	if !ok {
		return nil, errBadState
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, errBadState
	}
	valid := false
	for _, k := range f.keys {
		if hmac.Equal(got, f.mac(k, payload)) {
			valid = true
			break
		}
	}
	if !valid {
		return nil, errBadState
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, errBadState
	}
	var st oidcState
	if err := json.Unmarshal(b, &st); err != nil || st.State == "" || st.Nonce == "" || st.Verifier == "" {
		return nil, errBadState
	}
	if f.now().Unix() >= st.Expires {
		return nil, errBadState
	}
	return &st, nil
}

// role derives the role from the admin mapping. ok is false when no mapping
// is configured, in which case roles are left alone.
func (f *oidcFlow) role(p ssoProfile) (auth.Role, bool) {
	if len(f.cfg.AdminGroups) == 0 && len(f.cfg.AdminSubs) == 0 {
		return "", false
	}
	if slices.Contains(f.cfg.AdminSubs, p.Subject) || intersects(p.Groups, f.cfg.AdminGroups) {
		return auth.RoleAdmin, true
	}
	return auth.RoleUser, true
}

func intersects(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}

func randomString() string {
	raw, _ := newSecret()
	return raw
}

// oidcStart handles GET /auth/oidc/start (a plain link): it redirects to the
// provider with PKCE (S256), state and nonce. POST with link=1 (the Link
// button in Settings, covered by the cross-origin check) links the provider
// account to the signed-in user instead of signing in; a GET never does, so
// another site can't start a link for a signed-in visitor.
func (s *Service) oidcStart(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.d.Errors.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost && r.URL.Query().Get("link") != "" {
		redirect(w, r, "/settings/security")
		return
	}
	if ok, wait := s.allow(s.lim.oidc, r); !ok {
		s.tooMany(w, r, wait)
		return
	}
	next, link := r.URL.Query().Get("next"), false
	if r.Method == http.MethodPost {
		next, link = r.PostFormValue("next"), r.PostFormValue("link") == "1"
	}
	st := oidcState{
		State:    randomString(),
		Nonce:    randomString(),
		Verifier: oauth2.GenerateVerifier(),
		Next:     web.SafeNext(next),
		Expires:  s.oidc.now().Add(oidcStateTTL).Unix(),
	}
	if link {
		p := auth.FromContext(r.Context())
		if p == nil || p.ViaKey() {
			redirect(w, r, "/login?next=/settings/security")
			return
		}
		st.LinkUID, st.Next = p.UserID, "/settings/security"
	}
	p, err := s.oidc.discover(r.Context())
	if err != nil {
		s.d.Log.Warn("oidc unavailable", "err", err)
		s.d.Errors.Error(w, r, http.StatusServiceUnavailable, s.d.Cfg.OIDC.Name+" is not reachable. Try again in a minute.")
		return
	}
	s.setCookie(w, s.cookieOIDC, s.oidc.seal(st), oidcStateTTL)
	u := s.oidc.oauth(p).AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier))
	w.Header().Set("Cache-Control", "no-store")
	code := http.StatusFound
	if r.Method == http.MethodPost {
		code = http.StatusSeeOther // the provider's authorize endpoint takes a GET
	}
	http.Redirect(w, r, u, code)
}

// flexBool accepts true, false, "true" and "false" (providers differ).
type flexBool bool

func (b *flexBool) UnmarshalJSON(v []byte) error {
	switch strings.Trim(string(v), `"`) {
	case "true":
		*b = true
	default:
		*b = false
	}
	return nil
}

type ssoClaims struct {
	Subject           string          `json:"sub"`
	PreferredUsername string          `json:"preferred_username"`
	Nickname          string          `json:"nickname"`
	Email             string          `json:"email"`
	EmailVerified     flexBool        `json:"email_verified"`
	Groups            json.RawMessage `json:"groups"`
}

// groups decodes the groups claim; present is false when the claim is absent.
func (c ssoClaims) groups() (out []string, present bool) {
	if len(c.Groups) == 0 || string(c.Groups) == "null" {
		return nil, false
	}
	var raw []any
	if err := json.Unmarshal(c.Groups, &raw); err != nil {
		var one string
		if json.Unmarshal(c.Groups, &one) == nil && one != "" {
			return []string{one}, true
		}
		return []string{}, true
	}
	out = []string{}
	for _, g := range raw {
		if s, ok := g.(string); ok && s != "" && len(s) <= 200 && len(out) < maxGroups {
			out = append(out, s)
		}
	}
	return out, true
}

type ssoError struct {
	status int
	msg    string
}

func (e *ssoError) Error() string { return e.msg }

func ssoFail(status int, msg string) error { return &ssoError{status, msg} }

// oidcCallback handles GET /auth/oidc/callback.
func (s *Service) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		s.d.Errors.NotFound(w, r)
		return
	}
	if ok, wait := s.allow(s.lim.oidc, r); !ok {
		s.tooMany(w, r, wait)
		return
	}
	// The state is single use: drop the cookie whatever the outcome.
	s.clearCookie(w, s.cookieOIDC)
	prof, st, err := s.oidcExchange(r)
	if err == nil {
		err = s.completeSSO(w, r, st, prof)
	}
	if err != nil {
		var se *ssoError
		if errors.As(err, &se) {
			s.d.Errors.Error(w, r, se.status, se.msg)
			return
		}
		s.serverError(w, r, err)
	}
}

// oidcExchange checks the state cookie, redeems the code (with the PKCE
// verifier), verifies the ID token and nonce, and collects the claims.
func (s *Service) oidcExchange(r *http.Request) (ssoProfile, *oidcState, error) {
	name := s.d.Cfg.OIDC.Name
	expired := ssoFail(http.StatusBadRequest, "Sign-in with "+name+" expired or was started in another tab. Start again.")
	c, err := r.Cookie(s.cookieOIDC)
	if err != nil {
		return ssoProfile{}, nil, expired
	}
	st, err := s.oidc.open(c.Value)
	if err != nil {
		return ssoProfile{}, nil, expired
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		return ssoProfile{}, nil, ssoFail(http.StatusForbidden, "Sign-in with "+name+" was cancelled.")
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1 || q.Get("code") == "" {
		return ssoProfile{}, nil, expired
	}
	p, err := s.oidc.discover(r.Context())
	if err != nil {
		s.d.Log.Warn("oidc unavailable", "err", err)
		return ssoProfile{}, nil, ssoFail(http.StatusServiceUnavailable, name+" is not reachable. Try again in a minute.")
	}
	ctx, cancel := context.WithTimeout(s.oidc.ctx(r.Context()), 20*time.Second)
	defer cancel()
	failed := ssoFail(http.StatusBadGateway, "Sign-in with "+name+" failed. Start again.")
	tok, err := s.oidc.oauth(p).Exchange(ctx, q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		s.d.Log.Warn("oidc code exchange failed", "err", err)
		return ssoProfile{}, nil, failed
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		s.d.Log.Warn("oidc token response has no id_token")
		return ssoProfile{}, nil, failed
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: s.d.Cfg.OIDC.ClientID}).Verify(ctx, rawID)
	if err != nil {
		s.d.Log.Warn("oidc id token rejected", "err", err)
		return ssoProfile{}, nil, failed
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(st.Nonce)) != 1 {
		s.d.Log.Warn("oidc nonce mismatch")
		return ssoProfile{}, nil, failed
	}
	if idt.Subject == "" || len(idt.Subject) > 255 {
		s.d.Log.Warn("oidc id token has no usable subject")
		return ssoProfile{}, nil, failed
	}
	var claims ssoClaims
	if err := idt.Claims(&claims); err != nil {
		return ssoProfile{}, nil, failed
	}
	groups, ok := claims.groups()
	if !ok {
		// Some providers (Gitea) send groups only from the userinfo endpoint.
		ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok))
		if err != nil {
			s.d.Log.Warn("oidc userinfo failed", "err", err)
			return ssoProfile{}, nil, failed
		}
		var uc ssoClaims
		if err := ui.Claims(&uc); err != nil || uc.Subject != idt.Subject {
			s.d.Log.Warn("oidc userinfo subject mismatch")
			return ssoProfile{}, nil, failed
		}
		groups, _ = uc.groups()
		if claims.PreferredUsername == "" {
			claims.PreferredUsername = uc.PreferredUsername
		}
		if claims.Email == "" {
			claims.Email, claims.EmailVerified = uc.Email, uc.EmailVerified
		}
	}
	username := claims.PreferredUsername
	if username == "" {
		username = claims.Nickname
	}
	prof := ssoProfile{
		Provider:      s.oidc.issuer,
		Subject:       idt.Subject,
		Username:      username,
		EmailVerified: bool(claims.EmailVerified),
		Groups:        groups,
	}
	if e, err := NormalizeEmail(claims.Email); err == nil {
		prof.Email = e
	} else {
		prof.EmailVerified = false
	}
	return prof, st, nil
}

// completeSSO applies the sign-in rules: known identity, linking from
// Settings, linking by verified email (opt-in), provisioning, or refusal.
func (s *Service) completeSSO(w http.ResponseWriter, r *http.Request, st *oidcState, prof ssoProfile) error {
	ctx := r.Context()
	cfg := s.d.Cfg.OIDC
	name := cfg.Name
	role, mapped := s.oidc.role(prof)
	ssoActor := Actor(r)
	ssoActor.ActorUserID = 0
	ssoActor.Details = map[string]any{"via": "sso"}

	ident, err := s.store.IdentityBySubject(ctx, prof.Provider, prof.Subject)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if len(cfg.AllowedGroups) > 0 && !intersects(prof.Groups, cfg.AllowedGroups) {
		if ident != nil {
			// The provider no longer lets this person in: end what they
			// still hold here (sessions; admin rights and admin keys when
			// roles are mapped).
			err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
				if err := updateIdentityTx(ctx, tx, ident.ID, prof, s.store.now()); err != nil {
					return err
				}
				if mapped {
					if err := s.applySSORoleTx(ctx, tx, ident.UserID, role, ssoActor); err != nil {
						return err
					}
				}
				return s.sessions.RevokeAll(ctx, tx, ident.UserID)
			})
			if err != nil {
				return err
			}
		}
		return ssoFail(http.StatusForbidden, "Your "+name+" account is not in a group that may use this site.")
	}
	if ident != nil {
		if st.LinkUID != 0 {
			if ident.UserID != st.LinkUID {
				return ssoFail(http.StatusConflict, "This "+name+" account is linked to another user.")
			}
			redirect(w, r, "/settings/security?saved=linked")
			return nil
		}
		u, err := s.store.User(ctx, ident.UserID)
		if err != nil {
			return err
		}
		if !u.Active() {
			return ssoFail(http.StatusForbidden, "This account is disabled.")
		}
		err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			if err := updateIdentityTx(ctx, tx, ident.ID, prof, s.store.now()); err != nil {
				return err
			}
			if mapped {
				return s.applySSORoleTx(ctx, tx, u.ID, role, ssoActor)
			}
			return nil
		})
		if err != nil {
			return err
		}
		return s.ssoSignIn(w, r, u.ID, st.Next)
	}

	if st.LinkUID != 0 {
		p := auth.FromContext(ctx)
		if p == nil || p.UserID != st.LinkUID {
			return ssoFail(http.StatusForbidden, "Sign in again to link your "+name+" account.")
		}
		err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			if err := linkIdentityTx(ctx, tx, p.UserID, prof, s.store.now()); err != nil {
				return err
			}
			a := Actor(r)
			a.Action, a.Target = "sso.link", userTarget(p.UserID)
			a.Details = map[string]any{"provider": prof.Provider, "subject": prof.Subject}
			return auditTx(ctx, tx, a, s.store.now())
		})
		if errors.Is(err, ErrIdentityTaken) {
			return ssoFail(http.StatusConflict, "Your account is already linked to a "+name+" account. Unlink it first.")
		}
		if err != nil {
			return err
		}
		redirect(w, r, "/settings/security?saved=linked")
		return nil
	}

	if cfg.LinkByEmail && prof.EmailVerified && prof.Email != "" {
		u, err := s.store.UserByEmail(ctx, prof.Email)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if u != nil && u.EmailVerifiedAt != nil {
			if !u.Active() {
				return ssoFail(http.StatusForbidden, "This account is disabled.")
			}
			// Never into an account with a password: whoever registered it
			// may not be the address's owner (a mail scanner or a careless
			// click confirms an address), and would keep the password to
			// the account the owner then uses. Its owner proves it by
			// signing in with the password and linking from Settings.
			if u.HasPassword() {
				return ssoFail(http.StatusConflict, "An account here already uses this email address. Sign in with its password, then link "+name+" under Settings, Security.")
			}
			err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
				if err := linkIdentityTx(ctx, tx, u.ID, prof, s.store.now()); err != nil {
					return err
				}
				a := ssoActor
				a.Action, a.Target = "sso.link", userTarget(u.ID)
				a.Details = map[string]any{"via": "sso", "provider": prof.Provider, "subject": prof.Subject, "by": "email"}
				if err := auditTx(ctx, tx, a, s.store.now()); err != nil {
					return err
				}
				if mapped {
					return s.applySSORoleTx(ctx, tx, u.ID, role, ssoActor)
				}
				return nil
			})
			if errors.Is(err, ErrIdentityTaken) {
				return ssoFail(http.StatusConflict, "The account with this email is linked to another "+name+" account.")
			}
			if err != nil {
				return err
			}
			return s.ssoSignIn(w, r, u.ID, st.Next)
		}
	}

	inv, rawInvite, _ := s.invite(r)
	if inv != nil && s.EffectiveSignupMode() == SignupClosed {
		inv = nil
	}
	if !cfg.AutoProvision && inv == nil {
		return ssoFail(http.StatusForbidden, "No account here uses this "+name+" account. Ask an admin for an invite, or sign in with your password and link "+name+" from Settings.")
	}
	var uid int64
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		now := s.store.now()
		nu := NewUser{Role: auth.RoleUser}
		if inv != nil {
			used, err := useInviteTx(ctx, tx, rawInvite, now)
			if err != nil {
				return err
			}
			nu.Role = used.Role
		}
		if mapped {
			nu.Role = role
		}
		var err error
		if nu.Username, err = freeUsername(ctx, tx, prof.Username); err != nil {
			return err
		}
		if prof.EmailVerified && prof.Email != "" {
			var taken bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE email = ?)`, prof.Email).Scan(&taken); err != nil {
				return err
			}
			if !taken {
				nu.Email, nu.EmailVerified = prof.Email, true
			}
		}
		if uid, err = createUserTx(ctx, tx, nu, now); err != nil {
			return err
		}
		if err := linkIdentityTx(ctx, tx, uid, prof, now); err != nil {
			return err
		}
		a := Actor(r)
		a.ActorUserID, a.Action, a.Target = uid, "user.register", userTarget(uid)
		a.Details = map[string]any{"via": "sso", "invite": inv != nil, "role": nu.Role, "provider": prof.Provider, "subject": prof.Subject}
		return auditTx(ctx, tx, a, now)
	})
	if errors.Is(err, ErrInvalidToken) {
		s.clearCookie(w, s.cookieInvite)
		return ssoFail(http.StatusForbidden, "This invite has expired or has been used.")
	}
	if err != nil {
		return err
	}
	s.clearCookie(w, s.cookieInvite)
	return s.ssoSignIn(w, r, uid, st.Next)
}

// applySSORoleTx sets uid's role from the SSO mapping. It never demotes the
// last active admin: it keeps the role and logs a warning. A demotion ends
// all of the user's sessions (the sign-in in progress starts a new one) and
// revokes their keys with the admin scope: the provider no longer vouches
// for them as an admin.
func (s *Service) applySSORoleTx(ctx context.Context, tx *sql.Tx, uid int64, role auth.Role, actor AuditEntry) error {
	var old string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, uid).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if old == string(role) {
		return nil
	}
	if role != auth.RoleAdmin {
		last, err := lastActiveAdminTx(ctx, tx, uid)
		if err != nil {
			return err
		}
		if last {
			s.d.Log.Warn("sso role mapping would leave no active admin; keeping the admin role", "user", uid)
			return nil
		}
	}
	if err := setRoleTx(ctx, tx, uid, role, actor, s.store.now()); err != nil {
		return err
	}
	if old != string(auth.RoleAdmin) {
		return nil
	}
	if err := s.sessions.RevokeAll(ctx, tx, uid); err != nil {
		return err
	}
	return s.store.revokeKeysTx(ctx, tx, uid, true, revokeSSODemoted, actor)
}

func (s *Service) ssoSignIn(w http.ResponseWriter, r *http.Request, uid int64, next string) error {
	u, err := s.store.User(r.Context(), uid)
	if err != nil {
		return err
	}
	if err := s.signIn(r.Context(), w, r, u, "sso"); err != nil {
		return err
	}
	redirect(w, r, next)
	return nil
}

// freeUsername turns a provider username into a valid, unused local name.
// It never reuses an existing account's name: "ann" becomes "ann-2".
func freeUsername(ctx context.Context, tx *sql.Tx, want string) (string, error) {
	base := sanitizeUsername(want)
	taken := func(name string) (bool, error) {
		var t bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE username = ?)`, name).Scan(&t)
		return t, err
	}
	for i := 1; i <= 50; i++ {
		cand := base
		if i > 1 {
			suffix := fmt.Sprintf("-%d", i)
			cand = strings.TrimRight(trimTo(base, 32-len(suffix)), "-._") + suffix
		}
		t, err := taken(cand)
		if err != nil {
			return "", err
		}
		if !t {
			return cand, nil
		}
	}
	raw, _ := newSecret()
	return trimTo(base, 23) + "-" + strings.ToLower(hex.EncodeToString([]byte(raw))[:8]), nil
}

func sanitizeUsername(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		case b.Len() > 0:
			b.WriteRune('-')
		}
	}
	out := strings.TrimLeft(b.String(), "-._")
	out = strings.TrimRight(trimTo(out, 32), "-._")
	if out == "" {
		return "user"
	}
	return out
}

func trimTo(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
