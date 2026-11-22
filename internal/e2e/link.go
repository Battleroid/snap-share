package e2e

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrEncoding reports a value that is not canonical unpadded base64url of
	// the expected length.
	ErrEncoding = errors.New("e2e: invalid base64url value")
	// ErrLink reports a string that is not a {base}/s/{id}#{K_link} link.
	ErrLink = errors.New("e2e: invalid secret link")
)

// b64 is unpadded base64url that rejects non-zero trailing bits, so every
// value has exactly one encoding.
var b64 = base64.RawURLEncoding.Strict()

// Encode returns b as unpadded base64url.
func Encode(b []byte) string {
	return b64.EncodeToString(b)
}

// Decode16 decodes a 22-character base64url value (an id or salt).
func Decode16(s string) ([16]byte, error) {
	var out [16]byte
	return out, decodeFixed(s, out[:])
}

// Decode32 decodes a 43-character base64url value (K_link or any derived
// key, probe or commitment).
func Decode32(s string) ([32]byte, error) {
	var out [32]byte
	return out, decodeFixed(s, out[:])
}

func decodeFixed(s string, dst []byte) error {
	if len(s) != b64.EncodedLen(len(dst)) {
		return ErrEncoding
	}
	// The decoder skips CR and LF, so a short decode means the input held them.
	n, err := b64.Decode(dst, []byte(s))
	if err != nil || n != len(dst) {
		clear(dst)
		return ErrEncoding
	}
	return nil
}

func random(b []byte) error {
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("e2e: random: %w", err)
	}
	return nil
}

// NewLinkKey returns a fresh random K_link. Use a new one for every create
// attempt; never reuse one.
func NewLinkKey() ([KeySize]byte, error) {
	var k [KeySize]byte
	return k, random(k[:])
}

// NewSalt returns a fresh random envelope salt S.
func NewSalt() ([SaltSize]byte, error) {
	var s [SaltSize]byte
	return s, random(s[:])
}

// NewID returns a fresh random secret id. The server issues ids.
func NewID() ([IDSize]byte, error) {
	var id [IDSize]byte
	return id, random(id[:])
}

// FormatLink returns {base}/s/{id}#{K_link}. Trailing slashes on base are
// dropped.
func FormatLink(base string, id [IDSize]byte, key [KeySize]byte) string {
	return strings.TrimRight(base, "/") + "/s/" + Encode(id[:]) + "#" + Encode(key[:])
}

// ParseLink splits a link made by FormatLink. Surrounding whitespace is
// ignored. base is everything before the last "/s/" and is not otherwise
// checked. Errors never include the key.
func ParseLink(s string) (base string, id [IDSize]byte, key [KeySize]byte, err error) {
	s = strings.TrimSpace(s)
	path, frag, ok := strings.Cut(s, "#")
	if !ok {
		return "", id, key, fmt.Errorf("%w: no key fragment", ErrLink)
	}
	i := strings.LastIndex(path, "/s/")
	if i < 0 {
		return "", id, key, fmt.Errorf("%w: no /s/ path", ErrLink)
	}
	if id, err = Decode16(path[i+len("/s/"):]); err != nil {
		return "", [IDSize]byte{}, key, fmt.Errorf("%w: bad id", ErrLink)
	}
	if key, err = Decode32(frag); err != nil {
		return "", [IDSize]byte{}, [KeySize]byte{}, fmt.Errorf("%w: bad key", ErrLink)
	}
	return path[:i], id, key, nil
}
