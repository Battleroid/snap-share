package local

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Battleroid/snap-share/internal/blob"
)

// ReadOnly opens the blobs of a store's directory for reading. Unlike New it
// creates nothing, so it works on a read-only mount (the media worker's
// files directory).
type ReadOnly struct {
	dir  string
	root *os.Root
}

// OpenReadOnly opens dir, which must exist, for reading blobs.
func OpenReadOnly(dir string) (*ReadOnly, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("blob store %s: %w", dir, err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("blob store %s: %w", dir, err)
	}
	return &ReadOnly{dir: abs, root: root}, nil
}

// Close releases the root directory handle.
func (s *ReadOnly) Close() error { return s.root.Close() }

// Dir returns the absolute, clean directory.
func (s *ReadOnly) Dir() string { return s.dir }

// check resolves key through the root and requires each component to be
// what the store writes: the two shard levels directories and the blob a
// regular file. A symlink anywhere is refused, so a path handed to another
// program (ffmpeg) names the file that was checked.
func (s *ReadOnly) check(key string) (string, error) {
	p, err := keyPath(key)
	if err != nil {
		return "", blob.ErrNotFound
	}
	for _, c := range []struct {
		rel string
		dir bool
	}{{key[:2], true}, {filepath.Join(key[:2], key[2:4]), true}, {p, false}} {
		st, err := s.root.Lstat(c.rel)
		if errors.Is(err, fs.ErrNotExist) {
			return "", blob.ErrNotFound
		}
		if err != nil {
			return "", fmt.Errorf("open blob: %w", err)
		}
		if (c.dir && !st.IsDir()) || (!c.dir && !st.Mode().IsRegular()) {
			return "", blob.ErrNotFound
		}
	}
	return p, nil
}

// Path returns the absolute path of key after checking, through the root,
// that it is a regular file and no component is a symlink
// (blob.ErrNotFound otherwise).
func (s *ReadOnly) Path(key string) (string, error) {
	p, err := s.check(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir, p), nil
}

// Open returns a reader for key, with the same checks as Path.
func (s *ReadOnly) Open(ctx context.Context, key string) (blob.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := s.check(key)
	if err != nil {
		return nil, err
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
