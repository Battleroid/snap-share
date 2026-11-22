package items_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// Recorded User-Agents.
const (
	uaFirefox  = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
	uaDiscord  = "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)"
	uaSlack    = "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)"
	uaHeadless = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/159.0.0.0 Safari/537.36"
	uaCurl     = "curl/8.11.1"
)

// A 2x1 PNG.
var pixelPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAIAAAABCAIAAAB7QOjdAAAADUlEQVR4nGP4zwAE/wEHAAH/4iOeWQAAAABJRU5ErkJggg==")

// fakeMP4 sniffs as video/mp4; nothing here decodes it.
var fakeMP4 = "\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2" + strings.Repeat("\x00", 2000)

// hit is one request in an event-capture case. path is appended to "/slug".
type hit struct {
	path  string
	ua    string
	h     map[string]string
	owner bool
	head  bool
}

// visit sends a request without the same-origin headers testutil adds.
func (e *env) visit(slug string, h hit) int {
	e.t.Helper()
	method := "GET"
	if h.head {
		method = "HEAD"
	}
	req, err := http.NewRequestWithContext(e.ctx, method, e.s.URL+"/"+slug+h.path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("User-Agent", h.ua)
	for k, v := range h.h {
		req.Header.Set(k, strings.ReplaceAll(v, "SELF", e.s.URL))
	}
	if h.owner {
		authtest.Session(req, "ann")
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// thumb gives an item a thumbnail, as the media track would.
func (e *env) thumb(id int64) string {
	e.t.Helper()
	w, err := e.s.App.Files.Create(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	_, _ = w.Write(pixelPNG)
	key, err := w.Commit()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.svc.SetMedia(e.ctx, id, items.Media{ThumbKey: key}); err != nil {
		e.t.Fatal(err)
	}
	return key
}

// events flushes the stats writer and lists an item's events as
// "kind[/bot][@referrer]".
func (e *env) events(id int64) []string {
	e.t.Helper()
	if err := e.s.App.Stats.Flush(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	rows, err := e.s.App.DB.R.Query(`SELECT kind, bot_name, referrer_host, ip, ua_summary, visitor_key FROM item_events WHERE item_id = ? ORDER BY id`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var kind, bot, ref, ip, summary, key string
		if err := rows.Scan(&kind, &bot, &ref, &ip, &summary, &key); err != nil {
			e.t.Fatal(err)
		}
		if ip == "" || summary == "" || len(key) != 32 {
			e.t.Errorf("event %s without ip/summary/visitor key: %q %q %q", kind, ip, summary, key)
		}
		s := kind
		if bot != "" {
			s += "/" + bot
		}
		if ref != "" {
			s += "@" + ref
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestEventCapture(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	doc := map[string]string{"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none"}
	onPage := func(dest string) map[string]string {
		return map[string]string{"Sec-Fetch-Dest": dest, "Sec-Fetch-Site": "same-origin", "Referer": "SELF/"}
	}
	for _, tc := range []struct {
		name    string
		content string // "png", "mp4" or "link"
		hits    []hit
		want    []string
	}{
		{"discord unfurls page and image", "png", []hit{{path: "", ua: uaDiscord}, {path: "/raw", ua: uaDiscord}, {path: "/thumb", ua: uaDiscord}}, []string{"preview/Discord"}},
		{"slack unfurls", "png", []hit{{ua: uaSlack}}, []string{"preview/Slack"}},
		{"browser opens the share page", "png", []hit{{ua: uaFirefox, h: doc}}, []string{"view"}},
		{"reload is the same view", "png", []hit{{ua: uaFirefox, h: doc}, {ua: uaFirefox, h: doc}}, []string{"view"}},
		{"curl fetches the share page", "png", []hit{{ua: uaCurl}}, []string{"view"}},
		{"share page image is part of the view", "png", []hit{{ua: uaFirefox, h: doc}, {path: ".png", ua: uaFirefox, h: onPage("image")}}, []string{"view"}},
		{"image embedded on another site", "png", []hit{{path: ".png", ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "image", "Sec-Fetch-Site": "cross-site", "Referer": "https://forum.example/t/42?x=1"}}}, []string{"embed@forum.example"}},
		{"hotlinked without a referrer", "png", []hit{{path: "/raw", ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "image", "Sec-Fetch-Site": "cross-site"}}}, []string{"embed"}},
		{"old browser with a foreign referrer", "png", []hit{{path: "/raw", ua: uaFirefox, h: map[string]string{"Referer": "https://blog.example/"}}}, []string{"embed@blog.example"}},
		{"download button", "png", []hit{{path: "/download", ua: uaFirefox, h: doc}}, []string{"download"}},
		{"raw opened in a tab", "png", []hit{{path: "/raw", ua: uaFirefox, h: doc}}, []string{"download"}},
		{"curl downloads", "png", []hit{{path: ".png", ua: uaCurl}}, []string{"download"}},
		{"thumbnail fetches never count", "png", []hit{{path: "/thumb", ua: uaFirefox, h: onPage("image")}, {path: "/thumb", ua: uaCurl}}, nil},
		{"short link followed", "link", []hit{{ua: uaFirefox, h: doc}}, []string{"click"}},
		{"short link unfurled", "link", []hit{{ua: uaDiscord}}, []string{"preview/Discord"}},
		{"range requests are one play", "mp4", []hit{
			{ua: uaFirefox, h: doc},
			{path: ".mp4", ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "video", "Sec-Fetch-Site": "same-origin", "Referer": "SELF/", "Range": "bytes=0-"}},
			{path: ".mp4", ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "video", "Sec-Fetch-Site": "same-origin", "Referer": "SELF/", "Range": "bytes=1000-"}},
			{path: ".mp4", ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "video", "Sec-Fetch-Site": "same-origin", "Referer": "SELF/", "Range": "bytes=1500-"}},
		}, []string{"view", "play"}},
		{"curl range requests", "mp4", []hit{{path: "/raw", ua: uaCurl, h: map[string]string{"Range": "bytes=0-99"}}, {path: "/raw", ua: uaCurl, h: map[string]string{"Range": "bytes=100-"}}}, []string{"play"}},
		{"video embedded elsewhere", "mp4", []hit{{path: "/raw", ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "video", "Sec-Fetch-Site": "cross-site", "Referer": "https://chat.example/", "Range": "bytes=0-"}}}, []string{"embed@chat.example"}},
		{"owner's own requests", "png", []hit{{ua: uaFirefox, h: doc, owner: true}, {path: "/raw", ua: uaFirefox, h: doc, owner: true}, {path: "/download", ua: uaFirefox, owner: true}}, nil},
		{"headless chrome is dropped", "png", []hit{{ua: uaHeadless, h: doc}, {path: "/raw", ua: uaHeadless, h: doc}}, nil},
		{"HEAD is dropped", "png", []hit{{ua: uaFirefox, h: doc, head: true}}, nil},
		{"prefetch is dropped", "png", []hit{{ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "document", "Sec-Purpose": "prefetch"}}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var j map[string]any
			switch tc.content {
			case "link":
				_, j = e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://example.com/"}, as("ann"))
			case "mp4":
				j = e.put("ann", "clip.mp4", fakeMP4, "")
			default:
				j = e.put("ann", "pixel.png", string(pixelPNG), "")
				e.thumb(num(j, "id"))
			}
			slug := str(j, "slug")
			for _, h := range tc.hits {
				if code := e.visit(slug, h); code >= 400 {
					t.Fatalf("%s%s: %d", slug, h.path, code)
				}
			}
			got := e.events(num(j, "id"))
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("events %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEventCaptureRawOrigin(t *testing.T) {
	e := newEnv(t, testutil.Env("RAW_URL", "https://raw.test"))
	e.user("ann", 1, true)
	j := e.put("ann", "pixel.png", string(pixelPNG), "")
	slug := str(j, "slug")
	// The share page on the app origin loads the image from the raw origin.
	e.visit(slug, hit{ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "document"}})
	page := hit{path: ".png", ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "image", "Sec-Fetch-Site": "cross-site", "Referer": "SELF/"}}
	req := func(h hit) {
		r, _ := http.NewRequestWithContext(e.ctx, "GET", e.s.URL+"/"+slug+h.path, nil)
		r.Host = "raw.test"
		r.Header.Set("User-Agent", h.ua)
		for k, v := range h.h {
			r.Header.Set(k, strings.ReplaceAll(v, "SELF", e.s.URL))
		}
		res, err := e.client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("raw origin: %d", res.StatusCode)
		}
	}
	req(page)
	req(hit{path: ".png", ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "image", "Sec-Fetch-Site": "cross-site", "Referer": "https://forum.example/"}})
	if got := e.events(num(j, "id")); fmt.Sprint(got) != "[view embed@forum.example]" {
		t.Errorf("events %v", got)
	}
}

func TestPreviewedBadge(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	j := e.put("ann", "pixel.png", string(pixelPNG), "")
	slug, id := str(j, "slug"), num(j, "id")
	badge := func(where string) string {
		_, body := e.do("GET", where, nil, as("ann"))
		for _, b := range []string{"not opened", "previewed", "opened"} {
			if strings.Contains(body, `"><span class="pb-icon" aria-hidden="true"></span>`+b+`</span>`) {
				return b
			}
		}
		return "none"
	}
	if got := badge("/" + slug); got != "not opened" {
		t.Fatalf("new item: %s", got)
	}

	e.visit(slug, hit{ua: uaDiscord})
	if err := e.svc.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	it := e.item(slug)
	if it.PreviewedAt == nil || it.FirstOpened != nil || it.Views != 0 {
		t.Fatalf("after a preview: previewed %v, first opened %v, views %d", it.PreviewedAt, it.FirstOpened, it.Views)
	}
	first := *it.PreviewedAt
	for _, where := range []string{"/" + slug, "/"} {
		if got := badge(where); got != "previewed" {
			t.Errorf("%s: badge %s after a preview", where, got)
		}
	}
	if _, m := e.doJSON("GET", itemPath(id), nil, as("ann")); m["previewed_at"] == nil {
		t.Error("API item has no previewed_at")
	}

	// A later preview keeps the first time; a person opening it wins.
	time.Sleep(5 * time.Millisecond)
	e.visit(slug, hit{ua: uaSlack})
	e.visit(slug, hit{ua: uaFirefox, h: map[string]string{"Sec-Fetch-Dest": "document"}})
	if err := e.svc.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	it = e.item(slug)
	if !it.PreviewedAt.Equal(first) || it.FirstOpened == nil {
		t.Errorf("previewed %v (first %v), opened %v", it.PreviewedAt, first, it.FirstOpened)
	}
	if got := badge("/" + slug); got != "opened" {
		t.Errorf("badge %s after a view", got)
	}
}
