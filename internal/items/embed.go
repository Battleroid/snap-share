package items

// Link previews for chat apps: OpenGraph and Twitter cards on share and
// album pages, an oEmbed endpoint, and a frameable player page for videos.
//
// What the big two read (docs/media.md has the details):
//   - Discord: og:* and twitter:*; a video plays inline from og:video (the
//     file, https, MP4/WebM/MOV) when og:type is video.* or twitter:card is
//     player, and only with an og:image poster. Two or more og:image make a
//     gallery of up to four. Descriptions show up to 350 bytes.
//   - Slack: og:*, Twitter cards and oEmbed; it plays a video in an iframe of
//     twitter:player (or an oEmbed video's html), so videos get /{slug}/player.

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/web"
)

// EmbedImage is one og:image with its size (0 when unknown).
type EmbedImage struct {
	URL  string
	W, H int64
}

// EmbedView is the OpenGraph/Twitter card of a share or album page. Only
// unlisted, unexpired items without a password get the full card; others
// get a generic one with no media, title or URL (the slug can be the
// filename).
type EmbedView struct {
	Full     bool
	SiteName string // the owner's embed title; "" uses the site name
	Title    string
	Desc     string // og:description: a text excerpt, "3 files", "12 images · 2 videos"
	Type     string // website | video.other
	URL      string
	Images   []EmbedImage // og:image, in order; Discord shows up to four as a gallery
	Video    string       // og:video and twitter:player:stream: the file
	VideoTyp string
	VideoW   int64
	VideoH   int64
	Secure   bool   // the video URL is https (og:video:secure_url)
	Player   string // twitter:player: /{slug}/player, framed by Slack and others
	Card     string // twitter:card
	Color    string // theme-color, #rrggbb
	OEmbed   string // the oEmbed discovery URL
}

// Image is the first og:image, for twitter:image ("" when none).
func (e *EmbedView) Image() string {
	if len(e.Images) == 0 {
		return ""
	}
	return e.Images[0].URL
}

const (
	embedThumbWidth = 640 // thumbnail and poster width made by the media track (media.Config default)
	embedImages     = 4   // og:image tags on an album card (Discord's gallery)
	embedDescBytes  = 300 // under Discord's 350-byte description limit
	embedDescLines  = 12
	embedTextRead   = 4 << 10 // bytes read from a text file for its excerpt
)

// embedImageTypes are the image formats chat apps render from og:image;
// other formats (BMP, SVG, icons, TIFF) use the thumbnail.
var embedImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true, "image/avif": true}

// embedVideoTypes are the video formats Discord plays from og:video.
var embedVideoTypes = map[string]bool{"video/mp4": true, "video/webm": true, "video/quicktime": true}

// public reports whether anyone with the link may see it: unlisted,
// unexpired and without a password. Only these get a full card, an oEmbed
// response or a player.
func (s *Service) public(it *Item) bool {
	return it.Visibility == Unlisted && it.PasswordHash == "" && !it.Expired(s.now())
}

// playable reports whether it is a video a chat app can play inline.
func playable(it *Item) bool { return it.Kind == KindFile && embedVideoTypes[it.MIME] }

// Embed is the card for an item's share page. Albums use AlbumEmbed.
func (s *Service) Embed(ctx context.Context, it *Item) *EmbedView { return s.embed(ctx, it) }

