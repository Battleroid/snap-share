package dashboard

import (
	"encoding/json"
	"hash/fnv"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// PageSize is the number of items per dashboard page.
const PageSize = 50

// resultsID is the element htmx swaps when the filters change.
const resultsID = "dash-results"

type option struct {
	Value, Label string
	Selected     bool
}

type pill struct {
	Value, Label string
	Count        int
	Pressed      bool
}

// tag is one tag on a row. Hue (1 to 6) comes from the name alone, so a tag
// has the same colour in every row, tile and list.
type tag struct {
	Name string
	Hue  int
}

// tagHue is 1 + FNV-1a(lowercase name) mod 6.
func tagHue(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(name)))
	return int(h.Sum32()%6) + 1
}

func tagViews(names []string) []tag {
	out := make([]tag, len(names))
	for i, n := range names {
		out[i] = tag{Name: n, Hue: tagHue(n)}
	}
	return out
}

// row is one item in the table or grid.
type row struct {
	albums.Card
	Created    string
	Expires    string // "—" for never
	Visibility string // "private" or "" (unlisted is the default and is not marked)
	Opened     string // tooltip on the views: "not opened", "first opened …" or "previewed …"
	Tags       []tag
	TagNames   string              // all tag names, for the "+N" tooltip
	Badges     []map[string]string // state badges on a grid tile (media.tile)
	JSON       string              // what the edit dialog needs
	Cover      []string            // albums: up to three member pictures (three make the --grid frame)
	More       int                 // albums: members beyond the cover
}

type pageView struct {
	Title     string   // page heading: "Items", or "Albums" for the album filter
	Meta      []string // the heading's meta line
	FiltersOn bool     // a filter beyond search and type is set
	F         items.Filter
	View      string // table | grid
	Query     string // the filter as a query string, for "select all matching"
	Bound     string // created_before for "select all matching"
	Rows      []row
	Total     int
	Page      int
	Pages     int
	From, To  int
	PagerHref string
	Pills     []pill
	Visible   []option
	Expiring  []option
	Opened    []option
	Sorts     []option
	Albums    []option // filter select (includes "any album")
	Tags      []option // filter select (includes "any tag")
	AlbumList []option // the add-to-album dialog
	Expiries  []option
	DateFrom  string
	DateTo    string
	Host      string
	Sender    string
	MailOn    bool
}

var expiryLabels = []option{
	{Value: "never", Label: "never"}, {Value: "1h", Label: "1 hour"}, {Value: "1d", Label: "1 day"},
	{Value: "7d", Label: "7 days"}, {Value: "30d", Label: "30 days"}, {Value: "90d", Label: "90 days"},
}

var sortLabels = map[string]string{
	"new": "newest", "old": "oldest", "name": "name", "size": "size", "views": "most viewed",
	"opened": "last opened", "expires": "expires soonest",
}

func options(cur string, list ...option) []option {
	out := make([]option, len(list))
	for i, o := range list {
		o.Selected = o.Value == cur
		out[i] = o
	}
	return out
}

// itemJSON is the data the row menu's edit dialog starts from.
type itemJSON struct {
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
}

func fmtTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") }

// isFragment reports whether r is an htmx swap of the results (not a
// history restore, which needs the whole page).
func isFragment(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" &&
		r.Header.Get("HX-Target") == resultsID
}

