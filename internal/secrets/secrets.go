// Package secrets serves one-time secrets: text or files encrypted on the
// client (envelope v1, internal/e2e and static/js/e2e.js), stored as
// ciphertext only, and destroyed after a set number of views, a number of
// failed passphrase attempts, an expiry, or the owner's say-so.
//
// The server never sees K_link, the passphrase or the content keys, except
// in sealed mode (curl, ShareX), where it encrypts on the client's behalf and
// forgets the key once the link is returned. See docs/secrets.md and the
// "One-time secrets" section of docs/security.md.
//
// Secret ids, probes, upload tokens, tickets and keys are never logged; every
// route that carries an id is registered with web.Redact.
package secrets

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/ratelimit"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/web"
)

// Site settings (admin-editable).
var (
	// MaxBytes caps the plaintext size of a file secret. Browsers hold the
	// whole decrypted file in memory, hence the modest default.
	MaxBytes = settings.Define("secrets.max_bytes", int64(256<<20), settings.Range[int64](1<<10, 1<<40))
	// MaxViews caps the views a creator can ask for.
	MaxViews = settings.Define("secrets.max_views", 100, settings.Range(1, 10000))
	// MaxExpiry caps the lifetime of a secret, in seconds (minimum 5 minutes).
	MaxExpiry = settings.Define("secrets.max_expiry", int64(30*24*3600), settings.Range[int64](int64(MinExpiry/time.Second), 10*365*24*3600))
	// MaxFailures is the number of wrong passphrase attempts that burn a secret.
	MaxFailures = settings.Define("secrets.max_failures", 10, settings.Range(1, 1000))
	// Anonymous lets visitors without an account create text secrets.
	Anonymous = settings.Define("secrets.anonymous", false, nil)
	// AnonymousMaxBytes caps an anonymous text secret, measured after padding.
	AnonymousMaxBytes = settings.Define("secrets.anonymous_max_bytes", int64(64<<10), settings.Range[int64](e2e.MinTextPad, TextMaxBytes))
	// AnonymousMaxTotalBytes caps the ciphertext all live anonymous secrets
	// hold together.
	AnonymousMaxTotalBytes = settings.Define("secrets.anonymous_max_total_bytes", int64(256<<20), settings.Range[int64](64<<10, 1<<50))
	// AnonymousPerHour caps anonymous creates per hour, from all addresses
	// together.
	AnonymousPerHour = settings.Define("secrets.anonymous_per_hour", 60, settings.Range(1, 1_000_000))
)

// Fixed limits.
const (
	MinExpiry     = 5 * time.Minute
	DefaultExpiry = 24 * time.Hour
	// TextMaxBytes caps padded text (one STREAM chunk), so a text claim can
	// carry its ciphertext inline.
	TextMaxBytes = e2e.ChunkSize
	// UploadTTL is how long a secret may stay unfinalized before it is swept.
	UploadTTL = time.Hour
	// TombstoneTTL is how long a burned, expired or revoked secret's row is
	// kept so link holders can see what happened to it.
	TombstoneTTL = 30 * 24 * time.Hour
	// MinTicketTTL is the shortest download ticket; larger files get size at
	// 1 Mbit/s.
	MinTicketTTL = 15 * time.Minute

	maxOpenUploadsPerUser = 5
	maxOpenUploadsPerIP   = 5
	maxNoteRunes          = 200
	fullChunk             = e2e.ChunkSize + e2e.TagSize
)

// Notification describes something a secret's owner may want to hear about.
// It never carries key material; SecretID and Note must not be logged.
type Notification struct {
	OwnerID   int64
	SecretID  string
	Note      string
	Kind      stats.Kind // stats.KindRevealed or stats.KindBurned
	Reason    string     // burn reason for KindBurned: "failed attempts"
	ViewsLeft int
	MaxViews  int
	At        time.Time
	IP        string
	UASummary string
}

// Notifier tells owners about their secrets. It is called after a reveal of a
// secret created with notify set, and always when a secret is burned after
// too many failed passphrase attempts. Implementations must not block.
type Notifier interface {
	Notify(ctx context.Context, n Notification)
}

