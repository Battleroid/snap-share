package local

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/blob"
)

func TestReadOnly(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, Options{})
	w, err := s.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "content")
	key, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	// A directory the process cannot write: nothing is created.
	if err := os.RemoveAll(filepath.Join(s.Dir(), tmpDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Dir(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.Dir(), 0o700) })

	ro, err := OpenReadOnly(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if _, err := os.Stat(filepath.Join(s.Dir(), tmpDir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("OpenReadOnly created %s: %v", tmpDir, err)
	}
	p, err := ro.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := s.Path(key); p != want || !filepath.IsAbs(p) || filepath.Clean(p) != p {
		t.Errorf("Path = %q, want %q", p, want)
	}
	f, err := ro.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	_ = f.Close()
	if string(b) != "content" || f.Size() != 7 {
		t.Errorf("read %q, size %d", b, f.Size())
	}
}

func TestReadOnlyRefuses(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	mk := func(rel string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const (
		linkKey   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		inLinkKey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		dirLink   = "cccccccccccccccccccccccccccccccc"
		dirKey    = "dddddddddddddddddddddddddddddddd"
		fifoKey   = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		goodKey   = "ffffffffffffffffffffffffffffffff"
	)
	// A symlink out of the root, and one to a regular file inside it.
	if err := os.Symlink(outside, mk("aa/aa/"+linkKey)); err != nil {
		t.Fatal(err)
	}
	good := mk("ff/ff/" + goodKey)
	if err := os.WriteFile(good, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(good, mk("bb/bb/"+inLinkKey)); err != nil {
		t.Fatal(err)
	}
	// A shard directory that is a symlink to a directory holding the key.
	if err := os.MkdirAll(filepath.Join(dir, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real", dirLink), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "cc", "cc")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mk("dd/dd/"+dirKey), 0o700); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if _, err := ro.Path(goodKey); err != nil {
		t.Fatalf("control: %v", err)
	}
	for _, key := range []string{linkKey, inLinkKey, dirLink, dirKey, fifoKey,
		"../../../etc/passwd", strings.Repeat("A", 32), "aa", ""} {
		if p, err := ro.Path(key); !errors.Is(err, blob.ErrNotFound) {
			t.Errorf("Path(%q) = %q, %v; want ErrNotFound", key, p, err)
		}
		if f, err := ro.Open(ctx, key); !errors.Is(err, blob.ErrNotFound) {
			if f != nil {
				_ = f.Close()
			}
			t.Errorf("Open(%q): %v; want ErrNotFound", key, err)
		}
	}
}
