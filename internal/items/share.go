package items

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/render"
	"github.com/Battleroid/snap-share/internal/web"
)

// shareView is the share page's template data.
type shareView struct {
	Item        *Item
	Name        string
	View        string
	PageURL     string
	RawURL      string
	DownloadURL string
	ThumbURL    string
	Type        string
	Size        string           // human size, for the file card
	File        string           // "jpeg · 4032×3024 · 4.1 MB": type, dimensions and length, size
	Duration    string           // "0:06": the length the player shows until the media says its own (data-duration)
	Details     []map[string]any // page.dl items under the media
	ManageURL   string           // owner: the item's page (ItemPath)
	Lang        string
	Text        *textBlock // text and markdown views
	TooLarge    bool
	PDFFrame    bool
	Owner       bool
	Private     bool
	Password    bool
	Embed       *EmbedView // OpenGraph/Twitter tags (embed.go)
	Media       string     // owner: "pending" or "failed" while the preview is not there (mediastate.go)
}

// textBlock is a code or Markdown view (the "share.text" partial): a paste
// or text file on its share page, or one file of a gist.
type textBlock struct {
	ID          string // element id prefix, unique on the page
	Name        string
	View        string // viewText or viewMarkdown
	Lang        string
	Size        string // for the bar and the "too large to preview" notice
	RawURL      string
	DownloadURL string // gist files: a Download action in the bar
	Code        template.HTML
	Gutter      string
	Markdown    template.HTML
	TooLarge    bool
	Heading     bool // the name in the bar is a heading (a file of a gist)
}

// ShareTime is a time for a page.dl value: the kit's relative time (TextUtils
// reltime) over the UTC timestamp, which is what shows without script.
func ShareTime(t time.Time) template.HTML {
	u := t.UTC()
	abs := u.Format("2006-01-02 15:04")
	// #nosec G203 -- built from formatted times only
	return template.HTML(`<time class="pb-reltime" datetime="` + u.Format(time.RFC3339) + `" title="` + abs + ` UTC">` + abs + `</time>`)
}

// ShareDetails are the page.dl pairs every share page ends with: when the
// item was uploaded and when it expires. Only the owner also gets the view
// count, linked to the item's page: stats are never shown to visitors
// (docs/stats.md).
func ShareDetails(it *Item, owner bool) []map[string]any {
	expires := any("never")
	if it.ExpiresAt != nil {
		expires = ShareTime(*it.ExpiresAt)
	}
	d := []map[string]any{
		{"label": "uploaded", "value": ShareTime(it.CreatedAt), "mono": true},
		{"label": "expires", "value": expires, "mono": true},
	}
	if owner {
		// #nosec G203 -- two integers
		views := template.HTML(`<a class="pb-link" href="` + ItemPath(it.ID) + `">` + strconv.FormatInt(it.Views, 10) + `</a>`)
		d = append(d, map[string]any{"label": "views", "value": views, "mono": true})
	}
	return d
}

// ItemPath is the owner's page for an item: its details, controls and stats.
func ItemPath(id int64) string { return "/dashboard/items/" + strconv.FormatInt(id, 10) }

// fillText fills b from the cached render under renderKey, read only if it
// is at most renderMax bytes, and returns the bytes read. Without a render,
// text up to render.DefaultMaxBytes is shown plain (escaped) when blobKey is
// given, and anything else as too large to preview, with the raw link.
func (s *Service) fillText(ctx context.Context, b *textBlock, renderKey, blobKey string, size int64, lang string, renderMax int64) int64 {
	b.Lang = lang
	if renderKey != "" && renderMax > 0 {
		raw, ok, err := s.readBlob(ctx, renderKey, renderMax)
		if err == nil && ok {
			var rd rendered
			if json.Unmarshal(raw, &rd) == nil {
				b.Lang = rd.Lang
				b.Code = template.HTML(rd.Code)         // #nosec G203 -- produced by render.Highlight at creation
				b.Markdown = template.HTML(rd.Markdown) // #nosec G203 -- sanitized by render.Markdown at creation
				b.Gutter = gutter(rd.Lines)
				if b.View == viewMarkdown && b.Markdown == "" {
					b.View = viewText
				}
				return int64(len(raw))
			}
		}
	}
	// No cached render: too large to highlight, or the render is missing.
	b.View = viewText
	if blobKey == "" || size > render.DefaultMaxBytes {
		b.TooLarge = true
		return 0
	}
	src, ok, err := s.readBlob(ctx, blobKey, render.DefaultMaxBytes)
	if err != nil || !ok {
		b.TooLarge = true // shown with a raw link
		return 0
	}
	b.Code, b.Gutter = render.Plain(src), gutter(lineCount(src))
	return 0
}

