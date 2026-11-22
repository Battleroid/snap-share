package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
)

//go:embed all:templates
var templateFS embed.FS

// Page is what a handler passes to Renderer.Page.
type Page struct {
	Title      string   // <title> prefix; empty shows only the site name
	Nav        string   // active nav entry: home, dashboard, secrets, admin, settings
	Scripts    []string // extra ES modules, e.g. "js/upload.js"
	NoIndex    bool     // add robots noindex
	NoReferrer bool     // send no referrer from this page
	Head       template.HTML
	Data       any
}

// Site is per-request data every template can use as .Site.
type Site struct {
	Name        string
	OIDCEnabled bool
	OIDCName    string
	Values      map[string]any // feature-specific flags (e.g. "SignupOpen")
}

type view struct {
	Page
	P    *auth.Principal
	Site Site
	Path string
}

// Renderer executes page templates.
type Renderer struct {
	pages  map[string]*template.Template
	assets *Assets
	site   func(*http.Request) Site
	log    *slog.Logger
}

// NewRenderer parses every template. Layout and partials live in templates/_layout
// and templates/_partials; every other templates/<feature>/<page>.html is a page,
// named "<feature>/<page>", that defines "content" (and optionally "head").
func NewRenderer(assets *Assets, site func(*http.Request) Site, log *slog.Logger) (*Renderer, error) {
	sub, err := fs.Sub(templateFS, "templates")
	if err != nil {
		return nil, err
	}
	rd := &Renderer{pages: map[string]*template.Template{}, assets: assets, site: site, log: log}
	base := template.New("").Funcs(rd.funcs())
	for _, dir := range []string{"_layout", "_partials"} {
		if base, err = base.ParseFS(sub, dir+"/*.html"); err != nil {
			return nil, fmt.Errorf("parse %s: %w", dir, err)
		}
	}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") || strings.HasPrefix(p, "_") {
			return err
		}
		t, err := base.Clone()
		if err != nil {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		if _, err := t.New(p).Parse(string(b)); err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		rd.pages[strings.TrimSuffix(p, ".html")] = t
		return nil
	})
	return rd, err
}

// Has reports whether a page template exists.
func (rd *Renderer) Has(name string) bool { _, ok := rd.pages[name]; return ok }

// Page renders a full page.
func (rd *Renderer) Page(w http.ResponseWriter, r *http.Request, status int, name string, p Page) {
	rd.exec(w, r, status, name, "base", p)
}

// Fragment renders one named template from a page file (for htmx swaps).
func (rd *Renderer) Fragment(w http.ResponseWriter, r *http.Request, status int, name, block string, p Page) {
	rd.exec(w, r, status, name, block, p)
}

func (rd *Renderer) exec(w http.ResponseWriter, r *http.Request, status int, name, block string, p Page) {
	t, ok := rd.pages[name]
	if !ok {
		rd.log.Error("unknown template", "name", name)
		http.Error(w, "template missing", http.StatusInternalServerError)
		return
	}
	v := view{Page: p, P: auth.FromContext(r.Context()), Path: r.URL.Path}
	if rd.site != nil {
		v.Site = rd.site(r)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, block, v); err != nil {
		rd.log.Error("render", "template", name, "block", block, "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Add("Vary", "HX-Request")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	if p.NoIndex {
		h.Set("X-Robots-Tag", "noindex")
	}
	if p.NoReferrer {
		h.Set("Referrer-Policy", "no-referrer")
	}
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (rd *Renderer) funcs() template.FuncMap {
	return template.FuncMap{
		"asset": func(p string) (string, error) { return rd.assets.URL(p) },
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict: odd number of arguments")
			}
			m := make(map[string]any, len(kv)/2)
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
		"list":     func(v ...any) []any { return v },
		"bytes":    HumanBytes,
		"datetime": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") },
		"date":     func(t time.Time) string { return t.UTC().Format("2006-01-02") },
		"iso":      func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
		"ext":      path.Ext,
		"lower":    strings.ToLower,
		"join":     strings.Join,
		"add":      func(a, b int) int { return a + b },
		"plural": func(n int, one, many string) string {
			if n == 1 {
				return one
			}
			return many
		},
	}
}

// HumanBytes formats a size with SI units ("4.1 MB").
func HumanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	if v >= 100 {
		return fmt.Sprintf("%.0f %cB", v, "KMGTPE"[exp])
	}
	s := fmt.Sprintf("%.1f", v)
	s = strings.TrimSuffix(s, ".0") // "1 GB", not "1.0 GB"
	return fmt.Sprintf("%s %cB", s, "KMGTPE"[exp])
}
