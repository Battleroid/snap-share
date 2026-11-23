// Package obs sets up the process's log outputs (app log, audit stream) for
// log shippers such as Vector. Metrics live in internal/metrics.
package obs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// File is an append-only log file. Reopen closes it and opens the path
// again, so logrotate can move it away and send SIGHUP.
type File struct {
	mu   sync.Mutex
	dir  string // the directory opened as an os.Root
	name string // path under dir
	f    *os.File
}

// OpenFile opens path for appending, creating it with mode 0640. A relative
// path is taken under dataDir (and its directories are created); an
// absolute path's directory must exist. The file is opened through os.Root
// at that directory, so a symlink can't lead it elsewhere. Paths inside
// any of deny (the blob roots, which sweepers clean) are refused, as is
// anything that isn't a regular file.
func OpenFile(dataDir, path string, deny ...string) (*File, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return nil, errors.New("empty or invalid path")
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(dataDir, full)
	}
	full, err := filepath.Abs(full)
	if err != nil {
		return nil, err
	}
	for _, d := range deny {
		if d == "" {
			continue
		}
		ad, err := filepath.Abs(d)
		if err != nil {
			return nil, err
		}
		if within(full, ad) {
			return nil, fmt.Errorf("%s is inside %s, which holds uploaded content", path, d)
		}
	}
	lf := &File{dir: filepath.Dir(full), name: filepath.Base(full)}
	if !filepath.IsAbs(path) {
		// Under the data dir: open the root there and create the subdirectories.
		ad, err := filepath.Abs(dataDir)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(ad, full)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s leaves the data directory", path)
		}
		lf.dir, lf.name = ad, rel
		if err := os.MkdirAll(ad, 0o750); err != nil {
			return nil, err
		}
		if sub := filepath.Dir(rel); sub != "." {
			root, err := os.OpenRoot(ad)
			if err != nil {
				return nil, err
			}
			err = root.MkdirAll(sub, 0o750)
			_ = root.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	f, err := lf.open()
	if err != nil {
		return nil, err
	}
	lf.f = f
	return lf, nil
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (l *File) open() (*os.File, error) {
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(l.name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = fmt.Errorf("%s is not a regular file", filepath.Join(l.dir, l.name))
		}
		return nil, err
	}
	return f, nil
}

// Write appends p in one write.
func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return 0, os.ErrClosed
	}
	return l.f.Write(p)
}

// Reopen opens the path again and closes the old file. On error the old
// file stays in use.
func (l *File) Reopen() error {
	f, err := l.open()
	if err != nil {
		return err
	}
	l.mu.Lock()
	old := l.f
	l.f = f
	l.mu.Unlock()
	if old != nil {
		return old.Close()
	}
	return nil
}

// Close closes the file.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// lockedWriter serialises writes from several handlers to one stream, so
// lines from the app log and the audit stream never interleave.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
