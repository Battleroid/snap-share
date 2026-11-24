// Package local implements blob.Store on the local filesystem.
//
// Blobs live under random 128-bit hex keys sharded as ab/cd/<key>. Writes go
// to <root>/.tmp first and are moved into place on Commit after an fsync of
// the file and of the directories whose entries changed. Every path is
// resolved through an os.Root, so no key or name can reach outside the root.
//
// A free-space guard refuses writes that would leave less than MinFree bytes
// on the filesystem (blob.ErrInsufficientSpace).
package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/settings"
)

// MinFreeBytes is the free-space reserve shared by every local store.
var MinFreeBytes = settings.Define("storage.min_free_bytes", int64(1<<30), settings.Range[int64](0, 1<<50))

// TempMaxAge is how long a temp file may sit untouched before Reap removes it.
const TempMaxAge = 24 * time.Hour

const (
	tmpDir        = ".tmp"
	partialPrefix = "up-"
	writeSuffix   = ".part"
	// recheck bounds how many bytes may be written between free-space checks,
	// so concurrent writers notice a filling disk.
	recheck = 64 << 20
)

// OffsetError is returned by AppendPartial when the partial is not at the
// offset the caller expected.
type OffsetError struct{ Current int64 }

func (e *OffsetError) Error() string {
	return fmt.Sprintf("partial is at offset %d", e.Current)
}

// Options configures a Store.
type Options struct {
	// MinFree returns the reserve in bytes; nil or <= 0 disables the guard.
	MinFree func() int64
	// Statfs returns the bytes available to unprivileged users on the
	// filesystem holding dir. Nil uses the operating system.
	Statfs func(dir string) (uint64, error)
	// Now is the clock for Reap (nil: time.Now).
	Now func() time.Time
}

// Store is a blob.Store rooted in one directory.
type Store struct {
	dir     string
	root    *os.Root
	minFree func() int64
	statfs  func(string) (uint64, error)
	now     func() time.Time

	closeOnce sync.Once
}

var _ blob.Store = (*Store)(nil)

// New opens (creating if needed) a store in dir.
func New(dir string, o Options) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, tmpDir), 0o750); err != nil {
		return nil, fmt.Errorf("blob store %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("blob store %s: %w", dir, err)
	}
	s := &Store{dir: dir, root: root, minFree: o.MinFree, statfs: o.Statfs, now: o.Now}
	if s.statfs == nil {
		s.statfs = statfs
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Close releases the root directory handle.
func (s *Store) Close() error {
	var err error
	s.closeOnce.Do(func() { err = s.root.Close() })
	return err
}

// Dir returns the store's directory.
func (s *Store) Dir() string { return s.dir }

// ValidKey reports whether k has the key format (32 lowercase hex digits).
func ValidKey(k string) bool {
	if len(k) != 32 {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func keyPath(key string) (string, error) {
	if !ValidKey(key) {
		return "", fmt.Errorf("invalid blob key %q", key)
	}
	return filepath.Join(key[:2], key[2:4], key), nil
}

func randomHex() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (crypto/rand panics on failure)
	return hex.EncodeToString(b[:])
}

// CheckFree returns blob.ErrInsufficientSpace when writing n more bytes would
// cross the reserve.
func (s *Store) CheckFree(n int64) error {
	reserve := s.reserve()
	if reserve <= 0 {
		return nil
	}
	avail, err := s.available()
	if err != nil {
		return err
	}
	if avail-reserve < n {
		return blob.ErrInsufficientSpace
	}
	return nil
}

func (s *Store) reserve() int64 {
	if s.minFree == nil {
		return 0
	}
	return s.minFree()
}

func (s *Store) available() (int64, error) {
	a, err := s.statfs(s.dir)
	if err != nil {
		return 0, fmt.Errorf("statfs %s: %w", s.dir, err)
	}
	if a > 1<<62 {
		return 1 << 62, nil
	}
	return int64(a), nil // #nosec G115 -- bounded above
}

// allow charges n bytes against budget, refreshing it from statfs when it runs out.
func (s *Store) allow(budget *int64, n int64) error {
	reserve := s.reserve()
	if reserve <= 0 {
		return nil
	}
	if *budget >= n {
		*budget -= n
		return nil
	}
	avail, err := s.available()
	if err != nil {
		return err
	}
	left := avail - reserve
	if left < n {
		return blob.ErrInsufficientSpace
	}
	*budget = min(left-n, recheck)
	return nil
}

// Create starts a new blob in the temp directory.
func (s *Store) Create(ctx context.Context) (blob.Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.CheckFree(0); err != nil {
		return nil, err
	}
	tmp := filepath.Join(tmpDir, randomHex()+writeSuffix)
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return nil, fmt.Errorf("create blob: %w", err)
	}
	return &writer{s: s, f: f, tmp: tmp}, nil
}

type writer struct {
	s      *Store
	f      *os.File
	tmp    string
	budget int64
	done   bool
}

var errFinished = errors.New("blob writer already committed or aborted")

func (w *writer) Write(p []byte) (int, error) {
	if w.done {
		return 0, errFinished
	}
	if err := w.s.allow(&w.budget, int64(len(p))); err != nil {
		return 0, err
	}
	return w.f.Write(p)
}

func (w *writer) Commit() (string, error) {
	if w.done {
		return "", errFinished
	}
	w.done = true
	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		_ = w.s.root.Remove(w.tmp)
		return "", fmt.Errorf("sync blob: %w", err)
	}
	if err := w.f.Close(); err != nil {
		_ = w.s.root.Remove(w.tmp)
		return "", fmt.Errorf("close blob: %w", err)
	}
	key, err := w.s.place(w.tmp)
	if err != nil {
		_ = w.s.root.Remove(w.tmp)
		return "", err
	}
	return key, nil
}

func (w *writer) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	_ = w.f.Close()
	if err := w.s.root.Remove(w.tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// place moves a synced temp file to a new key and makes the rename durable.
func (s *Store) place(tmp string) (string, error) {
	for range 3 {
		key := randomHex()
		dir := filepath.Join(key[:2], key[2:4])
		_, statErr := s.root.Stat(dir)
		created := errors.Is(statErr, fs.ErrNotExist)
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return "", fmt.Errorf("blob dir: %w", err)
		}
		final := filepath.Join(dir, key)
		if _, err := s.root.Lstat(final); err == nil {
			continue // 128-bit collision; try another key
		}
		if err := s.root.Rename(tmp, final); err != nil {
			return "", fmt.Errorf("place blob: %w", err)
		}
		dirs := []string{dir}
		if created {
			dirs = append(dirs, key[:2], ".")
		}
		for _, d := range dirs {
			if err := s.syncDir(d); err != nil {
				return "", err
			}
		}
		return key, nil
	}
	return "", errors.New("place blob: no free key")
}

func (s *Store) syncDir(d string) error {
	f, err := s.root.Open(d)
	if err != nil {
		return fmt.Errorf("sync dir: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync dir: %w", err)
	}
	return nil
}

type file struct {
	*os.File
	size int64
}

func (f *file) Size() int64 { return f.size }

// Open returns a reader for key.
func (s *Store) Open(ctx context.Context, key string) (blob.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := keyPath(key)
	if err != nil {
		return nil, blob.ErrNotFound
	}
	f, err := s.root.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, blob.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open blob: %w", err)
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, blob.ErrNotFound
	}
	return &file{File: f, size: st.Size()}, nil
}

