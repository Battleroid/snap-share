// Package words provides the EFF large wordlist (7776 words, about 12.9 bits
// each) and a generator for word slugs and passphrases. See NOTICE for the
// list's licence.
package words

import (
	"bufio"
	"bytes"
	"crypto/rand"
	_ "embed"
	"errors"
	"math/big"
	"strings"
	"sync"
)

//go:embed eff_large_wordlist.txt
var raw []byte

// Size is the number of words in the list.
const Size = 7776

// list parses the embedded file once ("11111\tabacus" per line).
var list = sync.OnceValue(func() []string {
	out := make([]string, 0, Size)
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		_, w, ok := strings.Cut(sc.Text(), "\t")
		if ok && w != "" {
			out = append(out, w)
		}
	}
	return out
})

// List returns the words in list order. Callers must not modify it.
func List() []string { return list() }

// Generate returns n random words joined by sep, using crypto/rand.
func Generate(n int, sep string) (string, error) {
	if n < 1 {
		return "", errors.New("words: n must be at least 1")
	}
	l := list()
	max := big.NewInt(int64(len(l)))
	parts := make([]string, n)
	for i := range parts {
		j, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		parts[i] = l[j.Int64()]
	}
	return strings.Join(parts, sep), nil
}
