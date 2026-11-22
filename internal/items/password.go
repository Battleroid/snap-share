package items

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2Hasher is the fallback item-password hasher (Argon2id, m=19 MiB, t=2,
// p=1, PHC string format) used until the accounts track's hasher is wired in
// through Deps.Passwords.
type argon2Hasher struct{}

const (
	a2Mem  = 19 * 1024
	a2Time = 2
	a2Len  = 32
)

func (argon2Hasher) Hash(pw string) (string, error) {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(pw), salt, a2Time, a2Mem, 1, a2Len)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=1$%s$%s", argon2.Version, a2Mem, a2Time, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func (argon2Hasher) Verify(hash, pw string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, fmt.Errorf("password hash params: %w", err)
	}
	if m > 256*1024 || t > 10 || p < 1 || p > 4 {
		return false, errors.New("password hash params out of range")
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > 64 {
		return false, errors.New("bad password hash")
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want))) // #nosec G115 -- len checked above
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
