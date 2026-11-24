package albums

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// Card is one member on the album page and in the dashboard grid.
type Card struct {
	Item     *items.Item
	Name     string
	Type     string // lowercase type label ("png", "go", "link")
	Size     string // "" for links and albums
	URLs     items.URLs
	Picture  string // thumbnail or image URL; "" shows the type icon
	Icon     string // icon name for the type
	Lightbox string // "image", "video" or "audio" when the viewer can show it
	Duration string // "0:04" for a video, for its play badge; "" otherwise
	Private  bool
	Password bool
	Expired  bool
	// Media is "pending" or "failed" while the owner's page shows a processing
	// marker in place of the preview (MarkMedia); "" everywhere else.
	Media string
}

// Icon is the type icon for it.
func Icon(it *items.Item) string {
	switch it.Kind {
	case items.KindLink:
		return "link"
	case items.KindAlbum:
		return "folder"
	case items.KindGist:
		return "terminal"
	}
	return "file"
}

// CardFor builds a card for it. viewer says the requester may open it.
func (s *Service) CardFor(it *items.Item, viewer bool) Card {
	c := Card{
		Item: it, Name: it.Name(), Type: items.TypeOf(it), URLs: s.d.Items.URLs(it, viewer), Icon: Icon(it),
		Private: it.Visibility == items.Private, Password: it.PasswordHash != "", Expired: it.Expired(s.now()),
	}
	if it.Kind == items.KindFile || it.Kind == items.KindPaste || it.Kind == items.KindGist {
		c.Size = web.HumanBytes(it.Size)
	}
	c.Picture = s.picture(it, viewer)
	switch items.ViewKind(it) {
	case "image":
		if c.URLs.Raw != "" {
			c.Lightbox = "image"
		}
	case "video":
		if c.URLs.Raw != "" {
			c.Lightbox = "video"
			c.Duration = items.Duration(it)
		}
	case "audio":
		if c.URLs.Raw != "" {
			c.Lightbox = "audio"
		}
	}
	return c
}

type pageView struct {
	Album    *items.Item
	Name     string
	URL      string
	Owner    bool
	Private  bool
	Password bool
	Cover    []string // up to three pictures for the --grid frame
	More     int      // members beyond the cover
	Cards    []Card
	Details  []map[string]any // page.dl items
	Manage   string           // owner: the dashboard filtered to this album
	AddItems string           // owner: Items in add-to mode for this album
	Embed    *items.EmbedView // OpenGraph/Twitter card: a gallery of public members
}

// Page renders an album's share page (installed with items.SetAlbumPage).
// Members are listed only if this viewer could open them directly; the
// owner sees every member, marked when others cannot see it.
func (s *Service) Page(w http.ResponseWriter, r *http.Request, al *items.Item, owner bool) {
	members, err := Members(r.Context(), s.d.DB.R, al.ID)
	if err != nil {
		s.d.Items.Fail(w, r, err)
		return
	}
	v := pageView{
		Album: al, Name: al.Name(), URL: s.d.Items.PublicURL(al.Slug), Owner: owner,
		Private: al.Visibility == items.Private, Password: al.PasswordHash != "",
		Embed: s.d.Items.AlbumEmbed(r.Context(), al, members),
	}
	var size int64
	for _, m := range members {
		if !owner && !s.d.Items.Viewable(r, m) {
			continue
		}
		c := s.CardFor(m, true)
		v.Cards = append(v.Cards, c)
		if c.Size != "" {
			size += m.Size
		}
		if c.Picture != "" && len(v.Cover) < 3 {
			v.Cover = append(v.Cover, c.Picture)
		}
	}
	if owner && items.OwnerSession(r, al) {
		s.MarkMedia(r.Context(), v.Cards)
	}
	if len(v.Cover) > 0 && len(v.Cover) < 3 {
		v.Cover = v.Cover[:1]
	}
	if len(v.Cover) == 3 {
		v.More = len(v.Cards) - 3
	}
	expires := any("never")
	if al.ExpiresAt != nil {
		expires = items.ShareTime(*al.ExpiresAt)
	}
	v.Details = []map[string]any{
		{"label": "items", "value": strconv.Itoa(len(v.Cards)), "mono": true},
		{"label": "size", "value": web.HumanBytes(size), "mono": true},
		{"label": "created", "value": items.ShareTime(al.CreatedAt), "mono": true},
		{"label": "expires", "value": expires, "mono": true},
	}
	// share.js binds the kit's viewer; album.js is the owner's tools.
	scripts := []string{"js/share.js"}
	if owner {
		scripts = append(scripts, "js/album.js")
		v.Manage = "/dashboard?album=" + strconv.FormatInt(al.ID, 10)
		v.AddItems = "/dashboard?add_to=" + strconv.FormatInt(al.ID, 10)
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	s.d.Render.Page(w, r, http.StatusOK, "albums/album", web.Page{
		Title: v.Name, NoIndex: true, Scripts: scripts, Data: v,
	})
}

// MarkMedia sets Media on cards: the preview each is still waiting for. Call
// it only for the owner's own browser session; visitors never get a marker.
func (s *Service) MarkMedia(ctx context.Context, cards []Card) {
	list := make([]*items.Item, len(cards))
	for i, c := range cards {
		list[i] = c.Item
	}
	st := s.d.Items.MediaStates(ctx, list)
	for i := range cards {
		cards[i].Media = items.Marker(st[cards[i].Item.ID])
	}
}
