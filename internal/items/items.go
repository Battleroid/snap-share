// Package items is the sharing core: files, pastes and short links under one
// slug namespace, their uploads (chunked, multipart and raw PUT), share pages,
// raw content serving with isolation headers, password unlocks, expiry, and
// the composer, dashboard and defaults pages.
package items

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/ratelimit"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/web"
)

// Kind is what an item is.
type Kind string

// Kinds.
const (
	KindFile  Kind = "file"
	KindPaste Kind = "paste"
	KindLink  Kind = "link"
	KindAlbum Kind = "album"
)

// Visibility says who can open an item.
type Visibility string

// Visibilities. Unlisted items open for anyone with the link; private ones
// only for their owner.
const (
	Unlisted Visibility = "unlisted"
	Private  Visibility = "private"
)

// Expiry actions.
const (
	ExpireDelete  = "delete"
	ExpirePrivate = "private"
)

// Item is one row of the items table.
type Item struct {
	ID           int64
	OwnerID      int64
	Kind         Kind
	Slug         string
	Title        string
	Filename     string
	MIME         string
	Size         int64
	SHA256       string
	Width        int64 // 0 when unknown
	Height       int64
	DurationMS   int64
	BlobKey      string
	ThumbKey     string
	PreviewKey   string
	RenderKey    string
	TargetURL    string
	Visibility   Visibility
	PasswordHash string
	ExpiresAt    *time.Time
	ExpiryAction string
	Views        int64
	Downloads    int64
	FirstOpened  *time.Time
	LastOpened   *time.Time
	OpenedCount  int64
	PreviewedAt  *time.Time // first preview-bot fetch (media+stats track)
	Meta         map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Nonce        string // per-item random value, bound into signed raw URLs
	CreatedByKey int64  // the API key that created it; 0 for a browser session
	RenderSize   int64  // bytes of the cached render, charged with Size
}

// Protected reports whether opening the item needs more than the link.
func (it *Item) Protected() bool { return it.Visibility == Private || it.PasswordHash != "" }

// Expired reports whether the item's expiry has passed at now.
func (it *Item) Expired(now time.Time) bool {
	return it.ExpiresAt != nil && !it.ExpiresAt.After(now)
}

// Name is the display name: title, then filename, then the slug.
func (it *Item) Name() string {
	switch {
	case it.Title != "":
		return it.Title
	case it.Filename != "":
		return it.Filename
	case it.Kind == KindLink:
		return it.TargetURL
	}
	return it.Slug
}

// Ext is the lowercase extension (without the dot) used in raw URLs.
func (it *Item) Ext() string {
	e := strings.ToLower(strings.TrimPrefix(path.Ext(it.Filename), "."))
	if e == "" && it.Kind == KindPaste {
		return "txt"
	}
	if !validExt(e) {
		return ""
	}
	return e
}

// MetaString returns a string value from Meta.
func (it *Item) MetaString(k string) string {
	s, _ := it.Meta[k].(string)
	return s
}

// LinkChargeBytes is what a short link counts toward its owner's quota.
const LinkChargeBytes = 1 << 10

// charge is what the item counts toward its owner's quota: its content and
// cached render for files and pastes, a nominal LinkChargeBytes for links,
// nothing for albums.
func (it *Item) charge() int64 { return charge(it.Kind, it.Size, it.RenderSize) }

func charge(k Kind, size, renderSize int64) int64 {
	switch k {
	case KindFile, KindPaste:
		return size + renderSize
	case KindLink:
		return LinkChargeBytes
	}
	return 0
}

// chargeSQL is charge() over the items table, for reconciling usage; bind
// LinkChargeBytes to its placeholder.
const chargeSQL = `CASE kind WHEN 'link' THEN ? WHEN 'album' THEN 0 ELSE size + render_size END`

// ErrNotFound is returned for a missing item.
var ErrNotFound = errors.New("item not found")

// Blobs is the content store items need: blob.Store plus resumable partials
// and the free-space check. *local.Store implements it.
type Blobs interface {
	blob.Store
	CheckFree(n int64) error
	CreatePartial(ctx context.Context) (string, error)
	PartialSize(name string) (int64, error)
	AppendPartial(ctx context.Context, name string, offset int64, r io.Reader) (int64, error)
	OpenPartial(name string) (io.ReadSeekCloser, error)
	CommitPartial(ctx context.Context, name string) (string, error)
	RemovePartial(name string) error
}

// PasswordHasher hashes and checks item passwords.
type PasswordHasher interface {
	Hash(pw string) (string, error)
	Verify(hash, pw string) (bool, error)
}

