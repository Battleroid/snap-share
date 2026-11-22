package items_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestEmbeds(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	if err := items.SavePrefs(e.ctx, e.s.App.DB, 1, items.Prefs{EmbedColor: "#1F3FD6", EmbedTitle: "ann's shots"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	img := e.put("ann", "secret-plans.png", string(pixelPNG), "")
	vid := e.put("ann", "holiday-clip.mp4", fakeMP4, "")
	// Stand in for the media job: a poster and the probed size.
	e.thumb(num(vid, "id"))
	if err := e.svc.SetMedia(e.ctx, num(vid, "id"), items.Media{Width: 1920, Height: 1080, DurationMS: 62_000}); err != nil {
		t.Fatal(err)
	}
	head := func(slug string, opts ...reqOpt) (int, string, string) {
		res, body := e.do("GET", "/"+slug, nil, append([]reqOpt{header("User-Agent", uaDiscord)}, opts...)...)
		h, _, _ := strings.Cut(body, "</head>")
		return res.StatusCode, h, body
	}
	meta := func(prop, content string) string { return `<meta property="` + prop + `" content="` + content + `">` }

	code, h, body := head(str(img, "slug"))
	base := e.s.URL + "/" + str(img, "slug")
	for _, want := range []string{
		meta("og:site_name", "ann&#39;s shots"), meta("og:title", "secret-plans.png"), meta("og:type", "website"),
		meta("og:url", base), meta("og:image", base+".png"), meta("og:image:width", "2"), meta("og:image:height", "1"),
		`<meta name="twitter:card" content="summary_large_image">`, `<meta name="theme-color" content="#1f3fd6">`,
	} {
		if code != 200 || !strings.Contains(h, want) {
			t.Errorf("public image: head lacks %s", want)
		}
	}
	if strings.Contains(h, "og:video") || strings.Contains(h, `rel="alternate"`) {
		t.Error("image page has video or alternate links")
	}
	if !strings.Contains(body, "png · 2×1") {
		t.Error("meta list lacks the dimensions")
	}

	_, h, body = head(str(vid, "slug"))
	vbase := e.s.URL + "/" + str(vid, "slug")
	for _, want := range []string{
		meta("og:type", "video.other"), meta("og:image", vbase+"/thumb"), meta("og:image:width", "640"), meta("og:image:height", "360"),
		meta("og:video", vbase+".mp4"), meta("og:video:secure_url", vbase+".mp4"), meta("og:video:type", "video/mp4"),
		meta("og:video:width", "1920"), meta("og:video:height", "1080"), `<meta name="twitter:card" content="summary_large_image">`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("public video: head lacks %s", want)
		}
	}
	if !strings.Contains(body, `poster="`+vbase+`/thumb"`) || !strings.Contains(body, "mp4 · 1920×1080 · 1:02") {
		t.Error("video page lacks the poster or its dimensions and duration")
	}

	// Private, password-protected and expired items: nothing about the item.
	priv := e.put("ann", "private-name.png", string(pixelPNG), "visibility=private")
	pw := e.put("ann", "locked-name.png", string(pixelPNG), "", header(items.PasswordHeader, "hunter22"))
	exp := e.put("ann", "expired-name.png", string(pixelPNG), "")
	e.exec(`UPDATE items SET expires_at = 1 WHERE id = ?`, num(exp, "id"))
	for _, tc := range []struct {
		j    map[string]any
		name string
		code int
	}{{priv, "private-name", 404}, {pw, "locked-name", 200}, {exp, "expired-name", 404}} {
		code, h, body := head(str(tc.j, "slug"))
		if code != tc.code || strings.Contains(body, tc.name) || strings.Contains(h, "og:image") || strings.Contains(h, "og:video") ||
			strings.Contains(h, "og:url") || strings.Contains(h, "theme-color") {
			t.Errorf("%s: %d, leaks the item:\n%s", tc.name, code, h)
		}
	}
	if _, h, _ := head(str(pw, "slug")); !strings.Contains(h, meta("og:title", "snap")) {
		t.Error("unlock page has no generic card")
	}
	// The owner's view of a private item has the generic card too.
	_, h, _ = head(str(priv, "slug"), as("ann"))
	if strings.Contains(h, "og:image") || !strings.Contains(h, meta("og:title", "snap")) || strings.Contains(h, "og:url") {
		t.Errorf("private item, owner view: %s", h)
	}
}

func TestEmbedsRawOrigin(t *testing.T) {
	e := newEnv(t, testutil.Env("RAW_URL", "https://raw.test"))
	e.user("ann", 1, true)
	img := e.put("ann", "pixel.png", string(pixelPNG), "")
	_, body := e.do("GET", "/"+str(img, "slug"), nil, header("User-Agent", uaDiscord))
	if !strings.Contains(body, `<meta property="og:image" content="https://raw.test/`+str(img, "slug")+`.png">`) ||
		!strings.Contains(body, `<meta property="og:url" content="`+e.s.URL+"/"+str(img, "slug")+`">`) {
		t.Errorf("raw origin embed:\n%s", body)
	}
}
