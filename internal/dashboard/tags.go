package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// Tags are per-owner labels on items, matched by name ignoring case. Every
// change to an item's tags rewrites its items_fts tags column in the same
// transaction, so search finds items by tag.

// Tag limits.
const (
	MaxTagsPerItem  = 20
	MaxTagsPerOwner = 500
)

var (
	errTagExists   = web.Errorf(http.StatusConflict, "That tag exists.")
	errTagNotFound = web.Errorf(http.StatusNotFound, "No such tag.")
	errTooManyTags = web.Errorf(http.StatusBadRequest, "An item has at most %d tags.", MaxTagsPerItem)
	errOwnerTags   = web.Errorf(http.StatusBadRequest, "You have %d tags; delete some first.", MaxTagsPerOwner)
)

// Tag is one of an owner's tags with the number of items carrying it.
type Tag struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// normTags validates names and drops case-insensitive duplicates.
func normTags(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		n, err := items.TagName(raw)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, n) }) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Tags lists owner's tags by name.
func (s *Service) Tags(ctx context.Context, owner int64) ([]Tag, error) {
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT t.id, t.name, count(it.item_id) FROM tags t
		LEFT JOIN item_tags it ON it.tag_id = t.id WHERE t.owner_id = ? GROUP BY t.id ORDER BY t.name COLLATE NOCASE`, owner)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ItemTags returns the tag names of items ids, sorted by name.
func ItemTags(ctx context.Context, q items.Querier, ids []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	for chunk := range slices.Chunk(ids, 500) {
		if err := itemTagsChunk(ctx, q, chunk, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func itemTagsChunk(ctx context.Context, q items.Querier, ids []int64, out map[int64][]string) error {
	ph, args := placeholders(ids)
	rows, err := q.QueryContext(ctx, `SELECT it.item_id, t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id IN (`+ph+`) ORDER BY t.name COLLATE NOCASE`, args...) // #nosec G202 -- placeholders only
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			return err
		}
		out[id] = append(out[id], n)
	}
	return rows.Err()
}

// placeholders returns "?,?,…" and the ids as args.
func placeholders(ids []int64) (string, []any) {
	var b strings.Builder
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
		args[i] = id
	}
	return b.String(), args
}

// ensureTagsTx returns the ids of owner's tags names, creating missing ones.
func (s *Service) ensureTagsTx(ctx context.Context, tx *sql.Tx, owner int64, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	created := 0
	for _, n := range names {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE owner_id = ? AND name = ?`, owner, n).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := tx.ExecContext(ctx, `INSERT INTO tags (owner_id, name, created_at) VALUES (?, ?, ?)`, owner, n, db.Millis(s.now()))
			if err != nil {
				return nil, err
			}
			if id, err = res.LastInsertId(); err != nil {
				return nil, err
			}
			created++
		} else if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if created > 0 {
		var total int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tags WHERE owner_id = ?`, owner).Scan(&total); err != nil {
			return nil, err
		}
		if total > MaxTagsPerOwner {
			return nil, errOwnerTags
		}
	}
	return ids, nil
}

// reindexTagsTx rewrites the items_fts tags column of items ids.
func reindexTagsTx(ctx context.Context, tx *sql.Tx, ids []int64) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE items_fts SET tags = coalesce((SELECT group_concat(name, ' ') FROM
			(SELECT t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE it.item_id = ? ORDER BY t.name)), '') WHERE rowid = ?`,
			id, id); err != nil {
			return err
		}
	}
	return nil
}