func (s *Service) embed(ctx context.Context, it *Item) *EmbedView {
	if !s.public(it) || it.Kind == KindLink || it.Kind == KindAlbum {
		return &EmbedView{}
	}
	e := s.embedBase(ctx, it)
	switch viewKind(it.Kind, it.MIME, it.MetaString("lang")) {
	case viewImage:
		if img, ok := s.embedPicture(it); ok {
			e.Images = append(e.Images, img)
		}
	case viewVideo:
		if img, ok := s.embedPicture(it); ok { // the poster
			e.Images = append(e.Images, img)
		}
		if playable(it) {
			e.Video, e.VideoTyp, e.VideoW, e.VideoH = s.contentURL(it, modeRaw, false), it.MIME, it.Width, it.Height
			e.Secure = strings.HasPrefix(e.Video, "https://")
			e.Type = "video.other"
			e.Player = s.PublicURL(it.Slug) + "/player"
			e.Card = "player"
		}
	case viewText, viewMarkdown:
		e.Desc = s.textExcerpt(ctx, it.BlobKey)
	case viewGist:
		if err := LoadGistFiles(ctx, s.d.DB.R, it); err == nil && len(it.Files) > 0 {
			names := make([]string, 0, len(it.Files))
			for _, f := range it.Files {
				names = append(names, f.Name)
			}
			e.Desc = filesText(len(it.Files)) + ": " + strings.Join(names, ", ")
			if ex := s.textExcerpt(ctx, it.Files[0].BlobKey); ex != "" {
				e.Desc = clip(e.Desc+"\n\n"+ex, embedDescBytes, embedDescLines)
			}
		}
	default: // audio and other files: what it is
		e.Desc = typeLabel(it) + " · " + web.HumanBytes(it.Size)
	}
	if e.Card == "summary" && len(e.Images) > 0 {
		e.Card = "summary_large_image"
	}
	return e
}

// embedBase is the part of a full card every kind shares.
func (s *Service) embedBase(ctx context.Context, it *Item) *EmbedView {
	e := &EmbedView{
		Full: true, Title: it.Name(), Type: "website", URL: s.PublicURL(it.Slug), Card: "summary",
		OEmbed: s.origin() + "/oembed?format=json&url=" + url.QueryEscape(s.PublicURL(it.Slug)),
	}
	if prefs, err := LoadPrefs(ctx, s.d.DB, it.OwnerID); err == nil {
		e.SiteName = prefs.EmbedTitle
		if colorRe.MatchString(prefs.EmbedColor) {
			e.Color = strings.ToLower(prefs.EmbedColor)
		}
	}
	return e
}

// embedPicture is the image a card shows for it: the file itself for an
// image chat apps render, else its thumbnail (a video's poster).
func (s *Service) embedPicture(it *Item) (EmbedImage, bool) {
	if it.Kind == KindFile && embedImageTypes[it.MIME] && strings.HasPrefix(it.MIME, "image/") {
		return EmbedImage{URL: s.contentURL(it, modeRaw, false), W: it.Width, H: it.Height}, true
	}
	if it.ThumbKey == "" {
		return EmbedImage{}, false
	}
	w, h := fitWidth(it.Width, it.Height, embedThumbWidth)
	return EmbedImage{URL: s.contentURL(it, modeThumb, false), W: w, H: h}, true
}

// AlbumEmbed is the card for an album's page: a gallery of up to four of
// its members (images, and posters of videos) and what it holds. Only
// members anyone with the link may open count, whoever is viewing.
func (s *Service) AlbumEmbed(ctx context.Context, al *Item, members []*Item) *EmbedView {
	if !s.public(al) {
		return &EmbedView{}
	}
	e := s.embedBase(ctx, al)
	var images, videos, other int
	for _, m := range members {
		if !s.public(m) || m.Kind == KindAlbum {
			continue
		}
		switch viewKind(m.Kind, m.MIME, m.MetaString("lang")) {
		case viewImage:
			images++
		case viewVideo:
			videos++
		default:
			other++
		}
		if len(e.Images) < embedImages {
			if img, ok := s.embedPicture(m); ok {
				e.Images = append(e.Images, img)
			}
		}
	}
	var parts []string
	for _, c := range []struct {
		n         int
		one, many string
	}{{images, "image", "images"}, {videos, "video", "videos"}, {other, "file", "files"}} {
		switch c.n {
		case 0:
		case 1:
			parts = append(parts, "1 "+c.one)
		default:
			parts = append(parts, strconv.Itoa(c.n)+" "+c.many)
		}
	}
	e.Desc = strings.Join(parts, " · ")
	if len(e.Images) > 0 {
		e.Card = "summary_large_image"
	}
	return e
}

