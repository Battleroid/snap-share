package albums

import (
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
	Lightbox string // "image" or "video" when the lightbox can show it
	Private  bool
	Password bool
	Expired  bool
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
		Item: it, Name: it.Name(), Type: items.TypeLabel(it), URLs: s.d.Items.URLs(it, viewer), Icon: Icon(it),
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
		}
	}
	return c
}

type metaPair struct{ Label, Value string }

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
	Meta     []metaPair
	Manage   string // owner: the dashboard filtered to this album
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
	if len(v.Cover) > 0 && len(v.Cover) < 3 {
		v.Cover = v.Cover[:1]
	}
	if len(v.Cover) == 3 {
		v.More = len(v.Cards) - 3
	}
	expires := "never"
	if al.ExpiresAt != nil {
		expires = al.ExpiresAt.UTC().Format("2006-01-02 15:04")
	}
	v.Meta = []metaPair{
		{"items", strconv.Itoa(len(v.Cards))},
		{"size", web.HumanBytes(size)},
		{"expires", expires},
		{"created", al.CreatedAt.UTC().Format("2006-01-02")},
	}
	scripts := []string{"js/lightbox.js"}
	if owner {
		scripts = append(scripts, "js/album.js")
		v.Manage = "/dashboard?album=" + strconv.FormatInt(al.ID, 10)
	}
	scripts = append(scripts, s.HoverScripts()...)
	w.Header().Set("X-Robots-Tag", "noindex")
	s.d.Render.Page(w, r, http.StatusOK, "albums/album", web.Page{
		Title: v.Name, NoIndex: true, Scripts: scripts, Data: v,
	})
}

// HoverScripts lists the hover teaser module when this build has it, so pages
// can load it before the media track's file lands.
func (s *Service) HoverScripts() []string {
	if s.d.Assets == nil {
		return nil
	}
	if _, err := s.d.Assets.URL(HoverPreviewJS); err != nil {
		return nil
	}
	return []string{HoverPreviewJS}
}