// addTagsTx tags owner's items ids with names (the caller has checked ownership).
func (s *Service) addTagsTx(ctx context.Context, tx *sql.Tx, owner int64, ids []int64, names []string) error {
	tagIDs, err := s.ensureTagsTx(ctx, tx, owner, names)
	if err != nil {
		return err
	}
	for _, id := range ids {
		for _, t := range tagIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO item_tags (item_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, t); err != nil {
				return err
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM item_tags WHERE item_id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n > MaxTagsPerItem {
			return errTooManyTags
		}
	}
	return reindexTagsTx(ctx, tx, ids)
}

// removeTagsTx takes names off owner's items ids.
func (s *Service) removeTagsTx(ctx context.Context, tx *sql.Tx, owner int64, ids []int64, names []string) error {
	for _, id := range ids {
		for _, n := range names {
			if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE item_id = ? AND tag_id IN
				(SELECT id FROM tags WHERE owner_id = ? AND name = ?)`, id, owner, n); err != nil {
				return err
			}
		}
	}
	return reindexTagsTx(ctx, tx, ids)
}

// setTagsTx replaces the tags of owner's item id with names.
func (s *Service) setTagsTx(ctx context.Context, tx *sql.Tx, owner, id int64, names []string) error {
	if len(names) > MaxTagsPerItem {
		return errTooManyTags
	}
	tagIDs, err := s.ensureTagsTx(ctx, tx, owner, names)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE item_id = ?`, id); err != nil {
		return err
	}
	for _, t := range tagIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_tags (item_id, tag_id) VALUES (?, ?)`, id, t); err != nil {
			return err
		}
	}
	return reindexTagsTx(ctx, tx, []int64{id})
}

// taggedTx lists the items carrying tag id.
func taggedTx(ctx context.Context, tx *sql.Tx, tagID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT item_id FROM item_tags WHERE tag_id = ?`, tagID)
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

// API.

func (s *Service) apiTags(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	list, err := s.Tags(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"tags": list})
	return nil
}

type tagReq struct {
	Name string `json:"name"`
}

func (s *Service) apiTagCreate(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	var req tagReq
	if err := web.DecodeJSON(w, r, 4<<10, &req); err != nil {
		return err
	}
	name, err := items.TagName(req.Name)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var t Tag
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM tags WHERE owner_id = ? AND name = ?`, p.UserID, name).Scan(&one)
		if err == nil {
			return errTagExists
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		ids, err := s.ensureTagsTx(ctx, tx, p.UserID, []string{name})
		if err != nil {
			return err
		}
		t = Tag{ID: ids[0], Name: name}
		return nil
	})
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusCreated, t)
	return nil
}

// ownTagTx loads owner's tag {id}.
func ownTagTx(ctx context.Context, tx *sql.Tx, r *http.Request, owner int64) (Tag, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return Tag{}, errTagNotFound
	}
	t := Tag{ID: id}
	err = tx.QueryRowContext(ctx, `SELECT name FROM tags WHERE id = ? AND owner_id = ?`, id, owner).Scan(&t.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, errTagNotFound
	}
	return t, err
}

func (s *Service) apiTagRename(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	var req tagReq
	if err := web.DecodeJSON(w, r, 4<<10, &req); err != nil {
		return err
	}
	name, err := items.TagName(req.Name)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var t Tag
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if t, err = ownTagTx(ctx, tx, r, p.UserID); err != nil {
			return err
		}
		var other int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE owner_id = ? AND name = ?`, p.UserID, name).Scan(&other)
		if err == nil && other != t.ID {
			return errTagExists
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tags SET name = ? WHERE id = ?`, name, t.ID); err != nil {
			return err
		}
		t.Name = name
		ids, err := taggedTx(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		t.Count = len(ids)
		return reindexTagsTx(ctx, tx, ids)
	})
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, t)
	return nil
}

func (s *Service) apiTagDelete(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		t, err := ownTagTx(ctx, tx, r, p.UserID)
		if err != nil {
			return err
		}
		ids, err := taggedTx(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, t.ID); err != nil {
			return err
		}
		return reindexTagsTx(ctx, tx, ids)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ownItem loads owner's item {id} through q.
func ownItem(ctx context.Context, q items.Querier, r *http.Request, owner int64) (*items.Item, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, items.ErrNotFound
	}
	list, err := items.SelectItems(ctx, q, `id = ? AND owner_id = ?`, "", id, owner)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, items.ErrNotFound
	}
	return list[0], nil
}

func (s *Service) apiItemTags(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	it, err := ownItem(r.Context(), s.d.DB.R, r, p.UserID)
	if err != nil {
		return err
	}
	tags, err := ItemTags(r.Context(), s.d.DB.R, []int64{it.ID})
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"tags": orEmpty(tags[it.ID])})
	return nil
}

type itemTagsReq struct {
	Tags []string `json:"tags"`
}

func (s *Service) apiSetItemTags(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	var req itemTagsReq
	if err := web.DecodeJSON(w, r, 16<<10, &req); err != nil {
		return err
	}
	names, err := normTags(req.Tags)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var id int64
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		it, err := ownItem(ctx, tx, r, p.UserID)
		if err != nil {
			return err
		}
		if !items.MayEdit(p, it) { // a key without items:read tags only what it created
			return items.ErrNotFound
		}
		id = it.ID
		return s.setTagsTx(ctx, tx, p.UserID, it.ID, names)
	})
	if err != nil {
		return err
	}
	tags, err := ItemTags(ctx, s.d.DB.R, []int64{id})
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"tags": orEmpty(tags[id])})
	return nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
