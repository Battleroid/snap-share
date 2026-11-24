package web

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/version"
)

//go:embed all:templates
var templateFS embed.FS

// Page is what a handler passes to Renderer.Page.
type Page struct {
	Title string // <title> prefix; empty shows only the site name
	// Nav is ignored: the layout marks the current nav entry from the URL
	// (see NavKey). Handlers may drop it.
	Nav        string
	Scripts    []string // extra ES modules, e.g. "js/upload.js"
	NoIndex    bool     // add robots noindex
	NoReferrer bool     // send no referrer from this page
	Head       template.HTML
	Data       any
}

// Site is per-request data every template can use as .Site.
type Site struct {
	Name        string
	Host        string // the public host, e.g. snap.lan (auth.brand)
	OIDCEnabled bool
	OIDCName    string
	Values      map[string]any // feature-specific flags (e.g. "SignupOpen")
}

// Bell is the header's notification bell, as .Bell in the layout. Full
// pages get one for browser sessions once SetBell has installed a count.
type Bell struct {
	Unread int
}

// Quota is the signed-in user's storage, as .Quota in the app shell's
// header (layout.quota). It is set only when the user has a quota.
type Quota struct {
	Used, Total int64  // bytes used and the quota
	MaxFile     int64  // the largest file the user may upload
	ID          string // element id prefix; "" is "quota" (the header's)
}

// NewQuota makes a Quota; templates use it as the "quota" func (/_kit
// demos, which need their own id).
func NewQuota(id string, used, total, maxFile int64) *Quota {
	return &Quota{Used: used, Total: total, MaxFile: maxFile, ID: id}
}

// Pct is the share used, 0–100, rounded down.
func (q *Quota) Pct() int64 {
	if q.Total <= 0 {
		return 0
	}
	return min(max(q.Used, 0)*100/q.Total, 100)
}

// Free is what is left, never below zero.
func (q *Quota) Free() int64 { return max(q.Total-q.Used, 0) }

// Level colors the ring: "" (accent), "warn" from 80%, "down" from 95%.
func (q *Quota) Level() string {
	switch p := q.Pct(); {
	case p >= 95:
		return "down"
	case p >= 80:
		return "warn"
	}
	return ""
}

type view struct {
	Page
	P    *auth.Principal
	Site Site
	Path string
	Bell *Bell
	// Quota is the user's storage for the header ring; nil without a quota.
	Quota *Quota
	// Layout is the page's frame: LayoutApp, LayoutSite or LayoutAuth.
	Layout string
	// NavKey names the current nav entry (see NavKey).
	NavKey string
	// Sidebar shows the app shell's navigation: any signed-in principal
	// except a session held at the two-factor setup.
	Sidebar bool
}

// The three page frames in templates/_layout.
const (
	LayoutApp  = "app"  // signed-in pages: AppShell with header and sidebar
	LayoutSite = "site" // public pages: SiteHeader
	LayoutAuth = "auth" // sign-in and narrow one-task pages: a centered card, no chrome
)

// pageLayouts pins the pages whose frame does not depend on who is
// signed in. Every other page gets LayoutApp when a principal is signed in
// and LayoutSite when not. share/player is a document of its own and never
// renders a layout.
var pageLayouts = map[string]string{
	"share/unlock": LayoutAuth,
	"secrets/open": LayoutAuth,
	"share/x":      LayoutAuth,
	"core/error":   LayoutAuth,
	"share/item":   LayoutSite,
	"share/gist":   LayoutSite,
	"albums/album": LayoutSite,
}

// layoutFor picks the frame for page name and principal p.
func layoutFor(name string, p *auth.Principal) string {
	if strings.HasPrefix(name, "auth/") {
		return LayoutAuth
	}
	if l, ok := pageLayouts[name]; ok {
		return l
	}
	if p != nil {
		return LayoutApp
	}
	return LayoutSite
}

// navPrefixes maps URL paths to nav entries: the first entry whose path is
// the request path or a parent of it wins, so longer paths come first.
var navPrefixes = []struct{ path, key string }{
	{"/dashboard/stats", "stats"},
	{"/dashboard/secrets", "secrets"},
	{"/dashboard", "items"},
	{"/new/secret", "secrets"}, // reached from Secrets; its crumbs say so
	{"/notifications", "notifications"},
	{"/settings/security", "settings/security"},
	{"/settings/keys", "settings/keys"},
	{"/settings/defaults", "settings/defaults"},
	{"/settings/integrations", "settings/integrations"},
	{"/settings/notifications", "settings/notifications"},
	{"/settings", "settings/profile"},
	{"/admin/users", "admin/users"},
	{"/admin/invites", "admin/invites"},
	{"/admin/content", "admin/content"},
	{"/admin/secrets", "admin/secrets"},
	{"/admin/settings", "admin/settings"},
	{"/admin/audit", "admin/audit"},
	{"/admin", "admin/overview"},
	{"/_kit", "admin/kit"},
}

// NavKey names the nav entry a URL belongs to: "new", "items", "albums",
// "secrets", "stats", "notifications", "settings/<page>" or
// "admin/<page>"; "" for none. Pages rendered as a POST's answer match by
// the POST's path, which sits under the page's own.
func NavKey(u *url.URL) string {
	p := u.Path
	if p == "/" {
		return "new"
	}
	for _, e := range navPrefixes {
		if p == e.path || strings.HasPrefix(p, e.path+"/") {
			if e.key == "items" && p == "/dashboard" && u.Query().Get("type") == "album" {
				return "albums"
			}
			return e.key
		}
	}
	return ""
}

