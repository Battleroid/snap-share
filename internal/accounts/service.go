// Package accounts implements user accounts: password and SSO (OIDC) sign-in,
// browser sessions, sign-up modes, invites, emailed verify and reset links,
// and the account settings pages. Service is the request Authenticator.
package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/metrics"
	"github.com/Battleroid/snap-share/internal/ratelimit"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// Deps are the service's dependencies.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	DB       *db.DB
	Settings *settings.Store
	Jobs     *jobs.Queue // nil: no background flush/sweep (CLI)
	Mail     mail.Sender
	Render   *web.Renderer
	Errors   *web.ErrorPages
	SiteName func() string

	// Usage builds the Usage section of Settings › Profile (usage.go). Nil
	// leaves the section out.
	Usage func(ctx context.Context, userID int64) (*UsageView, error)

	// OIDCClient is the HTTP client for the OIDC provider. Nil uses a client
	// with timeouts. Tests pass the stub provider's client.
	OIDCClient *http.Client

	// Metrics counts sign-in attempts and rate-limit hits (nil: not counted).
	Metrics *metrics.Metrics
}

// BearerVerifier authenticates an Authorization: Bearer credential (API keys).
// It returns auth.ErrInvalidCredentials for an unknown, expired or revoked
// credential, auth.ErrForbiddenNetwork when the client address is not allowed,
// and otherwise a principal with KeyID and Scopes set.
type BearerVerifier interface {
	VerifyBearer(r *http.Request, token string) (*auth.Principal, error)
}

// Service is the accounts feature: the Authenticator plus its routes.
type Service struct {
	d        Deps
	store    *Store
	sessions *Sessions
	mailTpl  *mail.Templates
	oidc     *oidcFlow // nil when SSO is not configured
	lim      limits
	bearer   atomic.Pointer[bearerBox]

	cookieSession, cookieOIDC, cookieInvite string
	cookieAuthPrefix                        string // + challenge kind

	totpSeal *auth.Sealer              // authenticator-app secrets at rest (totp.go)
	totpAt   atomic.Pointer[time.Time] // nil: codes are checked against the clock; tests fix it
}

type bearerBox struct{ v BearerVerifier }

type limits struct {
	loginIP      *ratelimit.Limiter // per client address
	loginAccount *ratelimit.Limiter // failed sign-ins per account
	register     *ratelimit.Limiter
	reset        *ratelimit.Limiter
	verify       *ratelimit.Limiter
	invite       *ratelimit.Limiter
	oidc         *ratelimit.Limiter
	resend       *ratelimit.Limiter // verification emails per user
	passkeyBegin *ratelimit.Limiter // passkey ceremonies started, per client address
}

// New builds the service. Call Register to add its routes.
func New(d Deps) (*Service, error) {
	tpl, err := mail.LoadTemplates()
	if err != nil {
		return nil, err
	}
	if d.Mail == nil {
		d.Mail = mail.Disabled{}
	}
	if d.SiteName == nil {
		d.SiteName = func() string { return "snap" }
	}
	s := &Service{
		d:        d,
		store:    NewStore(d.DB),
		sessions: NewSessions(d.DB),
		mailTpl:  tpl,
		lim: limits{
			loginIP:      ratelimit.New(10, time.Minute, 10).Named("login_ip", d.Metrics),
			loginAccount: ratelimit.New(5, 15*time.Minute, 5).Named("login_account", d.Metrics),
			register:     ratelimit.New(5, time.Hour, 5).Named("register", d.Metrics),
			reset:        ratelimit.New(5, time.Hour, 5).Named("reset", d.Metrics),
			verify:       ratelimit.New(10, time.Hour, 10).Named("verify", d.Metrics), // two requests per link: check, then Confirm
			invite:       ratelimit.New(5, time.Hour, 5).Named("invite", d.Metrics),
			oidc:         ratelimit.New(20, time.Minute, 20).Named("oidc", d.Metrics),
			resend:       ratelimit.New(3, time.Hour, 3).Named("resend", d.Metrics),
			passkeyBegin: ratelimit.New(30, time.Minute, 30).Named("passkey_begin", d.Metrics),
		},
	}
	prefix := ""
	if d.Cfg.Secure() {
		prefix = "__Host-" // requires Secure, Path=/ and no Domain
	}
	s.cookieSession, s.cookieOIDC, s.cookieInvite = prefix+"snap_session", prefix+"snap_oidc", prefix+"snap_invite"
	s.cookieAuthPrefix = prefix + "snap_auth_"
	if s.totpSeal, err = auth.NewSealer(totpSealLabel, d.Cfg.SecretKey, d.Cfg.SecretKeyPrevious); err != nil {
		return nil, err
	}
	if d.Cfg.OIDC.Enabled() {
		if s.oidc, err = newOIDCFlow(d.Cfg, d.OIDCClient); err != nil {
			return nil, err
		}
	}
	if d.Jobs != nil {
		d.Jobs.Every("accounts.sessions.flush", time.Minute, s.sessions.Flush)
		d.Jobs.Every("accounts.challenges.sweep", time.Minute, s.sweepChallenges)
		d.Jobs.Every("accounts.sweep", time.Hour, func(ctx context.Context) error {
			if err := s.sessions.Flush(ctx); err != nil {
				return err
			}
			return s.sessions.Sweep(ctx)
		})
	}
	return s, nil
}

