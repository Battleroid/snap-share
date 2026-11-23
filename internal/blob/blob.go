// Package blob stores opaque content under random keys. The local filesystem
// implementation is added by the items package; other packages depend only on Store.
package blob

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned for a missing key.
var ErrNotFound = errors.New("blob not found")

// ErrInsufficientSpace is returned when writing would cross the free-space reserve.
var ErrInsufficientSpace = errors.New("not enough free disk space")

// Store is a content store rooted in one directory.
type Store interface {
	// Create starts writing a new blob. Nothing is visible under the key until
	// Commit; Abort (or a crash) leaves only a temp file for the reaper.
	Create(ctx context.Context) (Writer, error)
	// Open returns a reader for key that supports Seek (for Range requests).
	Open(ctx context.Context, key string) (File, error)
	// Delete removes key. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// Path returns the filesystem path for key, for tools (ffmpeg) that need one.
	Path(key string) (string, error)
}

// Writer is an in-progress blob.
type Writer interface {
	io.Writer
	// Commit makes the blob visible and returns its key.
	Commit() (key string, err error)
	// Abort discards the blob.
	Abort() error
}

// File is an open blob.
type File interface {
	io.ReadSeekCloser
	io.ReaderAt
	Size() int64
}
