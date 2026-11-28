// Package albums implements albums: items of kind album holding an ordered
// list of their owner's other items. It serves the album API and the album
// share page (a gallery with a lightbox), which the items slug dispatcher
// hands over to through items.Service.SetAlbumPage.
//
// Being in an album never changes an item: the gallery shows a member only to
// viewers who could open that member directly, and the album's own password
// and expiry apply to the album page alone.
package albums

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// MaxMembers caps the number of items in one album.
const MaxMembers = 2000

// Deps are the service's dependencies.
type Deps struct {
	Log    *slog.Logger
	DB     *db.DB
	Items  *items.Service
	Render *web.Renderer
	Errors *web.ErrorPages
	Assets *web.Assets // optional; unused since the kit teaser replaced the hover module
	Now    func() time.Time
}

// Service implements the album routes and the album page.
type Service struct {
	d   Deps
	log *slog.Logger
	now func() time.Time
}

// New builds the service.
func New(d Deps) (*Service, error) {
	if d.DB == nil || d.Items == nil || d.Render == nil || d.Errors == nil {
		return nil, errors.New("albums: missing dependency")
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

// Register adds the album API routes. The album page is installed with
// items.Service.SetAlbumPage(s.Page).
func (s *Service) Register(r *web.Router) {
	read := web.Scope(auth.ScopeAlbumsRead)
	write := web.Scope(auth.ScopeAlbumsWrite)
	r.HandleFunc("GET /api/v1/albums", s.handle(s.apiList), read)
	r.HandleFunc("POST /api/v1/albums", s.handle(s.apiCreate), write)
	r.HandleFunc("GET /api/v1/albums/{id}", s.handle(s.apiGet), read)
	r.HandleFunc("PATCH /api/v1/albums/{id}", s.handle(s.apiPatch), write)
	r.HandleFunc("DELETE /api/v1/albums/{id}", s.handle(s.apiDelete), write)
	r.HandleFunc("POST /api/v1/albums/{id}/items", s.handle(s.apiItems), write)
	r.HandleFunc("PUT /api/v1/albums/{id}/view", s.handle(s.apiView), write)
}

func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			s.d.Items.Fail(w, r, err)
		}
	}
}

// Errors callers branch on.
var (
	ErrNotFound    = items.ErrNotFound
	errMembers     = web.Errorf(http.StatusNotFound, "One or more items were not found.")
	errNested      = web.Errorf(http.StatusBadRequest, "Albums cannot contain albums.")
	errTooMany     = web.Errorf(http.StatusBadRequest, "An album holds at most %d items.", MaxMembers)
	errUnverified  = web.Errorf(http.StatusForbidden, "Verify your email address first.")
	errNotAMember  = web.Errorf(http.StatusBadRequest, "Only items in the album can be ordered.")
	errDuplicateID = web.Errorf(http.StatusBadRequest, "An item is listed twice.")
	// Putting items in an album names them by id; a key that cannot read
	// items could otherwise collect their links from the album page.
	errMembersScope = web.Errorf(http.StatusForbidden, "Adding items to an album needs the items:read permission.")
)

// Album returns owner's album id (ErrNotFound for other users' albums and
// for items that are not albums).
func (s *Service) Album(ctx context.Context, q items.Querier, owner, id int64) (*items.Item, error) {
	list, err := items.SelectItems(ctx, q, `id = ? AND owner_id = ? AND kind = 'album'`, "", id, owner)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return list[0], nil
}

// Containing returns owner's albums that hold item id, by title.
func Containing(ctx context.Context, q items.Querier, owner, id int64) ([]*items.Item, error) {
	return items.SelectItems(ctx, q, `owner_id = ? AND kind = 'album' AND id IN (SELECT album_id FROM album_items WHERE item_id = ?)`,
		`ORDER BY lower(title), id`, owner, id)
}

// Members returns an album's items in album order.
func Members(ctx context.Context, q items.Querier, albumID int64) ([]*items.Item, error) {
	return items.SelectItems(ctx, q, `id IN (SELECT item_id FROM album_items WHERE album_id = ?)`,
		`ORDER BY (SELECT a.position FROM album_items a WHERE a.album_id = ? AND a.item_id = items.id), id`, albumID, albumID)
}

func memberIDs(ctx context.Context, q items.Querier, albumID int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT item_id FROM album_items WHERE album_id = ? ORDER BY position, item_id`, albumID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ownedMembers checks that every id is one of owner's items and not an album.
func ownedMembers(ctx context.Context, q items.Querier, owner int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			return errDuplicateID
		}
		seen[id] = true
	}
	for chunk := range slices.Chunk(ids, 500) {
		args := []any{owner}
		ph := make([]byte, 0, len(chunk)*2)
		for i, id := range chunk {
			if i > 0 {
				ph = append(ph, ',')
			}
			ph = append(ph, '?')
			args = append(args, id)
		}
		var n, albums int
		err := q.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(kind = 'album'), 0) FROM items WHERE owner_id = ? AND id IN (`+string(ph)+`)`, args...).Scan(&n, &albums) // #nosec G202 -- placeholders only
		if err != nil {
			return err
		}
		if n != len(chunk) {
			return errMembers
		}
		if albums > 0 {
			return errNested
		}
	}
	return nil
}

// NewAlbum validates options for a new album of owner's (their upload
// defaults apply) without writing anything.
func (s *Service) NewAlbum(ctx context.Context, owner int64, opts items.CreateOptions) (items.NewItem, error) {
	return s.d.Items.Prepare(ctx, owner, items.KindAlbum, opts)
}

