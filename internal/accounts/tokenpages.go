package accounts

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/web"
)

// Token pages: emailed links look like /verify#t=<token>. token-page.js reads
// the fragment, removes it from the address bar and history, and POSTs it.

const msgBadLink = "This link has expired or has been used."

func (s *Service) verifyPage(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, http.StatusOK, "auth/verify", web.Page{Title: "Verify email", NoIndex: true, NoReferrer: true, Scripts: []string{"js/token-page.js"}})
}

// verify handles POST /verify from token-page.js (verify and email_change
// tokens). Without confirm=1 it only checks the token and answers with the
// account and address it is for, which the page shows next to a Confirm
// button: opening the link (a mail scanner does) must not verify anything,
// or an address could be confirmed for an account its owner never made.
func (s *Service) verify(w http.ResponseWriter, r *http.Request) {
	if ok, wait := s.allow(s.lim.verify, r); !ok {
		s.tooMany(w, r, wait)
		return
	}
	raw := r.PostFormValue("token")
	if r.PostFormValue("confirm") != "1" {
		t, err := s.store.PeekToken(r.Context(), raw, TokenVerify, TokenEmailChange)
		var u *User
		if err == nil {
			u, err = s.store.User(r.Context(), t.UserID)
			if errors.Is(err, ErrNotFound) {
				err = ErrInvalidToken
			}
		}
		switch {
		case errors.Is(err, ErrInvalidToken):
			s.d.Errors.Error(w, r, http.StatusNotFound, msgBadLink)
		case err != nil:
			s.serverError(w, r, err)
		default:
			web.JSON(w, http.StatusOK, map[string]string{"confirm": "1", "username": u.Username, "email": t.Email})
		}
		return
	}
	u, err := s.store.ConfirmEmail(r.Context(), raw)
	switch {
	case errors.Is(err, ErrInvalidToken):
		s.d.Errors.Error(w, r, http.StatusNotFound, msgBadLink)
		return
	case errors.Is(err, ErrEmailTaken):
		s.d.Errors.Error(w, r, http.StatusConflict, "That address now belongs to another account.")
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	next := "/"
	if p := auth.FromContext(r.Context()); p != nil && p.UserID == u.ID {
		next = "/settings/profile"
	}
	web.JSON(w, http.StatusOK, map[string]string{"message": u.Email + " is verified.", "next": next})
}

type resetData struct {
	State string // "" (decided by token-page.js), "sent" or "form"
	Token string // only when re-rendering the form after an error
	Error string
	Email string
}

func (s *Service) resetForm(w http.ResponseWriter, r *http.Request, status int, d resetData) {
	s.page(w, r, status, "auth/reset", web.Page{Title: "Reset password", NoIndex: true, NoReferrer: true, Scripts: []string{"js/token-page.js"}, Data: d})
}

func (s *Service) resetAvailable(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case !s.passwordLogin():
		s.d.Errors.Error(w, r, http.StatusForbidden, "Password sign-in is off.")
	case !s.MailEnabled():
		s.d.Errors.Error(w, r, http.StatusNotFound, "Password reset needs email, which is not set up. Ask an admin.")
	default:
		return true
	}
	return false
}

func (s *Service) resetPage(w http.ResponseWriter, r *http.Request) {
	// Admin-issued links work without SMTP, so only password sign-in gates the page.
	if !s.passwordLogin() {
		s.d.Errors.Error(w, r, http.StatusForbidden, "Password sign-in is off.")
		return
	}
	s.resetForm(w, r, http.StatusOK, resetData{})
}

// requestReset handles POST /reset/request. The answer is the same whether or
// not an account uses the address.
func (s *Service) requestReset(w http.ResponseWriter, r *http.Request) {
	if !s.resetAvailable(w, r) {
		return
	}
	if ok, wait := s.allow(s.lim.reset, r); !ok {
		s.tooMany(w, r, wait)
		return
	}
	ctx := r.Context()
	email, err := NormalizeEmail(r.PostFormValue("email"))
	if err != nil {
		s.resetForm(w, r, http.StatusUnprocessableEntity, resetData{Error: "Enter a valid email address.", Email: r.PostFormValue("email")})
		return
	}
	// Look up and send in the background: the response must not take longer
	// when the address belongs to an account.
	go s.sendReset(context.WithoutCancel(ctx), email)
	s.resetForm(w, r, http.StatusOK, resetData{State: "sent", Email: email})
}