// Deps are the service's dependencies.
type Deps struct {
	DB       *db.DB
	Blobs    blob.Store // rooted at SNAP_SECRETS_DIR; nothing else may live there
	Settings *settings.Store
	Render   *web.Renderer
	Errors   *web.ErrorPages
	Log      *slog.Logger

	// PublicURL is the base of sealed-mode links (config.PublicURL).
	PublicURL string
	// Stats records secret events (nil: none). VisitorSecret keys visitor ids.
	Stats         stats.Recorder
	VisitorSecret []byte
	// Notifier may be nil.
	Notifier Notifier
	// RateLimitExempt networks skip the per-IP limits and caps.
	RateLimitExempt []netip.Prefix
	// DefaultQuota is the storage quota of users without their own
	// (uploads.default_quota_bytes; 0 is unlimited). Secret ciphertext
	// counts against the same quota as items. Nil means 0.
	DefaultQuota func() int64

	// Now replaces the clock (tests). Nil means time.Now.
	Now func() time.Time
}

// Service implements the secrets API, pages and sweeper.
type Service struct {
	d        Deps
	now      func() time.Time
	notifyOn func(ctx context.Context, userID int64) bool

	openIP     *ratelimit.Limiter // info and claim, per client IP
	openSecret *ratelimit.Limiter // info and claim, per secret id
	createUser *ratelimit.Limiter // creates, per user
	anonIP     *ratelimit.Limiter // anonymous creates, per client IP
}

// New builds the service.
func New(d Deps) *Service {
	if d.Stats == nil {
		d.Stats = stats.Nop{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		d:          d,
		now:        now,
		openIP:     ratelimit.New(60, time.Minute, 30),
		openSecret: ratelimit.New(120, time.Minute, 120),
		createUser: ratelimit.New(300, time.Hour, 60),
		anonIP:     ratelimit.New(10, time.Hour, 3),
	}
}

// Register adds the pages and API routes.
func (s *Service) Register(r *web.Router) {
	id := "/api/v1/secrets/{id}"
	r.HandleFunc("GET /s/{id}", s.openPage, web.Public(), web.Redact(), web.WithCSP(web.CSPWasm))
	r.HandleFunc("GET /s", s.openPage, web.Public(), web.WithCSP(web.CSPWasm))
	r.HandleFunc("GET /new/secret", s.newPage, web.Public(), web.WithCSP(web.CSPWasm))
	r.HandleFunc("GET /dashboard/secrets", s.dashboardPage, web.SignedIn())
	r.HandleFunc("POST /dashboard/secrets/{id}/burn", s.dashboardBurn, web.Scope(auth.ScopeSecretsWrite), web.Redact())

	r.HandleFunc("POST /api/v1/secrets", s.create, web.Public())
	r.HandleFunc("GET /api/v1/secrets", s.list, web.Scope(auth.ScopeSecretsRead))
	r.HandleFunc("POST /api/v1/secrets/sealed", s.sealed, web.Scope(auth.ScopeSecretsWrite))
	r.HandleFunc("PUT "+id+"/chunks/{n}", s.putChunk, web.Public(), web.Redact())
	r.HandleFunc("POST "+id+"/finalize", s.finalize, web.Public(), web.Redact())
	r.HandleFunc("POST "+id+"/info", s.info, web.Public(), web.Redact())
	r.HandleFunc("POST "+id+"/claim", s.claim, web.Public(), web.Redact())
	r.HandleFunc("GET "+id+"/blob", s.blob, web.Public(), web.Redact(), web.WithCSP(web.CSPNone))
	r.HandleFunc("POST "+id+"/done", s.done, web.Public(), web.Redact())
	r.HandleFunc("DELETE "+id, s.revokeAPI, web.Scope(auth.ScopeSecretsWrite), web.Redact())
	r.HandleFunc("GET "+id+"/events", s.events, web.Scope(auth.ScopeSecretsRead), web.Redact())
}

// RegisterJobs adds the sweeper.
func (s *Service) RegisterJobs(q *jobs.Queue) {
	q.Every("secrets.sweep", time.Minute, s.Sweep)
}

// errNotFound is what every caller without a valid id, probe, token or
// ticket gets, whatever the reason.
var errNotFound = errors.New("secrets: not found")

// msgNotFound is the single 404 message for secrets: never existed, wrong
// probe, not finalized, wrong token or ticket all read the same.
const msgNotFound = "Nothing here. The link may be wrong, or the secret is gone."
