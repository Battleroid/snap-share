package dashboard

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// Selection limits.
const (
	MaxIDs      = 1000  // ids in one request
	MaxMatching = 10000 // items "select all matching" may act on
)

var (
	errSelection = web.Errorf(http.StatusBadRequest, "Send either ids or filter, expected and created_before.")
	errNoItems   = web.Errorf(http.StatusBadRequest, "Select at least one item.")
	errTooManyID = web.Errorf(http.StatusBadRequest, "Send at most %d ids; use a filter for more.", MaxIDs)
	errMatching  = web.Errorf(http.StatusBadRequest, "Act on at most %d items at once.", MaxMatching)
	errMissing   = web.Errorf(http.StatusNotFound, "One or more items were not found.")
	errAction    = web.Errorf(http.StatusBadRequest, "Unknown action.")
)

// bulkReq is POST /api/v1/items/bulk: a selection (ids, or a filter with the
// count the client saw and a created_before bound) and one action.
type bulkReq struct {
	IDs           []int64         `json:"ids"`
	Filter        *string         `json:"filter"`
	Expected      *int            `json:"expected"`
	CreatedBefore string          `json:"created_before"`
	Action        string          `json:"action"`
	Params        json.RawMessage `json:"params"`
}

// selection is a parsed bulkReq selection.
type selection struct {
	ids      []int64
	filter   *items.Filter
	expected int
}

