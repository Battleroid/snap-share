package accounts

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/web"
)

// Confirmation notices after a redirect (?saved=code). Fixed strings only.
var savedNotices = map[string]string{
	"profile":  "Profile saved.",
	"email":    "Check your inbox to confirm the new address.",
	"verify":   "Verification email sent.",
	"password": "Password changed. Your other sessions were signed out.",
	"session":  "Session signed out.",
	"linked":   "Account linked.",
	"unlinked": "Account unlinked.",
}

type profileData struct {
	User         *User
	PendingEmail string
	MailEnabled  bool
	Username     string
	Email        string
	Errors       map[string]string
	Notice       string
	Error        string
}

func (s *Service) profileForm(w http.ResponseWriter, r *http.Request, status int, d *profileData) {
	d.PendingEmail = s.store.PendingEmail(r.Context(), d.User.ID)
	d.MailEnabled = s.MailEnabled()
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
			} else if s.emailInUse(ctx, email) {
				d.Errors["email"] = "That address belongs to another account."
			} else if ok, _ := s.lim.resend.Allow("user:" + strconv.FormatInt(u.ID, 10)); !ok {
				d.Errors["email"] = "Too many emails sent. Try again in an hour."
			}
		}
		if d.Errors["email"] == "" {
			if err := s.startEmailChange(r, u, email); errors.Is(err, ErrEmailTaken) {
				d.Errors["email"] = "That address belongs to another account."
			} else if err != nil {
				s.serverError(w, r, err)
				return
			}
			saved = "email"
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

// startEmailChange sends a confirmation link for a new address. A verified
// address stays until the new one is confirmed; an unverified one is replaced.
func (s *Service) startEmailChange(r *http.Request, u *User, email string) error {
	ctx := r.Context()
	kind, mailKind := TokenEmailChange, mail.KindEmailChange
	if u.Email != "" && u.EmailVerifiedAt == nil {
		if err := s.store.replaceUnverifiedEmail(ctx, u.ID, email); err != nil {
			return err
		}
		kind, mailKind = TokenVerify, mail.KindVerify
	}
	raw, err := s.store.CreateToken(ctx, kind, u.ID, email, VerifyTTL)
	if err != nil {
		return err
	}
	_ = s.SendMail(ctx, mailKind, email, mail.Data{URL: s.Link("/verify", raw), Expires: humanDuration(VerifyTTL), Username: u.Username})
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
	CanUnlink     map[int64]bool
	Errors        map[string]string
	Notice        string
	Error         string
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
	d.CanUnlink = map[int64]bool{}
	for _, id := range idents {
		d.CanUnlink[id.ID] = len(idents) > 1 || (u.HasPassword() && d.PasswordLogin)
	}
	for _, se := range sessions {
		d.Sessions = append(d.Sessions, sessionRow{Session: se, Device: DeviceSummary(se.UserAgent), Current: se.ID == p.SessionID})
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

// changePassword handles POST /settings/security/password. Other sessions are
// signed out; this one stays.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	d := &securityData{Errors: map[string]string{}}
	current, next := r.PostFormValue("current"), r.PostFormValue("password")
	if u.HasPassword() {
		key := "user:" + strconv.FormatInt(u.ID, 10)
		if ok, wait := s.lim.loginAccount.Allow(key); !ok {
			d.Errors["current"] = "Too many attempts. Try again in " + humanWait(wait) + "."
			s.securityForm(w, r, http.StatusTooManyRequests, u, d)
			return
		}
		match, _, err := auth.VerifyPassword(ctx, u.PasswordHash, current)
		if err != nil && !errors.Is(err, auth.ErrMalformedHash) {
			s.serverError(w, r, err)
			return
		}
		if !match {
			d.Errors["current"] = "That is not your current password."
			s.securityForm(w, r, http.StatusUnprocessableEntity, u, d)
			return
		}
		s.lim.loginAccount.Reset(key)
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
	keep, _ := hex.DecodeString(auth.FromContext(ctx).SessionID)
	if err := s.store.SetPassword(ctx, u.ID, hash, keep, Actor(r)); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/settings/security?saved=password")
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

// signOutEverywhere handles POST /settings/security/signout-all, this browser included.
func (s *Service) signOutEverywhere(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.sessions.RevokeAll(ctx, tx, p.UserID); err != nil {
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
