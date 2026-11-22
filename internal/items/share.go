package items

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/render"
	"github.com/Battleroid/snap-share/internal/web"
)

type metaPair struct{ Label, Value string }

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
	Meta        []metaPair
	Lang        string
	Code        template.HTML
	Gutter      string
	Markdown    template.HTML
	TooLarge    bool
	PDFFrame    bool
	Owner       bool
	Private     bool
	Password    bool
	Embed       *embedView // OpenGraph/Twitter tags (media.go)
}

func (s *Service) sharePage(w http.ResponseWriter, r *http.Request, it *Item, owner bool) {
	if it.Kind == KindAlbum {
		s.notFound(w, r) // album pages belong to the albums package
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
	v, err := s.shareView(r.Context(), it, owner)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v.Embed = s.embed(r.Context(), it)
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
	expires := "never"
	if it.ExpiresAt != nil {
		expires = it.ExpiresAt.UTC().Format("2006-01-02 15:04")
	}
	v.Meta = []metaPair{
		{"size", web.HumanBytes(it.Size)},
		{"type", v.Type + mediaMeta(it)},
		{"expires", expires},
		{"uploaded", it.CreatedAt.UTC().Format("2006-01-02")},
	}
	if v.View != viewText && v.View != viewMarkdown {
		return v, nil
	}
	if it.RenderKey != "" {
		b, ok, err := s.readBlob(ctx, it.RenderKey, 16<<20)
		if err == nil && ok {
			var rd rendered
			if json.Unmarshal(b, &rd) == nil {
				v.Lang = rd.Lang
				v.Code = template.HTML(rd.Code)         // #nosec G203 -- produced by render.Highlight at creation
				v.Markdown = template.HTML(rd.Markdown) // #nosec G203 -- sanitized by render.Markdown at creation
				v.Gutter = gutter(rd.Lines)
				if v.View == viewMarkdown && v.Markdown == "" {
					v.View = viewText
				}
				return v, nil
			}
		}
	}
	// No cached render: too large to highlight, or the render is missing.
	if it.Size > render.DefaultMaxBytes {
		v.TooLarge = true
		v.View = viewText
		return v, nil
	}
	src, ok, err := s.readBlob(ctx, it.BlobKey, render.DefaultMaxBytes)
	if err != nil || !ok {
		v.TooLarge = true
		v.View = viewText
		return v, nil //nolint:nilerr // shown as "too large to preview" with a raw link
	}
	v.View, v.Code, v.Gutter, v.Lang = viewText, render.Plain(src), gutter(lineCount(src)), it.MetaString("lang")
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
