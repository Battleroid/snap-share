package items

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/render"
	"github.com/Battleroid/snap-share/internal/web"
)

type option struct{ Value, Label string }

func optionList(o []option) []map[string]any {
	out := make([]map[string]any, len(o))
	for i, x := range o {
		out[i] = map[string]any{"value": x.Value, "label": x.Label}
	}
	return out
}

func expiryOptions() []map[string]any { return web.ExpiryOptions() }

var visibilityOptions = optionList([]option{{"unlisted", "unlisted"}, {"private", "private"}})

// recentRow is a compact item row for the composer and dashboard.
type recentRow struct {
	Item     *Item
	Name     string
	URL      string
	Type     string
	Size     string
	Created  string
	Expires  string
	Opened   string // first-opened time, "" when never opened
	Icon     string
	Private  bool
	Password bool
	Media    string // "pending" or "failed": the processing marker (mediastate.go)
}

func (s *Service) rows(ctx context.Context, list []*Item) []recentRow {
	states := s.MediaStates(ctx, list)
	out := make([]recentRow, 0, len(list))
	for _, it := range list {
		row := recentRow{
			Item: it, Name: it.Name(), URL: s.PublicURL(it.Slug), Type: TypeOf(it), Size: web.HumanBytes(it.Size),
			Created: it.CreatedAt.UTC().Format("2006-01-02 15:04"), Expires: "—", Icon: "file",
			Private: it.Visibility == Private, Password: it.PasswordHash != "",
			Media: Marker(states[it.ID]),
		}
		switch it.Kind {
		case KindLink:
			row.Size, row.Icon = "—", "link"
		case KindGist:
			row.Icon = "terminal" // as albums.Icon
		case KindAlbum:
			row.Size, row.Icon = "—", "folder"
		}
		if it.ExpiresAt != nil {
			row.Expires = it.ExpiresAt.UTC().Format("2006-01-02 15:04")
		}
		if it.FirstOpened != nil {
			row.Opened = it.FirstOpened.UTC().Format("2006-01-02 15:04")
		}
		out = append(out, row)
	}
	return out
}

type composerView struct {
	Secret     any // secret composer data (nil: no Secret tab)
	Prefs      Prefs
	MaxUpload  int64
	PartSize   int
	Blocked    string
	Host       string
	Languages  []string
	Expiries   []map[string]any
	Visibility []map[string]any
	Recent     []recentRow
	Verified   bool
}

// Home renders the composer (the signed-in home page). It lists the newest
// items, so it is for browser sessions only: an API key is answered as
// anyone signed out would be.
func (s *Service) Home(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if p == nil || p.ViaKey() {
		s.d.Errors.Unauthenticated(w, r)
		return
	}
	ctx := r.Context()
	prefs, _ := LoadPrefs(ctx, s.d.DB, p.UserID)
	max := MaxUploadBytes.Get(s.d.Settings)
	if lim, err := s.userLimits(ctx, s.d.DB.R, p.UserID); err == nil {
		max = lim.maxUpload
	}
	recent, _, err := s.ListOwned(ctx, p.UserID, 8, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := composerView{
		Prefs: prefs, MaxUpload: max, PartSize: PartSize, Blocked: strings.Join(normList(BlockedExtensions.Get(s.d.Settings), "."), ","),
		Host: s.d.Cfg.PublicURL.Host, Languages: render.Languages(), Expiries: expiryOptions(), Visibility: visibilityOptions,
		Recent: s.rows(ctx, recent), Verified: p.EmailVerified,
	}
	scripts := []string{"js/upload.js", "js/composer.js", "js/hover-preview.js"}
	if s.d.SecretComposer != nil {
		v.Secret = s.d.SecretComposer(r)
		scripts = append(scripts, "js/secret-new.js")
	}
	s.d.Render.Page(w, r, http.StatusOK, "items/composer", web.Page{
		Title: "New", Nav: "home", Scripts: scripts, Data: v,
	})
}

type defaultsView struct {
	Prefs      Prefs
	Expiries   []map[string]any
	Visibility []map[string]any
	Saved      bool
	SiteName   string // the site's name, for the embed preview
	Error      string
}

func (s *Service) defaultsPage(w http.ResponseWriter, r *http.Request) error {
	p := auth.FromContext(r.Context())
	prefs, err := LoadPrefs(r.Context(), s.d.DB, p.UserID)
	if err != nil {
		return err
	}
	s.renderDefaults(w, r, http.StatusOK, defaultsView{Prefs: prefs, Saved: r.URL.Query().Get("saved") == "1"})
	return nil
}

func (s *Service) renderDefaults(w http.ResponseWriter, r *http.Request, status int, v defaultsView) {
	v.Expiries, v.Visibility = expiryOptions(), visibilityOptions
	v.SiteName = s.siteName()
	s.d.Render.Page(w, r, status, "defaults/defaults", web.Page{Title: "Defaults", Nav: "settings", Scripts: []string{"js/settings.js"}, Data: v})
}

func (s *Service) defaultsSave(w http.ResponseWriter, r *http.Request) error {
	p := auth.FromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		return web.Errorf(http.StatusBadRequest, "Malformed form body.")
	}
	f := r.PostForm
	length, _ := strconv.Atoi(f.Get("random_length"))
	prefs := Prefs{
		Naming: f.Get("naming"), RandomLength: length, Visibility: f.Get("visibility"), Expiry: f.Get("expiry"),
		StripLocation: f.Get("strip_location") != "", EmbedTitle: f.Get("embed_title"), EmbedColor: f.Get("embed_color"),
		DirectLink: f.Get("direct_link") != "",
	}
	if c := f.Get("embed_color"); c != "" && !colorRe.MatchString(c) {
		s.renderDefaults(w, r, http.StatusBadRequest, defaultsView{Prefs: prefs.normalized(), Error: "Use a color like #1f3fd6."})
		return nil
	}
	if length < MinRandomLength || length > MaxRandomLength {
		s.renderDefaults(w, r, http.StatusBadRequest, defaultsView{Prefs: prefs.normalized(), Error: "Random names are 10 to 32 characters long."})
		return nil
	}
	if err := SavePrefs(r.Context(), s.d.DB, p.UserID, prefs, s.now()); err != nil {
		return err
	}
	http.Redirect(w, r, "/settings/defaults?saved=1", http.StatusSeeOther)
	return nil
}

// deletePage is /x: its script reads the token from the fragment.
func (s *Service) deletePage(w http.ResponseWriter, r *http.Request) {
	s.d.Render.Page(w, r, http.StatusOK, "share/x", web.Page{
		Title: "Delete", NoIndex: true, NoReferrer: true, Scripts: []string{"js/token-x.js"},
	})
}