// Store returns the account store.
func (s *Service) Store() *Store { return s.store }

// Sessions returns the session store.
func (s *Service) Sessions() *Sessions { return s.sessions }

// SetBearerVerifier installs the API-key verifier. Until one is set, every
// Authorization header is rejected. Call before serving.
func (s *Service) SetBearerVerifier(v BearerVerifier) { s.bearer.Store(&bearerBox{v}) }

// Register adds the accounts routes.
func (s *Service) Register(r *web.Router) {
	r.HandleFunc("POST /login", s.login, web.Public())
	r.HandleFunc("GET /login/2fa", s.mfaPage, web.Public())
	s.registerTOTP(r)
	s.registerPasskeys(r)
	r.HandleFunc("POST /logout", s.logout, web.Public(), web.DuringSetup())

	r.HandleFunc("GET /register", s.registerPage, web.Public())
	r.HandleFunc("POST /register", s.register, web.Public())
	r.HandleFunc("GET /invite", s.invitePage, web.Public(), web.Redact())
	r.HandleFunc("POST /invite", s.acceptInvite, web.Public(), web.Redact())
	r.HandleFunc("GET /verify", s.verifyPage, web.Public(), web.Redact())
	r.HandleFunc("POST /verify", s.verify, web.Public(), web.Redact())
	r.HandleFunc("GET /reset", s.resetPage, web.Public(), web.Redact())
	r.HandleFunc("POST /reset", s.reset, web.Public(), web.Redact())
	r.HandleFunc("POST /reset/request", s.requestReset, web.Public())

	r.HandleFunc("GET /auth/oidc/start", s.oidcStart, web.Public())
	r.HandleFunc("POST /auth/oidc/start", s.oidcStart, web.Public())
	r.HandleFunc("GET /auth/oidc/callback", s.oidcCallback, web.Public(), web.Redact())

	r.HandleFunc("GET /settings/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings/profile", http.StatusSeeOther)
	}, web.SignedIn())
	r.HandleFunc("GET /settings/profile", s.sessionOnly(s.profilePage), web.SignedIn())
	r.HandleFunc("POST /settings/profile", s.sessionOnly(s.saveProfile), web.SignedIn())
	r.HandleFunc("POST /settings/profile/verify", s.sessionOnly(s.resendVerification), web.SignedIn())
	r.HandleFunc("GET /settings/security", s.sessionOnly(s.securityPage), web.SignedIn(), web.DuringSetup())
	r.HandleFunc("POST /settings/security/password", s.sessionOnly(s.changePassword), web.SignedIn())
	r.HandleFunc("POST /settings/security/sessions/{id}/revoke", s.sessionOnly(s.revokeSession), web.SignedIn())
	r.HandleFunc("POST /settings/security/signout-all", s.sessionOnly(s.signOutEverywhere), web.SignedIn())
	r.HandleFunc("POST /settings/security/identities/{id}/unlink", s.sessionOnly(s.unlinkIdentity), web.SignedIn())
}