// InsertTx creates the album n inside tx with members (owner's items, in order).
func (s *Service) InsertTx(ctx context.Context, tx *sql.Tx, n items.NewItem, members []int64) (*items.Item, error) {
	if len(members) > MaxMembers {
		return nil, errTooMany
	}
	if err := ownedMembers(ctx, tx, n.OwnerID, members); err != nil {
		return nil, err
	}
	al, _, err := s.d.Items.InsertTx(ctx, tx, n)
	if err != nil {
		return nil, err
	}
	if _, err := s.addTx(ctx, tx, al.ID, members); err != nil {
		return nil, err
	}
	return al, nil
}

// AddTx appends owner's items to owner's album albumID, skipping ones already
// in it, and returns how many were added.
func (s *Service) AddTx(ctx context.Context, tx *sql.Tx, owner, albumID int64, ids []int64) (int, error) {
	if _, err := s.Album(ctx, tx, owner, albumID); err != nil {
		return 0, err
	}
	if err := ownedMembers(ctx, tx, owner, ids); err != nil {
		return 0, err
	}
	return s.addTx(ctx, tx, albumID, ids)
}

func (s *Service) addTx(ctx context.Context, tx *sql.Tx, albumID int64, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var count int
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(max(position) + 1, 0) FROM album_items WHERE album_id = ?`, albumID).
		Scan(&count, &next); err != nil {
		return 0, err
	}
	now := db.Millis(s.now())
	added := 0
	for _, id := range ids {
		res, err := tx.ExecContext(ctx, `INSERT INTO album_items (album_id, item_id, position, added_at) VALUES (?, ?, ?, ?)
			ON CONFLICT DO NOTHING`, albumID, id, next, now)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
			next++
		}
	}
	if count+added > MaxMembers {
		return 0, errTooMany
	}
	return added, nil
}

// changeTx applies an items request: add, then remove, then order.
func (s *Service) changeTx(ctx context.Context, tx *sql.Tx, owner, albumID int64, req itemsReq) error {
	if _, err := s.Album(ctx, tx, owner, albumID); err != nil {
		return err
	}
	if err := ownedMembers(ctx, tx, owner, req.Add); err != nil {
		return err
	}
	if _, err := s.addTx(ctx, tx, albumID, req.Add); err != nil {
		return err
	}
	// Removing needs no ownership check: only this (owner's) album changes.
	for _, id := range req.Remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM album_items WHERE album_id = ? AND item_id = ?`, albumID, id); err != nil {
			return err
		}
	}
	if len(req.Order) == 0 {
		return nil
	}
	cur, err := memberIDs(ctx, tx, albumID)
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	next := make([]int64, 0, len(cur))
	for _, id := range req.Order {
		if seen[id] {
			return errDuplicateID
		}
		if !slices.Contains(cur, id) {
			return errNotAMember
		}
		seen[id] = true
		next = append(next, id)
	}
	for _, id := range cur {
		if !seen[id] {
			next = append(next, id)
		}
	}
	for pos, id := range next {
		if _, err := tx.ExecContext(ctx, `UPDATE album_items SET position = ? WHERE album_id = ? AND item_id = ?`, pos, albumID, id); err != nil {
			return err
		}
	}
	return nil
}

// Summary is an album with its member count.
type Summary struct {
	Album *items.Item
	Count int
}

// List returns owner's albums, newest first.
func (s *Service) List(ctx context.Context, owner int64) ([]Summary, error) {
	list, err := items.SelectItems(ctx, s.d.DB.R, `owner_id = ? AND kind = 'album'`, `ORDER BY created_at DESC, id DESC`, owner)
	if err != nil {
		return nil, err
	}
	counts, err := s.counts(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(list))
	for _, al := range list {
		out = append(out, Summary{Album: al, Count: counts[al.ID]})
	}
	return out, nil
}

func (s *Service) counts(ctx context.Context, owner int64) (map[int64]int, error) {
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT a.album_id, count(*) FROM album_items a JOIN items i ON i.id = a.album_id
		WHERE i.owner_id = ? GROUP BY a.album_id`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Cover is what a list shows for an album: its size and the first members
// with a picture.
type Cover struct {
	Count  int
	Thumbs []string // up to three image URLs
}

// Covers returns covers for owner's albums ids (for the dashboard).
func (s *Service) Covers(ctx context.Context, owner int64, ids []int64) (map[int64]Cover, error) {
	out := map[int64]Cover{}
	if len(ids) == 0 {
		return out, nil
	}
	counts, err := s.counts(ctx, owner)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		members, err := items.SelectItems(ctx, s.d.DB.R,
			`owner_id = ? AND id IN (SELECT item_id FROM album_items WHERE album_id = ?) AND (thumb_key IS NOT NULL OR mime LIKE 'image/%')`,
			`ORDER BY (SELECT a.position FROM album_items a WHERE a.album_id = ? AND a.item_id = items.id), id LIMIT 3`, owner, id, id)
		if err != nil {
			return nil, err
		}
		c := Cover{Count: counts[id]}
		for _, m := range members {
			if t := s.picture(m, true); t != "" {
				c.Thumbs = append(c.Thumbs, t)
			}
		}
		out[id] = c
	}
	return out, nil
}

// picture is an image URL for m: its thumbnail, or the image itself.
func (s *Service) picture(m *items.Item, viewer bool) string {
	u := s.d.Items.URLs(m, viewer)
	if u.Thumb != "" {
		return u.Thumb
	}
	if items.ViewKind(m) == "image" {
		return u.Raw
	}
	return ""
}

// ownerOf returns the principal, or 404 without one.
func ownerOf(r *http.Request) (*auth.Principal, error) {
	p := auth.FromContext(r.Context())
	if p == nil {
		return nil, ErrNotFound
	}
	return p, nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrNotFound
	}
	return id, nil
}
