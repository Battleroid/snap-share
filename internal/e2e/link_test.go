package e2e

import (
	"errors"
	"strings"
	"testing"
)

func TestRandomValues(t *testing.T) {
	k1, err := NewLinkKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := NewLinkKey()
	if err != nil {
		t.Fatal(err)
	}
	s1, err := NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	i1, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	i2, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 || s1 == s2 || i1 == i2 || k1 == ([32]byte{}) || s1 == ([16]byte{}) || i1 == ([16]byte{}) {
		t.Fatal("random values repeat or are zero")
	}
}

func TestEncodeDecode(t *testing.T) {
	id := tb16("enc id")
	key := tb32("enc key")
	if s := Encode(id[:]); len(s) != 22 || strings.ContainsAny(s, "+/=") {
		t.Fatalf("Encode(id) = %q", s)
	}
	if s := Encode(key[:]); len(s) != 43 {
		t.Fatalf("Encode(key) = %q", s)
	}
	gotID, err := Decode16(Encode(id[:]))
	if err != nil || gotID != id {
		t.Fatalf("Decode16 = %x, %v", gotID, err)
	}
	gotKey, err := Decode32(Encode(key[:]))
	if err != nil || gotKey != key {
		t.Fatalf("Decode32 = %x, %v", gotKey, err)
	}
	ks := Encode(key[:])
	for name, s := range map[string]string{
		"empty":         "",
		"short":         ks[:42],
		"long":          ks + "A",
		"padded":        ks[:42] + "=",
		"std chars":     "/" + ks[1:],
		"newline":       ks[:20] + "\n" + ks[21:],
		"non-canonical": ks[:42] + string(b64Alphabet[strings.IndexByte(b64Alphabet, ks[42])|1]),
	} {
		if _, err := Decode32(s); !errors.Is(err, ErrEncoding) {
			t.Errorf("%s: Decode32(%q) = %v", name, s, err)
		}
	}
	if _, err := Decode16(ks); !errors.Is(err, ErrEncoding) {
		t.Errorf("Decode16 of a 43-char value = %v", err)
	}
}

func TestLinkRoundTrip(t *testing.T) {
	id, key := tb16("link id"), tb32("link key")
	tests := []struct {
		in, base string
	}{
		{"https://snap.example", "https://snap.example"},
		{"https://snap.example/", "https://snap.example"},
		{"https://snap.example//", "https://snap.example"},
		{"http://10.0.0.2:8080/prefix", "http://10.0.0.2:8080/prefix"},
		{"", ""},
	}
	for _, tt := range tests {
		link := FormatLink(tt.in, id, key)
		if want := tt.base + "/s/" + Encode(id[:]) + "#" + Encode(key[:]); link != want {
			t.Errorf("FormatLink(%q) = %q, want %q", tt.in, link, want)
		}
		base, gotID, gotKey, err := ParseLink("  " + link + "\n")
		if err != nil || base != tt.base || gotID != id || gotKey != key {
			t.Errorf("ParseLink(%q) = %q, %x, %x, %v", link, base, gotID, gotKey, err)
		}
	}
}

func TestParseLinkErrorsHideKey(t *testing.T) {
	key := tb32("secret key")
	ks := Encode(key[:])
	_, _, _, err := ParseLink("https://h/s/short#" + ks)
	if !errors.Is(err, ErrLink) || strings.Contains(err.Error(), ks) {
		t.Fatalf("ParseLink error = %v", err)
	}
	_, _, _, err = ParseLink("https://h/s/" + Encode(make([]byte, 16)) + "#" + ks + "x")
	if !errors.Is(err, ErrLink) || strings.Contains(err.Error(), ks) {
		t.Fatalf("ParseLink error = %v", err)
	}
}