// Renderer executes page templates.
type Renderer struct {
	pages  map[string]*template.Template
	assets *Assets
	site   func(*http.Request) Site
	log    *slog.Logger
	bell   func(ctx context.Context, userID int64) int
	quota  func(ctx context.Context, userID int64) (Quota, bool)
	// fields numbers the ids the field partials give their controls, so two
	// fields with the same name on one page (a form and a dialog) never share one.
	fields atomic.Uint64
}

// SetQuota installs the storage lookup behind the header's quota ring: ok
// false (no quota, or an error) shows no ring. Call it during wiring.
func (rd *Renderer) SetQuota(q func(ctx context.Context, userID int64) (Quota, bool)) { rd.quota = q }

// SetBell installs the unread count shown by the header bell. Call it
// during wiring, before serving.
func (rd *Renderer) SetBell(unread func(ctx context.Context, userID int64) int) { rd.bell = unread }

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
	v := view{Page: p, P: auth.FromContext(r.Context()), Path: r.URL.Path, NavKey: NavKey(r.URL)}
	v.Layout = layoutFor(name, v.P)
	v.Sidebar = v.Layout == LayoutApp && !v.P.NeedsSetup
	if rd.site != nil {
		v.Site = rd.site(r)
	}
	// The bell lives in the app and site headers; the auth frame has no header.
	if block == "base" && rd.bell != nil && v.P != nil && !v.P.ViaKey() && !v.P.NeedsSetup && v.Layout != LayoutAuth {
		v.Bell = &Bell{Unread: rd.bell(r.Context(), v.P.UserID)}
	}
	// The quota ring is in the app shell's header only.
	if block == "base" && rd.quota != nil && v.Layout == LayoutApp && !v.P.ViaKey() && !v.P.NeedsSetup {
		if q, ok := rd.quota(r.Context(), v.P.UserID); ok && q.Total > 0 {
			v.Quota = &q
		}
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

var fieldIDChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func (rd *Renderer) funcs() template.FuncMap {
	return template.FuncMap{
		"asset": func(p string) (string, error) { return rd.assets.URL(p) },
		"build": version.Build,
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
		"list": func(v ...any) []any { return v },
		"fieldid": func(name string) string { // a page-unique id for a field partial's control
			return fmt.Sprintf("f-%s-%d", fieldIDChars.ReplaceAllString(name, "-"), rd.fields.Add(1))
		},
		"bytes":    HumanBytes,
		"expiries": ExpiryOptions,
		"datetime": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") },
		"date":     func(t time.Time) string { return t.UTC().Format("2006-01-02") },
		"iso":      func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
		"when":     when,
		"ext":      path.Ext,
		"lower":    strings.ToLower,
		"join":     strings.Join,
		"add":      func(a, b int) int { return a + b },
		"sub":      func(a, b int) int { return a - b },
		"initial": func(s string) string { // first letter, upper case, for avatars
			for _, r := range s {
				return strings.ToUpper(string(r))
			}
			return ""
		},
		"hasPrefix": strings.HasPrefix,
		"quota":     NewQuota,
		"attrs":     htmlAttrs,
		"mark":      brandMark,
		"avatar":    avatar,
		"plural": func(n int, one, many string) string {
			if n == 1 {
				return one
			}
			return many
		},
	}
}

// when is the time.Time in v (a time.Time or *time.Time), or the zero
// time for nil and anything else, for the pb.time partial.
func when(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case *time.Time:
		if t != nil {
			return *t
		}
	}
	return time.Time{}
}

// attrName is what the attrs func lets through: script hooks and htmx
// attributes, never event handlers, styles or URL-typed attributes but rel.
var attrName = regexp.MustCompile(`^(?:(?:data|hx|aria)-[a-z0-9-]+|rel|target)$`)

// htmlAttrs renders a dict of extra attributes (the partials' attrs key)
// as ` name="value"` pairs, names sorted, values escaped. html/template
// refuses attribute names that come from data, so they are checked here.
func htmlAttrs(v any) (template.HTMLAttr, error) {
	if v == nil {
		return "", nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return "", fmt.Errorf("attrs: want a dict, got %T", v)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		// hx-on* (and its data-hx-on* spelling) are htmx's inline event handlers.
		if !attrName.MatchString(k) || strings.HasPrefix(k, "hx-on") || strings.HasPrefix(k, "data-hx-on") {
			return "", fmt.Errorf("attrs: attribute %q is not allowed", k)
		}
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, ` %s="%s"`, k, html.EscapeString(fmt.Sprint(m[k])))
	}
	return template.HTMLAttr(b.String()), nil // #nosec G203 -- names allowlisted, values escaped
}

// brandMark is the two-letter mark beside the site name (kit pb-brand__mark).
func brandMark(name string) string {
	r := []rune(strings.ToLower(strings.TrimSpace(name)))
	if len(r) > 2 {
		r = r[:2]
	}
	return string(r)
}

// Avatar is what the kit's pbAvatar would write into span.pb-avatar: the
// initials and one of four pop colors, both from the name, so a person
// keeps their color. Rendering it on the server needs no script.
type Avatar struct {
	Initials string
	Color    int // data-c, 0–3
}

var avatarSplit = regexp.MustCompile(`[\s._@-]+`)

func avatar(name string) Avatar {
	var parts []string
	for _, s := range avatarSplit.Split(strings.TrimSpace(name), -1) {
		if s != "" {
			parts = append(parts, s)
		}
	}
	ini := "?"
	switch {
	case len(parts) > 1:
		ini = string([]rune(parts[0])[:1]) + string([]rune(parts[1])[:1])
	case len(parts) == 1:
		r := []rune(parts[0])
		ini = string(r[:min(2, len(r))])
	}
	var h uint32
	for _, c := range utf16.Encode([]rune(name)) { // as JS charCodeAt
		h = h*31 + uint32(c)
	}
	return Avatar{Initials: strings.ToLower(ini), Color: int(h % 4)}
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
