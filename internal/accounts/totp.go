package accounts

// Authenticator apps (TOTP, RFC 6238) and recovery codes as a second
// factor: setup and removal in Settings › Security, the code step of
// /login/2fa, and codes as proof in confirmOwner.
//
// Track T owns this file; these are the foundation's stubs.

import (
	"context"
	"net/http"

	"github.com/Battleroid/snap-share/internal/web"
)

// registerTOTP adds the authenticator-app and recovery-code routes.
func (s *Service) registerTOTP(r *web.Router) {}

// totpReadable reports whether uid's authenticator-app secret opens with
// a configured key (it doesn't after a key rotation without
// SNAP_SECRET_KEY_PREVIOUS); the code option is then not offered.
func (s *Service) totpReadable(ctx context.Context, uid int64) bool { return false }

// checkCodeProof verifies a code sent with a sensitive change (an
// authenticator-app code or a recovery code), charging the per-account
// failure budget.
func (s *Service) checkCodeProof(r *http.Request, u *User, code string) error { return errNoFactor }
