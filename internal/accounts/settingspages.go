package accounts

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/web"
)

// Confirmation notices after a redirect (?saved=code). Fixed strings only.
var savedNotices = map[string]string{
	"profile":       "Profile saved.",
	"email":         "Check your inbox to confirm the new address.",
	"verify":        "Verification email sent.",
	"password":      "Password changed. Your other sessions were signed out.",
	"password_keys": "Password changed. Your other sessions were signed out and your API keys revoked.",
	"session":       "Session signed out.",
	"linked":        "Account linked.",
	"unlinked":      "Account unlinked.",
}

type profileData struct {
	User         *User
	PendingEmail string
	MailEnabled  bool
	AskPassword  bool // changing the email needs the current password
	Username     string
	Email        string
	Errors       map[string]string
	Notice       string
	Error        string
}

func (s *Service) profileForm(w http.ResponseWriter, r *http.Request, status int, d *profileData) {
	d.PendingEmail = s.store.PendingEmail(r.Context(), d.User.ID)
	d.MailEnabled = s.MailEnabled()
	d.AskPassword = d.MailEnabled && s.passwordCounts(d.User)
	if d.Errors == nil {
		d.Errors = map[string]string{}
	}
	s.page(w, r, status, "settings/profile", web.Page{Title: "Profile", Nav: "settings", Data: d})
}

func (s *Service) currentUser(w http.ResponseWriter, r *http.Request) (*User, bool) {
	u, err := s.CurrentUser(r)
	if errors.Is(err, ErrNotFound) {
		s.d.Errors.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return u, true
}

func (s *Service) profilePage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	s.profileForm(w, r, http.StatusOK, &profileData{User: u, Username: u.Username, Email: u.Email, Notice: savedNotices[r.URL.Query().Get("saved")]})
}

