package accounts

// Confirm it's you for other packages (the admin reset, admin-scope API
// keys), and the admin reset of a user's second factors.

import (
	"context"
	"errors"
	"net/http"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/web"
)

// Proof is what a confirm-it's-you form shows the signed-in user (the
// "security.proof" template): the current password and, with 2FA, a code
// or a passkey; for an account without a password, a recent SSO sign-in
// or a passkey.
type Proof struct {
	AskPassword  bool // the current password counts
	Recent       bool // signed in within ReauthWindow
	OIDCEnabled  bool
	OIDCName     string
	Factors      Factors
	TOTPReadable bool
}

// Proof returns the confirm-it's-you fields for r's signed-in user.
func (s *Service) Proof(r *http.Request) (Proof, error) {
	u, err := s.CurrentUser(r)
	if err != nil {
		return Proof{}, err
	}
	f, err := s.store.Factors(r.Context(), u.ID)
	if err != nil {
		return Proof{}, err
	}
	return Proof{
		AskPassword: s.passwordCounts(u), Recent: s.recentAuth(r), OIDCEnabled: s.oidc != nil, OIDCName: s.d.Cfg.OIDC.Name,
		Factors: f, TOTPReadable: f.TOTP && s.totpReadable(r.Context(), u.ID),
	}, nil
}

// HasMFA reports whether uid has a second factor.
func (s *Service) HasMFA(ctx context.Context, uid int64) (bool, error) {
	f, err := s.store.Factors(ctx, uid)
	return f.Any(), err
}

// ConfirmOwner checks the confirm-it's-you fields of r's form ("current",
// "code", "passkey") for the signed-in user, as the settings pages do. It
// returns a *web.APIError with the message for the form when they fall short.
func (s *Service) ConfirmOwner(r *http.Request) error {
	u, err := s.CurrentUser(r)
	if err != nil {
		return err
	}
	return reauthAPIError(s.confirmOwner(r, u, r.PostFormValue("current"), s.passwordCounts(u)))
}

func reauthAPIError(err error) error {
	var re *errReauth
	if errors.As(err, &re) {
		return &web.APIError{Status: re.status, Msg: re.msg}
	}
	return err
}

// Refusals of AdminResetMFA.
var (
	errResetOwnMFA = &web.APIError{Status: http.StatusConflict, Msg: "Manage your own two-factor authentication in Settings, Security."}
	errResetByKey  = &web.APIError{Status: http.StatusForbidden, Msg: "Resetting two-factor authentication needs an admin's browser session."}
)

// AdminResetMFA removes every second factor of the user targetID and ends
// their sessions (Store.ResetMFA) for the admin making r, then emails the
// user. The admin confirms it's them in the same form: their password and,
// if they have one, their own second factor. API keys and the admin's own
// account are refused. Refusals are *web.APIError with the message to
// show; an unknown user is ErrNotFound.
func (s *Service) AdminResetMFA(r *http.Request, targetID int64) error {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	if p == nil || p.ViaKey() || p.SessionID == "" || !p.IsAdmin() {
		return errResetByKey
	}
	if p.UserID == targetID {
		return errResetOwnMFA
	}
	target, err := s.store.User(ctx, targetID)
	if err != nil {
		return err
	}
	admin, err := s.store.User(ctx, p.UserID)
	if err != nil {
		return err
	}
	if err := s.confirmOwner(r, admin, r.PostFormValue("current"), s.passwordCounts(admin)); err != nil {
		return reauthAPIError(err)
	}
	if err := s.store.ResetMFA(ctx, targetID, Actor(r)); err != nil {
		return err
	}
	s.securityNotice(ctx, target, SecurityMFAReset, "")
	return nil
}

// ResetMFA is the admin CLI's reset: Store.ResetMFA, then the email to the
// user, sent before it returns. Shell access to the server is the proof.
// It reports whether the email went out.
func (s *Service) ResetMFA(ctx context.Context, u *User, actor AuditEntry) (mailed bool, err error) {
	if err := s.store.ResetMFA(ctx, u.ID, actor); err != nil {
		return false, err
	}
	if !u.EmailVerified() || !s.MailEnabled() {
		return false, nil
	}
	data := mail.Data{URL: s.d.Cfg.Origin() + "/settings/security", Username: u.Username, Event: SecurityMFAReset}
	return s.SendMail(ctx, mail.KindSecurity, u.Email, data) == nil, nil
}
