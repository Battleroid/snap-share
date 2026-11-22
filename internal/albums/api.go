package albums

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// AlbumJSON is an album in API responses: the item fields, the member count
// and, for a single album, its members in order.
type AlbumJSON struct {
	items.ItemJSON
	Count int              `json:"count"`
	Items []items.ItemJSON `json:"items,omitempty"`
}

func (s *Service) albumJSON(ctx context.Context, owner int64, al *items.Item, withItems bool) (AlbumJSON, error) {
	prefs, _ := items.LoadPrefs(ctx, s.d.DB, owner)
	j := AlbumJSON{ItemJSON: s.d.Items.JSON(al, false)}
	members, err := Members(ctx, s.d.DB.R, al.ID)
	if err != nil {
		return j, err
	}
	j.Count = len(members)
	if withItems {
		j.Items = make([]items.ItemJSON, 0, len(members))
		for _, m := range members {
			j.Items = append(j.Items, s.d.Items.JSON(m, prefs.DirectLink))
		}
	}
	return j, nil
}

func (s *Service) apiList(w http.ResponseWriter, r *http.Request) error {
	p, err := ownerOf(r)
	if err != nil {
		return err
	}
	list, err := s.List(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	out := make([]AlbumJSON, 0, len(list))
	for _, a := range list {
		out = append(out, AlbumJSON{ItemJSON: s.d.Items.JSON(a.Album, false), Count: a.Count})
	}
	web.JSON(w, http.StatusOK, map[string]any{"albums": out, "total": len(out)})
	return nil
}

func (s *Service) own(r *http.Request) (*auth.Principal, *items.Item, error) {
	p, err := ownerOf(r)
	if err != nil {
		return nil, nil, err
	}
	id, err := pathID(r)
	if err != nil {
		return nil, nil, err
	}
	al, err := s.Album(r.Context(), s.d.DB.R, p.UserID, id)
	return p, al, err
}

func (s *Service) apiGet(w http.ResponseWriter, r *http.Request) error {
	p, al, err := s.own(r)
	if err != nil {
		return err
	}
	j, err := s.albumJSON(r.Context(), p.UserID, al, true)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, j)
	return nil
}

// createReq is POST /api/v1/albums: the usual creation options plus members.
type createReq struct {
	Title        string  `json:"title"`
	Expires      string  `json:"expires"`
	ExpiryAction string  `json:"expiry_action"`
	Visibility   string  `json:"visibility"`
	Password     string  `json:"password"`
	Naming       string  `json:"naming"`
	Slug         string  `json:"slug"`
	Length       int     `json:"length"`
	Items        []int64 `json:"items"`
}

func (s *Service) apiCreate(w http.ResponseWriter, r *http.Request) error {
	p, err := ownerOf(r)
	if err != nil {
		return err
	}
	if !p.EmailVerified {
		return errUnverified
	}
	var req createReq
	if err := web.DecodeJSON(w, r, 64<<10, &req); err != nil {
		return err
	}
	ctx := r.Context()
	n, err := s.NewAlbum(ctx, p.UserID, items.CreateOptions{
		Title: req.Title, Expires: req.Expires, ExpiryAction: req.ExpiryAction, Visibility: req.Visibility,
		Password: req.Password, Naming: req.Naming, Slug: req.Slug, Length: req.Length,
	})
	if err != nil {
		return err
	}
	var al *items.Item
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		al, err = s.InsertTx(ctx, tx, n, req.Items)
		return err
	})
	if err != nil {
		return err
	}
	j, err := s.albumJSON(ctx, p.UserID, al, true)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusCreated, j)
	return nil
}

// apiPatch edits the album as PATCH /api/v1/items/{id} edits any item (title,
// expiry, visibility, password, slug) and answers with the album.
func (s *Service) apiPatch(w http.ResponseWriter, r *http.Request) error {
	p, al, err := s.own(r)
	if err != nil {
		return err
	}
	c := &capture{h: http.Header{}}
	if err := s.d.Items.Patch(c, r); err != nil {
		return err
	}
	if c.status != http.StatusOK {
		for k, v := range c.h {
			w.Header()[k] = v
		}
		w.WriteHeader(c.status)
		_, _ = c.buf.WriteTo(w)
		return nil
	}
	al, err = s.Album(r.Context(), s.d.DB.R, p.UserID, al.ID)
	if err != nil {
		return err
	}
	j, err := s.albumJSON(r.Context(), p.UserID, al, true)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, j)
	return nil
}

// capture records a handler's response so it can be replaced.
type capture struct {
	h      http.Header
	status int
	buf    bytes.Buffer
}

func (c *capture) Header() http.Header { return c.h }
func (c *capture) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}
func (c *capture) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.buf.Write(b)
}

// apiDelete deletes the album; its members stay as they are.
func (s *Service) apiDelete(w http.ResponseWriter, r *http.Request) error {
	_, al, err := s.own(r)
	if err != nil {
		return err
	}
	if err := s.d.Items.Delete(r.Context(), al.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// itemsReq is POST /api/v1/albums/{id}/items. add appends (in the given
// order), remove takes out, and order moves the listed members to the front
// in that order, keeping the rest after them as they were.
type itemsReq struct {
	Add    []int64 `json:"add"`
	Remove []int64 `json:"remove"`
	Order  []int64 `json:"order"`
}

func (s *Service) apiItems(w http.ResponseWriter, r *http.Request) error {
	p, err := ownerOf(r)
	if err != nil {
		return err
	}
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req itemsReq
	if err := web.DecodeJSON(w, r, 256<<10, &req); err != nil {
		return err
	}
	if len(req.Add) > MaxMembers || len(req.Remove) > MaxMembers || len(req.Order) > MaxMembers {
		return errTooMany
	}
	ctx := r.Context()
	if err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error { return s.changeTx(ctx, tx, p.UserID, id, req) }); err != nil {
		return err
	}
	al, err := s.Album(ctx, s.d.DB.R, p.UserID, id)
	if err != nil {
		return err
	}
	j, err := s.albumJSON(ctx, p.UserID, al, true)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, j)
	return nil
}