// saveProfile handles POST /settings/profile: username, and email (a new
// address is confirmed by an emailed link before it replaces a verified one).
func (s *Service) saveProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	d := &profileData{User: u, Errors: map[string]string{},
		Username: strings.TrimSpace(r.PostFormValue("username")), Email: strings.TrimSpace(r.PostFormValue("email"))}

	if d.Username != u.Username {
		if err := ValidUsername(d.Username); err != nil {
			d.Errors["username"] = "Use 1–32 letters, digits, . _ or -, starting with a letter or digit."
		} else if err := s.store.SetUsername(ctx, u.ID, d.Username); errors.Is(err, ErrUsernameTaken) {
			d.Errors["username"] = "That username is taken."
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
	}

	saved := "profile"
	if !strings.EqualFold(d.Email, u.Email) {
		var email string
		var err error
		switch {
		case d.Email == "":
			d.Errors["email"] = "Enter an email address."
		case !s.MailEnabled():
			d.Errors["email"] = "Changing the address needs email, which is not set up."
		default:
			if email, err = NormalizeEmail(d.Email); err != nil {
				d.Errors["email"] = "Enter a valid email address."
			}
		}
		// The address is where reset links go: changing it needs the owner.
		if d.Errors["email"] == "" {
			if err := s.confirmOwner(r, u, r.PostFormValue("current"), s.passwordCounts(u)); err != nil {
				var re *errReauth
				if !errors.As(err, &re) {
					s.serverError(w, r, err)
					return
				}
				if s.passwordCounts(u) {
					d.Errors["current"] = re.msg
				} else {
					d.Errors["email"] = re.msg
				}
			}
		}
		// The mail limit comes before anything that depends on the address,
		// and the answer is the same whether or not another account uses it.
		if d.Errors["email"] == "" && d.Errors["current"] == "" {
			if ok, _ := s.lim.resend.Allow("user:" + strconv.FormatInt(u.ID, 10)); !ok {
				d.Errors["email"] = "Too many emails sent. Try again in an hour."
			} else if err := s.startEmailChange(r, u, email); err != nil {
				s.serverError(w, r, err)
				return
			} else {
				saved = "email"
			}
		}
	}
	if len(d.Errors) > 0 {
		if fresh, err := s.store.User(ctx, u.ID); err == nil {
			d.User = fresh
		}
		s.profileForm(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	redirect(w, r, "/settings/profile?saved="+saved)
}

// startEmailChange sends a confirmation link for a new address; the current
// address stays until the new one is confirmed. When another account uses
// the address, nothing is sent (confirming could not succeed anyway), but
// the change is shown as pending all the same: the profile page must not
// tell anyone which addresses have accounts.
func (s *Service) startEmailChange(r *http.Request, u *User, email string) error {
	ctx := r.Context()
	raw, err := s.store.CreateToken(ctx, TokenEmailChange, u.ID, email, VerifyTTL)
	if err != nil {
		return err
	}
	// In the background, so the answer takes as long either way.
	go func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if s.emailInUse(ctx, email) {
			return
		}
		_ = s.SendMail(ctx, mail.KindEmailChange, email, mail.Data{URL: s.Link("/verify", raw), Expires: humanDuration(VerifyTTL), Username: u.Username})
	}(context.WithoutCancel(ctx))
	return nil
}

// resendVerification handles POST /settings/profile/verify.
func (s *Service) resendVerification(w http.ResponseWriter, r *http.Request) {
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	if u.EmailVerified() || !s.MailEnabled() {
		redirect(w, r, "/settings/profile")
		return
	}
	if ok, _ := s.lim.resend.Allow("user:" + strconv.FormatInt(u.ID, 10)); !ok {
		s.profileForm(w, r, http.StatusTooManyRequests, &profileData{User: u, Username: u.Username, Email: u.Email, Error: "Too many emails sent. Try again in an hour."})
		return
	}
	raw, err := s.store.CreateToken(r.Context(), TokenVerify, u.ID, u.Email, VerifyTTL)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.SendMail(r.Context(), mail.KindVerify, u.Email, mail.Data{URL: s.Link("/verify", raw), Expires: humanDuration(VerifyTTL), Username: u.Username}); err != nil {
		s.profileForm(w, r, http.StatusBadGateway, &profileData{User: u, Username: u.Username, Email: u.Email, Error: "The email could not be sent. Try again later."})
		return
	}
	redirect(w, r, "/settings/profile?saved=verify")
}

type sessionRow struct {
	Session
	Device  string
	Current bool
}

type securityData struct {
	User          *User
	Sessions      []sessionRow
	Identities    []Identity
	OIDCEnabled   bool
	OIDCName      string
	PasswordLogin bool
	AskPassword   bool // sensitive changes take the current password
	Recent        bool // signed in within ReauthWindow (stands in for the password otherwise)
	CanUnlink     map[int64]bool
	Errors        map[string]string
	Notice        string
	Error         string

	// Two-factor authentication.
	Factors      Factors
	TOTPReadable bool         // the authenticator-app secret opens with a configured key
	Required     bool         // auth.require_2fa covers this user
	NeedsSetup   bool         // ... and they have no second factor yet
	Passkeys     []passkeyRow // passkey.go
	NewCodes     []string     // recovery codes just made, shown once
}

func (s *Service) securityForm(w http.ResponseWriter, r *http.Request, status int, u *User, d *securityData) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	sessions, err := s.sessions.List(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	idents, err := s.store.Identities(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d.User, d.Identities = u, idents
	d.OIDCEnabled, d.OIDCName, d.PasswordLogin = s.oidc != nil, s.d.Cfg.OIDC.Name, s.passwordLogin()
	d.AskPassword, d.Recent = s.passwordCounts(u), s.recentAuth(r)
	d.CanUnlink = map[int64]bool{}
	for _, id := range idents {
		d.CanUnlink[id.ID] = len(idents) > 1 || (u.HasPassword() && d.PasswordLogin)
	}
	for _, se := range sessions {
		d.Sessions = append(d.Sessions, sessionRow{Session: se, Device: DeviceSummary(se.UserAgent), Current: se.ID == p.SessionID})
	}
	if d.Factors, err = s.store.Factors(ctx, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	d.TOTPReadable = d.Factors.TOTP && s.totpReadable(ctx, u.ID)
	d.Required, d.NeedsSetup = s.policyCovers(u.Role), p.NeedsSetup
	if d.Passkeys, err = s.passkeyRows(ctx, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if d.Errors == nil {
		d.Errors = map[string]string{}
	}
	s.page(w, r, status, "settings/security", web.Page{Title: "Security", Nav: "settings", Data: d})
}

func (s *Service) securityPage(w http.ResponseWriter, r *http.Request) {
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	s.securityForm(w, r, http.StatusOK, u, &securityData{Notice: savedNotices[r.URL.Query().Get("saved")]})
}

// changePassword handles POST /settings/security/password. It needs the
// current password (an account without one: a sign-in in the last 10
// minutes). Every session ends, this browser gets a new one, and with
// revoke_keys=1 (checked by default) the API keys are revoked too.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	d := &securityData{Errors: map[string]string{}}
	current, next := r.PostFormValue("current"), r.PostFormValue("password")
	if !u.HasPassword() {
		current = "" // setting a first password: no current one to check
	}
	// The password being changed proves the owner even when password
	// sign-in is off.
	if err := s.confirmOwner(r, u, current, u.HasPassword()); err != nil {
		var re *errReauth
		if !errors.As(err, &re) {
			s.serverError(w, r, err)
			return
		}
		if u.HasPassword() {
			d.Errors["current"] = re.msg
		} else {
			d.Error = re.msg
		}
		s.securityForm(w, r, re.status, u, d)
		return
	}
	if err := auth.CheckPassword(next); err != nil {
		d.Errors["password"] = passwordMessage(err)
		s.securityForm(w, r, http.StatusUnprocessableEntity, u, d)
		return
	}
	hash, err := auth.HashPassword(ctx, next)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	revokeKeys := r.PostFormValue("revoke_keys") == "1"
	actor := Actor(r)
	var raw string
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		now := s.store.now()
		// Every session ends, this one included: a stolen copy of this
		// browser's cookie (a common reason to change the password) stops
		// working. This browser gets a fresh session below.
		if err := setPasswordTx(ctx, tx, u.ID, hash, nil, now); err != nil {
			return err
		}
		if revokeKeys {
			if err := s.store.revokeKeysTx(ctx, tx, u.ID, false, revokePasswordChange, actor); err != nil {
				return err
			}
		}
		var err error
		if raw, err = s.sessions.create(ctx, tx, u.ID, actor.IP, r.UserAgent(), false); err != nil {
			return err
		}
		a := actor
		a.Action, a.Target = "user.password_change", userTarget(u.ID)
		a.Details = map[string]any{"revoke_keys": revokeKeys}
		return auditTx(ctx, tx, a, now)
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.setCookie(w, s.cookieSession, raw, SessionAbsolute)
	saved := "password"
	if revokeKeys {
		saved = "password_keys"
	}
	redirect(w, r, "/settings/security?saved="+saved)
}

// revokeSession handles POST /settings/security/sessions/{id}/revoke.
func (s *Service) revokeSession(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	id := r.PathValue("id")
	if err := s.sessions.Revoke(r.Context(), p.UserID, id); errors.Is(err, ErrNotFound) {
		s.d.Errors.NotFound(w, r)
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	if id == p.SessionID {
		s.clearCookie(w, s.cookieSession)
		redirect(w, r, "/")
		return
	}
	redirect(w, r, "/settings/security?saved=session")
}

// signOutEverywhere handles POST /settings/security/signout-all: every
// session ends, this browser included, and every API key is revoked.
func (s *Service) signOutEverywhere(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.sessions.RevokeAll(ctx, tx, p.UserID); err != nil {
			return err
		}
		// Someone who held a session may have made keys with it.
		if err := s.store.revokeKeysTx(ctx, tx, p.UserID, false, revokeSignOutAll, Actor(r)); err != nil {
			return err
		}
		a := Actor(r)
		a.Action, a.Target = "auth.logout_everywhere", userTarget(p.UserID)
		return auditTx(ctx, tx, a, s.store.now())
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.clearCookie(w, s.cookieSession)
	redirect(w, r, "/login")
}

// unlinkIdentity handles POST /settings/security/identities/{id}/unlink.
func (s *Service) unlinkIdentity(w http.ResponseWriter, r *http.Request) {
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.d.Errors.NotFound(w, r)
		return
	}
	if err := s.confirmOwner(r, u, r.PostFormValue("current"), s.passwordCounts(u)); err != nil {
		var re *errReauth
		if !errors.As(err, &re) {
			s.serverError(w, r, err)
			return
		}
		s.securityForm(w, r, re.status, u, &securityData{Error: "Not unlinked. " + re.msg})
		return
	}
	err = s.store.UnlinkIdentity(r.Context(), u.ID, id, s.passwordLogin(), Actor(r))
	switch {
	case errors.Is(err, ErrNotFound):
		s.d.Errors.NotFound(w, r)
	case errors.Is(err, ErrLastSignIn):
		msg := "Set a password before unlinking, or you could not sign in again."
		if !s.passwordLogin() {
			msg = "This is your only way to sign in."
		}
		s.securityForm(w, r, http.StatusConflict, u, &securityData{Error: msg})
	case err != nil:
		s.serverError(w, r, err)
	default:
		redirect(w, r, "/settings/security?saved=unlinked")
	}
}
