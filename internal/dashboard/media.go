package dashboard

import (
	"net/http"
	"strconv"

	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// mediaAs are the places a processing marker can sit, which pick the markup
// of the fragment (the media.lead, media.thumb and media.note partials):
// lead is the small square of a table or list row, tile the frame of a grid
// or album tile, note the line under a share page's media.
var mediaAs = map[string]bool{"lead": true, "tile": true, "note": true}

// mediaFragment is GET /dashboard/items/{id}/media?as=lead|tile|note, the
// element a processing marker polls every 5 s. While the item's media job is
// still queued or running it answers 204, so the marker stays as it is and
// its shimmer does not restart. Otherwise it answers what takes the marker's
// place and has no polling left: the thumbnail or poster, the type icon with
// "no preview", or (note) nothing.
//
// Only the owner's browser session gets an answer. A missing item, and any
// item of someone else, answer the same 404, so it cannot tell whether an id
// exists or what state it is in.
func (s *Service) mediaFragment(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	as := r.URL.Query().Get("as")
	id, perr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !mediaAs[as] || perr != nil {
		return items.ErrNotFound
	}
	it, err := s.d.Items.Get(r.Context(), id)
	if err != nil || it.OwnerID != p.UserID {
		return items.ErrNotFound
	}
	state := s.d.Items.MediaState(r.Context(), it)
	w.Header().Set("Cache-Control", "no-store")
	if state == items.MediaPending {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	c := s.d.Albums.CardFor(it, true)
	s.d.Render.Fragment(w, r, http.StatusOK, "dashboard/items", "media-fragment", web.Page{Data: map[string]any{
		"as": as, "id": it.ID, "state": items.Marker(state), "thumb": c.Picture, "icon": albums.Icon(it),
	}})
	return nil
}
