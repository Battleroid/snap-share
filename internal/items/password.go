package items

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/Battleroid/snap-share/internal/web"
)

// Item password policy. Shorter passwords set before the minimum existed
// still unlock their items.
const (
	MinItemPasswordLen = 8   // characters
	MaxItemPasswordLen = 256 // bytes
)

var (
	errPasswordShort = web.Errorf(http.StatusBadRequest, "Item passwords are at least %d characters.", MinItemPasswordLen)
	errPasswordLong  = web.Errorf(http.StatusBadRequest, "The password is too long.")
)

// checkItemPassword applies the policy to a new item password ("" is no password).
func checkItemPassword(pw string) error {
	switch {
	case pw == "":
		return nil
	case len(pw) > MaxItemPasswordLen:
		return errPasswordLong
	case utf8.RuneCountInString(pw) < MinItemPasswordLen:
		return errPasswordShort
	}
	return nil
}

// Unlock attempts.
const (
	unlockSlots        = 2  // concurrent unlock hashes (the hasher shares 4 with sign-in)
	unlockItemFails    = 20 // wrong passwords per item per hour, from all addresses together
	unlockAddrAttempts = 20 // attempts per address per minute, at any items
	unlockBusyTimeout  = 5 * time.Second
)

var errUnlockBusy = errors.New("items: unlock hashing busy")

// verifyUnlock checks an unlock attempt. Unlocks hash on their own small
// semaphore, so a flood of them can take at most unlockSlots of the
// hasher's slots and never starve sign-in.
func (s *Service) verifyUnlock(ctx context.Context, it *Item, pw string) (bool, error) {
	if len(pw) > MaxItemPasswordLen {
		return false, nil
	}
	t := time.NewTimer(unlockBusyTimeout)
	defer t.Stop()
	select {
	case s.unlockHash <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	case <-t.C:
		return false, errUnlockBusy
	}
	defer func() { <-s.unlockHash }()
	return s.passwords.Verify(it.PasswordHash, pw)
}

// unlockTooMany answers 429 on the unlock page.
func (s *Service) unlockTooMany(w http.ResponseWriter, r *http.Request, it *Item, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(wait.Seconds()+0.999))))
	s.unlockPage(w, r, it, http.StatusTooManyRequests, "Too many attempts. Try again later.")
}

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
