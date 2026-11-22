package dashboard_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

func TestDashboardPage(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	png := e.put("ann", "sunset.png", pngData)
	e.put("ann", "notes.txt", "n")
	e.put("bob", "bob-secret.png", pngData)
	e.album("ann", "Trip", id(png))

	if res, _ := e.do("GET", "/dashboard", nil); res.StatusCode != 303 {
		t.Errorf("anonymous: %d", res.StatusCode)
	}
	res, page := e.do("GET", "/dashboard", nil, as("ann"))
	if res.StatusCode != 200 || !strings.Contains(page, "<html") || !strings.Contains(page, `id="dash-form"`) || !strings.Contains(page, `id="dash-results"`) {
		t.Fatalf("full page: %d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Vary"), "HX-Request") {
		t.Errorf("Vary: %q", res.Header.Get("Vary"))
	}
	// 29 B of PNG, 1 B of text and the text's cached render.
	var used int64
	if err := e.s.App.DB.R.QueryRow(`SELECT used_bytes FROM users WHERE id = 1`).Scan(&used); err != nil || used <= 30 {
		t.Fatalf("used %d: %v", used, err)
	}
	for _, want := range []string{"sunset.png", "notes.txt", "Trip", `aria-current="page">dashboard<`, "not opened", "js/dashboard.js", web.HumanBytes(used) + " used"} {
		if !strings.Contains(page, want) {
			t.Errorf("full page without %q", want)
		}
	}
	if strings.Contains(page, "bob-secret") || strings.Contains(page, "<meter") {
		t.Error("full page content")
	}
	// Pills count per type, ignoring the type filter; empty types are left out.
	pill := regexp.MustCompile(`data-type="(\w*)">(\w+) <span class="pb-pill__count">(\d+)</span>`)
	counts := map[string]string{}
	for _, m := range pill.FindAllStringSubmatch(page, -1) {
		counts[m[2]] = m[3]
	}
	if counts["all"] != "3" || counts["image"] != "1" || counts["text"] != "1" || counts["album"] != "1" || counts["video"] != "" {
		t.Errorf("pills: %v", counts)
	}
	_, page = e.do("GET", "/dashboard?type=image", nil, as("ann"))
	if !strings.Contains(page, `aria-pressed="true" data-manual data-type="image"`) || strings.Contains(page, "notes.txt") || !strings.Contains(page, `<span class="pb-pill__count">3</span>`) {
		t.Error("type filter page")
	}

	// htmx swaps get the results fragment only; history restores get the page.
	frag := func(hdr map[string]string) (string, *resp) {
		opts := []reqOpt{as("ann")}
		for k, v := range hdr {
			opts = append(opts, header(k, v))
		}
		res, body := e.do("GET", "/dashboard?q=sunset", nil, opts...)
		return body, res
	}
	body, fres := frag(map[string]string{"HX-Request": "true", "HX-Target": "dash-results"})
	if fres.StatusCode != 200 || strings.Contains(body, "<html") || strings.Contains(body, `id="dash-form"`) || !strings.Contains(body, "data-results") ||
		!strings.Contains(body, "sunset.png") || strings.Contains(body, "notes.txt") || !strings.Contains(fres.Header.Get("Vary"), "HX-Request") {
		t.Errorf("fragment: %d %s", fres.StatusCode, body)
	}
	if body, _ := frag(map[string]string{"HX-Request": "true", "HX-Target": "dash-results", "HX-History-Restore-Request": "true"}); !strings.Contains(body, "<html") {
		t.Error("history restore must get the whole page")
	}
	if body, _ := frag(map[string]string{"HX-Request": "true"}); !strings.Contains(body, "<html") {
		t.Error("other htmx targets get the whole page")
	}

	// The fragment carries what "select all matching" sends.
	if !regexp.MustCompile(`data-total="1" data-query="q=sunset" data-bound="20\d\d-`).MatchString(body) {
		t.Error("select-all data")
	}

	// Grid view: Media cards, hover-preview markup on items with a teaser.
	if err := e.s.App.Items.SetMedia(e.ctx, id(png), items.Media{ThumbKey: "thumbkey", PreviewKey: "previewkey"}); err != nil {
		t.Fatal(err)
	}
	_, grid := e.do("GET", "/dashboard?view=grid", nil, as("ann"))
	slug := str(png, "slug")
	if !strings.Contains(grid, `class="app-dash-grid"`) || !regexp.MustCompile(`data-preview="[^"]*/`+slug+`/preview"><img src="[^"]*/`+slug+`/thumb"`).MatchString(grid) {
		t.Error("grid hover preview markup")
	}
	if !strings.Contains(grid, `name="view" value="grid" checked`) {
		t.Error("grid segment not checked")
	}
	if _, ok := assetExists(e, albums.HoverPreviewJS); ok != strings.Contains(grid, "hover-preview.js") {
		t.Errorf("hover-preview.js referenced=%v, exists=%v", !ok, ok)
	}

	// Storage against a quota.
	e.exec(`UPDATE users SET quota_bytes = 1000000 WHERE id = 1`)
	_, page = e.do("GET", "/dashboard", nil, as("ann"))
	if !regexp.MustCompile(`<meter class="pb-meterbar" min="0" max="1000000" low="750000" high="900000" optimum="0" value="\d+"`).MatchString(page) ||
		!strings.Contains(page, "of 1 MB") {
		t.Error("storage meter")
	}

	// Rows carry the edit data; the opened badge follows first_opened_at.
	e.exec(`UPDATE items SET first_opened_at = 1700000000000 WHERE id = ?`, id(png))
	_, page = e.do("GET", "/dashboard", nil, as("ann"))
	if !strings.Contains(page, ">opened<") || !strings.Contains(page, `title="first opened 2023-11-14 22:13"`) || !strings.Contains(page, `data-item="{&#34;id&#34;:`) {
		t.Error("rows")
	}
	if !strings.Contains(page, `data-type-input`) || !strings.Contains(page, `hx-push-url="true"`) {
		t.Error("htmx wiring")
	}
}

func assetExists(e *env, p string) (string, bool) {
	u, err := e.s.App.Assets.URL(p)
	return u, err == nil
}
