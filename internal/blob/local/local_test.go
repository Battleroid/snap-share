package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
)

func newStore(t *testing.T, o Options) *Store {
	t.Helper()
	s, err := New(t.TempDir(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func tmpEntries(t *testing.T, s *Store) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(s.Dir(), tmpDir))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestCommitIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, Options{})
	w, err := s.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "hello "); err != nil {
		t.Fatal(err)
	}
	// Nothing outside .tmp exists before Commit.
	var files []string
	_ = filepath.WalkDir(s.Dir(), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	if len(files) != 1 || !strings.Contains(files[0], string(filepath.Separator)+tmpDir+string(filepath.Separator)) {
		t.Fatalf("before commit: %v", files)
	}
	_, _ = io.WriteString(w, "world")
	key, err := w.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidKey(key) {
		t.Fatalf("key %q", key)
	}
	if n := tmpEntries(t, s); len(n) != 0 {
		t.Errorf("temp left behind: %v", n)
	}
	p, _ := s.Path(key)
	if want := filepath.Join(s.Dir(), key[:2], key[2:4], key); p != want {
		t.Errorf("path %s want %s", p, want)
	}
	f, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, _ := io.ReadAll(f)
	if string(b) != "hello world" || f.Size() != 11 {
		t.Errorf("read %q size %d", b, f.Size())
	}
	buf := make([]byte, 5)
	if _, err := f.ReadAt(buf, 6); err != nil || string(buf) != "world" {
		t.Errorf("ReadAt %q %v", buf, err)
	}
	if _, err := w.Commit(); err == nil {
		t.Error("second commit accepted")
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, key); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
}

func TestAbort(t *testing.T) {
	s := newStore(t, Options{})
	w, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "discard me")
	if err := w.Abort(); err != nil {
		t.Fatal(err)
	}
	if n := tmpEntries(t, s); len(n) != 0 {
		t.Errorf("abort left %v", n)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Error("write after abort")
	}
}

func TestKeysCannotTraverse(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, Options{})
	// A file outside the store that a traversal would reach.
	outside := filepath.Join(filepath.Dir(s.Dir()), "outside")
	_ = os.WriteFile(outside, []byte("secret"), 0o600)
	bad := []string{
		"", "../outside", "../../etc/passwd", "ab/cd/../../../outside", "/etc/passwd",
		"0123456789abcdef0123456789ABCDEF", "0123456789abcdef0123456789abcde", "0123456789abcdef0123456789abcdef0",
		"..23456789abcdef0123456789abcdef", "0123456789abcdef/123456789abcdef", "0123456789abcdef\x00123456789abcdef",
	}
	for _, k := range bad {
		if _, err := s.Path(k); err == nil {
			t.Errorf("Path(%q) accepted", k)
		}
		if _, err := s.Open(ctx, k); !errors.Is(err, blob.ErrNotFound) {
			t.Errorf("Open(%q): %v", k, err)
		}
		if err := s.Delete(ctx, k); err == nil {
			t.Errorf("Delete(%q) accepted", k)
		}
		if _, err := s.PartialSize(k); err == nil {
			t.Errorf("PartialSize(%q) accepted", k)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "secret" {
		t.Error("outside file touched")
	}
	// A symlink planted inside the store cannot lead out of it either.
	key := "aabbccddeeff00112233445566778899"
	_ = os.MkdirAll(filepath.Join(s.Dir(), "aa", "bb"), 0o750)
	if err := os.Symlink(outside, filepath.Join(s.Dir(), "aa", "bb", key)); err != nil {
		t.Fatal(err)
	}
	if f, err := s.Open(ctx, key); err == nil {
		_ = f.Close()
		t.Error("symlink out of the root was followed")
	}
}

func TestFreeSpaceGuard(t *testing.T) {
	ctx := context.Background()
	var avail atomic.Int64
	avail.Store(10 << 20)
	s := newStore(t, Options{
		MinFree: func() int64 { return 4 << 20 },
		Statfs:  func(string) (uint64, error) { return uint64(avail.Load()), nil },
	})
	if err := s.CheckFree(6 << 20); err != nil {
		t.Errorf("6 MiB with 10 free and 4 reserved: %v", err)
	}
	if err := s.CheckFree(6<<20 + 1); !errors.Is(err, blob.ErrInsufficientSpace) {
		t.Errorf("over reserve: %v", err)
	}
	w, err := s.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for i := range 6 {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		avail.Add(-1 << 20) // the disk fills as we write
	}
	if _, err := w.Write(chunk); !errors.Is(err, blob.ErrInsufficientSpace) {
		t.Fatalf("write past reserve: %v", err)
	}
	_ = w.Abort()

	avail.Store(3 << 20)
	if _, err := s.Create(ctx); !errors.Is(err, blob.ErrInsufficientSpace) {
		t.Errorf("create below reserve: %v", err)
	}
	name := ""
	avail.Store(5 << 20)
	if name, err = s.CreatePartial(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendPartial(ctx, name, 0, bytes.NewReader(make([]byte, 2<<20))); !errors.Is(err, blob.ErrInsufficientSpace) {
		t.Errorf("partial past reserve: %v", err)
	}
}

func TestPartials(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, Options{})
	name, err := s.CreatePartial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.AppendPartial(ctx, name, 0, strings.NewReader("abc")); err != nil || n != 3 {
		t.Fatalf("append: %d %v", n, err)
	}
	var oe *OffsetError
	if _, err := s.AppendPartial(ctx, name, 1, strings.NewReader("x")); !errors.As(err, &oe) || oe.Current != 3 {
		t.Fatalf("wrong offset: %v", err)
	}
	if _, err := s.AppendPartial(ctx, name, 3, strings.NewReader("def")); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.PartialSize(name); n != 6 {
		t.Fatalf("size %d", n)
	}
	r, err := s.OpenPartial(name)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "abcdef" {
		t.Fatalf("%q", b)
	}
	key, err := s.CommitPartial(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PartialSize(name); !errors.Is(err, blob.ErrNotFound) {
		t.Error("partial still present after commit")
	}
	f, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(f)
	_ = f.Close()
	if string(b) != "abcdef" {
		t.Fatalf("%q", b)
	}
	n2, _ := s.CreatePartial(ctx)
	if err := s.RemovePartial(n2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendPartial(ctx, n2, 0, strings.NewReader("x")); !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("append to removed partial: %v", err)
	}
}

func TestReap(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := newStore(t, Options{Now: func() time.Time { return now }})
	w, _ := s.Create(ctx)
	_, _ = io.WriteString(w, "abandoned")
	old, _ := s.CreatePartial(ctx)
	fresh, _ := s.CreatePartial(ctx)
	ents := tmpEntries(t, s)
	if len(ents) != 3 {
		t.Fatalf("%v", ents)
	}
	past := now.Add(-25 * time.Hour)
	for _, e := range ents {
		if !strings.Contains(e, fresh) {
			_ = os.Chtimes(filepath.Join(s.Dir(), tmpDir, e), past, past)
		}
	}
	if err := s.Reap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PartialSize(old); !errors.Is(err, blob.ErrNotFound) {
		t.Error("old partial kept")
	}
	if _, err := s.PartialSize(fresh); err != nil {
		t.Error("fresh partial reaped")
	}
	if n := len(tmpEntries(t, s)); n != 1 {
		t.Errorf("%d temp files left", n)
	}
}