// Delete removes key; a missing key is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := keyPath(key)
	if err != nil {
		return err
	}
	if err := s.root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete blob: %w", err)
	}
	return nil
}

// Path returns the absolute filesystem path of key after validating its format.
func (s *Store) Path(key string) (string, error) {
	p, err := keyPath(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir, p), nil
}

// Partials are resumable temp files for chunked uploads. They are named by a
// random hex id, appended at an expected offset, and committed into a key.

func partialPath(name string) (string, error) {
	if !ValidKey(name) {
		return "", fmt.Errorf("invalid partial name %q", name)
	}
	return filepath.Join(tmpDir, partialPrefix+name), nil
}

// CreatePartial creates an empty partial and returns its name.
func (s *Store) CreatePartial(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name := randomHex()
	p, _ := partialPath(name)
	f, err := s.root.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", fmt.Errorf("create partial: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return name, nil
}

// PartialSize returns the bytes received so far (blob.ErrNotFound if gone).
func (s *Store) PartialSize(name string) (int64, error) {
	p, err := partialPath(name)
	if err != nil {
		return 0, blob.ErrNotFound
	}
	st, err := s.root.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, blob.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// AppendPartial copies r to the end of the partial, which must currently be
// offset bytes long (else *OffsetError). Bytes written before a read or write
// error are kept; the returned count says how many.
//
// A part is not fsynced: CommitPartial syncs the whole file once, and a resume
// asks PartialSize for the offset, so a crash that loses unsynced bytes only
// moves the resume point back. Syncing every part made each one wait on the
// disk (an 8 MiB fsync costs ~400 ms on a raidz of HDDs without a SLOG), which
// capped browser uploads near 15 MB/s on any network.
func (s *Store) AppendPartial(ctx context.Context, name string, offset int64, r io.Reader) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	p, err := partialPath(name)
	if err != nil {
		return 0, blob.ErrNotFound
	}
	f, err := s.root.OpenFile(p, os.O_WRONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, blob.ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("open partial: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if st.Size() != offset {
		return 0, &OffsetError{Current: st.Size()}
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	bw := &budgetWriter{s: s, w: f}
	return io.CopyBuffer(bw, r, make([]byte, 256<<10))
}

type budgetWriter struct {
	s      *Store
	w      io.Writer
	budget int64
}

func (b *budgetWriter) Write(p []byte) (int, error) {
	if err := b.s.allow(&b.budget, int64(len(p))); err != nil {
		return 0, err
	}
	return b.w.Write(p)
}

// OpenPartial opens a partial for reading (hashing, sniffing).
func (s *Store) OpenPartial(name string) (io.ReadSeekCloser, error) {
	p, err := partialPath(name)
	if err != nil {
		return nil, blob.ErrNotFound
	}
	f, err := s.root.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, blob.ErrNotFound
	}
	return f, err
}

// CommitPartial moves a finished partial to a new key.
func (s *Store) CommitPartial(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p, err := partialPath(name)
	if err != nil {
		return "", blob.ErrNotFound
	}
	f, err := s.root.OpenFile(p, os.O_WRONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return "", blob.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	serr := f.Sync()
	_ = f.Close()
	if serr != nil {
		return "", fmt.Errorf("sync partial: %w", serr)
	}
	return s.place(p)
}

// RemovePartial deletes a partial; a missing one is not an error.
func (s *Store) RemovePartial(name string) error {
	p, err := partialPath(name)
	if err != nil {
		return err
	}
	if err := s.root.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Reap removes temp files (abandoned writes and partials) untouched for
// TempMaxAge. Run it periodically (jobs.Every).
func (s *Store) Reap(ctx context.Context) error {
	d, err := s.root.Open(tmpDir)
	if err != nil {
		return fmt.Errorf("reap: %w", err)
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return fmt.Errorf("reap: %w", err)
	}
	cutoff := s.now().Add(-TempMaxAge)
	var errs []error
	for _, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := s.root.Remove(filepath.Join(tmpDir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
