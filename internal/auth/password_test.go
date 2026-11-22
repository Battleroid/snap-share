package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestHashAndVerify(t *testing.T) {
	ctx := context.Background()
	h, err := HashPassword(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\$argon2id\$v=19\$m=19456,t=2,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`).MatchString(h) {
		t.Fatalf("not PHC argon2id with the expected parameters: %s", h)
	}
	h2, _ := HashPassword(ctx, "correct horse battery")
	if h == h2 {
		t.Fatal("salt is not random")
	}
	cases := []struct {
		pw   string
		want bool
	}{
		{"correct horse battery", true},
		{"correct horse batterY", false},
		{"", false},
		{strings.Repeat("x", MaxPasswordLen+1), false},
	}
	for _, c := range cases {
		ok, rehash, err := VerifyPassword(ctx, h, c.pw)
		if err != nil || ok != c.want || rehash {
			t.Errorf("VerifyPassword(%q) = %v, %v, %v", c.pw[:min(len(c.pw), 20)], ok, rehash, err)
		}
	}
	if NeedsRehash(h) {
		t.Error("fresh hash needs rehash")
	}
}

func TestVerifyOtherParams(t *testing.T) {
	// A valid hash made with weaker parameters verifies and asks for a rehash.
	ctx := context.Background()
	salt := []byte("somesaltsomesalt")
	key, err := derive(ctx, "older password", salt, argonParams{memory: 4096, time: 1, threads: 1}, 32)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawStdEncoding
	old := "$argon2id$v=19$m=4096,t=1,p=1$" + b64.EncodeToString(salt) + "$" + b64.EncodeToString(key)
	if ok, rehash, err := VerifyPassword(ctx, old, "older password"); err != nil || !ok || !rehash {
		t.Fatalf("right password: %v %v %v", ok, rehash, err)
	}
	if ok, rehash, err := VerifyPassword(ctx, old, "wrong password"); err != nil || ok || rehash {
		t.Fatalf("wrong password: %v %v %v", ok, rehash, err)
	}
	if !NeedsRehash(old) {
		t.Error("weaker params should need rehash")
	}
}

func TestMalformedHashes(t *testing.T) {
	for _, h := range []string{
		"",
		"plain",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=99999999,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=19456,t=99,p=1$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g",
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$aGFzaA",
	} {
		if _, _, err := VerifyPassword(context.Background(), h, "password123"); !errors.Is(err, ErrMalformedHash) {
			t.Errorf("%q: %v", h, err)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	cases := map[string]error{
		"short":                               ErrPasswordTooShort,
		"ten chars!":                          nil,
		"ééééééééé":                           ErrPasswordTooShort, // 9 characters, 18 bytes
		"éééééééééé":                          nil,
		strings.Repeat("a", MaxPasswordLen):   nil,
		strings.Repeat("a", MaxPasswordLen+1): ErrPasswordTooLong,
		"alllowercase-no-digits-is-fine":      nil,
	}
	for pw, want := range cases {
		if got := CheckPassword(pw); !errors.Is(got, want) {
			t.Errorf("CheckPassword(%.20q) = %v, want %v", pw, got, want)
		}
	}
}

func TestDummyTakesComparableTime(t *testing.T) {
	ctx := context.Background()
	h, _ := HashPassword(ctx, "some password here")
	measure := func(f func()) time.Duration {
		best := time.Hour
		for range 3 {
			start := time.Now()
			f()
			best = min(best, time.Since(start))
		}
		return best
	}
	verify := measure(func() { _, _, _ = VerifyPassword(ctx, h, "wrong password") })
	dummy := measure(func() { _ = VerifyDummy(ctx, "wrong password") })
	if dummy < verify/3 || dummy > verify*3 {
		t.Errorf("dummy %v vs verify %v", dummy, verify)
	}
}

func TestHashSemaphore(t *testing.T) {
	// Saturate the slots and check that a waiting hash honours cancellation.
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := HashPassword(ctx, "blocked password")
	for range cap(hashSlots) {
		<-hashSlots
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}
