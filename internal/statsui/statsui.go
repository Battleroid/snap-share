// Package statsui is the owner's view of who opened what: the per-item
// stats page, its CSV export and per-item notification override, the
// secret timeline page, the owner-wide "Your stats" page (and GET /api/v1/stats)
// and the stats API (GET /api/v1/items/{id}/stats and
// /events). Visitor IPs and User-Agents reach only the item's owner;
// admins get totals for other users' items. See docs/stats.md.
package statsui

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/web"
)

// Items is what the stats pages need from the items service.
type Items interface {
	Get(ctx context.Context, id int64) (*items.Item, error)
	PublicURL(slug string) string
	// Usage is the account's used bytes and effective quota (0: none).
	Usage(ctx context.Context, userID int64) (used, quota int64, err error)
}

// Secrets is what the secret timeline needs from the secrets service.
type Secrets interface {
	OwnerSecret(ctx context.Context, owner int64, id string) (secrets.Summary, error)
	SetNotify(ctx context.Context, owner int64, id string, on bool) error
}

// Notify is the notification settings the pages read and change.
type Notify interface {
	Enabled(ctx context.Context, userID int64) bool
	Override(ctx context.Context, userID, itemID int64) (notify.Mode, error)
	SetOverride(ctx context.Context, userID, itemID int64, m notify.Mode) error
}

// Deps are the service's dependencies.
type Deps struct {
	DB      *db.DB
	Render  *web.Renderer
	Errors  *web.ErrorPages
	Log     *slog.Logger
	Items   Items
	Secrets Secrets // nil: no secret timeline
	Notify  Notify  // nil: no notification controls
	// AdminCIDRs is SNAP_ADMIN_CIDRS: admins get the totals of other users'
	// items only from these networks (any network when empty), as for /admin.
	AdminCIDRs []netip.Prefix
	// RetentionDays is the stats.retention_days setting, for notes about how
	// far back raw events reach. Nil: the notes leave the number out.
	RetentionDays func() int
	Now           func() time.Time
}

// Service implements the stats routes.
type Service struct {
	d   Deps
	log *slog.Logger
	now func() time.Time
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.DB == nil || d.Render == nil || d.Errors == nil || d.Items == nil {
		return nil, errors.New("statsui: missing dependency")
	}
	s := &Service{d: d, log: d.Log, now: d.Now}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// ItemStatsPath is the stats page of an item (the dashboard links here).
func ItemStatsPath(id int64) string {
	return "/dashboard/items/" + strconv.FormatInt(id, 10) + "/stats"
}

// SecretTimelinePath is the timeline page of a secret.
func SecretTimelinePath(id string) string { return "/dashboard/secrets/" + id + "/timeline" }

// Register adds the routes.
func (s *Service) Register(r *web.Router) {
	r.HandleFunc("GET /dashboard/stats", s.userPage, web.SignedIn())
	r.HandleFunc("GET /api/v1/stats", s.apiUser, web.Scope(auth.ScopeItemsRead))
	r.HandleFunc("GET /dashboard/items/{id}/stats", s.itemPage, web.SignedIn())
	r.HandleFunc("GET /dashboard/items/{id}/events.csv", s.itemCSV, web.SignedIn())
	r.HandleFunc("POST /dashboard/items/{id}/notify", s.itemNotify, web.SignedIn())
	r.HandleFunc("GET /api/v1/items/{id}/stats", s.apiStats, web.Scope(auth.ScopeItemsRead))
	r.HandleFunc("GET /api/v1/items/{id}/events", s.apiEvents, web.Scope(auth.ScopeItemsRead))
	if s.d.Secrets != nil {
		r.HandleFunc("GET /dashboard/secrets/{id}/timeline", s.secretPage, web.SignedIn(), web.Redact())
		r.HandleFunc("POST /dashboard/secrets/{id}/notify", s.secretNotify, web.SignedIn(), web.Redact())
	}
}

// access is what a principal may see of an item's stats.
type access int

const (
	accessNone   access = iota
	accessTotals        // admins, for other users' items: counts only
	accessOwner         // everything, including visitor IPs and User-Agents
)

// loadItem reads {id} and decides access. Anyone without access gets a
// 404, so item ids can't be probed.
func (s *Service) loadItem(w http.ResponseWriter, r *http.Request, allowAdmin bool) (*items.Item, access, bool) {
	p := auth.FromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 || p == nil {
		s.d.Errors.NotFound(w, r)
		return nil, accessNone, false
	}
	it, err := s.d.Items.Get(r.Context(), id)
	if errors.Is(err, items.ErrNotFound) {
		s.d.Errors.NotFound(w, r)
		return nil, accessNone, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil, accessNone, false
	}
	switch {
	case it.OwnerID == p.UserID:
		return it, accessOwner, true
	case allowAdmin && p.IsAdmin() && (len(s.d.AdminCIDRs) == 0 || clientip.In(clientip.From(r.Context()), s.d.AdminCIDRs)):
		return it, accessTotals, true
	}
	s.d.Errors.NotFound(w, r)
	return nil, accessNone, false
}

func (s *Service) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("statsui", "err", err)
	s.d.Errors.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
}

// midnight is 00:00 UTC of t's day.
func midnight(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
