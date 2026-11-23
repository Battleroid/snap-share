package items

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/stats"
)

const itemCols = `id, owner_id, kind, slug, title, filename, mime, size, sha256, width, height, duration_ms,
	blob_key, thumb_key, preview_key, render_key, target_url, visibility, password_hash, expires_at, expiry_action,
	views, downloads, first_opened_at, last_opened_at, opened_count, meta, created_at, updated_at, previewed_at,
	nonce, created_by_key, render_size`

type scanner interface{ Scan(...any) error }

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func scanItem(sc scanner) (*Item, error) {
	var (
		it                              Item
		sha, bk, tk, pk, rk, target, pw sql.NullString
		w, h, dur, exp, first, last     sql.NullInt64
		previewed, byKey                sql.NullInt64
		meta                            string
		created, updated                int64
	)
	err := sc.Scan(&it.ID, &it.OwnerID, &it.Kind, &it.Slug, &it.Title, &it.Filename, &it.MIME, &it.Size, &sha, &w, &h, &dur,
		&bk, &tk, &pk, &rk, &target, &it.Visibility, &pw, &exp, &it.ExpiryAction,
		&it.Views, &it.Downloads, &first, &last, &it.OpenedCount, &meta, &created, &updated, &previewed,
		&it.Nonce, &byKey, &it.RenderSize)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	it.SHA256, it.BlobKey, it.ThumbKey, it.PreviewKey, it.RenderKey = sha.String, bk.String, tk.String, pk.String, rk.String
	it.TargetURL, it.PasswordHash = target.String, pw.String
	it.Width, it.Height, it.DurationMS = w.Int64, h.Int64, dur.Int64
	it.ExpiresAt, it.FirstOpened, it.LastOpened = db.NullTime(exp), db.NullTime(first), db.NullTime(last)
	it.PreviewedAt = db.NullTime(previewed)
	it.CreatedByKey = byKey.Int64
	it.CreatedAt, it.UpdatedAt = db.Time(created), db.Time(updated)
	if meta != "" && meta != "{}" {
		_ = json.Unmarshal([]byte(meta), &it.Meta)
	}
	if it.Meta == nil {
		it.Meta = map[string]any{}
	}
	return &it, nil
}

func getItem(ctx context.Context, q querier, where string, args ...any) (*Item, error) {
	return scanItem(q.QueryRowContext(ctx, `SELECT `+itemCols+` FROM items WHERE `+where, args...))
}

// ExistsTx reports whether item id exists (the stats writer's
// WriterOptions.ItemExists).
func ExistsTx(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM items WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Get returns the item with id.
func (s *Service) Get(ctx context.Context, id int64) (*Item, error) {
	return getItem(ctx, s.d.DB.R, `id = ?`, id)
}

// GetBySlug returns the item with slug (case-insensitive).
func (s *Service) GetBySlug(ctx context.Context, slug string) (*Item, error) {
	if !ValidSlug(slug) {
		return nil, ErrNotFound
	}
	return getItem(ctx, s.d.DB.R, `slug = ?`, slug)
}

// ListOwned returns owner's items, newest first, and the total count.
func (s *Service) ListOwned(ctx context.Context, owner int64, limit, offset int) ([]*Item, int, error) {
	var total int
	if err := s.d.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE owner_id = ?`, owner).Scan(&total); err != nil {
		return nil, 0, err
	}
	out, err := queryAll(ctx, s.d.DB.R, `SELECT `+itemCols+` FROM items WHERE owner_id = ?
		ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, func(rows *sql.Rows) (*Item, error) { return scanItem(rows) },
		owner, limit, offset)
	return out, total, err
}