// textExcerpt is the start of a text blob for a card: valid UTF-8, line
// ends kept, at most embedDescLines lines and embedDescBytes bytes, with an
// ellipsis when cut. "" when it can't be read or is blank.
func (s *Service) textExcerpt(ctx context.Context, key string) string {
	if key == "" {
		return ""
	}
	f, err := s.d.Blobs.Open(ctx, key)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, embedTextRead))
	if err != nil {
		return ""
	}
	more := f.Size() > int64(len(b))
	for len(b) > 0 && !utf8.Valid(b) { // a rune cut by the read limit
		b, more = b[:len(b)-1], true
	}
	t := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
	if t == "" {
		return ""
	}
	if more {
		t += "\n…"
	}
	return clip(t, embedDescBytes, embedDescLines)
}

// clip shortens t to at most maxLines lines and maxBytes bytes, on a rune
// boundary, ending in "…" when it cut anything.
func clip(t string, maxBytes, maxLines int) string {
	cut := false
	if lines := strings.SplitN(t, "\n", maxLines+1); len(lines) > maxLines {
		t, cut = strings.Join(lines[:maxLines], "\n"), true
	}
	if len(t) > maxBytes {
		end := maxBytes - len("…")
		for end > 0 && !utf8.RuneStart(t[end]) {
			end--
		}
		t, cut = t[:end], true
	}
	t = strings.TrimRight(t, " \n\t")
	if cut && !strings.HasSuffix(t, "…") {
		t += "…"
	}
	return t
}

// fitWidth mirrors the media package: scale w×h down to at most maxW wide.
func fitWidth(w, h, maxW int64) (int64, int64) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	tw := min(maxW, w)
	return tw, max(1, int64(math.Round(float64(h)*float64(tw)/float64(w))))
}

// The player: GET /{slug}/player, a page with nothing but the video, for
// Slack and other apps that play twitter:player or oEmbed videos in an
// iframe. Unlike every other page it may be framed by any site; it has no
// scripts, forms or account state. Only public, playable videos have one.

// playerSize is a video's frame for players that need one: its own size,
// else 1280×720.
func playerSize(it *Item) (int64, int64) {
	if it.Width > 0 && it.Height > 0 {
		return it.Width, it.Height
	}
	return 1280, 720
}

type playerView struct {
	Title  string
	Src    string
	Poster string
	W, H   int64
}

func (s *Service) playerPage(w http.ResponseWriter, r *http.Request, it *Item) {
	if !s.public(it) || !playable(it) {
		s.notFound(w, r)
		return
	}
	v := playerView{Title: it.Name(), Src: s.contentURL(it, modeRaw, false)}
	v.W, v.H = playerSize(it)
	if it.ThumbKey != "" {
		v.Poster = s.contentURL(it, modeThumb, false)
	}
	media := "'self'"
	if raw := s.d.Cfg.RawOrigin(); raw != "" {
		media += " " + raw
	}
	h := w.Header()
	h.Del("X-Frame-Options")
	h.Set("Content-Security-Policy", "default-src 'none'; media-src "+media+"; img-src "+media+"; style-src "+s.origin()+"/static/; "+
		"frame-ancestors *; base-uri 'none'; form-action 'none'")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Robots-Tag", "noindex")
	s.recordEvent(r, it, modePlayer, false)
	s.d.Render.Fragment(w, r, http.StatusOK, "share/player", "player", web.Page{Title: v.Title, Data: v})
}

// oEmbed: GET /oembed?url=<share page>&format=json (https://oembed.com).
// Slack reads it; Discord fills gaps from it. Only public items and albums
// answer; anything else is a 404 that spends the slug miss budget, like the
// page itself. Only JSON: format=xml gets 501.

