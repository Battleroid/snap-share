package secrets

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

// openPage serves the reveal page. It is the same for every id and never
// touches the database: the id and key are read by secret-open.js, which
// strips the key from the address bar before doing anything else.
func (s *Service) openPage(w http.ResponseWriter, r *http.Request) {
	s.d.Render.Page(w, r, http.StatusOK, "secrets/open", web.Page{
		Title:      "Secret",
		NoIndex:    true,
		NoReferrer: true,
		Scripts:    []string{"js/secret-open.js"},
	})
}

// Option is a value and label for a select.
type Option struct {
	Value int64
	Label string
}

// ComposerData is what the "secret.composer" partial needs. The composer
// page at / renders it as its Secret tab with
// {{template "secret.composer" .Data.Secret}}; that page must also load
// js/secret-new.js and be served with web.WithCSP(web.CSPWasm).
type ComposerData struct {
	Anonymous     bool  // text only, no note or notify
	MaxBytes      int64 // file plaintext limit
	TextMaxBytes  int64 // padded text limit
	MaxViews      int
	Views         []int
	Expiries      []Option // seconds
	DefaultExpiry int64
	Notify        bool // offer "notify me"
}

// Composer returns the composer's settings for principal p (nil: an
// anonymous visitor, when anonymous secrets are on).
func (s *Service) Composer(p *auth.Principal) ComposerData {
	maxV := MaxViews.Get(s.d.Settings)
	maxE := MaxExpiry.Get(s.d.Settings)
	d := ComposerData{
		Anonymous:     p == nil,
		MaxBytes:      MaxBytes.Get(s.d.Settings),
		TextMaxBytes:  TextMaxBytes,
		MaxViews:      maxV,
		DefaultExpiry: int64(s.defaultExpiry() / time.Second),
		Notify:        p != nil && s.d.Notifier != nil,
	}
	if d.Anonymous {
		d.TextMaxBytes = AnonymousMaxBytes.Get(s.d.Settings)
	}
	for _, v := range []int{1, 2, 3, 5, 10} {
		if v <= maxV {
			d.Views = append(d.Views, v)
		}
	}
	for _, e := range []Option{
		{300, "5 minutes"}, {3600, "1 hour"}, {86400, "1 day"}, {7 * 86400, "7 days"}, {30 * 86400, "30 days"},
	} {
		if e.Value <= maxE {
			d.Expiries = append(d.Expiries, e)
		}
	}
	return d
}

// newPage is the standalone composer.
func (s *Service) newPage(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if p == nil && !Anonymous.Get(s.d.Settings) {
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/new/secret"), http.StatusSeeOther)
		return
	}
	s.d.Render.Page(w, r, http.StatusOK, "secrets/new", web.Page{
		Title:   "New secret",
		Nav:     "secrets",
		Scripts: []string{"js/secret-new.js"},
		Data:    map[string]any{"Secret": s.Composer(p)},
	})
}

// dashboardRow is one secret on the owner's page.
type dashboardRow struct {
	Summary
	Events      []Event
	StateStatus string // badge status for State
	OpenedText  string // opened | link opened | not opened
	OpenStatus  string
	Burnable    bool
}

// Label is the timeline wording for an event kind.
func (e Event) Label() string {
	switch e.Kind {
	case "link_opened":
		return "link opened"
	case "failed":
		return "wrong passphrase"
	}
	return e.Kind
}

// Client is the timeline's short description of who made the request.
func (e Event) Client() string {
	switch {
	case e.UASummary != "":
		return e.UASummary
	case e.Bot != "":
		return e.Bot
	}
	return "—"
}

func newDashboardRow(sm Summary, evs []Event) dashboardRow {
	d := dashboardRow{Summary: sm, Events: evs, OpenedText: "not opened", OpenStatus: "unknown"}
	switch sm.State {
	case stateReady:
		d.StateStatus, d.Burnable = "ok", true
	case stateUploading:
		d.StateStatus, d.Burnable = "paused", true
	case stateExpired:
		d.StateStatus = "unknown"
	default:
		d.StateStatus = "down"
	}
	if sm.OpenedAt != nil {
		d.OpenedText, d.OpenStatus = "opened", "ok"
	} else {
		for _, e := range evs {
			if e.Kind == "link_opened" {
				d.OpenedText, d.OpenStatus = "link opened", "info"
				break
			}
		}
	}
	return d
}

const dashboardPageSize = 50

func (s *Service) dashboardPage(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if !p.Can(auth.ScopeSecretsRead) {
		s.fail(w, r, http.StatusForbidden, "This needs the secrets:read permission.")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	items, total, err := s.ownerList(r.Context(), p.UserID, dashboardPageSize, (page-1)*dashboardPageSize)
	if err != nil {
		s.internal(w, r, "dashboard", err)
		return
	}
	rows := make([]dashboardRow, 0, len(items))
	for _, it := range items {
		evs, err := s.ownerEvents(r.Context(), p.UserID, it.ID, 20)
		if err != nil {
			s.internal(w, r, "dashboard events", err)
			return
		}
		rows = append(rows, newDashboardRow(it, evs))
	}
	pages := max(1, (total+dashboardPageSize-1)/dashboardPageSize)
	first := 0
	if total > 0 {
		first = (page-1)*dashboardPageSize + 1
	}
	s.d.Render.Page(w, r, http.StatusOK, "secrets/dashboard", web.Page{
		Title: "Secrets",
		Nav:   "secrets",
		Data: map[string]any{
			"Rows":   rows,
			"Total":  total,
			"First":  first,
			"Last":   first + len(rows) - 1,
			"Page":   page,
			"Pages":  pages,
			"Burned": r.URL.Query().Get("burned") == "1",
		},
	})
}

// dashboardBurn is the Burn now form on the owner's page.
func (s *Service) dashboardBurn(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	id := r.PathValue("id")
	if !validID(id) {
		s.notFound(w, r)
		return
	}
	_, ok, err := s.Revoke(r.Context(), id, p.UserID)
	if err != nil {
		s.internal(w, r, "burn", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	next := "/dashboard/secrets?burned=1"
	if pg, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && pg > 1 {
		next += "&page=" + strconv.Itoa(pg)
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}
