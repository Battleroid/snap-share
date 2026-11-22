package items

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

// Hooks and helpers for the albums and dashboard packages, which build on
// items without reaching into its internals.

// AlbumPage renders an album's share page. The dispatcher has already checked
// the album's expiry, visibility and password and counted the view; owner is
// whether the viewer owns the album.
type AlbumPage func(w http.ResponseWriter, r *http.Request, album *Item, owner bool)

type hooks struct {
	albumPage AlbumPage
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

// Viewable reports whether the request could open it directly: its owner
// always; anyone else while it is unexpired and unlisted, and, if it has a
// password, only once this browser has unlocked it.
func (s *Service) Viewable(r *http.Request, it *Item) bool {
	if p := auth.FromContext(r.Context()); p != nil && p.UserID == it.OwnerID {
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

// TypeLabel is a short lowercase type for lists ("png", "go", "link").
func TypeLabel(it *Item) string {
	if it.Kind == KindAlbum {
		return "album"
	}
	return typeLabel(it)
}

// DownloadName is the filename an item downloads as.
func DownloadName(it *Item) string { return downloadName(it) }

// ResolveExpiry turns an expiry spec (never, 1h, 1d, 7d, 30d, 90d or RFC 3339)
// into a time, capped by the site maximum; nil means never.
func (s *Service) ResolveExpiry(spec string) (*time.Time, error) {
	if !validExpirySpec(spec) {
		return nil, errExpiry
	}
	return parseExpiry(spec, s.now(), MaxExpiryDays.Get(s.d.Settings))
}

var errPasswordLong = web.Errorf(http.StatusBadRequest, "The password is too long.")

// HashPassword hashes an item password with the configured hasher.
func (s *Service) HashPassword(pw string) (string, error) {
	if len(pw) > 256 {
		return "", errPasswordLong
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
	r, err := s.resolve(ctx, prefs, options(opts), kind)
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
		ExpiresAt: exp, ExpiryAction: r.ExpiryAction, Naming: r.Naming,
	}, nil
}

// DeleteTx deletes it inside tx: the row, its search entry and stats, and its
// owner's usage. Call DeleteBlobs after the transaction commits.
func (s *Service) DeleteTx(ctx context.Context, tx *sql.Tx, it *Item) error {
	return s.deleteTx(ctx, tx, it)
}

// DeleteBlobs removes an item's stored content (after DeleteTx committed).
func (s *Service) DeleteBlobs(ctx context.Context, it *Item) { s.deleteBlobs(ctx, it) }

// Usage returns a user's stored bytes and quota (0: unlimited).
func (s *Service) Usage(ctx context.Context, userID int64) (used, quota int64, err error) {
	l, err := s.userLimits(ctx, s.d.DB.R, userID)
	if err != nil {
		return 0, 0, err
	}
	return l.used, l.quota, nil
}

// Patch is the PATCH /api/v1/items/{id} handler, for routes that edit an
// item of a particular kind (albums) the same way. It answers 404 unless the
// principal owns item {id}, writes the item JSON on success and returns any
// error for the caller to render.
func (s *Service) Patch(w http.ResponseWriter, r *http.Request) error { return s.apiPatch(w, r) }

// Fail renders an error returned by these helpers the way the items routes do.
func (s *Service) Fail(w http.ResponseWriter, r *http.Request, err error) { s.fail(w, r, err) }
