package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
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

// fontDir holds the kit's woff2 files. patchbay.css requests them without
// ?v=, so they are cached as immutable by path: they are hash-pinned in
// VENDOR.md. A kit update that changes a font ships new bytes under the same
// name, so browsers keep the old copy until it expires or is cleared.
const fontDir = "vendor/patchbay/fonts/"

// Handler serves /static/<path>. Requests carrying the current ?v= hash are
// cached for a year, as are the vendored fonts; others must revalidate.
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
		if r.URL.Query().Get("v") == h || strings.HasPrefix(p, fontDir) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, "", time.Time{}, rs)
	})
}

// patchbayPin is the vendored Patchbay commit, read once from the patchbay row of VENDOR.md
// (scripts/vendor-patchbay.sh rewrites it).
var patchbayPin = sync.OnceValue(func() string {
	b, err := fs.ReadFile(staticFS, "static/vendor/VENDOR.md")
	if err != nil {
		return ""
	}
	m := regexp.MustCompile("(?m)^\\| `patchbay/` \\|.*?commit `([0-9a-f]{40})`").FindSubmatch(b)
	if m == nil {
		return ""
	}
	return string(m[1])
})

// PatchbayPin returns the short commit of the vendored Patchbay kit, or "" if VENDOR.md names none.
func PatchbayPin() string {
	if p := patchbayPin(); len(p) >= 7 {
		return p[:7]
	}
	return ""
}