func (s *Service) page(w http.ResponseWriter, r *http.Request) error {
	p, err := principal(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	q := r.URL.Query()
	f, _ := items.ParseFilter(q) // invalid values are dropped
	f.CreatedBefore = nil
	v := pageView{F: f, View: "table", Bound: boundNow(s.now()), Host: s.d.Cfg.PublicURL.Host}
	if q.Get("view") == "grid" {
		v.View = "grid"
	}
	v.Page, _ = strconv.Atoi(q.Get("page"))
	v.Page = max(v.Page, 1)

	list, total, err := s.d.Items.Find(ctx, p.UserID, f, PageSize, (v.Page-1)*PageSize)
	if err != nil {
		return err
	}
	v.Total = total
	v.Pages = max(1, (total+PageSize-1)/PageSize)
	if len(list) > 0 {
		v.From = (v.Page-1)*PageSize + 1
		v.To = v.From + len(list) - 1
	}
	qs := f.Values()
	v.Query = qs.Encode()
	pq := url.Values{}
	for k, vals := range qs {
		pq[k] = vals
	}
	if v.View == "grid" {
		pq.Set("view", "grid")
	}
	v.PagerHref = "/dashboard?"
	if e := pq.Encode(); e != "" {
		v.PagerHref += e + "&"
	}
	v.PagerHref += "page="

	if v.Rows, err = s.rows(r, p.UserID, list); err != nil {
		return err
	}
	counts, err := s.d.Items.TypeCounts(ctx, p.UserID, f)
	if err != nil {
		return err
	}
	all := 0
	for _, n := range counts {
		all += n
	}
	v.Title, v.Meta = "Items", []string{strconv.Itoa(all) + " " + plural(all, "item", "items")}
	if f.Type == "album" {
		n := counts["album"]
		v.Title, v.Meta = "Albums", []string{strconv.Itoa(n) + " " + plural(n, "album", "albums")}
	}
	v.Pills = append(v.Pills, pill{Value: "", Label: "all", Count: all, Pressed: f.Type == ""})
	for _, t := range items.Types {
		v.Pills = append(v.Pills, pill{Value: t, Label: t, Count: counts[t], Pressed: f.Type == t})
	}

	if isFragment(r) {
		s.d.Render.Fragment(w, r, http.StatusOK, "dashboard/items", "dash-results", web.Page{Data: v})
		return nil
	}

	// The full page: filter controls and dialogs.
	v.FiltersOn = f.Visibility != "" || f.Expiring || f.Opened != "" || f.Album != 0 || f.Tag != "" || f.From != nil || f.To != nil
	v.Visible = options(f.Visibility, option{Value: "", Label: "any visibility"}, option{Value: "unlisted", Label: "unlisted"},
		option{Value: "private", Label: "private"}, option{Value: items.ByPassword, Label: "password"})
	exp := ""
	if f.Expiring {
		exp = "1"
	}
	v.Expiring = options(exp, option{Value: "", Label: "any expiry"}, option{Value: "1", Label: "expiring soon"})
	v.Opened = options(f.Opened, option{Value: "", Label: "any status"}, option{Value: items.OpenedNever, Label: "not opened"},
		option{Value: items.OpenedRecent, Label: "opened in 7 days"}, option{Value: items.OpenedPreviewed, Label: "previewed, not opened"})
	sortCur := f.Sort
	if sortCur == "" {
		sortCur = items.Sorts[0]
	}
	for _, so := range items.Sorts {
		v.Sorts = append(v.Sorts, option{Value: so, Label: sortLabels[so], Selected: so == sortCur})
	}
	als, err := s.d.Albums.List(ctx, p.UserID)
	if err != nil {
		return err
	}
	v.Albums = []option{{Value: "", Label: "any album", Selected: f.Album == 0}}
	for _, a := range als {
		id := strconv.FormatInt(a.Album.ID, 10)
		v.Albums = append(v.Albums, option{Value: id, Label: a.Album.Name(), Selected: a.Album.ID == f.Album})
		v.AlbumList = append(v.AlbumList, option{Value: id, Label: a.Album.Name()})
	}
	tags, err := s.Tags(ctx, p.UserID)
	if err != nil {
		return err
	}
	v.Tags = []option{{Value: "", Label: "any tag", Selected: f.Tag == ""}}
	for _, t := range tags {
		if t.Count > 0 || t.Name == f.Tag {
			v.Tags = append(v.Tags, option{Value: t.Name, Label: t.Name, Selected: t.Name == f.Tag})
		}
	}
	v.Expiries = expiryLabels
	if f.From != nil {
		v.DateFrom = f.From.Format(time.DateOnly)
	}
	if f.To != nil {
		v.DateTo = f.To.Format(time.DateOnly)
	}
	v.Sender = p.Username
	v.MailOn = s.MailEnabled()
	scripts := append([]string{"js/dashboard.js"}, s.d.Albums.HoverScripts()...)
	s.d.Render.Page(w, r, http.StatusOK, "dashboard/items", web.Page{Title: v.Title, Nav: "dashboard", Scripts: scripts, Data: v})
	return nil
}

// rows builds the table/grid rows for owner's items.
func (s *Service) rows(r *http.Request, owner int64, list []*items.Item) ([]row, error) {
	ctx := r.Context()
	ids := idsOf(list)
	tags, err := ItemTags(ctx, s.d.DB.R, ids)
	if err != nil {
		return nil, err
	}
	var albumIDs []int64
	for _, it := range list {
		if it.Kind == items.KindAlbum {
			albumIDs = append(albumIDs, it.ID)
		}
	}
	covers, err := s.d.Albums.Covers(ctx, owner, albumIDs)
	if err != nil {
		return nil, err
	}
	states := s.d.Items.MediaStates(ctx, list)
	out := make([]row, 0, len(list))
	for _, it := range list {
		rw := row{Card: s.d.Albums.CardFor(it, true), Created: fmtTime(it.CreatedAt), Expires: "—", Tags: tagViews(tags[it.ID]), TagNames: strings.Join(tags[it.ID], ", ")}
		rw.Media = items.Marker(states[it.ID])
		if rw.Expired {
			rw.Badges = []map[string]string{{"status": "down", "text": "expired"}}
		}
		if it.Visibility == items.Private {
			rw.Visibility = "private"
		}
		switch {
		case it.FirstOpened != nil:
			rw.Opened = "first opened " + fmtTime(*it.FirstOpened)
		case it.PreviewedAt != nil:
			rw.Opened = "previewed " + fmtTime(*it.PreviewedAt)
		default:
			rw.Opened = "not opened"
		}
		if it.ExpiresAt != nil {
			rw.Expires = fmtTime(*it.ExpiresAt)
		}
		if it.Kind == items.KindAlbum {
			c := covers[it.ID]
			rw.Size = strconv.Itoa(c.Count) + " " + plural(c.Count, "item", "items")
			// The grid shows the cover; the table keeps the album icon.
			rw.Cover, rw.More = c.Thumbs, max(c.Count-len(c.Thumbs), 0)
		}
		b, _ := json.Marshal(itemJSON{
			ID: it.ID, Name: it.Name(), Title: it.Title, Slug: it.Slug, Kind: string(it.Kind), URL: rw.URLs.Page,
			Visibility: string(it.Visibility), Password: it.PasswordHash != "", ExpiresAt: it.ExpiresAt, Tags: orEmpty(tags[it.ID]),
		})
		rw.JSON = string(b)
		out = append(out, rw)
	}
	return out, nil
}
