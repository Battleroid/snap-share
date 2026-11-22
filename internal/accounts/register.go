package accounts

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/web"
)

const inviteCookieAge = time.Hour

type registerForm struct {
	Invite        *Invite
	Username      string
	Email         string
	Errors        map[string]string
	EmailField    bool
	EmailRequired bool
	Password      bool // the local form is offered
	SSO           bool // the SSO button is offered
}

// invite returns the usable invite carried by this browser's invite cookie.
// stale is true when a cookie was present but the invite can no longer be used.
func (s *Service) invite(r *http.Request) (inv *Invite, raw string, stale bool) {
	c, err := r.Cookie(s.cookieInvite)
	if err != nil || c.Value == "" {
		return nil, "", false
	}
	inv, err = s.store.InviteByToken(r.Context(), c.Value)
	if err != nil {
		return nil, "", true
	}
	return inv, c.Value, false
}

// signupPath decides how this request may register: with an invite, by open
// sign-up, or not at all (msg says why).
func (s *Service) signupPath(w http.ResponseWriter, r *http.Request) (inv *Invite, raw, msg string) {
	inv, raw, stale := s.invite(r)
	mode := s.EffectiveSignupMode()
	switch {
	case mode == SignupClosed:
		return nil, "", "Sign-up is closed."
	case inv != nil:
		return inv, raw, ""
	case stale:
		s.clearCookie(w, s.cookieInvite)
		return nil, "", "This invite has expired or has been used."
	case mode == SignupOpen:
		return nil, "", ""
	}
	return nil, "", "Sign-up is by invite only."
}

func (s *Service) newRegisterForm(inv *Invite) *registerForm {
	f := &registerForm{
		Invite:   inv,
		Errors:   map[string]string{},
		Password: s.passwordLogin(),
		SSO:      s.oidc != nil && (inv != nil || s.d.Cfg.OIDC.AutoProvision),
	}
	f.EmailField = s.MailEnabled()
	f.EmailRequired = inv == nil
	if inv != nil {
		f.Email = inv.Email
	}
	return f
}

func (s *Service) registerPage(w http.ResponseWriter, r *http.Request) {
	if auth.FromContext(r.Context()) != nil {
		redirect(w, r, "/")
		return
	}
	inv, _, msg := s.signupPath(w, r)
	if msg != "" {
		s.d.Errors.Error(w, r, http.StatusForbidden, msg)
		return
	}
	s.page(w, r, http.StatusOK, "auth/register", web.Page{Title: "Create an account", Data: s.newRegisterForm(inv)})
}

