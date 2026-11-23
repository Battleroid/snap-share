// Package keys implements API keys: the snap_… bearer tokens, their scopes,
// network and content restrictions, the key and integration settings pages
// (ShareX downloads, curl and CLI setup), the keys and /me API, and the CLI
// downloads under /dl/.
package keys

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/metrics"
	"github.com/Battleroid/snap-share/internal/ratelimit"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// Owners resolves a key's owner to a principal, failing for missing and
// disabled accounts. *accounts.Store implements it.
type Owners interface {
	PrincipalFor(ctx context.Context, id int64) (*auth.Principal, error)
}

// OwnerCheck asks a user with two-factor authentication to confirm it's
// them (the password and a second factor) before a key with the admin
// scope is made. *accounts.Service implements it.
type OwnerCheck interface {
	HasMFA(ctx context.Context, uid int64) (bool, error)
	Proof(r *http.Request) (accounts.Proof, error)
	// ConfirmOwner checks the form's "current", "code" and "passkey"
	// fields; a *web.APIError carries the message for the form.
	ConfirmOwner(r *http.Request) error
}

// Deps are the service's dependencies.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Metrics  *metrics.Metrics // nil: nothing is counted
	DB       *db.DB
	Settings *settings.Store
	Jobs     *jobs.Queue // nil: last-use is flushed only by calling Flush
	Owners   Owners
	Owner    OwnerCheck
	Render   *web.Renderer
	Errors   *web.ErrorPages
	SiteName func() string // nil: "snap"
	Now      func() time.Time
}

// Service is the keys feature: the bearer verifier, its rate limit and routes.
type Service struct {
	d     Deps
	log   *slog.Logger
	now   func() time.Time
	store *Store
	limit *ratelimit.Limiter
	cmp   func(want []byte, secret string) bool // matchSecret; tests count calls
}

// Per-key request budget: generous, it only stops runaway scripts.
const (
	keyRate  = 600 // per minute
	keyBurst = 300
)

// New builds the service and registers the last-use flush on d.Jobs.
func New(d Deps) (*Service, error) {
	if d.Cfg == nil || d.DB == nil || d.Owners == nil || d.Owner == nil || d.Render == nil || d.Errors == nil {
		return nil, errors.New("keys: missing dependency")
	}
	s := &Service{d: d, log: d.Log, now: d.Now, cmp: matchSecret}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if d.SiteName == nil {
		s.d.SiteName = func() string { return "snap" }
	}
	s.store = NewStore(d.DB, s.now)
	s.limit = ratelimit.New(keyRate, time.Minute, keyBurst).WithClock(s.now).Named("api_key", d.Metrics)
	if d.Jobs != nil {
		d.Jobs.Every("keys.last_used.flush", 30*time.Second, s.store.Flush)
	}
	return s, nil
}

// Store returns the key store.
func (s *Service) Store() *Store { return s.store }

// Flush writes batched last-use data (call before shutdown).
func (s *Service) Flush(ctx context.Context) error { return s.store.Flush(ctx) }

// Register adds the keys routes.
func (s *Service) Register(r *web.Router) {
	r.HandleFunc("GET /settings/keys", s.sessionOnly(s.listPage), web.SignedIn())
	r.HandleFunc("GET /settings/keys/new", s.sessionOnly(s.newPage), web.SignedIn())
	r.HandleFunc("POST /settings/keys", s.sessionOnly(s.createForm), web.SignedIn())
	r.HandleFunc("POST /settings/keys/{id}/revoke", s.sessionOnly(s.revokeForm), web.SignedIn())

	// Keys are managed from a browser only: a key can never mint or revoke keys.
	r.HandleFunc("GET /api/v1/keys", s.sessionOnly(s.apiList), web.SignedIn())
	r.HandleFunc("POST /api/v1/keys", s.sessionOnly(s.apiCreate), web.SignedIn())
	r.HandleFunc("DELETE /api/v1/keys/{id}", s.sessionOnly(s.apiRevoke), web.SignedIn())
	r.HandleFunc("GET /api/v1/me", s.me, web.Scope(auth.ScopeAccountRead))

	r.HandleFunc("GET /settings/integrations", s.sessionOnly(s.integrationsPage), web.SignedIn())
	r.HandleFunc("POST /settings/integrations/sharex/{kind}", s.sessionOnly(s.sharexDownload), web.SignedIn())

	r.HandleFunc("GET /dl/{name}", s.download, web.Public())
}

// sessionOnly refuses principals that authenticated with an API key.
func (s *Service) sessionOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p := auth.FromContext(r.Context()); p == nil || p.ViaKey() {
			s.d.Errors.Error(w, r, http.StatusForbidden, "API keys can only be managed from a browser session.")
			return
		}
		h(w, r)
	}
}

// VerifyBearer implements accounts.BearerVerifier. Malformed keys (bad shape
// or checksum) are refused before any database lookup. Unknown, revoked and
// expired keys, wrong secrets and disabled owners all give
// auth.ErrInvalidCredentials; a client address outside the key's networks
// gives auth.ErrForbiddenNetwork. The principal's scopes are the key's scopes
// that the owner's current role allows, so a demotion takes effect at once.
func (s *Service) VerifyBearer(r *http.Request, token string) (*auth.Principal, error) {
	pub, secret, ok := ParseToken(token)
	if !ok {
		return nil, auth.ErrInvalidCredentials
	}
	ctx := r.Context()
	k, err := s.store.ByPublicID(ctx, pub)
	want := dummyHash[:]
	if err == nil {
		want = k.secretHash
	}
	match := s.cmp(want, secret) // the same work whether or not the id exists
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Error("key lookup", "err", err)
		}
		return nil, auth.ErrInvalidCredentials
	}
	now := s.now()
	if !match || k.Status(now) != StatusActive {
		return nil, auth.ErrInvalidCredentials
	}
	owner, err := s.d.Owners.PrincipalFor(ctx, k.UserID)
	if err != nil {
		if !errors.Is(err, accounts.ErrNotFound) {
			s.log.Error("key owner lookup", "err", err)
		}
		return nil, auth.ErrInvalidCredentials
	}
	ip := clientip.From(ctx)
	if len(k.CIDRs) > 0 && (!ip.IsValid() || !clientip.In(ip, k.CIDRs)) {
		return nil, auth.ErrForbiddenNetwork
	}
	addr := ""
	if ip.IsValid() {
		addr = ip.String()
	}
	s.store.touch(k.ID, now, addr)
	return &auth.Principal{
		UserID:        owner.UserID,
		Username:      owner.Username,
		Role:          owner.Role,
		EmailVerified: owner.EmailVerified,
		KeyID:         k.ID,
		Scopes:        EffectiveScopes(k.Scopes, owner.Role),
		Key:           k.Constraints.principal(),
	}, nil
}

// EffectiveScopes is the part of scopes that role allows.
func EffectiveScopes(scopes []auth.Scope, role auth.Role) []auth.Scope {
	out := make([]auth.Scope, 0, len(scopes))
	for _, sc := range scopes {
		if auth.RoleAllows(role, sc) && !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	return out
}

// RateLimit is middleware, installed after authentication, that limits each
// API key to a generous request budget (429 with Retry-After beyond it).
func (s *Service) RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := auth.FromContext(r.Context()); p.ViaKey() {
			if ok, wait := s.limit.Allow(fmt.Sprintf("key:%d", p.KeyID)); !ok {
				w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
				s.d.Errors.Error(w, r, http.StatusTooManyRequests, "Too many requests for this key. Slow down and try again.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("keys", "route", r.Pattern, "err", err)
	s.d.Errors.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
}
