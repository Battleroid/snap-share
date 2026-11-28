package albums

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// The layouts an album page shows its members in: tiles, or one row each
// (the whole name, type and size beside a small preview).
const (
	ViewGrid = "grid"
	ViewList = "list"
)

func validView(v string) bool { return v == ViewGrid || v == ViewList }

var errView = web.Errorf(http.StatusBadRequest, "View must be grid or list.")

// DefaultView is the layout visitors get first: the owner's choice
// (album_prefs), else the grid.
func DefaultView(ctx context.Context, q items.Querier, albumID int64) (string, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT view FROM album_prefs WHERE album_id = ?`, albumID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !validView(v)) {
		return ViewGrid, nil
	}
	return v, err
}

// viewFor is the layout a page request asks for (?view=list), else def.
func viewFor(r *http.Request, def string) string {
	if v := r.URL.Query().Get("view"); validView(v) {
		return v
	}
	return def
}

// apiView handles PUT /api/v1/albums/{id}/view {"view": "grid"|"list"}:
// the layout visitors get by default. It answers {"view": …}.
func (s *Service) apiView(w http.ResponseWriter, r *http.Request) error {
	p, al, err := s.own(r)
	if err != nil {
		return err
	}
	if !items.MayEdit(p, al) { // a key without albums:read changes only its own albums
		return ErrNotFound
	}
	var req struct {
		View string `json:"view"`
	}
	if err := web.DecodeJSON(w, r, 1<<10, &req); err != nil {
		return err
	}
	if !validView(req.View) {
		return errView
	}
	err = s.d.DB.Tx(r.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.Context(), `INSERT INTO album_prefs (album_id, view) VALUES (?, ?)
			ON CONFLICT (album_id) DO UPDATE SET view = excluded.view`, al.ID, req.View)
		return err
	})
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]string{"view": req.View})
	return nil
}
