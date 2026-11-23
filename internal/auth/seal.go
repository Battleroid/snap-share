package auth

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

// ErrUnsealable means no configured key opens a sealed value: the secret
// key was rotated without SNAP_SECRET_KEY_PREVIOUS, or the value was
// sealed for another user or purpose.
var ErrUnsealable = errors.New("sealed value cannot be decrypted")

// Sealer encrypts small values at rest with AES-256-GCM under keys derived
// (HKDF-SHA256, the label as info) from SNAP_SECRET_KEY, and from
// SNAP_SECRET_KEY_PREVIOUS for reading during a rotation. The label and the
// user id are the associated data, so a value copied to another user's row,
// or to another purpose, does not open.
type Sealer struct {
	label string
	aeads []cipher.AEAD // [current, previous]
}

// NewSealer derives one key per non-empty secret; the first is current.
func NewSealer(label string, secrets ...[]byte) (*Sealer, error) {
	s := &Sealer{label: label}
	for _, k := range secrets {
		if len(k) == 0 {
			continue
		}
		dk, err := hkdf.Key(sha256.New, k, nil, label, 32)
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
		return nil, errors.New("auth: secret key is required")
	}
	return s, nil
}

func (s *Sealer) ad(userID int64) []byte {
	ad := make([]byte, 0, len(s.label)+8)
	ad = append(ad, s.label...)
	return binary.BigEndian.AppendUint64(ad, uint64(userID)) //nolint:gosec // G115: ids are positive; any bit pattern works as AD
}

// Seal returns nonce || ciphertext under the current key, with a fresh
// random nonce.
func (s *Sealer) Seal(userID int64, plaintext []byte) ([]byte, error) {
	aead := s.aeads[0]
	nonce := make([]byte, aead.NonceSize(), aead.NonceSize()+len(plaintext)+aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("auth: nonce: %w", err)
	}
	return aead.Seal(nonce, nonce, plaintext, s.ad(userID)), nil
}

// Open decrypts a value from Seal, trying the current key, then the
// previous one. current reports whether the current key opened it; when
// false the caller should seal the value again.
func (s *Sealer) Open(userID int64, sealed []byte) (plaintext []byte, current bool, err error) {
	for i, aead := range s.aeads {
		n := aead.NonceSize()
		if len(sealed) < n+aead.Overhead() {
			return nil, false, ErrUnsealable
		}
		pt, err := aead.Open(nil, sealed[:n], sealed[n:], s.ad(userID))
		if err == nil {
			return pt, i == 0, nil
		}
	}
	return nil, false, ErrUnsealable
}