// Deps are the service's dependencies.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	DB       *db.DB
	Settings *settings.Store
	Jobs     *jobs.Queue // nil: no periodic tasks are registered
	Blobs    Blobs       // the files store (cfg.FilesDir)
	Render   *web.Renderer
	Errors   *web.ErrorPages

	// Passwords hashes item passwords. Nil uses a built-in Argon2id hasher.
	Passwords PasswordHasher
	// ExtraReserved adds slugs to ReservedNames.
	ExtraReserved []string
	// OnCreate, if set, runs after an item is created and before the response
	// is written (the media track makes image thumbnails here). The item is
	// re-read afterwards so the response reflects any changes.
	OnCreate func(ctx context.Context, it *Item)

	// SecretComposer, if set, supplies the data for the composer's Secret tab
	// (template "secret.composer"); nil hides the tab.
	SecretComposer func(r *http.Request) any

	// Stats receives visitor events (view, download, embed, play, click,
	// preview; see events.go). Nil records none. StatsKey is the visitor-key
	// secret (media+stats track).
	Stats    stats.Recorder
	StatsKey []byte
	// OnOpened, if set, runs after each counter flush commits, with the
	// items people opened since the previous flush (stats-ui+notify track:
	// open notifications). It runs on the flusher's goroutine.
	OnOpened func(ctx context.Context, opened []Opened)
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

// Service implements the items routes and background tasks.
type Service struct {
	d         Deps
	log       *slog.Logger
	now       func() time.Time
	passwords PasswordHasher

	unlockKeys [][]byte // [current, previous]
	rawKeys    [][]byte

	unlockLimit     *ratelimit.Limiter // per address and slug
	unlockItemLimit *ratelimit.Limiter // wrong passwords per item, all addresses
	unlockAddrLimit *ratelimit.Limiter // attempts per address, all items
	unlockHash      chan struct{}      // unlock hashing slots (password.go)
	deleteLimit     *ratelimit.Limiter
	missLimit       *ratelimit.Limiter // slug misses per address
	missAll         *ratelimit.Limiter // slug misses from all addresses
	createLimit     *ratelimit.Limiter // new items per user

	counts counters
	ev     events // visitor event capture (events.go)

	upMu sync.Mutex
	ups  map[string]*upState

	hooks hooks // hooks.go: set by other packages before serving
}

// New builds the service and registers its periodic tasks on d.Jobs.
func New(d Deps) (*Service, error) {
	if d.Cfg == nil || d.DB == nil || d.Settings == nil || d.Blobs == nil || d.Render == nil || d.Errors == nil {
		return nil, errors.New("items: missing dependency")
	}
	s := &Service{
		d:               d,
		log:             d.Log,
		now:             d.Now,
		passwords:       d.Passwords,
		unlockLimit:     ratelimit.New(5, time.Minute, 5),
		unlockItemLimit: ratelimit.New(unlockItemFails, time.Hour, unlockItemFails),
		unlockAddrLimit: ratelimit.New(unlockAddrAttempts, time.Minute, unlockAddrAttempts),
		unlockHash:      make(chan struct{}, unlockSlots),
		deleteLimit:     ratelimit.New(20, time.Minute, 20),
		missLimit:       ratelimit.New(MissesPerMinute, time.Minute, MissesPerMinute),
		missAll:         ratelimit.New(MissesPerMinuteAll, time.Minute, MissesPerMinuteAll),
		createLimit:     ratelimit.New(CreatesPerMinute, time.Minute, CreatesBurst),
		counts:          counters{m: map[int64]*counterDelta{}},
		ups:             map[string]*upState{},
		ev:              newEvents(d),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.passwords == nil {
		s.passwords = argon2Hasher{}
	}
	for _, k := range [][]byte{d.Cfg.SecretKey, d.Cfg.SecretKeyPrevious} {
		if len(k) == 0 {
			continue
		}
		uk, err := hkdf.Key(sha256.New, k, nil, "snap items unlock cookie v1", 32)
		if err != nil {
			return nil, err
		}
		rk, err := hkdf.Key(sha256.New, k, nil, "snap items raw url v1", 32)
		if err != nil {
			return nil, err
		}
		s.unlockKeys = append(s.unlockKeys, uk)
		s.rawKeys = append(s.rawKeys, rk)
	}
	if len(s.unlockKeys) == 0 {
		return nil, errors.New("items: secret key is required")
	}
	if d.Jobs != nil {
		d.Jobs.Every("items.counters", 10*time.Second, s.FlushCounters)
		d.Jobs.Every("items.expiry", time.Minute, s.Sweep)
		d.Jobs.Every("items.uploads.reap", 15*time.Minute, s.ReapUploads)
		d.Jobs.Every("items.quota.reconcile", time.Hour, s.Reconcile)
		d.Jobs.Every("items.orphans", time.Hour, s.SweepOrphans)
	}
	return s, nil
}

// Register adds the items routes. The slug dispatcher is the router's fallback.
// SetSecretComposer installs the Secret tab's data source after construction
// (the secrets service is built after items). Call before serving.
func (s *Service) SetSecretComposer(f func(r *http.Request) any) { s.d.SecretComposer = f }

func (s *Service) Register(r *web.Router) {
	write := web.Scope(auth.ScopeItemsWrite)
	read := web.Scope(auth.ScopeItemsRead)

	// Browser resumable uploads.
	r.HandleFunc("POST /api/v1/uploads", s.handle(s.uploadInit), write)
	r.HandleFunc("GET /api/v1/uploads/{id}", s.handle(s.uploadStatus), write)
	r.HandleFunc("HEAD /api/v1/uploads/{id}", s.handle(s.uploadHead), write)
	r.HandleFunc("PUT /api/v1/uploads/{id}", s.handle(s.uploadPut), write, web.LargeBody())
	r.HandleFunc("POST /api/v1/uploads/{id}/complete", s.handle(s.uploadComplete), write)
	r.HandleFunc("DELETE /api/v1/uploads/{id}", s.handle(s.uploadAbort), write)
	// One-shot uploads (ShareX, curl, sp).
	r.HandleFunc("POST /api/v1/upload", s.handle(s.uploadMultipart), write, web.LargeBody())
	r.HandleFunc("PUT /api/v1/upload/{filename}", s.handle(s.uploadPutRaw), write, web.LargeBody())
	r.HandleFunc("POST /api/v1/pastes", s.handle(s.createPaste), write, web.LargeBody())
	r.HandleFunc("POST /api/v1/links", s.handle(s.createLink), write)

	r.HandleFunc("GET /api/v1/items", s.handle(s.apiList), read)
	r.HandleFunc("GET /api/v1/items/{id}", s.handle(s.apiGet), read)
	r.HandleFunc("PATCH /api/v1/items/{id}", s.handle(s.apiPatch), write)
	r.HandleFunc("DELETE /api/v1/items/{id}", s.handle(s.apiDelete), web.Scope(auth.ScopeItemsDelete))

	// Deletion by token (ShareX DeletionURL); the token travels in the body.
	r.HandleFunc("GET /x", s.deletePage, web.Public())
	r.HandleFunc("POST /api/v1/delete/lookup", s.handle(s.tokenLookup), web.Public(), web.Redact())
	r.HandleFunc("POST /api/v1/delete", s.handle(s.tokenDelete), web.Public(), web.Redact())

	r.HandleFunc("POST /unlock/{slug}", s.unlock, web.Public(), web.Redact())

	r.HandleFunc("POST /dashboard/items/{id}/delete", s.handle(s.dashboardDelete), web.Scope(auth.ScopeItemsDelete))
	r.HandleFunc("GET /settings/defaults", s.handle(s.defaultsPage), web.SignedIn())
	r.HandleFunc("POST /settings/defaults", s.handle(s.defaultsSave), web.SignedIn())

	r.Fallback(http.HandlerFunc(s.dispatch), web.Public())
}

// handle adapts a handler that returns an error.
func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			s.fail(w, r, err)
		}
	}
}

