package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password policy: length only, no composition rules.
const (
	MinPasswordLen = 10  // characters
	MaxPasswordLen = 512 // bytes
)

// Password errors.
var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d bytes", MaxPasswordLen)
	ErrMalformedHash    = errors.New("malformed password hash")
)

// Argon2id parameters for new hashes (OWASP's m=19 MiB, t=2, p=1).
const (
	argonMemory  = 19456 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// hashSlots bounds concurrent Argon2id computations across the process, so a
// burst of logins cannot exhaust memory (each hash takes ~19 MiB).
var hashSlots = make(chan struct{}, 4)

// dummySalt is used for the hash that runs when a login matches no account.
var dummySalt = []byte("snap-dummy-salt!")

// CheckPassword reports whether pw satisfies the password policy.
func CheckPassword(pw string) error {
	if len(pw) > MaxPasswordLen {
		return ErrPasswordTooLong
	}
	if utf8.RuneCountInString(pw) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	return nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

var currentParams = argonParams{argonMemory, argonTime, argonThreads}

func derive(ctx context.Context, pw string, salt []byte, p argonParams, keyLen uint32) ([]byte, error) {
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(pw), salt, p.time, p.memory, p.threads, keyLen), nil
}

// HashPassword returns a PHC-format Argon2id hash of pw:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash> (unpadded standard base64).
// It does not check the policy; call CheckPassword first.
func HashPassword(ctx context.Context, pw string) (string, error) {
	if len(pw) > MaxPasswordLen {
		return "", ErrPasswordTooLong
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := derive(ctx, pw, salt, currentParams, argonKeyLen)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		currentParams.memory, currentParams.time, currentParams.threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks pw against a hash from HashPassword. needsRehash is
// true when the hash uses parameters other than the current ones.
func VerifyPassword(ctx context.Context, encoded, pw string) (ok, needsRehash bool, err error) {
	if len(pw) > MaxPasswordLen {
		// Still spend the time so oversized input is not a timing oracle.
		_ = VerifyDummy(ctx, "")
		return false, false, nil
	}
	p, salt, want, err := parseHash(encoded)
	if err != nil {
		return false, false, err
	}
	got, err := derive(ctx, pw, salt, p, uint32(len(want))) // #nosec G115 -- len(want) is bounded by parseHash
	if err != nil {
		return false, false, err
	}
	ok = subtle.ConstantTimeCompare(got, want) == 1
	return ok, ok && p != currentParams, nil
}

// VerifyDummy spends the same time as VerifyPassword for a login that matches
// no account (or an account without a password), so response times do not
// reveal which usernames exist.
func VerifyDummy(ctx context.Context, pw string) error {
	if len(pw) > MaxPasswordLen {
		pw = pw[:MaxPasswordLen]
	}
	_, err := derive(ctx, pw, dummySalt, currentParams, argonKeyLen)
	return err
}

// NeedsRehash reports whether encoded was made with other parameters than new hashes.
func NeedsRehash(encoded string) bool {
	p, _, _, err := parseHash(encoded)
	return err != nil || p != currentParams
}

func parseHash(encoded string) (argonParams, []byte, []byte, error) {
	var p argonParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, ErrMalformedHash
	}
	var m, t, threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &threads); err != nil {
		return p, nil, nil, ErrMalformedHash
	}
	// Bound the work a stored hash can demand.
	if m < 8 || m > 1<<20 || t < 1 || t > 16 || threads < 1 || threads > 16 {
		return p, nil, nil, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return p, nil, nil, ErrMalformedHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return p, nil, nil, ErrMalformedHash
	}
	return argonParams{memory: m, time: t, threads: uint8(threads)}, salt, key, nil
}
