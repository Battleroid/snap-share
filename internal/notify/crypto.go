package notify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// tokenKeyInfo separates the ntfy token key from other uses of SNAP_SECRET_KEY.
const tokenKeyInfo = "snap notify ntfy token v1" //nolint:gosec // G101: an HKDF label, not a credential

// errTokenUnreadable means no configured key opens a stored token (the
// secret key was rotated without SNAP_SECRET_KEY_PREVIOUS).
var errTokenUnreadable = errors.New("stored ntfy token cannot be decrypted; set it again")

// sealer encrypts ntfy access tokens at rest with AES-256-GCM under keys
// derived from SNAP_SECRET_KEY (and SNAP_SECRET_KEY_PREVIOUS for reading
// during rotation). The user id is the associated data, so a sealed token
// copied to another user's row does not open.
type sealer struct {
	aeads []cipher.AEAD // [current, previous]
}

func newSealer(keys ...[]byte) (*sealer, error) {
	s := &sealer{}
	for _, k := range keys {
		if len(k) == 0 {
			continue
		}
		dk, err := hkdf.Key(sha256.New, k, nil, tokenKeyInfo, 32)
		if err != nil {
			return nil, err
		}
		block, err := aes.NewCipher(dk)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		s.aeads = append(s.aeads, aead)
	}
	if len(s.aeads) == 0 {
		return nil, errors.New("notify: secret key is required")
	}
	return s, nil
}

func tokenAD(userID int64) []byte {
	ad := make([]byte, 0, len(tokenKeyInfo)+8)
	ad = append(ad, tokenKeyInfo...)
	return binary.BigEndian.AppendUint64(ad, uint64(userID)) //nolint:gosec // G115: ids are positive; any bit pattern works as AD
}

// seal returns nonce || ciphertext with a fresh random nonce.
func (s *sealer) seal(userID int64, token string) ([]byte, error) {
	aead := s.aeads[0]
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(token)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("notify: nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, []byte(token), tokenAD(userID)), nil
}

// open decrypts a value from seal, trying the current key, then the previous.
func (s *sealer) open(userID int64, ct []byte) (string, error) {
	for _, aead := range s.aeads {
		n := aead.NonceSize()
		if len(ct) < n+aead.Overhead() {
			return "", errTokenUnreadable
		}
		pt, err := aead.Open(nil, ct[:n], ct[n:], tokenAD(userID))
		if err == nil {
			return string(pt), nil
		}
	}
	return "", errTokenUnreadable
}
