package notify

import (
	"errors"

	"github.com/Battleroid/snap-share/internal/auth"
)

// tokenKeyInfo separates the ntfy token key from other uses of SNAP_SECRET_KEY.
const tokenKeyInfo = "snap notify ntfy token v1" //nolint:gosec // G101: an HKDF label, not a credential

// errTokenUnreadable means no configured key opens a stored token (the
// secret key was rotated without SNAP_SECRET_KEY_PREVIOUS).
var errTokenUnreadable = errors.New("stored ntfy token cannot be decrypted; set it again")

// sealer encrypts ntfy access tokens at rest (auth.Sealer: AES-256-GCM
// under keys derived from SNAP_SECRET_KEY, the user id bound in), so a
// sealed token copied to another user's row does not open.
type sealer struct{ s *auth.Sealer }

func newSealer(keys ...[]byte) (*sealer, error) {
	s, err := auth.NewSealer(tokenKeyInfo, keys...)
	if err != nil {
		return nil, errors.New("notify: secret key is required")
	}
	return &sealer{s}, nil
}

// seal returns nonce || ciphertext with a fresh random nonce.
func (s *sealer) seal(userID int64, token string) ([]byte, error) {
	return s.s.Seal(userID, []byte(token))
}

// open decrypts a value from seal, trying the current key, then the previous.
func (s *sealer) open(userID int64, ct []byte) (string, error) {
	pt, _, err := s.s.Open(userID, ct)
	if err != nil {
		return "", errTokenUnreadable
	}
	return string(pt), nil
}
