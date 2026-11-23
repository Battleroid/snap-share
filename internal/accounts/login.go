package accounts

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

const msgWrongLogin = "Wrong username or password."

// loginQueueWait is how long a sign-in waits for parallel attempts on the
// same name to finish when the failure budget is otherwise spent.
const loginQueueWait = 10 * time.Second

// loginKey is the per-name failure budget's key: the submitted login,
// trimmed and lowercased (usernames and addresses match case-insensitively).
func loginKey(login string) string { return "login:" + strings.ToLower(strings.TrimSpace(login)) }

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

	// Failed attempts per submitted name. Each attempt takes a token before
	// the password is hashed, so parallel guesses get at most the budget that
	// is left; a successful one gives its token back, and parallel successful
	// sign-ins wait for each other instead of locking the account out. The
	// key is the name as typed (normalised), not the account it resolves to:
	// a shared budget would tell anyone which email address belongs to which
	// username (a locked username would make its address answer 429 too).
	acctKey := loginKey(login)
	res, ok, wait := s.lim.loginAccount.Reserve(ctx, acctKey, loginQueueWait)
	if !ok {
		s.loginForm(w, r, http.StatusTooManyRequests, login, next, "Too many attempts. Try again in "+humanWait(wait)+".")
		return
	}
	defer res.Keep() // a no-op once refunded

	u, err := s.store.UserByLogin(ctx, login)
	if err != nil && !errors.Is(err, ErrNotFound) {
		res.Refund()
		s.serverError(w, r, err)
		return
	}

	ok = false
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
		res.Refund()
		s.serverError(w, r, err)
		return
	}
	if !ok {
		res.Keep()
		e := Actor(r)
		e.Action = "auth.login_failed"
		if u != nil {
			e.Target = userTarget(u.ID)
		}
		_ = s.store.Audit(ctx, e)
		s.loginForm(w, r, http.StatusUnauthorized, login, next, msgWrongLogin)
		return
	}
	res.Refund()
	s.lim.loginAccount.Reset(acctKey)
	if !u.Active() {
		s.loginForm(w, r, http.StatusForbidden, login, next, "This account is disabled.")
		return
	}
	// With a second factor the password only opens the second step.
	if f, err := s.store.Factors(ctx, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	} else if f.Any() {
		if err := s.startMFA(ctx, w, r, u.ID, challengeData{}, next); err != nil {
			s.serverError(w, r, err)
		}
		return
	}
	if err := s.signIn(ctx, w, r, u, "password"); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.afterSignIn(w, r, u, next)
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
