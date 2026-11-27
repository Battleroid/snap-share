package items

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
)

// Hooks and helpers for the albums and dashboard packages, which build on
// items without reaching into its internals.

// AlbumPage renders an album's share page. The dispatcher has already checked
// the album's expiry, visibility and password and counted the view; owner is
// whether the viewer owns the album.
type AlbumPage func(w http.ResponseWriter, r *http.Request, album *Item, owner bool)

type hooks struct {
	albumPage    AlbumPage
	albumMembers func(ctx context.Context, albumID int64) ([]*Item, error)
	mediaJobs    MediaJobStates
}

// SetAlbumMembers installs the lookup of an album's members in order, for
// its oEmbed response. Call it before serving.
func (s *Service) SetAlbumMembers(f func(ctx context.Context, albumID int64) ([]*Item, error)) {
	s.hooks.albumMembers = f
}

// SetAlbumPage installs the album share page. Call it before serving; without
// one, album slugs answer 404.
func (s *Service) SetAlbumPage(h AlbumPage) { s.hooks.albumPage = h }

// albumPage is the dispatcher's branch for album slugs.
func (s *Service) albumPage(w http.ResponseWriter, r *http.Request, it *Item, owner bool) {
	if s.hooks.albumPage == nil {
		s.notFound(w, r)
		return
	}
	if it.PasswordHash != "" && !owner && !s.unlocked(r, it) {
		s.unlockPage(w, r, it, http.StatusOK, "")
		return
	}
	s.countView(r, it, owner)
	s.hooks.albumPage(w, r, it, owner)
}

// IsOwner reports whether p gets the owner's view of it on the public routes
// (share pages, content, unlocks, album pages, signed raw URLs): the owner's
// browser session, or one of the owner's API keys holding items:read. Any
// other key of the owner is treated like a visitor, so it never reads more
// than its scopes allow through the API.
func IsOwner(p *auth.Principal, it *Item) bool {
	return p != nil && p.UserID == it.OwnerID && (!p.ViaKey() || p.Can(auth.ScopeItemsRead))
}

// Viewable reports whether the request could open it directly: its owner
// (IsOwner) always; anyone else while it is unexpired and unlisted, and, if
// it has a password, only once this browser has unlocked it.
func (s *Service) Viewable(r *http.Request, it *Item) bool {
	if IsOwner(auth.FromContext(r.Context()), it) {
		return true
	}
	if it.Expired(s.now()) || it.Visibility == Private {
		return false
	}
	return it.PasswordHash == "" || s.unlocked(r, it)
}

// URLs are an item's addresses.
type URLs struct {
	Page     string
	Raw      string // "" without content
	Download string
	Thumb    string // "" without a thumbnail
	Preview  string // "" without a hover teaser
}

// URLs returns it's addresses. viewer says the requester may open it (signed
// raw-origin URLs for protected items).
func (s *Service) URLs(it *Item, viewer bool) URLs {
	u := URLs{Page: s.PublicURL(it.Slug)}
	if it.BlobKey != "" {
		u.Raw = s.contentURL(it, modeRaw, viewer)
		u.Download = s.contentURL(it, modeDownload, viewer)
	}
	if it.Kind == KindGist {
		u.Download = s.contentURL(it, modeDownload, viewer) // the zip
	}
	if it.ThumbKey != "" {
		u.Thumb = s.contentURL(it, modeThumb, viewer)
	}
	if it.PreviewKey != "" {
		u.Preview = s.contentURL(it, modePreview, viewer)
	}
	return u
}

// ViewKind is how a share page shows it: image, video, audio, pdf, text, markdown or file.
func ViewKind(it *Item) string { return viewKind(it.Kind, it.MIME, it.MetaString("lang")) }

// DownloadName is the filename an item downloads as.
func DownloadName(it *Item) string { return downloadName(it) }

// RenameName is the filename the Edit drawer offers to change: a file's
// download name, "" for anything that cannot be renamed (PATCH filename).
func RenameName(it *Item) string {
	if it.Kind != KindFile {
		return ""
	}
	return downloadName(it)
}

