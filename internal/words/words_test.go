package words

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != "addd35536511597a02fa0a9ff1e5284677b8883b83e986e43f15a3db996b903e" {
		t.Fatalf("wordlist changed: %s", got)
	}
	l := List()
	if len(l) != Size {
		t.Fatalf("got %d words", len(l))
	}
	seen := map[string]bool{}
	for _, w := range l {
		if seen[w] {
			t.Errorf("duplicate %q", w)
		}
		seen[w] = true
	}
}

func TestGenerate(t *testing.T) {
	in := map[string]bool{}
	for _, w := range List() {
		in[w] = true
	}
	seen := map[string]bool{}
	for range 200 {
		s, err := Generate(3, "~")
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(s, "~")
		if len(parts) != 3 {
			t.Fatalf("%q", s)
		}
		for _, p := range parts {
			if !in[p] {
				t.Fatalf("%q not in list", p)
			}
		}
		seen[s] = true
	}
	if len(seen) < 195 {
		t.Errorf("only %d distinct of 200", len(seen))
	}
	if _, err := Generate(0, "-"); err == nil {
		t.Error("n=0 accepted")
	}
}
