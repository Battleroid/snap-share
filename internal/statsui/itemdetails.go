package statsui

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/items"
)

// itemOwner is what the item page adds for the owner above the stats: the
// preview, the details and the controls. Admins viewing someone else's item
// never get it (totals only).
type itemOwner struct {
	Card     albums.Card
	Details  []map[string]any // page.dl pairs
	JSON     string           // the Edit drawer's item (item-edit.js)
	Host     string
	Delete   string // POST target of Delete
	Download string // "" for links and albums
	Rename   bool   // a file: the header has Rename (the drawer on its file name)
}

// editItem is the item as the Edit drawer reads it (the dashboard row's JSON).
type editItem struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Title      string     `json:"title"`
	Slug       string     `json:"slug"`
	Kind       string     `json:"kind"`
	URL        string     `json:"url"`
	Visibility string     `json:"visibility"`
	Password   bool       `json:"password"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Tags       []string   `json:"tags"`
	Filename   string     `json:"filename"` // files only: the drawer's rename field
}

func (s *Service) ownerItem(r *http.Request, it *items.Item, userID int64) (*itemOwner, error) {
	ctx := r.Context()
	o := &itemOwner{Host: s.d.Host, Delete: ItemPath(it.ID) + "/delete", Rename: items.RenameName(it) != ""}
	if s.d.Card != nil {
		o.Card = s.d.Card(it)
	}
	if it.Kind == items.KindFile || it.Kind == items.KindGist || it.Kind == items.KindPaste {
		o.Download = o.Card.URLs.Download
	}
	tags := []string{}
	if s.d.Tags != nil {
		t, err := s.d.Tags(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		if t != nil {
			tags = t
		}
	}
	var inAlbums []*items.Item
	if s.d.Albums != nil && it.Kind != items.KindAlbum {
		var err error
		if inAlbums, err = s.d.Albums(ctx, userID, it.ID); err != nil {
			return nil, err
		}
	}
	url := s.d.Items.PublicURL(it.Slug)
	b, err := json.Marshal(editItem{
		ID: it.ID, Name: it.Name(), Title: it.Title, Slug: it.Slug, Kind: string(it.Kind), URL: url,
		Visibility: string(it.Visibility), Password: it.PasswordHash != "", ExpiresAt: it.ExpiresAt, Tags: tags, Filename: items.RenameName(it),
	})
	if err != nil {
		return nil, err
	}
	o.JSON = string(b)
	o.Details = itemDetails(it, o.Card, url, tags, inAlbums)
	return o, nil
}

// itemDetails are the page.dl pairs: the link, what it is, who can open it,
// its lifetime, and how it is filed.
func itemDetails(it *items.Item, c albums.Card, url string, tags []string, in []*items.Item) []map[string]any {
	what := c.Type
	if c.Size != "" {
		what += " · " + c.Size
	}
	what += items.MediaMeta(it)
	if it.Kind == items.KindLink {
		what = it.TargetURL
	}
	who := string(it.Visibility)
	if it.PasswordHash != "" {
		who += " · password"
	}
	expires := any("never")
	if it.ExpiresAt != nil {
		action := "then deleted"
		if it.ExpiryAction == items.ExpirePrivate {
			action = "then private"
		}
		expires = template.HTML(string(items.ShareTime(*it.ExpiresAt)) + " · " + action) // #nosec G203 -- a formatted time and a constant
	}
	whatLabel := "type"
	if it.Kind == items.KindLink {
		whatLabel = "target"
	}
	d := []map[string]any{
		{"label": "link", "value": url, "mono": true, "copy": url},
		{"label": whatLabel, "value": what, "mono": true},
		{"label": "visibility", "value": who, "mono": true},
		{"label": "uploaded", "value": items.ShareTime(it.CreatedAt), "mono": true},
		{"label": "expires", "value": expires, "mono": true},
		{"label": "tags", "value": strings.Join(tags, ", ")},
	}
	if it.Kind != items.KindAlbum {
		d = append(d, map[string]any{"label": "albums", "value": albumLinks(in)})
	}
	if it.SHA256 != "" {
		d = append(d, map[string]any{"label": "sha256", "value": it.SHA256, "mono": true, "copy": it.SHA256})
	}
	return d
}

// albumLinks is the albums an item is in, each linked to its page; "" for none.
func albumLinks(in []*items.Item) template.HTML {
	var b strings.Builder
	for i, a := range in {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`<a class="pb-link" href="` + template.HTMLEscapeString(ItemPath(a.ID)) + `">` + template.HTMLEscapeString(a.Name()) + `</a>`)
	}
	return template.HTML(b.String()) // #nosec G203 -- escaped names and numeric paths
}

// itemStatsRedirect is GET /dashboard/items/{id}/stats, the item page's old
// address (notification links, bookmarks): it keeps the query (days, page).
// Anyone the page would refuse gets its 404, so ids can't be probed here either.
func (s *Service) itemStatsRedirect(w http.ResponseWriter, r *http.Request) {
	it, _, ok := s.loadItem(w, r, true)
	if !ok {
		return
	}
	// Only the page's own parameters, as numbers: nothing else reaches Location.
	q := url.Values{}
	if r.URL.Query().Has("days") {
		q.Set("days", strconv.Itoa(parseDays(r)))
	}
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 1 {
		q.Set("page", strconv.Itoa(p))
	}
	to := ItemPath(it.ID)
	if len(q) > 0 {
		to += "?" + q.Encode()
	}
	http.Redirect(w, r, to, http.StatusMovedPermanently) // #nosec G710 -- a fixed path and two integers
}
