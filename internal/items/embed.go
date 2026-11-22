package items

// OpenGraph/Twitter embed data for share pages (media+stats track).

import (
	"context"
	"math"
	"strings"
)

// embedView is the OpenGraph/Twitter card of a share page. Only unlisted
// items without a password get the full card; others get a generic one with
// no media, title or URL (the slug can be the filename).
type embedView struct {
	Full     bool
	SiteName string // the owner's embed title; "" uses the site name
	Title    string
	Type     string // website | video.other
	URL      string
	Image    string
	ImageW   int64
	ImageH   int64
	Video    string
	VideoTyp string
	VideoW   int64
	VideoH   int64
	Secure   bool   // the video URL is https (og:video:secure_url)
	Card     string // twitter:card
	Color    string // theme-color, #rrggbb
}

// Thumbnail and poster widths made by the media track (media.Config defaults).
const embedThumbWidth = 640

func (s *Service) embed(ctx context.Context, it *Item) *embedView {
	if it.Visibility != Unlisted || it.PasswordHash != "" || it.Expired(s.now()) || it.Kind == KindLink || it.Kind == KindAlbum {
		return &embedView{}
	}
	e := &embedView{Full: true, Title: it.Name(), Type: "website", URL: s.PublicURL(it.Slug), Card: "summary"}
	if prefs, err := LoadPrefs(ctx, s.d.DB, it.OwnerID); err == nil {
		e.SiteName = prefs.EmbedTitle
		if colorRe.MatchString(prefs.EmbedColor) {
			e.Color = strings.ToLower(prefs.EmbedColor)
		}
	}
	thumb := func() {
		if it.ThumbKey == "" {
			return
		}
		e.Image = s.contentURL(it, modeThumb, false)
		e.ImageW, e.ImageH = fitWidth(it.Width, it.Height, embedThumbWidth)
	}
	switch viewKind(it.Kind, it.MIME, it.MetaString("lang")) {
	case viewImage:
		switch it.MIME {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
			e.Image, e.ImageW, e.ImageH = s.contentURL(it, modeRaw, false), it.Width, it.Height
		default: // formats chat apps rarely render (AVIF, BMP, SVG, icons)
			thumb()
		}
	case viewVideo:
		thumb() // the poster
		if it.MIME == "video/mp4" || it.MIME == "video/webm" {
			e.Video, e.VideoTyp, e.VideoW, e.VideoH = s.contentURL(it, modeRaw, false), it.MIME, it.Width, it.Height
			e.Secure = strings.HasPrefix(e.Video, "https://")
			e.Type = "video.other"
		}
	}
	if e.Image != "" {
		e.Card = "summary_large_image"
	}
	return e
}

// fitWidth mirrors the media package: scale w×h down to at most maxW wide.
func fitWidth(w, h, maxW int64) (int64, int64) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	tw := min(maxW, w)
	return tw, max(1, int64(math.Round(float64(h)*float64(tw)/float64(w))))
}