func parseSelection(req bulkReq) (selection, error) {
	switch {
	case req.Filter == nil && len(req.IDs) > 0:
		if req.Expected != nil || req.CreatedBefore != "" {
			return selection{}, errSelection
		}
		if len(req.IDs) > MaxIDs {
			return selection{}, errTooManyID
		}
		ids := make([]int64, 0, len(req.IDs))
		for _, id := range req.IDs {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		return selection{ids: ids}, nil
	case req.Filter != nil && len(req.IDs) == 0:
		if req.Expected == nil || req.CreatedBefore == "" {
			return selection{}, errSelection
		}
		v, err := url.ParseQuery(*req.Filter)
		if err != nil {
			return selection{}, web.Errorf(http.StatusBadRequest, "The filter is not a query string.")
		}
		v.Set("created_before", req.CreatedBefore)
		f, err := items.ParseFilter(v)
		if err != nil {
			return selection{}, err
		}
		return selection{filter: &f, expected: *req.Expected}, nil
	case req.Filter == nil && len(req.IDs) == 0 && req.IDs != nil:
		return selection{}, errNoItems
	}
	return selection{}, errSelection
}

// resolve loads owner's items in sel through q. Ids that are not owner's
// items fail the whole request; a filter whose count differs from the
// expected one answers 409.
func (s *Service) resolve(ctx context.Context, q items.Querier, owner int64, sel selection) ([]*items.Item, error) {
	ids := sel.ids
	if sel.filter != nil {
		n, err := s.d.Items.Count(ctx, q, owner, *sel.filter)
		if err != nil {
			return nil, err
		}
		if n != sel.expected {
			return nil, web.Errorf(http.StatusConflict, "The selection changed: %d %s match now, not %d. Reload and try again.",
				n, plural(n, "item", "items"), sel.expected)
		}
		if n == 0 {
			return nil, errNoItems
		}
		if n > MaxMatching {
			return nil, errMatching
		}
		if ids, err = s.d.Items.MatchIDs(ctx, q, owner, *sel.filter, MaxMatching); err != nil {
			return nil, err
		}
	}
	out := make([]*items.Item, 0, len(ids))
	for chunk := range slices.Chunk(ids, 500) {
		ph, args := placeholders(chunk)
		list, err := items.SelectItems(ctx, q, `owner_id = ? AND id IN (`+ph+`)`, "", append([]any{owner}, args...)...)
		if err != nil {
			return nil, err
		}
		if len(list) != len(chunk) {
			return nil, errMissing
		}
		out = append(out, list...)
	}
	// Keep the request's order (the list order for filters).
	pos := make(map[int64]int, len(ids))
	for i, id := range ids {
		pos[id] = i
	}
	slices.SortFunc(out, func(a, b *items.Item) int { return pos[a.ID] - pos[b.ID] })
	return out, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func idsOf(list []*items.Item) []int64 {
	out := make([]int64, len(list))
	for i, it := range list {
		out[i] = it.ID
	}
	return out
}

// Actions.

type bulkCtx struct {
	p   *auth.Principal
	sel selection
	req bulkReq
}

type action struct {
	scopes []auth.Scope
	run    func(s *Service, w http.ResponseWriter, r *http.Request, b bulkCtx) error
}

var actions = map[string]action{
	"set_expiry":     {[]auth.Scope{auth.ScopeItemsWrite}, (*Service).setExpiry},
	"set_visibility": {[]auth.Scope{auth.ScopeItemsWrite}, (*Service).setVisibility},
	"set_password":   {[]auth.Scope{auth.ScopeItemsWrite}, (*Service).setPassword},
	"clear_password": {[]auth.Scope{auth.ScopeItemsWrite}, (*Service).clearPassword},
	"add_to_album":   {[]auth.Scope{auth.ScopeAlbumsWrite}, (*Service).addToAlbum},
	"new_album":      {[]auth.Scope{auth.ScopeAlbumsWrite}, (*Service).newAlbum},
	"add_tags":       {[]auth.Scope{auth.ScopeItemsWrite}, (*Service).addTags},
	"remove_tags":    {[]auth.Scope{auth.ScopeItemsWrite}, (*Service).removeTags},
	"delete":         {[]auth.Scope{auth.ScopeItemsDelete}, (*Service).deleteItems},
	"download_zip":   {[]auth.Scope{auth.ScopeItemsRead}, (*Service).downloadZip},
	"send_email":     {[]auth.Scope{auth.ScopeItemsWrite}, (*Service).sendEmail},
}

func (s *Service) bulk(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	var req bulkReq
	if err := web.DecodeJSON(w, r, 64<<10, &req); err != nil {
		return err
	}
	a, ok := actions[req.Action]
	if !ok {
		return errAction
	}
	for _, sc := range a.scopes {
		if !p.Can(sc) {
			return web.Errorf(http.StatusForbidden, "This needs the %s permission.", sc)
		}
	}
	sel, err := parseSelection(req)
	if err != nil {
		return err
	}
	return a.run(s, w, r, bulkCtx{p: p, sel: sel, req: req})
}

// params decodes the action's parameters strictly: unknown fields are refused.
func params(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return web.Errorf(http.StatusBadRequest, "Invalid params: %v", err)
	}
	return nil
}

// result is the bulk response.
type result struct {
	Action  string `json:"action"`
	Count   int    `json:"count"`             // items changed (or included)
	Skipped int    `json:"skipped,omitempty"` // selected items the action does not apply to
}

func respond(w http.ResponseWriter, v any) {
	web.JSON(w, http.StatusOK, v)
}

// update runs one UPDATE over the selection inside a transaction.
func (s *Service) update(r *http.Request, b bulkCtx, set string, args ...any) (int, error) {
	ctx := r.Context()
	n := 0
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		list, err := s.resolve(ctx, tx, b.p.UserID, b.sel)
		if err != nil {
			return err
		}
		n = len(list)
		for chunk := range slices.Chunk(idsOf(list), 500) {
			ph, ids := placeholders(chunk)
			q := `UPDATE items SET ` + set + `, updated_at = ? WHERE owner_id = ? AND id IN (` + ph + `)` // #nosec G202 -- set is a constant; values are bound
			a := append(append(append([]any{}, args...), db.Millis(s.now()), b.p.UserID), ids...)
			if _, err := tx.ExecContext(ctx, q, a...); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

func (s *Service) setExpiry(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	var p struct {
		Expires      string  `json:"expires"`
		ExpiryAction *string `json:"expiry_action"`
	}
	if err := params(b.req.Params, &p); err != nil {
		return err
	}
	exp, err := s.d.Items.ResolveExpiry(p.Expires)
	if err != nil {
		return err
	}
	kc := b.p.Constraints()
	exp = kc.ClampExpiry(exp, s.now()) // an API key's expiry cap, as for PATCH
	var expAt any
	if exp != nil {
		expAt = db.Millis(*exp)
	}
	set, args := `expires_at = ?`, []any{expAt}
	if p.ExpiryAction != nil {
		if *p.ExpiryAction != items.ExpireDelete && *p.ExpiryAction != items.ExpirePrivate {
			return web.Errorf(http.StatusBadRequest, "Expiry action must be delete or private.")
		}
		set, args = set+`, expiry_action = ?`, append(args, *p.ExpiryAction)
	}
	// As for PATCH: a capped key's items are deleted when they expire.
	if kc != nil && kc.MaxExpiry > 0 {
		if p.ExpiryAction != nil && *p.ExpiryAction == items.ExpirePrivate {
			return items.ErrCappedPrivate
		}
		if p.ExpiryAction == nil {
			set += `, expiry_action = 'delete'`
		}
	}
	n, err := s.update(r, b, set, args...)
	if err != nil {
		return err
	}
	respond(w, map[string]any{"action": b.req.Action, "count": n, "expires_at": exp})
	return nil
}

func (s *Service) setVisibility(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	var p struct {
		Visibility string `json:"visibility"`
	}
	if err := params(b.req.Params, &p); err != nil {
		return err
	}
	if p.Visibility != string(items.Unlisted) && p.Visibility != string(items.Private) {
		return web.Errorf(http.StatusBadRequest, "Visibility must be unlisted or private.")
	}
	// A key that forces a visibility cannot change it either, as for PATCH.
	if v := b.p.Constraints().ForcedVisibility(); v != "" && p.Visibility != v {
		return web.Errorf(http.StatusForbidden, "This key can only make %s items.", v)
	}
	set, args := `visibility = ?`, []any{p.Visibility}
	// As for PATCH: a key with an expiry cap re-applies it to what it changes,
	// so an item it made private (or one without an expiry) cannot come back
	// unlisted with no expiry.
	if kc := b.p.Constraints(); kc != nil && kc.MaxExpiry > 0 {
		limit := db.Millis(*kc.ClampExpiry(nil, s.now()))
		set += `, expires_at = min(coalesce(expires_at, ?), ?), expiry_action = 'delete'`
		args = append(args, limit, limit)
	}
	n, err := s.update(r, b, set, args...)
	if err != nil {
		return err
	}
	respond(w, result{Action: b.req.Action, Count: n})
	return nil
}

func (s *Service) setPassword(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	var p struct {
		Password string `json:"password"`
	}
	if err := params(b.req.Params, &p); err != nil {
		return err
	}
	if p.Password == "" {
		return web.Errorf(http.StatusBadRequest, "Enter a password.")
	}
	// One hash for the whole selection: hashing is the slow part, and unlocks
	// stay per item (the unlock cookie is keyed by item id).
	h, err := s.d.Items.HashPassword(p.Password)
	if err != nil {
		return err
	}
	n, err := s.update(r, b, `password_hash = ?`, h)
	if err != nil {
		return err
	}
	respond(w, result{Action: b.req.Action, Count: n})
	return nil
}

func (s *Service) clearPassword(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	if err := params(b.req.Params, &struct{}{}); err != nil {
		return err
	}
	n, err := s.update(r, b, `password_hash = NULL`)
	if err != nil {
		return err
	}
	respond(w, result{Action: b.req.Action, Count: n})
	return nil
}

// members splits a selection into items an album can hold and albums.
func members(list []*items.Item) (ids []int64, skipped int) {
	for _, it := range list {
		if it.Kind == items.KindAlbum {
			skipped++
			continue
		}
		ids = append(ids, it.ID)
	}
	return ids, skipped
}

func (s *Service) addToAlbum(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	var p struct {
		AlbumID int64 `json:"album_id"`
	}
	if err := params(b.req.Params, &p); err != nil {
		return err
	}
	ctx := r.Context()
	var res result
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		list, err := s.resolve(ctx, tx, b.p.UserID, b.sel)
		if err != nil {
			return err
		}
		ids, skipped := members(list)
		n, err := s.d.Albums.AddTx(ctx, tx, b.p.UserID, p.AlbumID, ids)
		if err != nil {
			return err
		}
		res = result{Action: b.req.Action, Count: n, Skipped: skipped + len(ids) - n}
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, res)
	return nil
}

func (s *Service) newAlbum(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	var p struct {
		Title      string `json:"title"`
		Visibility string `json:"visibility"`
		Expires    string `json:"expires"`
	}
	if err := params(b.req.Params, &p); err != nil {
		return err
	}
	if !b.p.EmailVerified {
		return web.Errorf(http.StatusForbidden, "Verify your email address first.")
	}
	ctx := r.Context()
	n, err := s.d.Albums.NewAlbum(ctx, b.p.UserID, items.CreateOptions{Title: p.Title, Visibility: p.Visibility, Expires: p.Expires})
	if err != nil {
		return err
	}
	var al *items.Item
	var res result
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		list, err := s.resolve(ctx, tx, b.p.UserID, b.sel)
		if err != nil {
			return err
		}
		ids, skipped := members(list)
		if al, err = s.d.Albums.InsertTx(ctx, tx, n, ids); err != nil {
			return err
		}
		res = result{Action: b.req.Action, Count: len(ids), Skipped: skipped}
		return nil
	})
	if err != nil {
		return err
	}
	respond(w, map[string]any{"action": res.Action, "count": res.Count, "skipped": res.Skipped,
		"album": map[string]any{"id": al.ID, "slug": al.Slug, "name": al.Name(), "url": s.d.Items.PublicURL(al.Slug)}})
	return nil
}

