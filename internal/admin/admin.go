// Package admin serves the admin area: overview, users, invites, content and
// secret moderation, site settings and the audit log, plus a small JSON API
// under /api/v1/admin. Every route is web.Admin(): the admin scope, and an
// address inside SNAP_ADMIN_CIDRS when that is set.
package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// Deps are the admin area's dependencies.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	DB       *db.DB
	Settings *settings.Store
	Render   *web.Renderer
	Errors   *web.ErrorPages
	Accounts *accounts.Service
	Items    *items.Service
	Secrets  *secrets.Service

	// Now replaces the clock (tests). Nil means time.Now.
	Now func() time.Time
}

// Admin serves /admin and /api/v1/admin.
type Admin struct {
	d   Deps
	now func() time.Time
}

// New returns the admin area.
func New(d Deps) *Admin {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Admin{d: d, now: now}
}

// Register adds the admin routes.
func (a *Admin) Register(r *web.Router) {
	adm := web.Admin()
	r.HandleFunc("GET /admin", a.overviewPage, adm)

	r.HandleFunc("GET /admin/users", a.usersPage, adm)
	r.HandleFunc("GET /admin/users/new", a.newUserPage, adm)
	r.HandleFunc("POST /admin/users", a.createUser, adm)
	r.HandleFunc("GET /admin/users/{id}", a.userPage, adm)
	r.HandleFunc("POST /admin/users/{id}/role", a.setRole, adm)
	r.HandleFunc("POST /admin/users/{id}/status", a.setStatus, adm)
	r.HandleFunc("POST /admin/users/{id}/limits", a.setLimits, adm)
	r.HandleFunc("POST /admin/users/{id}/reset", a.resetPassword, adm)
	r.HandleFunc("POST /admin/users/{id}/delete", a.deleteUserForm, adm)

	r.HandleFunc("GET /admin/invites", a.invitesPage, adm)
	r.HandleFunc("POST /admin/invites", a.createInvite, adm)
	r.HandleFunc("POST /admin/invites/{id}/revoke", a.revokeInvite, adm)

	r.HandleFunc("GET /admin/content", a.contentPage, adm)
	r.HandleFunc("POST /admin/content/{id}/delete", a.deleteItem, adm)

	r.HandleFunc("GET /admin/secrets", a.secretsPage, adm)
	r.HandleFunc("POST /admin/secrets/burn", a.burnSecretForm, adm)

	r.HandleFunc("GET /admin/settings", a.settingsPage, adm)
	r.HandleFunc("POST /admin/settings", a.saveSettings, adm)
	r.HandleFunc("POST /admin/settings/apply", a.applySetting, adm)
	r.HandleFunc("POST /admin/settings/test-email", a.testEmail, adm)

	r.HandleFunc("GET /admin/audit", a.auditPage, adm)

	a.registerAPI(r)
}

// tab is one entry of the admin tab bar.
type tab struct {
	Href, Label, Icon string
	Current           bool
}

var tabs = []tab{
	{"/admin", "Overview", "activity", false},
	{"/admin/users", "Users", "lock", false},
	{"/admin/invites", "Invites", "link", false},
	{"/admin/content", "Content", "file", false},
	{"/admin/secrets", "Secrets", "key", false},
	{"/admin/settings", "Settings", "settings", false},
	{"/admin/audit", "Audit", "clock", false},
}

// base is what every admin page carries.
type base struct {
	Tabs   []tab
	Notice string // success message
	Error  string // failure message
	Back   string // this page's URL, for forms that return here
}

// Messages for ?done=code after a redirect.
var doneNotices = map[string]string{
	"settings":     "Settings saved.",
	"revoked":      "Invite revoked.",
	"role":         "Role saved.",
	"enabled":      "User enabled.",
	"disabled":     "User disabled. They are signed out everywhere.",
	"limits":       "Limits saved.",
	"reset_sent":   "Reset link sent.",
	"deleted":      "User deleted, with their items and secrets.",
	"item_deleted": "Item deleted.",
	"burned":       "Secret burned. Its link no longer works.",
	"gone":         "That secret was already gone; its files are deleted.",
}

func (a *Admin) base(r *http.Request, current string) base {
	b := base{Tabs: make([]tab, len(tabs)), Notice: doneNotices[r.URL.Query().Get("done")], Back: r.URL.RequestURI()}
	if r.Method != http.MethodGet {
		b.Back = current // a page rendered as a POST's answer returns to its tab, not to the POST URL
	}
	copy(b.Tabs, tabs)
	for i := range b.Tabs {
		b.Tabs[i].Current = b.Tabs[i].Href == current
	}
	return b
}

func (a *Admin) page(w http.ResponseWriter, r *http.Request, status int, name, title string, data any) {
	w.Header().Set("Cache-Control", "no-store")
	a.d.Render.Page(w, r, status, name, web.Page{Title: title, Nav: "admin", Scripts: []string{"js/admin.js"}, Data: data})
}

func (a *Admin) fail(w http.ResponseWriter, r *http.Request, err error) {
	a.d.Log.Error("admin", "route", r.Pattern, "err", err)
	a.d.Errors.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
}

// back returns the same-origin admin URL a form asked to return to, with
// ?done=code, or fallback.
func back(r *http.Request, fallback, code string) string {
	to := web.SafeNext(r.PostFormValue("back"))
	if !strings.HasPrefix(to, "/admin") {
		to = fallback
	}
	u, err := url.Parse(to)
	if err != nil {
		u = &url.URL{Path: fallback}
	}
	q := u.Query()
	q.Del("done")
	if code != "" {
		q.Set("done", code)
	}
	u.RawQuery = q.Encode()
	return u.RequestURI()
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusSeeOther) // #nosec G710 -- to comes from back() or a constant: a relative /admin path
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// perPage is the page size of every admin table.
const perPage = 50

// pager is a table footer: "From–To of Total" and the page links.
type pager struct {
	Page, Pages, From, To, Total int
	Href                         string // base URL ending in "page="
}

// pageOf reads ?page= (1-based) and returns it with the row offset.
func pageOf(r *http.Request) (page, offset int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	return page, (page - 1) * perPage
}

func newPager(path string, q url.Values, page, total, rows int) pager {
	q.Del("page")
	q.Del("done")
	href := path + "?"
	if enc := q.Encode(); enc != "" {
		href += enc + "&"
	}
	p := pager{Page: page, Pages: max(1, (total+perPage-1)/perPage), Total: total, Href: href + "page="}
	if rows > 0 {
		p.From = (page-1)*perPage + 1
		p.To = p.From + rows - 1
	}
	return p
}

// dash returns s, or "—" when it is empty.
func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func stamp(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// errLastAdmin is the message for accounts.ErrLastAdmin.
const msgLastAdmin = "This is the last active admin. Make someone else an admin first."

func isLastAdmin(err error) bool { return errors.Is(err, accounts.ErrLastAdmin) }
