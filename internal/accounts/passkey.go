package accounts

// Passkeys and security keys (WebAuthn): registration and management in
// Settings › Security, sign-in without a password, the passkey path of
// /login/2fa, and a passkey assertion as proof in confirmOwner.
//
// Track P owns this file; these are the foundation's stubs.

import (
	"context"
	"net/http"

	"github.com/Battleroid/snap-share/internal/web"
)

// registerPasskeys adds the passkey routes.
func (s *Service) registerPasskeys(r *web.Router) {}

// checkPasskeyProof verifies a passkey assertion (with user verification)
// sent with a sensitive change, against this browser's reauth challenge.
func (s *Service) checkPasskeyProof(r *http.Request, u *User, assertion string) error {
	return errNoFactor
}

// passkeyRow is a passkey on the security page.
type passkeyRow struct {
	ID       int64
	Name     string
	Created  string
	LastUsed string // "" when never used
	Synced   bool   // backed up (a synced passkey)
}

// passkeyRows lists uid's passkeys for the security page.
func (s *Service) passkeyRows(ctx context.Context, uid int64) ([]passkeyRow, error) { return nil, nil }
