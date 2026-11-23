package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The vendored Patchbay files are the kit's, byte for byte: every file under
// static/vendor/patchbay has a line in VENDOR.md with its sha256, and every
// line names a file that is there.
func TestVendoredPatchbayHashes(t *testing.T) {
	md, err := fs.ReadFile(staticFS, "static/vendor/VENDOR.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `(patchbay/[^`]+)` \\| `([0-9a-f]{64})` \\|$")
	want := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(md), -1) {
		want[m[1]] = m[2]
	}
	if len(want) == 0 {
		t.Fatal("VENDOR.md lists no patchbay files")
	}
	seen := map[string]bool{}
	err = fs.WalkDir(staticFS, "static/vendor/patchbay", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimPrefix(p, "static/vendor/")
		seen[name] = true
		b, err := fs.ReadFile(staticFS, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		switch h, ok := want[name]; {
		case !ok:
			t.Errorf("%s is not listed in VENDOR.md", name)
		case h != hex.EncodeToString(sum[:]):
			t.Errorf("%s: sha256 %x, VENDOR.md says %s", name, sum, h)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("VENDOR.md lists %s, which is not vendored", name)
		}
	}
}