func (s *Service) tagAction(w http.ResponseWriter, r *http.Request, b bulkCtx, add bool) error {
	var p struct {
		Tags []string `json:"tags"`
	}
	if err := params(b.req.Params, &p); err != nil {
		return err
	}
	names, err := normTags(p.Tags)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return web.Errorf(http.StatusBadRequest, "Name at least one tag.")
	}
	if len(names) > MaxTagsPerItem {
		return errTooManyTags
	}
	ctx := r.Context()
	n := 0
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		list, err := s.resolve(ctx, tx, b.p.UserID, b.sel)
		if err != nil {
			return err
		}
		n = len(list)
		if add {
			return s.addTagsTx(ctx, tx, b.p.UserID, idsOf(list), names)
		}
		return s.removeTagsTx(ctx, tx, b.p.UserID, idsOf(list), names)
	})
	if err != nil {
		return err
	}
	respond(w, result{Action: b.req.Action, Count: n})
	return nil
}

func (s *Service) addTags(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	return s.tagAction(w, r, b, true)
}

func (s *Service) removeTags(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	return s.tagAction(w, r, b, false)
}

func (s *Service) deleteItems(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	if err := params(b.req.Params, &struct{}{}); err != nil {
		return err
	}
	ctx := r.Context()
	var list []*items.Item
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if list, err = s.resolve(ctx, tx, b.p.UserID, b.sel); err != nil {
			return err
		}
		for _, it := range list {
			if err := s.d.Items.DeleteTx(ctx, tx, it); err != nil {
				return fmt.Errorf("delete item %d: %w", it.ID, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, it := range list {
		s.d.Items.DeleteBlobs(ctx, it)
	}
	respond(w, result{Action: b.req.Action, Count: len(list)})
	return nil
}

// boundNow is the created_before bound a page hands to "select all": the
// render time, so items uploaded later are never swept in.
func boundNow(now time.Time) string { return now.UTC().Format(time.RFC3339Nano) }
