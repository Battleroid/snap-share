package e2e

import (
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// Label is the protocol label. It is the HKDF salt of the probe key and the
// prefix of the HKDF salt of the main keys.
const Label = "snap-e2e-v1"

// Sizes in bytes of the random values in an envelope.
const (
	IDSize   = 16 // secret id, issued by the server
	KeySize  = 32 // K_link and every derived key
	SaltSize = 16 // envelope salt S, chosen by the creator
)

// Argon2id bounds. Clients refuse parameters outside them, so a malicious
// server can neither downgrade the passphrase cost nor make the client
// allocate unbounded memory.
const (
	MinKDFMemKiB = 19456
	MaxKDFMemKiB = 262144
	MinKDFTime   = 1
	MaxKDFTime   = 10
	KDFThreads   = 1
)

// Labels of the HKDF-Expand outputs. Each info is lp(label) || id.
const (
	labelProbe  = "probe"
	labelAuth   = "auth"
	labelEnc    = "enc"
	labelMeta   = "meta"
	labelCommit = "commit"
)

var (
	// ErrKDFParams reports Argon2id parameters outside the accepted bounds.
	ErrKDFParams = errors.New("e2e: kdf parameters out of bounds")
	// ErrPassphrase reports an empty passphrase or one that is not valid UTF-8.
	ErrPassphrase = errors.New("e2e: invalid passphrase")
	// ErrCommit reports that the derived key commitment does not match the
	// stored one: wrong passphrase, wrong link, or tampered envelope.
	ErrCommit = errors.New("e2e: key commitment mismatch")
)

// KDFParams are the Argon2id parameters for the optional passphrase. The JSON
// names follow RFC 9106 (m in KiB, t passes, p lanes).
type KDFParams struct {
	MemKiB  uint32 `json:"m"`
	Time    uint32 `json:"t"`
	Threads uint8  `json:"p"`
}

// DefaultKDF returns the parameters creators use unless told otherwise:
// m=65536 KiB, t=3, p=1.
func DefaultKDF() KDFParams {
	return KDFParams{MemKiB: 65536, Time: 3, Threads: KDFThreads}
}

// Validate reports whether p is inside the accepted bounds:
// m in [19456, 262144] KiB, t in [1, 10], p = 1.
func (p KDFParams) Validate() error {
	switch {
	case p.MemKiB < MinKDFMemKiB || p.MemKiB > MaxKDFMemKiB:
		return fmt.Errorf("%w: m=%d KiB, want %d..%d", ErrKDFParams, p.MemKiB, MinKDFMemKiB, MaxKDFMemKiB)
	case p.Time < MinKDFTime || p.Time > MaxKDFTime:
		return fmt.Errorf("%w: t=%d, want %d..%d", ErrKDFParams, p.Time, MinKDFTime, MaxKDFTime)
	case p.Threads != KDFThreads:
		return fmt.Errorf("%w: p=%d, want %d", ErrKDFParams, p.Threads, KDFThreads)
	}
	return nil
}

// Keys are the per-secret keys derived from K_link, the envelope salt and the
// optional passphrase. The server stores Hash(Auth) and Commit.
type Keys struct {
	Auth, Enc, Meta, Commit [32]byte
}

// CheckCommit compares the derived commitment with the one the server
// returned, in constant time. Call it before decrypting anything.
func (k Keys) CheckCommit(commit []byte) error {
	if subtle.ConstantTimeCompare(k.Commit[:], commit) != 1 {
		return ErrCommit
	}
	return nil
}

// Hash returns SHA-256(b). The server stores Hash(probe) and Hash(K_auth).
func Hash(b []byte) [32]byte {
	return sha256.Sum256(b)
}

// info returns lp(label) || id: one length byte, the ASCII label, then the
// 16 raw id bytes.
func info(label string, id [IDSize]byte) string {
	b := make([]byte, 0, 1+len(label)+IDSize)
	b = append(b, byte(len(label))) //nolint:gosec // labels are short constants
	b = append(b, label...)
	b = append(b, id[:]...)
	return string(b)
}

// ProbeKey derives K_probe = HKDF-SHA256(IKM=K_link, salt="snap-e2e-v1",
// info=lp("probe")||id, L=32). It does not depend on the envelope salt, so a
// client can compute it before asking the server for anything.
func ProbeKey(key [KeySize]byte, id [IDSize]byte) [KeySize]byte {
	out, err := hkdf.Key(sha256.New, key[:], []byte(Label), info(labelProbe, id), KeySize)
	if err != nil {
		panic("e2e: hkdf: " + err.Error()) // unreachable: fixed, valid lengths
	}
	return [KeySize]byte(out)
}

// PassphraseKey derives K_pw = Argon2id(UTF-8(NFC(passphrase)), salt, t, m,
// p, 32). The passphrase must be non-empty, valid UTF-8, and p must pass
// Validate.
func PassphraseKey(passphrase []byte, salt [SaltSize]byte, p KDFParams) ([KeySize]byte, error) {
	if err := p.Validate(); err != nil {
		return [KeySize]byte{}, err
	}
	if len(passphrase) == 0 {
		return [KeySize]byte{}, fmt.Errorf("%w: empty", ErrPassphrase)
	}
	if !utf8.Valid(passphrase) {
		return [KeySize]byte{}, fmt.Errorf("%w: not valid UTF-8", ErrPassphrase)
	}
	pw := norm.NFC.Bytes(passphrase)
	out := argon2.IDKey(pw, salt[:], p.Time, p.MemKiB, p.Threads, KeySize)
	k := [KeySize]byte(out)
	clear(out)
	return k, nil
}

// DeriveKeys derives the envelope keys:
//
//	PRK = HKDF-Extract(salt = "snap-e2e-v1" || S, IKM = K_link || K_pw)
//	K_x = HKDF-Expand(PRK, lp(x) || id, 32) for x in auth, enc, meta, commit
//
// A nil passphrase means none: IKM is K_link alone and p is ignored. A
// non-nil passphrase must be non-empty valid UTF-8 and p must pass Validate.
func DeriveKeys(key [KeySize]byte, id [IDSize]byte, salt [SaltSize]byte, passphrase []byte, p KDFParams) (Keys, error) {
	ikm := make([]byte, 0, 2*KeySize)
	ikm = append(ikm, key[:]...)
	defer clear(ikm)
	if passphrase != nil {
		kpw, err := PassphraseKey(passphrase, salt, p)
		if err != nil {
			return Keys{}, err
		}
		ikm = append(ikm, kpw[:]...)
		clear(kpw[:])
	}

	extractSalt := make([]byte, 0, len(Label)+SaltSize)
	extractSalt = append(extractSalt, Label...)
	extractSalt = append(extractSalt, salt[:]...)
	prk, err := hkdf.Extract(sha256.New, ikm, extractSalt)
	if err != nil {
		return Keys{}, fmt.Errorf("e2e: hkdf extract: %w", err)
	}
	defer clear(prk)

	var k Keys
	for _, out := range []struct {
		label string
		dst   *[KeySize]byte
	}{
		{labelAuth, &k.Auth},
		{labelEnc, &k.Enc},
		{labelMeta, &k.Meta},
		{labelCommit, &k.Commit},
	} {
		b, err := hkdf.Expand(sha256.New, prk, info(out.label, id), KeySize)
		if err != nil {
			return Keys{}, fmt.Errorf("e2e: hkdf expand: %w", err)
		}
		copy(out.dst[:], b)
		clear(b)
	}
	return k, nil
}