// Authenticate implements auth.Authenticator: the Authorization header when
// present (never falling back to cookies), else the session cookie, else anonymous.
func (s *Service) Authenticate(r *http.Request) (*auth.Principal, auth.Method, error) {
	if hs := r.Header.Values("Authorization"); len(hs) > 0 {
		if len(hs) != 1 {
			return nil, auth.MethodNone, auth.ErrInvalidCredentials
		}
		scheme, token, ok := strings.Cut(hs[0], " ")
		token = strings.TrimSpace(token)
		box := s.bearer.Load()
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || box == nil || box.v == nil {
			return nil, auth.MethodNone, auth.ErrInvalidCredentials
		}
		p, err := box.v.VerifyBearer(r, token)
		s.d.Metrics.AuthAttempt("key", err == nil && p != nil)
		if err != nil {
			if errors.Is(err, auth.ErrForbiddenNetwork) {
				return nil, auth.MethodNone, auth.ErrForbiddenNetwork
			}
			return nil, auth.MethodNone, auth.ErrInvalidCredentials
		}
		if p == nil {
			return nil, auth.MethodNone, auth.ErrInvalidCredentials
		}
		return p, auth.MethodBearer, nil
	}
	raw := cookieValue(r, s.cookieSession)
	if raw == "" {
		return nil, auth.MethodNone, nil
	}
	p, sa, err := s.sessions.lookup(r.Context(), raw)
	if err == nil {
		// With roles from the SSO mapping, the provider decides who is an
		// admin, and it is asked again at each SSO sign-in. A session keeps
		// admin rights for SSOAdminMaxAge after one; after that (or after a
		// password sign-in) admin pages need a fresh SSO sign-in.
		// A password sign-in by a user auth.require_2fa covers, who has no
		// second factor yet, may only set one up (web.DuringSetup). SSO
		// sign-ins leave 2FA to the identity provider. Decided on the role
		// in the database, before the SSO admin limit below.
		if !sa.HasMFA && sa.SSOAt.IsZero() && s.policyCovers(p.Role) {
			p.NeedsSetup = true
		}
		if p.Role == auth.RoleAdmin && sa.Linked && s.ssoRoles() && s.sessions.now().Sub(sa.SSOAt) > SSOAdminMaxAge {
			p.Role = auth.RoleUser
		}
		return p, auth.MethodSession, nil
	}
	if !errors.Is(err, ErrNotFound) {
		s.d.Log.Error("session lookup", "err", err)
	}
	// A stale or unknown cookie is just anonymous.
	return nil, auth.MethodNone, nil
}

// CurrentUser loads the signed-in user of r.
func (s *Service) CurrentUser(r *http.Request) (*User, error) {
	p := auth.FromContext(r.Context())
	if p == nil {
		return nil, ErrNotFound
	}
	return s.store.User(r.Context(), p.UserID)
}

// MailEnabled reports whether outgoing mail is configured.
func (s *Service) MailEnabled() bool { return s.d.Cfg.SMTP.Enabled() }

// OIDCEnabled reports whether SSO is configured.
func (s *Service) OIDCEnabled() bool { return s.oidc != nil }

func (s *Service) passwordLogin() bool { return PasswordLogin.Get(s.d.Settings) }

// EffectiveSignupMode is the sign-up mode in force: "open" needs SMTP and
// otherwise acts as "invite".
func (s *Service) EffectiveSignupMode() string {
	m := SignupMode.Get(s.d.Settings)
	if m == SignupOpen && !s.MailEnabled() {
		return SignupInvite
	}
	return m
}

func (s *Service) signupOpen() bool {
	return s.EffectiveSignupMode() == SignupOpen && s.passwordLogin()
}

// SiteValues are the template flags the accounts track provides (.Site.Values).
func (s *Service) SiteValues(r *http.Request) map[string]any {
	p := auth.FromContext(r.Context())
	return map[string]any{
		"MailEnabled":      s.MailEnabled(),
		"SignupOpen":       s.signupOpen(),
		"PasswordLoginOff": !s.passwordLogin(),
		"VerifyEmail":      p != nil && !p.ViaKey() && !p.EmailVerified,
	}
}

// Link returns an emailed link with the token in the fragment, which browsers
// never send to the server (so it never reaches access logs).
func (s *Service) Link(path, token string) string {
	return s.d.Cfg.Origin() + path + "#t=" + token
}

// SendMail renders kind and sends it to one address. Bodies and links are never logged.
func (s *Service) SendMail(ctx context.Context, kind, to string, data mail.Data) error {
	if !s.MailEnabled() {
		return mail.ErrDisabled
	}
	data.Site = s.d.SiteName()
	m, err := s.mailTpl.Compose(kind, to, data)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := s.d.Mail.Send(ctx, m); err != nil {
		s.d.Log.Warn("mail send failed", "kind", kind, "err", err)
		return err
	}
	return nil
}