// register handles POST /register for invited and open sign-ups.
func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if auth.FromContext(ctx) != nil {
		redirect(w, r, "/")
		return
	}
	if ok, wait := s.allow(s.lim.register, r); !ok {
		s.tooMany(w, r, wait)
		return
	}
	inv, rawInvite, msg := s.signupPath(w, r)
	if msg != "" {
		s.d.Errors.Error(w, r, http.StatusForbidden, msg)
		return
	}
	f := s.newRegisterForm(inv)
	if !f.Password {
		s.d.Errors.Error(w, r, http.StatusForbidden, "Password sign-in is off.")
		return
	}
	f.Username = strings.TrimSpace(r.PostFormValue("username"))
	f.Email = strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	if !f.EmailField {
		f.Email = ""
	}

	if err := ValidUsername(f.Username); err != nil {
		f.Errors["username"] = "Use 1–32 letters, digits, . _ or -, starting with a letter or digit."
	} else if _, err := s.store.UserByUsername(ctx, f.Username); err == nil {
		f.Errors["username"] = "That username is taken."
	}
	switch {
	case f.Email == "" && f.EmailRequired:
		f.Errors["email"] = "Enter your email address."
	case f.Email != "":
		if e, err := NormalizeEmail(f.Email); err != nil {
			f.Errors["email"] = "Enter a valid email address."
		} else if f.Email = e; s.emailInUse(ctx, e) {
			f.Errors["email"] = "That address belongs to another account."
		}
	}
	if err := auth.CheckPassword(password); err != nil {
		f.Errors["password"] = passwordMessage(err)
	}
	if len(f.Errors) > 0 {
		s.page(w, r, http.StatusUnprocessableEntity, "auth/register", web.Page{Title: "Create an account", Data: f})
		return
	}

	hash, err := auth.HashPassword(ctx, password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Which address the account starts with, and which one still needs confirming.
	nu := NewUser{Username: f.Username, PasswordHash: hash, Role: auth.RoleUser}
	var confirmKind, confirmEmail string
	switch {
	case inv == nil: // open sign-up: the address must be verified before creating content
		nu.Email, confirmKind, confirmEmail = f.Email, TokenVerify, f.Email
	case inv.Email != "" && ((f.Email == "" && !f.EmailField) || strings.EqualFold(f.Email, inv.Email)):
		nu.Email, nu.EmailVerified = inv.Email, true // the admin vouched for this address
	case f.Email != "": // invited, chose another address: it is added once confirmed
		confirmKind, confirmEmail = TokenEmailChange, f.Email
	}

	var uid int64
	var confirmRaw string
	actor := Actor(r)
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		now := s.store.now()
		if inv != nil {
			used, err := useInviteTx(ctx, tx, rawInvite, now)
			if err != nil {
				return err
			}
			nu.Role = used.Role
		}
		var err error
		if uid, err = createUserTx(ctx, tx, nu, now); err != nil {
			return err
		}
		if confirmKind != "" {
			ttl := VerifyTTL
			if confirmRaw, err = createTokenTx(ctx, tx, confirmKind, uid, confirmEmail, ttl, now); err != nil {
				return err
			}
		}
		actor.ActorUserID, actor.Action, actor.Target = uid, "user.register", userTarget(uid)
		actor.Details = map[string]any{"invite": inv != nil, "role": nu.Role}
		if inv != nil {
			actor.Details["invite_id"] = inv.ID
		}
		return auditTx(ctx, tx, actor, now)
	})
	switch {
	case errors.Is(err, ErrInvalidToken):
		s.clearCookie(w, s.cookieInvite)
		s.d.Errors.Error(w, r, http.StatusForbidden, "This invite has expired or has been used.")
		return
	case errors.Is(err, ErrUsernameTaken):
		f.Errors["username"] = "That username is taken."
	case errors.Is(err, ErrEmailTaken) && !f.EmailField:
		s.d.Errors.Error(w, r, http.StatusConflict, "The invited address belongs to another account. Ask for a new invite.")
		return
	case errors.Is(err, ErrEmailTaken):
		f.Errors["email"] = "That address belongs to another account."
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	if len(f.Errors) > 0 {
		s.page(w, r, http.StatusUnprocessableEntity, "auth/register", web.Page{Title: "Create an account", Data: f})
		return
	}
	s.clearCookie(w, s.cookieInvite)
	u, err := s.store.User(ctx, uid)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if confirmRaw != "" {
		kind := mail.KindVerify
		if confirmKind == TokenEmailChange {
			kind = mail.KindEmailChange
		}
		_ = s.SendMail(ctx, kind, confirmEmail, mail.Data{URL: s.Link("/verify", confirmRaw), Expires: humanDuration(VerifyTTL), Username: u.Username})
	}
	if err := s.signIn(ctx, w, r, u, "register"); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/")
}

func (s *Service) emailInUse(ctx context.Context, email string) bool {
	_, err := s.store.UserByEmail(ctx, email)
	return err == nil
}

func passwordMessage(err error) string {
	if errors.Is(err, auth.ErrPasswordTooLong) {
		return "Use at most 512 bytes."
	}
	return "Use at least 10 characters."
}

// invitePage is the landing page of an invite link (/invite#t=…).
func (s *Service) invitePage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, http.StatusOK, "auth/invite", web.Page{Title: "Invite", NoIndex: true, NoReferrer: true, Scripts: []string{"js/token-page.js"}})
}

// acceptInvite checks an invite token posted by token-page.js and keeps it in
// a short-lived cookie for /register and the SSO callback.
func (s *Service) acceptInvite(w http.ResponseWriter, r *http.Request) {
	if ok, wait := s.allow(s.lim.invite, r); !ok {
		s.tooMany(w, r, wait)
		return
	}
	if p := auth.FromContext(r.Context()); p != nil {
		s.d.Errors.Error(w, r, http.StatusConflict, "You're signed in as "+p.Username+". Sign out to use this invite.")
		return
	}
	if s.EffectiveSignupMode() == SignupClosed {
		s.d.Errors.Error(w, r, http.StatusForbidden, "Sign-up is closed.")
		return
	}
	raw := r.PostFormValue("token")
	if _, err := s.store.InviteByToken(r.Context(), raw); err != nil {
		if errors.Is(err, ErrInvalidToken) {
			s.d.Errors.Error(w, r, http.StatusNotFound, "This invite has expired or has been used.")
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.setCookie(w, s.cookieInvite, raw, inviteCookieAge)
	web.JSON(w, http.StatusOK, map[string]string{"redirect": "/register"})
}
