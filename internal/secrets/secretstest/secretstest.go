// Package secretstest provides test helpers for the secrets service: a
// minimal blob store in a temporary directory, and a test server wired with
// it. It stands in for the items track's local store in tests.
package secretstest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// Store is a blob.Store over one directory. Keys are 32 hex characters.
type Store struct {
	root *os.Root
	dir  string
}

var _ blob.Store = (*Store)(nil)

var keyRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NewStore returns a store in dir (created if needed).
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o750); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Store{root: root, dir: dir}, nil
}

// Create implements blob.Store.
func (s *Store) Create(context.Context) (blob.Writer, error) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	key := hex.EncodeToString(b[:])
	f, err := s.root.OpenFile(filepath.Join("tmp", key), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &writer{s: s, f: f, key: key}, nil
}

type writer struct {
	s    *Store
	f    *os.File
	key  string
	done bool
}

func (w *writer) Write(p []byte) (int, error) { return w.f.Write(p) }

func (w *writer) Commit() (string, error) {
	if w.done {
		return "", errors.New("secretstest: writer already closed")
	}
	w.done = true
	if err := w.f.Close(); err != nil {
		return "", err
	}
	if err := w.s.root.Rename(filepath.Join("tmp", w.key), w.key); err != nil {
		return "", err
	}
	return w.key, nil
}

func (w *writer) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	_ = w.f.Close()
	return w.s.root.Remove(filepath.Join("tmp", w.key))
}

type file struct {
	*os.File
	size int64
}

func (f file) Size() int64 { return f.size }

// Open implements blob.Store.
func (s *Store) Open(_ context.Context, key string) (blob.File, error) {
	if !keyRE.MatchString(key) {
		return nil, blob.ErrNotFound
	}
	f, err := s.root.Open(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, blob.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return file{File: f, size: st.Size()}, nil
}

// Delete implements blob.Store.
func (s *Store) Delete(_ context.Context, key string) error {
	if !keyRE.MatchString(key) {
		return nil
	}
	err := s.root.Remove(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Path implements blob.Store.
func (s *Store) Path(key string) (string, error) {
	if !keyRE.MatchString(key) {
		return "", fmt.Errorf("secretstest: bad key")
	}
	return filepath.Join(s.dir, key), nil
}

// Keys lists the committed blobs, sorted.
func (s *Store) Keys() []string {
	entries, _ := os.ReadDir(s.dir)
	var out []string
	for _, e := range entries {
		if !e.IsDir() && keyRE.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Server starts a test server whose secrets live in a Store under a temp dir.
func Server(t testing.TB, opts ...testutil.Option) (*testutil.Server, *Store) {
	t.Helper()
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.root.Close() })
	opts = append(opts, testutil.AppOptions(func(o *app.Options) { o.SecretsBlob = st }))
	return testutil.NewServer(t, opts...), st
}