// Error responses for conditions callers branch on.
var (
	errQuota       = web.Errorf(http.StatusRequestEntityTooLarge, "That would put you over your storage quota.")
	errBlocked     = web.Errorf(http.StatusUnsupportedMediaType, "This file type is not allowed.")
	errUnverified  = web.Errorf(http.StatusForbidden, "Verify your email address first.")
	errSlugTaken   = web.Errorf(http.StatusConflict, "That name is taken.")
	errNoAccount   = web.Errorf(http.StatusForbidden, "Your account cannot upload.")
	errCreateLimit = web.Errorf(http.StatusTooManyRequests, "You are creating items too quickly. Try again in a minute.")
)

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *web.APIError
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &ae):
		s.d.Errors.Error(w, r, ae.Status, ae.Msg)
	case errors.As(err, &mbe):
		s.d.Errors.Error(w, r, http.StatusRequestEntityTooLarge, "The upload is over your size limit.")
	case errors.Is(err, blob.ErrInsufficientSpace):
		s.d.Errors.Error(w, r, http.StatusInsufficientStorage, "The server is low on disk space.")
	case errors.Is(err, ErrNotFound):
		s.d.Errors.NotFound(w, r)
	case errors.Is(err, context.Canceled) && r.Context().Err() != nil:
		// The client went away; nobody is listening for a response.
	default:
		s.log.Error("items request failed", "method", r.Method, "err", err)
		s.d.Errors.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
	}
}

// creator returns the principal if it may create content.
func creator(r *http.Request) (*auth.Principal, error) {
	p := auth.FromContext(r.Context())
	if p == nil {
		return nil, web.Errorf(http.StatusUnauthorized, "Sign in first.")
	}
	if !p.EmailVerified {
		return nil, errUnverified
	}
	return p, nil
}

// Creation budget per user, shared by sessions and keys: uploads (counted
// when they start), pastes and links. Clients in SNAP_RATELIMIT_EXEMPT are
// not limited.
const (
	CreatesPerMinute = 120
	CreatesBurst     = 60
)

// allowCreate spends one creation from p's budget.
func (s *Service) allowCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) error {
	if clientip.In(clientip.From(r.Context()), s.d.Cfg.RateLimitExempt) {
		return nil
	}
	ok, wait := s.createLimit.Allow("create:" + strconv.FormatInt(p.UserID, 10))
	if ok {
		return nil
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(wait.Seconds()+0.999))))
	return errCreateLimit
}

func (s *Service) origin() string { return s.d.Cfg.Origin() }

// PublicURL is the share page URL for a slug.
func (s *Service) PublicURL(slug string) string { return s.origin() + "/" + slug }
