package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

// Assets serves embedded static files with content-hash cache busting.
type Assets struct {
	fs     fs.FS
	hashes map[string]string
}

// NewAssets hashes every embedded static file.
func NewAssets() (*Assets, error) {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	a := &Assets{fs: sub, hashes: map[string]string{}}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		a.hashes[p] = hex.EncodeToString(sum[:6])
		return nil
	})
	return a, err
}

// URL returns the versioned URL for a static path like "js/app.js".
func (a *Assets) URL(p string) (string, error) {
	h, ok := a.hashes[p]
	if !ok {
		return "", fmt.Errorf("unknown asset %q", p)
	}
	return "/static/" + p + "?v=" + h, nil
}

var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".json":  "application/json",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".ico":   "image/x-icon",
	".woff2": "font/woff2",
	".wasm":  "application/wasm",
	".txt":   "text/plain; charset=utf-8",
	".md":    "text/plain; charset=utf-8",
}

// Handler serves /static/<path>. Requests carrying the current ?v= hash are
// cached for a year; others must revalidate.
func (a *Assets) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/static/")
		h, ok := a.hashes[p]
		ct, typed := contentTypes[path.Ext(p)]
		if !ok || !typed || strings.HasSuffix(p, ".md") || strings.HasSuffix(p, "LICENSE") {
			http.NotFound(w, r)
			return
		}
		f, err := a.fs.Open(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = f.Close() }()
		rs, ok := f.(interface {
			Read([]byte) (int, error)
			Seek(int64, int) (int64, error)
		})
		if !ok {
			http.Error(w, "unseekable asset", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("ETag", `"`+h+`"`)
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		if r.URL.Query().Get("v") == h {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, "", time.Time{}, rs)
	})
}
