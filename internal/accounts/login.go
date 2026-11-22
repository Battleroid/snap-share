package accounts

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

const msgWrongLogin = "Wrong username or password."

func (s *Service) loginForm(w http.ResponseWriter, r *http.Request, status int, login, next, msg string) {
	s.page(w, r, status, "auth/login", web.Page{Title: "Sign in", Data: map[string]any{
		"Next": next, "Login": login, "Error": msg,
	}})
}

// login handles POST /login. Rate limits run before any password hashing.
func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	login := strings.TrimSpace(r.PostFormValue("login"))
	password := r.PostFormValue("password")
	next := web.SafeNext(r.PostFormValue("next"))
	if len(login) > 254 {
		login = login[:254]
	}
	if !s.passwordLogin() {
		s.loginForm(w, r, http.StatusForbidden, "", next, "Password sign-in is off.")
		return
	}
	if ok, wait := s.allow(s.lim.loginIP, r); !ok {
		s.loginForm(w, r, http.StatusTooManyRequests, login, next, "Too many attempts. Try again in "+humanWait(wait)+".")
		return
	}
	if login == "" || password == "" {
		s.loginForm(w, r, http.StatusBadRequest, login, next, msgWrongLogin)
		return
	}

	u, err := s.store.UserByLogin(ctx, login)
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.serverError(w, r, err)
		return
	}
	// Failed attempts per account; keyed by user id when the login matches one,
	// so username and email share a budget.
	acctKey := "login:" + strings.ToLower(login)
	if u != nil {
		acctKey = "user:" + strconv.FormatInt(u.ID, 10)
	}
	// Only failures spend the per-account budget (Allow below), so parallel
	// successful sign-ins never lock an account out.
	if ok, wait := s.lim.loginAccount.Check(acctKey); !ok {
		s.loginForm(w, r, http.StatusTooManyRequests, login, next, "Too many attempts. Try again in "+humanWait(wait)+".")
		return
	}

	ok := false
	if u == nil || !u.HasPassword() {
		err = auth.VerifyDummy(ctx, password)
	} else {
		var rehash bool
		ok, rehash, err = auth.VerifyPassword(ctx, u.PasswordHash, password)
		if ok && rehash {
			if h, herr := auth.HashPassword(ctx, password); herr == nil {
				_ = s.store.Rehash(ctx, u.ID, h)
			}
		}
	}
	if err != nil && !errors.Is(err, auth.ErrMalformedHash) {
		s.serverError(w, r, err)
		return
	}
	if !ok {
		s.lim.loginAccount.Allow(acctKey)
		e := Actor(r)
		e.Action = "auth.login_failed"
		if u != nil {
			e.Target = userTarget(u.ID)
		}
		_ = s.store.Audit(ctx, e)
		s.loginForm(w, r, http.StatusUnauthorized, login, next, msgWrongLogin)
		return
	}
	s.lim.loginAccount.Reset(acctKey)
	if !u.Active() {
		s.loginForm(w, r, http.StatusForbidden, login, next, "This account is disabled.")
		return
	}
	if err := s.signIn(ctx, w, r, u, "password"); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, next)
}

// logout handles POST /logout: ends this browser's session.
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.cookieSession); err == nil {
		if err := s.sessions.Delete(r.Context(), c.Value); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.clearCookie(w, s.cookieSession)
	redirect(w, r, "/")
}