func (s *Service) sharePage(w http.ResponseWriter, r *http.Request, it *Item, owner bool) {
	if it.Kind == KindAlbum {
		s.albumPage(w, r, it, owner) // hooks.go: the albums package renders it
		return
	}
	if it.PasswordHash != "" && !owner && !s.unlocked(r, it) {
		s.unlockPage(w, r, it, http.StatusOK, "")
		return
	}
	if it.Kind == KindLink {
		s.countView(r, it, owner)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		http.Redirect(w, r, it.TargetURL, http.StatusFound) // #nosec G710 -- a short link; target validated at creation
		return
	}
	s.countView(r, it, owner)
	if it.Kind == KindGist {
		s.gistPage(w, r, it, owner)
		return
	}
	v, err := s.shareView(r.Context(), it, owner)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Embed = s.embed(r.Context(), it)
	if owner && OwnerSession(r, it) {
		v.Media = Marker(s.MediaState(r.Context(), it))
	}
	s.d.Render.Page(w, r, http.StatusOK, "share/item", web.Page{
		Title: v.Name, NoIndex: true, Scripts: []string{"js/share.js"}, Data: v,
	})
}

func (s *Service) shareView(ctx context.Context, it *Item, owner bool) (*shareView, error) {
	v := &shareView{
		Item: it, Name: it.Name(), View: viewKind(it.Kind, it.MIME, it.MetaString("lang")),
		PageURL: s.PublicURL(it.Slug), RawURL: s.contentURL(it, modeRaw, true), DownloadURL: s.contentURL(it, modeDownload, true),
		Type: typeLabel(it), Owner: owner, Private: it.Visibility == Private, Password: it.PasswordHash != "",
		PDFFrame: s.d.Cfg.RawURL != nil,
	}
	if it.ThumbKey != "" {
		v.ThumbURL = s.contentURL(it, modeThumb, true)
	}
	v.Size = web.HumanBytes(it.Size)
	v.File = v.Type + mediaMeta(it) + " · " + v.Size
	if it.DurationMS > 0 {
		v.Duration = clock(time.Duration(it.DurationMS) * time.Millisecond)
	}
	if owner {
		v.ManageURL = ItemPath(it.ID)
	}
	v.Details = append([]map[string]any{
		{"label": "file", "value": v.File, "mono": true},
	}, ShareDetails(it, owner)...)
	if v.View != viewText && v.View != viewMarkdown {
		return v, nil
	}
	b := &textBlock{ID: "share", Name: v.Name, View: v.View, Size: v.Size, RawURL: v.RawURL}
	s.fillText(ctx, b, it.RenderKey, it.BlobKey, it.Size, it.MetaString("lang"), 16<<20)
	v.Text, v.View, v.Lang, v.TooLarge = b, b.View, b.Lang, b.TooLarge
	return v, nil
}

func gutter(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(i))
	}
	return b.String()
}

type unlockView struct {
	Slug  string
	Next  string
	Error string
}

// unlockPage asks for an item's password. It names nothing about the item.
func (s *Service) unlockPage(w http.ResponseWriter, r *http.Request, it *Item, status int, msg string) {
	next := r.URL.Path
	if r.Method == http.MethodPost {
		next = web.SafeNext(r.PostForm.Get("next"))
	}
	w.Header().Set("Cache-Control", "no-store")
	s.d.Render.Page(w, r, status, "share/unlock", web.Page{
		Title: "Password required", NoIndex: true,
		Data: unlockView{Slug: it.Slug, Next: next, Error: msg},
	})
}