type oembedJSON struct {
	Version      string `json:"version"`
	Type         string `json:"type"` // photo | video | link
	Title        string `json:"title,omitempty"`
	ProviderName string `json:"provider_name"`
	ProviderURL  string `json:"provider_url"`
	URL          string `json:"url,omitempty"`  // photo
	HTML         string `json:"html,omitempty"` // video
	Width        int64  `json:"width,omitempty"`
	Height       int64  `json:"height,omitempty"`
	ThumbURL     string `json:"thumbnail_url,omitempty"`
	ThumbW       int64  `json:"thumbnail_width,omitempty"`
	ThumbH       int64  `json:"thumbnail_height,omitempty"`
}

func (s *Service) oembed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if f := q.Get("format"); f != "" && f != "json" {
		s.d.Errors.Error(w, r, http.StatusNotImplemented, "Only JSON is supported.")
		return
	}
	u, err := url.Parse(q.Get("url"))
	if err != nil || !strings.EqualFold(u.Scheme+"://"+u.Host, s.origin()) || u.RawQuery != "" {
		s.notFound(w, r)
		return
	}
	slug, m, ok := parsePath(u.Path)
	if !ok || m != modePage || IsReserved(slug, s.d.ExtraReserved...) {
		s.notFound(w, r)
		return
	}
	it, err := s.GetBySlug(r.Context(), slug)
	if err != nil || !s.public(it) || it.Kind == KindLink {
		s.notFound(w, r)
		return
	}
	var e *EmbedView
	if it.Kind == KindAlbum {
		if s.hooks.albumMembers == nil {
			s.notFound(w, r)
			return
		}
		members, err := s.hooks.albumMembers(r.Context(), it.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		e = s.AlbumEmbed(r.Context(), it, members)
	} else {
		e = s.embed(r.Context(), it)
	}
	o := oembedJSON{Version: "1.0", Type: "link", Title: e.Title, ProviderName: s.siteName(), ProviderURL: s.origin()}
	if e.SiteName != "" {
		o.ProviderName = e.SiteName
	}
	if len(e.Images) > 0 {
		o.ThumbURL, o.ThumbW, o.ThumbH = e.Images[0].URL, e.Images[0].W, e.Images[0].H
	}
	maxW, _ := strconv.ParseInt(q.Get("maxwidth"), 10, 64)
	maxH, _ := strconv.ParseInt(q.Get("maxheight"), 10, 64)
	switch {
	case e.Player != "":
		pw, ph := fitBox(playerSize(it))(maxW, maxH)
		o.Type, o.Width, o.Height = "video", pw, ph
		o.HTML = fmt.Sprintf(`<iframe src="%s" width="%d" height="%d" frameborder="0" allow="autoplay; fullscreen; picture-in-picture" allowfullscreen title="%s"></iframe>`,
			template.HTMLEscapeString(e.Player), pw, ph, template.HTMLEscapeString(e.Title))
	case it.Kind == KindFile && embedImageTypes[it.MIME] && len(e.Images) > 0 && e.Images[0].W > 0:
		o.Type, o.URL = "photo", e.Images[0].URL
		o.Width, o.Height = fitBox(e.Images[0].W, e.Images[0].H)(maxW, maxH)
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(o)
}

func (s *Service) siteName() string {
	if s.d.SiteName != nil {
		return s.d.SiteName()
	}
	return "snap"
}

// fitBox scales w×h down to fit within maxW×maxH (either 0: no limit).
func fitBox(w, h int64) func(maxW, maxH int64) (int64, int64) {
	return func(maxW, maxH int64) (int64, int64) {
		scale := 1.0
		if maxW > 0 && w > maxW {
			scale = float64(maxW) / float64(w)
		}
		if maxH > 0 && float64(h)*scale > float64(maxH) {
			scale = float64(maxH) / float64(h)
		}
		return max(1, int64(math.Round(float64(w)*scale))), max(1, int64(math.Round(float64(h)*scale)))
	}
}
