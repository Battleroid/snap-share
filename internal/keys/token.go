package keys

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"hash/crc32"
	"strings"
)

// Token layout: snap_<public id>_<secret>_<checksum>, every part base62 at a
// fixed length, so it is parsed by position. The checksum is CRC-32 (IEEE) of
// everything before it, which lets secret scanners and the server reject a
// mistyped or truncated key without touching the database.
const (
	tokenPrefix  = "snap_"
	publicIDLen  = 12
	secretLen    = 43 // 43 base62 characters carry 256.03 bits
	checksumLen  = 6  // 62^6 > 2^32
	tokenLen     = len(tokenPrefix) + publicIDLen + 1 + secretLen + 1 + checksumLen
	secretStart  = len(tokenPrefix) + publicIDLen + 1
	checksumFrom = secretStart + secretLen + 1
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// TokenLen is the length of every key.
const TokenLen = tokenLen

// randomBase62 returns n uniformly random base62 characters.
func randomBase62(n int) string {
	out := make([]byte, 0, n)
	var buf [64]byte
	for len(out) < n {
		_, _ = rand.Read(buf[:]) // never fails (crypto/rand panics on failure)
		for _, b := range buf {
			if b >= 248 { // 248 = 4*62: reject to keep the distribution uniform
				continue
			}
			out = append(out, base62[b%62])
			if len(out) == n {
				break
			}
		}
	}
	return string(out)
}

func checksum(s string) string {
	v := crc32.ChecksumIEEE([]byte(s))
	var out [checksumLen]byte
	for i := checksumLen - 1; i >= 0; i-- {
		out[i] = base62[v%62]
		v /= 62
	}
	return string(out[:])
}

// newToken returns a fresh key, its public id and the SHA-256 of its secret.
func newToken() (token, publicID string, secretHash []byte) {
	publicID = randomBase62(publicIDLen)
	secret := randomBase62(secretLen)
	body := tokenPrefix + publicID + "_" + secret
	h := sha256.Sum256([]byte(secret))
	return body + "_" + checksum(body), publicID, h[:]
}

func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

// ParseToken splits a key into its public id and secret. It checks the shape
// and the checksum only; ok is false for anything that is not a well-formed key.
func ParseToken(token string) (publicID, secret string, ok bool) {
	if len(token) != tokenLen || !strings.HasPrefix(token, tokenPrefix) ||
		token[secretStart-1] != '_' || token[checksumFrom-1] != '_' {
		return "", "", false
	}
	publicID = token[len(tokenPrefix) : secretStart-1]
	secret = token[secretStart : checksumFrom-1]
	sum := token[checksumFrom:]
	if !isBase62(publicID) || !isBase62(secret) || !isBase62(sum) {
		return "", "", false
	}
	if subtle.ConstantTimeCompare([]byte(sum), []byte(checksum(token[:checksumFrom-1]))) != 1 {
		return "", "", false
	}
	return publicID, secret, true
}

// ValidPublicID reports whether s has the shape of a public id.
func ValidPublicID(s string) bool { return len(s) == publicIDLen && isBase62(s) }

// matchSecret reports, in constant time, whether secret hashes to want.
func matchSecret(want []byte, secret string) bool {
	got := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

// dummyHash is compared against when no key has the public id, so an unknown
// id costs the same work as a wrong secret.
var dummyHash = sha256.Sum256([]byte("snap unknown key"))
