package admin

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

type contentRow struct {
	ID                           int64
	Owner, Name, URL, Kind, Size string
	Visibility                   string
	CreatedAt                    time.Time
	ExpiresAt                    *time.Time
	Views                        int64
	Password                     bool
	Picture                      string // a thumbnail anyone with the link may load, or ""
	Icon                         string // the type icon, as on the dashboard (albums.Icon)
}

type contentData struct {
	base
	Owner, Kind, Q string
	Kinds          []map[string]any
	Rows           []contentRow
	Pager          pager
}

var kindOptions = []map[string]any{
	{"value": "", "label": "all kinds"}, {"value": "file", "label": "file"}, {"value": "paste", "label": "paste"},
	{"value": "gist", "label": "gist"}, {"value": "link", "label": "link"}, {"value": "album", "label": "album"},
}

func (a *Admin) contentPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := contentData{base: a.base(r, "/admin/content"), Owner: q.Get("owner"), Kind: q.Get("kind"), Q: q.Get("q"), Kinds: kindOptions}
	f := items.ListFilter{Owner: d.Owner, Query: d.Q, Limit: perPage}
	switch items.Kind(d.Kind) {
	case items.KindFile, items.KindPaste, items.KindGist, items.KindLink, items.KindAlbum:
		f.Kind = items.Kind(d.Kind)
	default:
		d.Kind = ""
	}
	page, offset := pageOf(r)
	f.Offset = offset
	list, total, err := a.d.Items.ListAll(r.Context(), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, it := range list {
		row := contentRow{
			ID: it.ID, Owner: dash(it.OwnerName), Name: it.Name(), URL: a.d.Items.PublicURL(it.Slug), Kind: string(it.Kind),
			Size: web.HumanBytes(it.Size), Visibility: string(it.Visibility), CreatedAt: it.CreatedAt,
			ExpiresAt: it.ExpiresAt, Views: it.Views, Password: it.PasswordHash != "", Icon: albums.Icon(it.Item),
		}
		// A private or locked item's thumbnail is its owner's only, and an
		// expired one is gone. Thumbnails record no event (items.eventKind).
		if it.ThumbKey != "" && it.Visibility != items.Private && !row.Password && !it.Expired(time.Now()) {
			row.Picture = "/" + it.Slug + "/thumb"
		}
		if it.Kind == items.KindLink || it.Kind == items.KindAlbum {
			row.Size = "—"
		}
		d.Rows = append(d.Rows, row)
	}
	d.Pager = newPager("/admin/content", q, page, total, len(list))
	a.page(w, r, http.StatusOK, "admin/content", "Content", d)
}

// deleteItem handles POST /admin/content/{id}/delete: a moderation delete,
// audited with the owner and the item's slug.
func (a *Admin) deleteItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		a.d.Errors.NotFound(w, r)
		return
	}
	it, err := a.d.Items.Get(ctx, id)
	if errors.Is(err, items.ErrNotFound) {
		a.d.Errors.NotFound(w, r)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.d.Items.Delete(ctx, id); err != nil && !errors.Is(err, items.ErrNotFound) {
		a.fail(w, r, err)
		return
	}
	e := accounts.Actor(r)
	e.Action, e.Target = "moderation.delete", fmt.Sprintf("item:%d", id)
	e.Details = map[string]any{"owner_id": it.OwnerID, "slug": it.Slug, "kind": it.Kind, "size": it.Size}
	if err := a.d.Accounts.Store().Audit(ctx, e); err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, back(r, "/admin/content", "item_deleted"))
}
