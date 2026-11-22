package keys

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestNewTokenParses(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		tok, pub, hash := newToken()
		if len(tok) != 68 {
			t.Fatalf("length %d", len(tok))
		}
		if !strings.HasPrefix(tok, "snap_"+pub+"_") || tok[17] != '_' || tok[61] != '_' {
			t.Fatalf("layout: %s", tok)
		}
		gotPub, secret, ok := ParseToken(tok)
		if !ok || gotPub != pub {
			t.Fatalf("parse %s: %q %v", tok, gotPub, ok)
		}
		if len(secret) != secretLen || !matchSecret(hash, secret) {
			t.Fatalf("secret does not match its hash")
		}
		if sum := sha256.Sum256([]byte(secret)); string(sum[:]) != string(hash) {
			t.Fatal("stored hash is not SHA-256 of the secret")
		}
		if seen[pub] || seen[secret] {
			t.Fatal("repeated random value")
		}
		seen[pub], seen[secret] = true, true
	}
}

func TestRandomBase62(t *testing.T) {
	counts := map[byte]int{}
	s := randomBase62(62 * 200)
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(base62, rune(s[i])) {
			t.Fatalf("character %q outside the alphabet", s[i])
		}
		counts[s[i]]++
	}
	if len(counts) != 62 {
		t.Fatalf("only %d distinct characters", len(counts))
	}
	for c, n := range counts {
		if n < 100 || n > 330 { // expected 200 each; wildly off means a biased mapping
			t.Errorf("%q appeared %d times", c, n)
		}
	}
}

// withSum rebuilds the checksum, so a test can exercise the checks after it.
func withSum(body string) string { return body + "_" + checksum(body) }

func TestParseTokenRejects(t *testing.T) {
	good, pub, _ := newToken()
	secret := good[secretStart : checksumFrom-1]
	body := good[:checksumFrom-1]
	cases := map[string]string{
		"empty":             "",
		"garbage":           "hello",
		"bearer word":       "Bearer " + good,
		"prefix only":       "snap_",
		"truncated":         good[:len(good)-1],
		"extra char":        good + "0",
		"leading space":     " " + good[1:],
		"uppercase prefix":  "SNAP_" + good[5:],
		"other prefix":      "snip_" + good[5:],
		"dash separators":   strings.ReplaceAll(good, "_", "-"),
		"moved separator":   withSum("snap_" + pub[:11] + "_" + pub[11:] + secret),
		"short secret":      withSum("snap_" + pub + "_" + secret[:42]),
		"bad checksum char": body + "_" + "00000-",
		"wrong checksum":    body + "_" + checksum(body+"x"),
		"non-base62 secret": withSum("snap_" + pub + "_" + secret[:42] + "-"),
		"non-base62 id":     withSum("snap_" + pub[:11] + "+" + "_" + secret),
		"unicode":           withSum("snap_" + pub[:10] + "é" + "_" + secret),
	}
	for name, tok := range cases {
		if p, s, ok := ParseToken(tok); ok {
			t.Errorf("%s: %q parsed as (%q, %q)", name, tok, p, s)
		}
	}
	// Changing any single character outside the separators breaks the checksum.
	for i := len(tokenPrefix); i < len(good); i++ {
		if good[i] == '_' {
			continue
		}
		c := byte('A')
		if good[i] == 'A' {
			c = 'B'
		}
		tok := good[:i] + string(c) + good[i+1:]
		if _, _, ok := ParseToken(tok); ok {
			t.Errorf("position %d: altered key still parses", i)
		}
	}
}

func TestChecksumFixedWidth(t *testing.T) {
	for _, s := range []string{"", "a", "snap_x", strings.Repeat("z", 100)} {
		if c := checksum(s); len(c) != checksumLen || !isBase62(c) {
			t.Errorf("checksum(%q) = %q", s, c)
		}
	}
	if checksum("a") == checksum("b") {
		t.Error("checksum ignores input")
	}
}

func TestMatchSecret(t *testing.T) {
	_, _, hash := newToken()
	if matchSecret(hash, "nope") || matchSecret(dummyHash[:], "nope") || matchSecret(nil, "") {
		t.Error("matched a wrong secret")
	}
	if !matchSecret(dummyHash[:], "snap unknown key") {
		t.Error("dummy hash is not SHA-256 of its input")
	}
}