func (s *Service) sendReset(ctx context.Context, email string) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	u, err := s.store.UserByEmail(ctx, email)
	if err != nil || !u.Active() {
		return
	}
	raw, err := s.store.CreateToken(ctx, TokenReset, u.ID, u.Email, ResetTTL)
	if err != nil {
		s.d.Log.Error("reset token", "err", err)
		return
	}
	_ = s.SendMail(ctx, mail.KindReset, u.Email, mail.Data{URL: s.Link("/reset", raw), Expires: humanDuration(ResetTTL), Username: u.Username})
}

// resetCheck handles POST /reset/check from token-page.js: does this reset
// link still work? It lets the page say "expired or used" before anyone types
// a password. It reveals what POST /reset already does (a bad token is a 404
// there too, before the password is looked at) and no more: the answer is
// {"valid":"1"} or the same 404 for a malformed, unknown, used or expired
// token, never an address, a username or a factor. It is limited per client
// address like the other token pages and changes nothing.
func (s *Service) resetCheck(w http.ResponseWriter, r *http.Request) {
	if !s.passwordLogin() {
		s.d.Errors.Error(w, r, http.StatusForbidden, "Password sign-in is off.")
		return
	}
	if ok, wait := s.allow(s.lim.resetCheck, r); !ok {
		s.tooMany(w, r, wait)
		return
	}
	if _, err := s.store.PeekToken(r.Context(), strings.TrimSpace(r.PostFormValue("token")), TokenReset); err != nil {
		if errors.Is(err, ErrInvalidToken) {
			s.d.Errors.Error(w, r, http.StatusNotFound, msgBadLink)
			return
		}
		s.serverError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, map[string]string{"valid": "1"})
}

// reset handles POST /reset: a reset token plus the new password.
func (s *Service) reset(w http.ResponseWriter, r *http.Request) {
	if !s.passwordLogin() {
		s.d.Errors.Error(w, r, http.StatusForbidden, "Password sign-in is off.")
		return
	}
	if ok, wait := s.allow(s.lim.reset, r); !ok {
		s.tooMany(w, r, wait)
		return
	}
	ctx := r.Context()
	raw := strings.TrimSpace(r.PostFormValue("token"))
	password := r.PostFormValue("password")
	tok, err := s.store.PeekToken(ctx, raw, TokenReset)
	if err != nil {
		if errors.Is(err, ErrInvalidToken) {
			s.d.Errors.Error(w, r, http.StatusNotFound, msgBadLink)
			return
		}
		s.serverError(w, r, err)
		return
	}
	if err := auth.CheckPassword(password); err != nil {
		s.resetForm(w, r, http.StatusUnprocessableEntity, resetData{State: "form", Token: raw, Error: passwordMessage(err)})
		return
	}
	hash, err := auth.HashPassword(ctx, password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// With a second factor, the emailed link alone is not enough: nothing
	// changes (token, password, sessions, keys) until the factor succeeds,
	// so someone who only has the mailbox can't lock the owner out.
	if f, err := s.store.Factors(ctx, tok.UserID); err != nil {
		s.serverError(w, r, err)
		return
	} else if f.Any() {
		if err := s.startMFA(ctx, w, r, tok.UserID, challengeData{ResetToken: hashSecret(raw), ResetPassword: hash}, "/"); err != nil {
			s.serverError(w, r, err)
		}
		return
	}
	u, err := s.store.ResetPassword(ctx, raw, hash, Actor(r).IP)
	if errors.Is(err, ErrInvalidToken) {
		s.d.Errors.Error(w, r, http.StatusNotFound, msgBadLink)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !u.Active() {
		s.d.Errors.Error(w, r, http.StatusForbidden, "Password changed, but this account is disabled.")
		return
	}
	s.securityNotice(ctx, u, SecurityPasswordReset, "")
	if err := s.signIn(ctx, w, r, u, "reset"); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.afterSignIn(w, r, u, "/")
}