// queryAll reads every row with scan and closes the rows before returning,
// so no rows stay open while the caller does I/O.
func queryAll[T any](ctx context.Context, q querier, query string, scan func(*sql.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// NewItem describes an item to insert.
type NewItem struct {
	OwnerID      int64
	Kind         Kind
	Title        string
	Filename     string
	MIME         string
	Size         int64
	SHA256       string
	BlobKey      string
	RenderKey    string
	RenderSize   int64 // bytes of the RenderKey blob
	TargetURL    string
	Visibility   Visibility
	PasswordHash string
	ExpiresAt    *time.Time
	ExpiryAction string
	Meta         map[string]any
	Naming       Naming
	SearchText   string // paste or text excerpt for search
	SearchNames  string // file names for search; "" uses Filename
	CreatedByKey int64  // the API key creating it (0: a browser session)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullMillis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return db.Millis(*t)
}

// newToken returns a 256-bit URL-safe token and its SHA-256 hex.
func newToken() (string, string) {
	var b [32]byte
	_, _ = rand.Read(b[:])
	tok := base64.RawURLEncoding.EncodeToString(b[:])
	return tok, tokenHash(tok)
}

// newNonce returns 128 random bits as hex (items.nonce).
func newNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// slugTaken reports whether slug is in use (case-insensitive), reserved, or
// held for its previous owner after a delete, expiry or rename (owner may
// take back their own released slugs).
func (s *Service) slugTaken(ctx context.Context, q querier, slug string, owner int64) (bool, error) {
	if IsReserved(slug, s.d.ExtraReserved...) {
		return true, nil
	}
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM items WHERE slug = ?
		UNION ALL SELECT 1 FROM released_slugs WHERE slug = ? AND previous_owner != ? AND released_at > ?`,
		slug, slug, owner, db.Millis(s.slugHeldSince())).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// slugHeldSince is the release time after which a freed slug is still held
// for its previous owner.
func (s *Service) slugHeldSince() time.Time {
	return s.now().AddDate(0, 0, -SlugCooldownDays.Get(s.d.Settings))
}

// releaseSlugTx records that owner gave up slug (delete, expiry, rename).
func (s *Service) releaseSlugTx(ctx context.Context, tx *sql.Tx, slug string, owner int64) error {
	if SlugCooldownDays.Get(s.d.Settings) <= 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO released_slugs (slug, previous_owner, released_at) VALUES (?, ?, ?)
		ON CONFLICT (slug) DO UPDATE SET slug = excluded.slug, previous_owner = excluded.previous_owner, released_at = excluded.released_at`,
		slug, owner, db.Millis(s.now()))
	return err
}

// pickSlug finds a free slug for n inside tx.
func (s *Service) pickSlug(ctx context.Context, tx *sql.Tx, n Naming, filename string, owner int64) (string, error) {
	for attempt := range 20 {
		c := n.candidate(attempt, filename)
		if c == "" {
			break
		}
		if !ValidSlug(c) {
			if n.Scheme == NamingCustom {
				return "", errSlugInvalid
			}
			continue
		}
		taken, err := s.slugTaken(ctx, tx, c, owner)
		if err != nil {
			return "", err
		}
		if !taken {
			return c, nil
		}
	}
	if n.Scheme == NamingCustom {
		return "", errSlugTaken
	}
	// A reserved stem ("sitemap.xml") takes every suffixed candidate too.
	if n.Scheme == NamingOriginal {
		return s.pickSlug(ctx, tx, Naming{Scheme: NamingRandom}, "", owner)
	}
	return "", errors.New("no free slug after 20 attempts")
}

// InsertTx inserts n (choosing its slug) inside tx and returns the item and
// its deletion token. It also indexes the item for search. Quota is the
// caller's business.
func (s *Service) InsertTx(ctx context.Context, tx *sql.Tx, n NewItem) (*Item, string, error) {
	slug, err := s.pickSlug(ctx, tx, n.Naming, n.Filename, n.OwnerID)
	if err != nil {
		return nil, "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM released_slugs WHERE slug = ?`, slug); err != nil {
		return nil, "", err
	}
	if n.Visibility == "" {
		n.Visibility = Unlisted
	}
	if n.ExpiryAction == "" {
		n.ExpiryAction = ExpireDelete
	}
	meta := "{}"
	if len(n.Meta) > 0 {
		b, err := json.Marshal(n.Meta)
		if err != nil {
			return nil, "", err
		}
		meta = string(b)
	}
	tok, hash := newToken()
	now := db.Millis(s.now())
	if n.RenderKey == "" && n.Kind != KindGist { // a gist's renders are its files'
		n.RenderSize = 0
	}
	if n.SearchNames == "" {
		n.SearchNames = n.Filename
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO items (owner_id, kind, slug, title, filename, mime, size, sha256,
		blob_key, render_key, render_size, target_url, visibility, password_hash, expires_at, expiry_action, deletion_token_hash, meta,
		created_at, updated_at, nonce, created_by_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.OwnerID, string(n.Kind), slug, n.Title, n.Filename, n.MIME, n.Size, nullStr(n.SHA256),
		nullStr(n.BlobKey), nullStr(n.RenderKey), n.RenderSize, nullStr(n.TargetURL), string(n.Visibility), nullStr(n.PasswordHash),
		nullMillis(n.ExpiresAt), n.ExpiryAction, hash, meta, now, now, newNonce(), nullInt(n.CreatedByKey))
	if err != nil {
		return nil, "", fmt.Errorf("insert item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO items_fts (rowid, title, filename, body, target_url) VALUES (?, ?, ?, ?, ?)`,
		id, n.Title, n.SearchNames, n.SearchText, n.TargetURL); err != nil {
		return nil, "", fmt.Errorf("index item: %w", err)
	}
	it, err := getItem(ctx, tx, `id = ?`, id)
	return it, tok, err
}

// reindexTx refreshes the searchable title, filename and link target. A
// gist's file names, indexed at creation, never change.
func reindexTx(ctx context.Context, tx *sql.Tx, it *Item) error {
	if it.Kind == KindGist {
		_, err := tx.ExecContext(ctx, `UPDATE items_fts SET title = ? WHERE rowid = ?`, it.Title, it.ID)
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE items_fts SET title = ?, filename = ?, target_url = ? WHERE rowid = ?`,
		it.Title, it.Filename, it.TargetURL, it.ID)
	return err
}

// Delete removes an item, settles its owner's usage and deletes its blobs.
func (s *Service) Delete(ctx context.Context, id int64) error {
	var it *Item
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if it, err = getItem(ctx, tx, `id = ?`, id); err != nil {
			return err
		}
		return s.deleteTx(ctx, tx, it)
	})
	if err != nil {
		return err
	}
	s.deleteBlobs(ctx, it)
	return nil
}

// accountGone marks a context deleting an account's items (DeleteOwnedBy).
type accountGone struct{}

// deleteTx deletes it inside tx. A gist's files are read onto it first:
// their rows go with the item, and deleteBlobs needs their keys.
func (s *Service) deleteTx(ctx context.Context, tx *sql.Tx, it *Item) error {
	if err := LoadGistFiles(ctx, tx, it); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items WHERE id = ?`, it.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid = ?`, it.ID); err != nil {
		return err
	}
	if err := stats.DeleteItemStats(ctx, tx, it.ID); err != nil {
		return err
	}
	// The slug stays with its owner for the cooldown. Removed by someone
	// else (an admin's moderation) or with the owner's account (whose id a
	// new account could get), it is held for nobody.
	holder := it.OwnerID
	if p := auth.FromContext(ctx); (p != nil && p.UserID != it.OwnerID) || ctx.Value(accountGone{}) != nil {
		holder = 0
	}
	if err := s.releaseSlugTx(ctx, tx, it.Slug, holder); err != nil {
		return err
	}
	if c := it.charge(); c > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET used_bytes = max(used_bytes - ?, 0), updated_at = ? WHERE id = ?`,
			c, db.Millis(s.now()), it.OwnerID); err != nil {
			return err
		}
	}
	// Counts batched for it since the last flush are dropped; the flush
	// also skips ids that no longer exist.
	s.forgetCounts(it.ID)
	return nil
}

// deleteBlobs deletes the item's blobs, and a gist's files and renders (as
// deleteTx loaded them).
func (s *Service) deleteBlobs(ctx context.Context, it *Item) {
	ctx = context.WithoutCancel(ctx)
	keys := []string{it.BlobKey, it.ThumbKey, it.PreviewKey, it.RenderKey}
	for _, f := range it.Files {
		keys = append(keys, f.BlobKey, f.RenderKey)
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		if err := s.d.Blobs.Delete(ctx, k); err != nil {
			s.log.Warn("delete blob", "item", it.ID, "err", err)
		}
	}
}

// DeleteOwnedBy deletes every item of a user (call before deleting the user,
// whose rows cascade but whose blobs would not).
func (s *Service) DeleteOwnedBy(ctx context.Context, owner int64) error {
	ctx = context.WithValue(ctx, accountGone{}, true)
	for {
		list, _, err := s.ListOwned(ctx, owner, 200, 0)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return nil
		}
		for _, it := range list {
			if err := s.Delete(ctx, it.ID); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}
}

// Media is what the media package records for a file item.
type Media struct {
	ThumbKey   string // "" leaves the current value
	PreviewKey string
	Width      int64 // 0 leaves the current value
	Height     int64
	DurationMS int64
}

// SetMedia stores thumbnail/teaser keys and dimensions, deleting replaced blobs.
// If the item is gone the new blobs are deleted and ErrNotFound is returned.
func (s *Service) SetMedia(ctx context.Context, id int64, m Media) error {
	var old *Item
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if old, err = getItem(ctx, tx, `id = ?`, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE items SET
			thumb_key = coalesce(?, thumb_key), preview_key = coalesce(?, preview_key),
			width = coalesce(?, width), height = coalesce(?, height), duration_ms = coalesce(?, duration_ms),
			updated_at = ? WHERE id = ?`,
			nullStr(m.ThumbKey), nullStr(m.PreviewKey), nullInt(m.Width), nullInt(m.Height), nullInt(m.DurationMS),
			db.Millis(s.now()), id)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		for _, k := range []string{m.ThumbKey, m.PreviewKey} {
			if k != "" {
				_ = s.d.Blobs.Delete(ctx, k)
			}
		}
		return err
	}
	if err != nil {
		return err
	}
	if m.ThumbKey != "" && old.ThumbKey != "" && old.ThumbKey != m.ThumbKey {
		_ = s.d.Blobs.Delete(ctx, old.ThumbKey)
	}
	if m.PreviewKey != "" && old.PreviewKey != "" && old.PreviewKey != m.PreviewKey {
		_ = s.d.Blobs.Delete(ctx, old.PreviewKey)
	}
	return nil
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
