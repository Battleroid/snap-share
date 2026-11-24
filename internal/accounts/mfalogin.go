package accounts

// The second step of a password sign-in (and of a password reset) for a
// user with a second factor: GET /login/2fa shows the ways in; each factor
// verifies itself (totp.go: codes, passkey.go: passkeys) and then calls
// finishMFA, which applies a held reset and starts the session.

import (
	"context"
	"errors"
	"net/http"

	"github.com/Battleroid/snap-share/internal/web"
)

// startMFA stores an mfa_login challenge for uid and sends the browser to
// the second step. data holds a password reset waiting for the factor.
func (s *Service) startMFA(ctx context.Context, w http.ResponseWriter, r *http.Request, uid int64, data challengeData, next string) error {
	if err := s.newChallenge(ctx, w, r, ChallengeMFALogin, uid, data, web.SafeNext(next)); err != nil {
		return err
	}
	redirect(w, r, "/login/2fa")
	return nil
}

// mfaData is the second-step page.
type mfaData struct {
	Passkeys      bool // the user has a passkey or security key
	TOTP          bool // ... an authenticator app whose secret this server can read
	RecoveryLeft  int
	Reset         bool   // finishing a password reset
	Username      string // whose sign-in this is (the password already checked out)
	Error         string // a problem with the sign-in as a whole: too many attempts
	CodeError     string // what is wrong with the authenticator code, shown on its field
	RecoveryError string // the same for the recovery code
}

// mfaPage handles GET /login/2fa.
func (s *Service) mfaPage(w http.ResponseWriter, r *http.Request) {
	s.mfaForm(w, r, http.StatusOK, "")
}

// mfaForm renders the second step for this browser's mfa_login challenge,
// or sends it back to sign in when there is none.
func (s *Service) mfaForm(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.mfaRender(w, r, status, func(d *mfaData) { d.Error = msg })
}

// mfaCodeForm is mfaForm with the problem on the field of the kind of code that was entered.
func (s *Service) mfaCodeForm(w http.ResponseWriter, r *http.Request, status int, kind, msg string) {
	s.mfaRender(w, r, status, func(d *mfaData) {
		if kind == "recovery" {
			d.RecoveryError = msg
		} else {
			d.CodeError = msg
		}
	})
}

func (s *Service) mfaRender(w http.ResponseWriter, r *http.Request, status int, set func(*mfaData)) {
	ctx := r.Context()
	c, err := s.peekChallenge(ctx, r, ChallengeMFALogin)
	if err != nil {
		if !errors.Is(err, ErrNoChallenge) {
			s.serverError(w, r, err)
			return
		}
		redirect(w, r, "/login")
		return
	}
	f, err := s.store.Factors(ctx, c.UserID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	u, err := s.store.User(ctx, c.UserID)
	if errors.Is(err, ErrNotFound) {
		redirect(w, r, "/login")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	d := mfaData{
		Passkeys: f.Passkeys > 0, TOTP: f.TOTP && s.totpReadable(ctx, c.UserID), RecoveryLeft: f.RecoveryLeft,
		Reset: len(c.Data.ResetToken) > 0, Username: u.Username,
	}
	set(&d)
	scripts := []string{}
	if d.Passkeys {
		scripts = append(scripts, "js/webauthn.js")
	}
	if d.TOTP {
		scripts = append(scripts, "js/code-input.js") // the six boxes over input[name=code]
	}
	s.page(w, r, status, "auth/mfa", web.Page{Title: "Two-factor authentication", NoIndex: true, NoReferrer: true, Scripts: scripts, Data: d})
}

// finishMFA completes the second step after a factor succeeded on the
// challenge c (already taken). factor names it for the audit log: "totp",
// "recovery" or "key". A held password reset is applied first, in one
// transaction (token, password, sessions, keys). It returns where to go.
func (s *Service) finishMFA(ctx context.Context, w http.ResponseWriter, r *http.Request, c *challenge, factor string) (string, error) {
	u, err := s.store.User(ctx, c.UserID)
	if err != nil {
		return "", err
	}
	if !u.Active() {
		return "", errDisabled
	}
	method := "password+" + factor
	if len(c.Data.ResetToken) > 0 {
		if u, err = s.store.resetPasswordByHash(ctx, c.Data.ResetToken, c.Data.ResetPassword, Actor(r).IP); err != nil {
			return "", err
		}
		method = "reset+" + factor
		s.securityNotice(ctx, u, SecurityPasswordReset, "")
	}
	if err := s.signIn(ctx, w, r, u, method); err != nil {
		return "", err
	}
	if c.Next == "" {
		return "/", nil
	}
	return c.Next, nil
}

// errDisabled is a sign-in by a disabled account.
var errDisabled = &errReauth{status: http.StatusForbidden, msg: "This account is disabled."}