// CreateInvite creates an invite and, with email set and mail configured,
// sends it. It returns the link (shown once) and whether an email went out.
func (s *Service) CreateInvite(ctx context.Context, ni NewInvite, actor AuditEntry, by string) (link string, inv *Invite, sent bool, err error) {
	raw, inv, err := s.store.CreateInvite(ctx, ni, actor)
	if err != nil {
		return "", nil, false, err
	}
	link = s.Link("/invite", raw)
	if inv.Email != "" && s.MailEnabled() {
		sent = s.SendMail(ctx, mail.KindInvite, inv.Email, mail.Data{URL: link, Expires: humanDuration(ni.TTL), By: by}) == nil
	}
	return link, inv, sent, nil
}

// ResetLink issues a password reset link for uid (admin CLI and create-user).
func (s *Service) ResetLink(ctx context.Context, uid int64, ttl time.Duration) (string, error) {
	raw, err := s.store.CreateToken(ctx, TokenReset, uid, "", ttl)
	if err != nil {
		return "", err
	}
	return s.Link("/reset", raw), nil
}

func (s *Service) sessionOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p := auth.FromContext(r.Context()); p == nil || p.ViaKey() || p.SessionID == "" {
			s.d.Errors.Error(w, r, http.StatusForbidden, "Account settings need a browser session.")
			return
		}
		h(w, r)
	}
}

// allow takes a token from l for the client address unless it is exempt.
func (s *Service) allow(l *ratelimit.Limiter, r *http.Request) (bool, time.Duration) {
	ip := clientip.From(r.Context())
	if ip.IsValid() && clientip.In(ip, s.d.Cfg.RateLimitExempt) {
		return true, 0
	}
	return l.Allow(clientip.LimitKey(ip))
}

func (s *Service) tooMany(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
	s.d.Errors.Error(w, r, http.StatusTooManyRequests, "Too many attempts. Try again in "+humanWait(wait)+".")
}

// Cookies are Secure whenever the public URL is https (always in production;
// __Host- names then require it). Over plain http they could not be set at all.
func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func (s *Service) setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the public URL scheme
		Name: name, Value: value, Path: "/", MaxAge: int(maxAge.Seconds()),
		Secure: s.d.Cfg.Secure(), HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (s *Service) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the public URL scheme
		Name: name, Value: "", Path: "/", MaxAge: -1,
		Secure: s.d.Cfg.Secure(), HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// signIn starts a fresh session for u and sets the cookie. Any session the
// browser already had is ended first (no fixation, no stacking).
func (s *Service) signIn(ctx context.Context, w http.ResponseWriter, r *http.Request, u *User, method string) error {
	if c, err := r.Cookie(s.cookieSession); err == nil {
		_ = s.sessions.Delete(ctx, c.Value)
	}
	actor := Actor(r)
	var raw string
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if raw, err = s.sessions.create(ctx, tx, u.ID, actor.IP, r.UserAgent(), method == "sso"); err != nil {
			return err
		}
		if err := s.store.touchLogin(ctx, tx, u.ID); err != nil {
			return err
		}
		actor.ActorUserID, actor.Action, actor.Target = u.ID, "auth.login", userTarget(u.ID)
		actor.Details = map[string]any{"method": method}
		return auditTx(ctx, tx, actor, s.store.now())
	})
	if err != nil {
		return err
	}
	s.d.Metrics.AuthAttempt(method, true)
	s.setCookie(w, s.cookieSession, raw, SessionAbsolute)
	return nil
}

func (s *Service) page(w http.ResponseWriter, r *http.Request, status int, name string, p web.Page) {
	w.Header().Set("Cache-Control", "no-store")
	s.d.Render.Page(w, r, status, name, p)
}

func (s *Service) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.d.Log.Error("accounts", "route", r.Pattern, "err", err)
	s.d.Errors.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, web.SafeNext(to), http.StatusSeeOther) // #nosec G710 -- web.SafeNext keeps it a relative path
}

func humanWait(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "a minute"
	case d < time.Hour:
		n := int(d.Minutes()) + 1
		return fmt.Sprintf("%d minutes", n)
	default:
		return "an hour"
	}
}

func humanDuration(d time.Duration) string {
	switch {
	case d == 24*time.Hour:
		return "1 day"
	case d >= 48*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%d days", d/(24*time.Hour))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= time.Hour:
		return "1 hour"
	default:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
}
