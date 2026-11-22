// Package dashboard is the signed-in item manager: the /dashboard page
// (search, filters, table and grid views, selection), bulk actions over
// selected or matching items (expiry, visibility, password, albums, tags,
// zip download, sending links by email, delete) and tags.
package dashboard

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// Deps are the service's dependencies.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	DB       *db.DB
	Settings *settings.Store
	Items    *items.Service
	Albums   *albums.Service
	Blobs    blob.Store      // the files store, for zip downloads
	Users    *accounts.Store // the sender's verified address for "Send link"
	Mail     mail.Sender
	Render   *web.Renderer
	Errors   *web.ErrorPages
	SiteName func() string
	Now      func() time.Time
}

// Service implements the dashboard routes.
type Service struct {
	d    Deps
	log  *slog.Logger
	now  func() time.Time
	mail *mailTemplates

	zipMu   sync.Mutex
	tickets map[string]zipTicket
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.Cfg == nil || d.DB == nil || d.Settings == nil || d.Items == nil || d.Albums == nil || d.Blobs == nil || d.Users == nil ||
		d.Mail == nil || d.Render == nil || d.Errors == nil {
		return nil, errors.New("dashboard: missing dependency")
	}
	s := &Service{d: d, log: d.Log, now: d.Now, tickets: map[string]zipTicket{}}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.d.SiteName == nil {
		s.d.SiteName = func() string { return "snap" }
	}
	var err error
	if s.mail, err = loadMailTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

// Register adds the dashboard routes.
func (s *Service) Register(r *web.Router) {
	read := web.Scope(auth.ScopeItemsRead)
	write := web.Scope(auth.ScopeItemsWrite)

	r.HandleFunc("GET /dashboard", s.handle(s.page), web.SignedIn())
	// Bulk needs items:read to resolve the selection, and each action checks
	// the scope it needs on top (see actions in bulk.go).
	r.HandleFunc("POST /api/v1/items/bulk", s.handle(s.bulk), read)
	r.HandleFunc("GET /api/v1/zip/{token}", s.handle(s.zip), read, web.Redact())

	r.HandleFunc("GET /api/v1/tags", s.handle(s.apiTags), read)
	r.HandleFunc("POST /api/v1/tags", s.handle(s.apiTagCreate), write)
	r.HandleFunc("PATCH /api/v1/tags/{id}", s.handle(s.apiTagRename), write)
	r.HandleFunc("DELETE /api/v1/tags/{id}", s.handle(s.apiTagDelete), write)
	r.HandleFunc("GET /api/v1/items/{id}/tags", s.handle(s.apiItemTags), read)
	r.HandleFunc("PUT /api/v1/items/{id}/tags", s.handle(s.apiSetItemTags), write)
}

func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			s.fail(w, r, err)
		}
	}
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return // the client went away
	}
	s.d.Items.Fail(w, r, err)
}

func principal(r *http.Request) (*auth.Principal, error) {
	p := auth.FromContext(r.Context())
	if p == nil {
		return nil, web.Errorf(http.StatusUnauthorized, "Sign in first.")
	}
	return p, nil
}
