package items_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
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
	if strings.Contains(h, "og:video") || strings.Contains(h, "twitter:player") {
		t.Error("image page has video tags")
	}
	if !strings.Contains(h, `<link rel="alternate" type="application/json+oembed" href="`+e.s.URL+"/oembed?format=json&amp;url="+url.QueryEscape(base)+`"`) {
		t.Error("image page lacks the oEmbed link")
	}
	if !strings.Contains(body, "png · 2×1") {
		t.Error("meta list lacks the dimensions")
	}

	_, h, body = head(str(vid, "slug"))
	vbase := e.s.URL + "/" + str(vid, "slug")
	for _, want := range []string{
		meta("og:type", "video.other"), meta("og:image", vbase+"/thumb"), meta("og:image:width", "640"), meta("og:image:height", "360"),
		meta("og:video", vbase+".mp4"), meta("og:video:secure_url", vbase+".mp4"), meta("og:video:type", "video/mp4"),
		meta("og:video:width", "1920"), meta("og:video:height", "1080"), `<meta name="twitter:card" content="player">`,
		`<meta name="twitter:player" content="` + vbase + `/player">`, `<meta name="twitter:player:width" content="1920">`,
		`<meta name="twitter:player:height" content="1080">`, `<meta name="twitter:player:stream" content="` + vbase + `.mp4">`,
		`<meta name="twitter:player:stream:content_type" content="video/mp4">`, `<meta name="twitter:image" content="` + vbase + `/thumb">`,
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

func TestEmbedTextAndFiles(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	head := func(slug string) string {
		_, body := e.do("GET", "/"+slug, nil, header("User-Agent", uaDiscord))
		h, _, _ := strings.Cut(body, "</head>")
		return h
	}
	desc := func(h string) string {
		_, rest, ok := strings.Cut(h, `<meta property="og:description" content="`)
		if !ok {
			return ""
		}
		d, _, _ := strings.Cut(rest, `">`)
		return d
	}

	// A short text: the whole of it, line ends kept.
	short := e.put("ann", "notes.txt", "first line\r\nsecond line\n", "")
	if d := desc(head(str(short, "slug"))); d != "first line\nsecond line" {
		t.Errorf("short text: %q", d)
	}
	// A long one: at most 12 lines and 300 bytes, on a rune boundary, with an ellipsis.
	long := strings.Repeat("ünïcödé lïne of téxt\n", 40)
	lt := e.put("ann", "long.txt", long, "")
	d := desc(head(str(lt, "slug")))
	if len(d) > 300 || strings.Count(d, "\n") > 11 || !strings.HasSuffix(d, "…") || !strings.HasPrefix(d, "ünïcödé lïne") {
		t.Errorf("long text (%d bytes): %q", len(d), d)
	}
	// Markup in the text is escaped in the attribute, not interpreted.
	tag := e.put("ann", "tag.txt", `<script>alert("x")</script>`, "")
	if h := head(str(tag, "slug")); strings.Contains(h, "<script>alert") || !strings.Contains(h, "&lt;script&gt;") {
		t.Errorf("markup in excerpt:\n%s", h)
	}
	// Other files: what they are and how big.
	bin := e.put("ann", "archive.zip", "PK\x03\x04"+strings.Repeat("\x00", 100), "")
	if d := desc(head(str(bin, "slug"))); !strings.Contains(d, "·") || !strings.Contains(d, "B") {
		t.Errorf("file description: %q", d)
	}
	// A password-protected text: nothing of it.
	pw := e.put("ann", "locked.txt", "the secret plan", "", header(items.PasswordHeader, "hunter22"))
	if h := head(str(pw, "slug")); strings.Contains(h, "secret plan") || strings.Contains(h, "og:description") {
		t.Errorf("locked text leaks:\n%s", h)
	}
}

func TestPlayerPage(t *testing.T) {
	e := newEnv(t, testutil.Env("RAW_URL", "https://raw.test"))
	e.user("ann", 1, true)
	vid := e.put("ann", "clip.mp4", fakeMP4, "")
	e.thumb(num(vid, "id"))
	slug := str(vid, "slug")
	res, body := e.do("GET", "/"+slug+"/player", nil, header("User-Agent", "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)"))
	if res.StatusCode != 200 {
		t.Fatalf("player: %d", res.StatusCode)
	}
	if xfo := res.Header.Get("X-Frame-Options"); xfo != "" {
		t.Errorf("player has X-Frame-Options %q", xfo)
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{"frame-ancestors *", "default-src 'none'", "media-src 'self' https://raw.test", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("player CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "script-src") || strings.Contains(body, "<script") {
		t.Error("player has scripts")
	}
	if !strings.Contains(body, `<video controls playsinline preload="metadata" src="https://raw.test/`+slug+`.mp4" poster="https://raw.test/`+slug+`/thumb"`) ||
		strings.Contains(body, "app-header") {
		t.Errorf("player body:\n%s", body)
	}
	// Every other page still refuses framing.
	res, _ = e.do("GET", "/"+slug, nil)
	if res.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Error("share page is frameable")
	}

	// No player for what isn't a public video.
	img := e.put("ann", "pixel.png", string(pixelPNG), "")
	priv := e.put("ann", "private.mp4", fakeMP4, "visibility=private")
	pw := e.put("ann", "locked.mp4", fakeMP4, "", header(items.PasswordHeader, "hunter22"))
	exp := e.put("ann", "old.mp4", fakeMP4, "")
	e.exec(`UPDATE items SET expires_at = 1 WHERE id = ?`, num(exp, "id"))
	for name, j := range map[string]map[string]any{"image": img, "private": priv, "password": pw, "expired": exp} {
		if res, _ := e.do("GET", "/"+str(j, "slug")+"/player", nil); res.StatusCode != 404 {
			t.Errorf("%s: player %d", name, res.StatusCode)
		}
	}
	// Not even for the owner of a private one.
	if res, _ := e.do("GET", "/"+str(priv, "slug")+"/player", nil, as("ann")); res.StatusCode != 404 {
		t.Errorf("private, owner: player %d", res.StatusCode)
	}
	// The raw origin sends it to the app origin, like any page.
	res, _ = e.do("GET", "/"+slug+"/player", nil, func(r *http.Request) { r.Host = "raw.test" })
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), e.s.URL+"/"+slug+"/player") {
		t.Errorf("raw origin player: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestOEmbed(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	img := e.put("ann", "pixel.png", string(pixelPNG), "")
	vid := e.put("ann", "clip.mp4", fakeMP4, "")
	e.thumb(num(vid, "id"))
	if err := e.svc.SetMedia(e.ctx, num(vid, "id"), items.Media{Width: 1920, Height: 1080}); err != nil {
		t.Fatal(err)
	}
	txt := e.put("ann", "notes.txt", "hello", "")
	priv := e.put("ann", "private.png", string(pixelPNG), "visibility=private")
	pw := e.put("ann", "locked.png", string(pixelPNG), "", header(items.PasswordHeader, "hunter22"))
	get := func(page string, extra string) (int, map[string]any) {
		res, body := e.do("GET", "/oembed?format=json&url="+url.QueryEscape(page)+extra, nil, header("User-Agent", "Slackbot 1.0"))
		var o map[string]any
		_ = json.Unmarshal([]byte(body), &o)
		return res.StatusCode, o
	}
	page := func(j map[string]any) string { return e.s.URL + "/" + str(j, "slug") }

	code, o := get(page(img), "")
	if code != 200 || o["type"] != "photo" || o["url"] != page(img)+".png" || o["width"] != 2.0 || o["provider_name"] != "snap" || o["version"] != "1.0" {
		t.Errorf("photo: %d %v", code, o)
	}
	code, o = get(page(vid), "&maxwidth=640")
	html, _ := o["html"].(string)
	if code != 200 || o["type"] != "video" || o["width"] != 640.0 || o["height"] != 360.0 ||
		!strings.Contains(html, `<iframe src="`+page(vid)+`/player" width="640" height="360"`) || o["thumbnail_url"] != page(vid)+"/thumb" {
		t.Errorf("video: %d %v", code, o)
	}
	if code, o = get(page(txt), ""); code != 200 || o["type"] != "link" || o["title"] != "notes.txt" {
		t.Errorf("text: %d %v", code, o)
	}
	for name, u := range map[string]string{
		"private":  page(priv),
		"password": page(pw),
		"unknown":  e.s.URL + "/nosuchslug",
		"foreign":  "https://elsewhere.example/" + str(img, "slug"),
		"raw path": page(img) + "/raw",
		"reserved": e.s.URL + "/dashboard",
		"query":    page(img) + "?x=1",
	} {
		if code, o := get(u, ""); code != 404 || o["type"] != nil {
			t.Errorf("%s: %d %v", name, code, o)
		}
	}
	if res, _ := e.do("GET", "/oembed?format=xml&url="+url.QueryEscape(page(img)), nil); res.StatusCode != http.StatusNotImplemented {
		t.Errorf("xml: %d", res.StatusCode)
	}
}

func TestAlbumEmbed(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	var ids []int64
	for i := range 5 {
		j := e.put("ann", "photo"+strconv.Itoa(i)+".png", string(pixelPNG), "")
		ids = append(ids, num(j, "id"))
	}
	vid := e.put("ann", "clip.mp4", fakeMP4, "")
	e.thumb(num(vid, "id"))
	txt := e.put("ann", "notes.txt", "hello", "")
	priv := e.put("ann", "private.png", string(pixelPNG), "visibility=private")
	// The private member first: it must not be in the gallery or the counts.
	members := append([]int64{num(priv, "id"), num(vid, "id")}, ids...)
	members = append(members, num(txt, "id"))
	res, al := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "Trip", "items": members}, as("ann"))
	if res.StatusCode != 201 {
		t.Fatalf("album: %d %v", res.StatusCode, al)
	}
	_, body := e.do("GET", "/"+str(al, "slug"), nil, header("User-Agent", uaDiscord))
	h, _, _ := strings.Cut(body, "</head>")
	if n := strings.Count(h, `<meta property="og:image" `); n != 4 {
		t.Errorf("%d og:image tags, want 4:\n%s", n, h)
	}
	for _, want := range []string{
		`<meta property="og:title" content="Trip">`, `<meta property="og:description" content="5 images · 1 video · 1 file">`,
		`<meta property="og:image" content="` + e.s.URL + "/" + str(vid, "slug") + `/thumb">`,
		`<meta name="twitter:card" content="summary_large_image">`, `application/json+oembed`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("album head lacks %s", want)
		}
	}
	if strings.Contains(h, str(priv, "slug")) {
		t.Error("album embed shows a private member")
	}
	// A private album: the generic card.
	e.exec(`UPDATE items SET visibility = 'private' WHERE id = ?`, num(al, "id"))
	_, body = e.do("GET", "/"+str(al, "slug"), nil, as("ann"))
	if h, _, _ := strings.Cut(body, "</head>"); strings.Contains(h, "og:image") || strings.Contains(h, `og:title" content="Trip"`) || strings.Contains(h, "og:url") {
		t.Errorf("private album leaks:\n%s", h)
	}
}