// ResolveExpiry turns an expiry spec (never, 1h, 1d, 7d, 30d, 90d or RFC 3339)
// into a time, capped by the site maximum; nil means never.
func (s *Service) ResolveExpiry(spec string) (*time.Time, error) {
	if !validExpirySpec(spec) {
		return nil, errExpiry
	}
	return parseExpiry(spec, s.now(), MaxExpiryDays.Get(s.d.Settings))
}

// HashPassword checks a new item password against the policy
// (MinItemPasswordLen) and hashes it with the configured hasher.
func (s *Service) HashPassword(pw string) (string, error) {
	if err := checkItemPassword(pw); err != nil {
		return "", err
	}
	return s.passwords.Hash(pw)
}

// CreateOptions are the share options a new item takes, as in the API.
type CreateOptions struct {
	Title, Expires, ExpiryAction, Visibility, Password, Naming, Slug string
	Length                                                           int
}

// Prepare validates opts against owner's upload defaults and returns a
// NewItem of kind with them applied (title, expiry, visibility, password and
// naming), ready for InsertTx.
func (s *Service) Prepare(ctx context.Context, owner int64, kind Kind, opts CreateOptions) (NewItem, error) {
	prefs, err := LoadPrefs(ctx, s.d.DB, owner)
	if err != nil {
		return NewItem{}, err
	}
	r, err := s.resolve(ctx, owner, prefs, options(opts), kind)
	if err != nil {
		return NewItem{}, err
	}
	exp, err := s.expiresAt(r)
	if err != nil {
		return NewItem{}, err
	}
	if r.Naming.Scheme == NamingOriginal {
		r.Naming.Scheme = NamingRandom
	}
	return NewItem{
		OwnerID: owner, Kind: kind, Title: r.Title, Visibility: r.Visibility, PasswordHash: r.PasswordHash,
		ExpiresAt: exp, ExpiryAction: r.ExpiryAction, Naming: r.Naming, CreatedByKey: r.KeyID,
	}, nil
}

// DeleteTx deletes it inside tx: the row, its search entry and stats, and its
// owner's usage. Call DeleteBlobs with the same it after the transaction
// commits (a gist's file keys are read onto it here).
func (s *Service) DeleteTx(ctx context.Context, tx *sql.Tx, it *Item) error {
	return s.deleteTx(ctx, tx, it)
}

// DeleteBlobs removes an item's stored content, a gist's files included
// (after DeleteTx committed).
func (s *Service) DeleteBlobs(ctx context.Context, it *Item) { s.deleteBlobs(ctx, it) }

// Usage returns a user's stored bytes and quota (0: unlimited).
func (s *Service) Usage(ctx context.Context, userID int64) (used, quota int64, err error) {
	l, err := s.userLimits(ctx, s.d.DB.R, userID)
	if err != nil {
		return 0, 0, err
	}
	return l.used, l.quota, nil
}

// Limits returns a user's stored bytes, quota (0: unlimited) and the
// largest file they may upload, in one read.
func (s *Service) Limits(ctx context.Context, userID int64) (used, quota, maxFile int64, err error) {
	l, err := s.userLimits(ctx, s.d.DB.R, userID)
	if err != nil {
		return 0, 0, 0, err
	}
	return l.used, l.quota, l.maxUpload, nil
}

// Patch is the PATCH /api/v1/items/{id} handler, for routes that edit an
// item of a particular kind (albums) the same way. It answers 404 unless the
// principal owns item {id}, writes the item JSON on success and returns any
// error for the caller to render.
func (s *Service) Patch(w http.ResponseWriter, r *http.Request) error { return s.apiPatch(w, r) }

// Fail renders an error returned by these helpers the way the items routes do.
func (s *Service) Fail(w http.ResponseWriter, r *http.Request, err error) { s.fail(w, r, err) }

// MediaMeta is " · W×H · m:ss" for media with known dimensions or length, else "".
func MediaMeta(it *Item) string { return mediaMeta(it) }
