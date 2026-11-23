package accounts

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1, secret "12345678901234567890". The RFC lists
// 8-digit codes; a 6-digit code is their last six digits.
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, tc := range []struct {
		unix int64
		code string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		if got := totpCode(secret, totpStep(time.Unix(tc.unix, 0))); got != tc.code[2:] {
			t.Errorf("T=%d: %s, want %s", tc.unix, got, tc.code[2:])
		}
	}
}

func TestTOTPMatch(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	cur := totpStep(now)
	for _, tc := range []struct {
		name string
		code string
		step int64
		ok   bool
	}{
		{"current", totpCode(secret, cur), cur, true},
		{"one step early", totpCode(secret, cur-1), cur - 1, true},
		{"one step late", totpCode(secret, cur+1), cur + 1, true},
		{"two steps early", totpCode(secret, cur-2), 0, false},
		{"two steps late", totpCode(secret, cur+2), 0, false},
		{"spaces", " " + totpCode(secret, cur)[:3] + " " + totpCode(secret, cur)[3:] + " ", cur, true},
		{"too short", totpCode(secret, cur)[:5], 0, false},
		{"letters", "12a456", 0, false},
		{"empty", "", 0, false},
	} {
		step, ok := totpMatch(secret, tc.code, now)
		if ok != tc.ok || step != tc.step {
			t.Errorf("%s: step %d ok %v, want %d %v", tc.name, step, ok, tc.step, tc.ok)
		}
	}
	if _, ok := totpMatch([]byte("another secret, 20 b"), totpCode(secret, cur), now); ok {
		t.Error("a code from another secret matched")
	}
}

func TestTOTPURI(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri := totpURI("My share & co", "ann.b", secret)
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/My share & co:ann.b" {
		t.Errorf("label: %s", uri)
	}
	if strings.Contains(uri, "+") {
		t.Errorf("spaces must be %%20: %s", uri)
	}
	q := u.Query()
	if q.Get("secret") != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" || q.Get("issuer") != "My share & co" ||
		q.Get("algorithm") != "SHA1" || q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Errorf("query: %s", uri)
	}
}
