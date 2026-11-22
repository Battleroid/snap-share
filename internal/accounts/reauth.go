package accounts

// Recovery and re-authentication: what ends when an account's owner may
// have lost control of it, and the proof asked for before sensitive changes.

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
)

// KeyRevoker ends a user's API keys inside the caller's transaction and
// audits it. The keys track implements it (keys.Store); until one is set
// (the CLI), keys are left alone.
type KeyRevoker interface {
	RevokeAllForUser(ctx context.Context, tx *sql.Tx, userID int64, reason string, actor AuditEntry) (int, error)
	RevokeAdminForUser(ctx context.Context, tx *sql.Tx, userID int64, reason string, actor AuditEntry) (int, error)
}

type keyRevokerBox struct{ v KeyRevoker }

// SetKeyRevoker installs the API-key revoker. Call before serving.
func (s *Service) SetKeyRevoker(k KeyRevoker) { s.store.SetKeyRevoker(k) }

// SetKeyRevoker installs the API-key revoker on the store.
func (s *Store) SetKeyRevoker(k KeyRevoker) { s.keys.Store(&keyRevokerBox{k}) }

// Reasons recorded with revoked keys.
const (
	revokePasswordReset  = "password_reset"
	revokePasswordChange = "password_change"
	revokeSignOutAll     = "signout_everywhere"
	revokeDisabled       = "disabled"
	revokeSSODemoted     = "sso_demoted"
)

// revokeKeysTx revokes all of uid's API keys (admin only: just those with
// the admin scope).
func (s *Store) revokeKeysTx(ctx context.Context, tx *sql.Tx, uid int64, adminOnly bool, reason string, actor AuditEntry) error {
	box := s.keys.Load()
	if box == nil || box.v == nil {
		return nil
	}
	var err error
	if adminOnly {
		_, err = box.v.RevokeAdminForUser(ctx, tx, uid, reason, actor)
	} else {
		_, err = box.v.RevokeAllForUser(ctx, tx, uid, reason, actor)
	}
	return err
}

// Re-authentication.
const (
	// ReauthWindow is how recent a sign-in must be to stand in for the
	// current password (accounts without one: SSO only).
	ReauthWindow = 10 * time.Minute
	// SSOAdminMaxAge is how long a session keeps admin rights after an SSO
	// sign-in when roles come from the SSO mapping (SNAP_OIDC_ADMIN_GROUPS or
	// SNAP_OIDC_ADMIN_SUBS) and the user has an SSO identity. After that,
	// admin pages need a fresh SSO sign-in, which re-reads the mapping.
	SSOAdminMaxAge = 24 * time.Hour
)

// ssoRoles reports whether roles come from the SSO mapping.
func (s *Service) ssoRoles() bool {
	return s.oidc != nil && (len(s.d.Cfg.OIDC.AdminGroups) > 0 || len(s.d.Cfg.OIDC.AdminSubs) > 0)
}

// recentAuth reports whether this browser's session signed in within ReauthWindow.
func (s *Service) recentAuth(r *http.Request) bool {
	p := auth.FromContext(r.Context())
	if p == nil || p.SessionID == "" {
		return false
	}
	at := s.sessions.AuthAt(r.Context(), p.SessionID)
	return !at.IsZero() && s.sessions.now().Sub(at) <= ReauthWindow
}

// errReauth is a failed re-authentication; msg is for the form.
type errReauth struct {
	status int
	msg    string
}

func (e *errReauth) Error() string { return e.msg }

// passwordCounts reports whether u's password is a way to prove who they are.
func (s *Service) passwordCounts(u *User) bool { return u.HasPassword() && s.passwordLogin() }

// confirmOwner checks that the person making a sensitive change is the
// account's owner: with usePassword, the current password; otherwise a
// sign-in within ReauthWindow (for SSO accounts, a fresh SSO sign-in). It
// returns an *errReauth when they are not.
func (s *Service) confirmOwner(r *http.Request, u *User, current string, usePassword bool) error {
	if !usePassword || !u.HasPassword() {
		if s.recentAuth(r) {
			return nil
		}
		name := "single sign-on"
		if s.oidc != nil {
			name = s.d.Cfg.OIDC.Name
		}
		return &errReauth{http.StatusForbidden, "Sign in with " + name + " again first: this needs a sign-in from the last 10 minutes."}
	}
	if current == "" {
		return &errReauth{http.StatusUnprocessableEntity, "Enter your current password."}
	}
	key := "user:" + strconv.FormatInt(u.ID, 10)
	res, ok, wait := s.lim.loginAccount.Reserve(r.Context(), key, loginQueueWait)
	if !ok {
		return &errReauth{http.StatusTooManyRequests, "Too many attempts. Try again in " + humanWait(wait) + "."}
	}
	defer res.Keep()
	match, _, err := auth.VerifyPassword(r.Context(), u.PasswordHash, current)
	if err != nil && !errors.Is(err, auth.ErrMalformedHash) {
		res.Refund()
		return err
	}
	if !match {
		return &errReauth{http.StatusUnprocessableEntity, "That is not your current password."}
	}
	res.Refund()
	s.lim.loginAccount.Reset(key)
	return nil
}
