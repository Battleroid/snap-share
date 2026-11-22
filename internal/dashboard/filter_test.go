package dashboard_test

import (
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// The list filters through GET /api/v1/items (the dashboard runs the same queries).
func TestListFilters(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	png := e.put("ann", "sunset.png", pngData)
	mp4 := e.put("ann", "clip.mp4", mp4Data)
	e.put("ann", "song.flac", flacData)
	txt := e.put("ann", "notes.txt", "remember the milk")
	e.put("ann", "data.json", `{"k":1}`)
	e.put("ann", "bundle.zip", zipData)
	gop := e.paste("ann", "main.go", "package main\n")
	lnk := e.link("ann", "https://example.com/landing")
	al := e.album("ann", "Holiday album", id(png), id(mp4))
	e.put("bob", "sunset-bob.png", pngData)

	types := map[string][]string{
		"image": {"sunset.png"}, "video": {"clip.mp4"}, "audio": {"song.flac"}, "text": {"data.json", "notes.txt"},
		"paste": {"main.go"}, "link": {"https://example.com/landing"}, "album": {"Holiday album"}, "other": {"bundle.zip"},
	}
	for typ, want := range types {
		got := e.names("ann", "type="+typ)
		slices.Sort(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("type=%s: %v want %v", typ, got, want)
		}
	}
	if got := e.names("ann", ""); len(got) != 9 {
		t.Errorf("all: %v", got)
	}

	// Search: FTS for 3+ characters (title, filename, paste text, link
	// target), LIKE on title and filename for shorter terms. Never another user's items.
	search := map[string][]string{
		"sunset":            {"sunset.png"},
		"SUNSET":            {"sunset.png"},
		"unse":              {"sunset.png"},
		"milk":              {"notes.txt"},
		"package":           {"main.go"},
		"example.com":       {"https://example.com/landing"},
		"holiday":           {"Holiday album"},
		"go":                {"main.go"},
		"sunset png":        {"sunset.png"},
		"sunset OR clip":    {},
		"NOT sunset":        {},
		"title:sunset":      {},
		`"sunset"`:          {},
		`"`:                 {},
		"*":                 {},
		"sun*":              {},
		"%":                 {},
		"_":                 {},
		"NEAR(sunset clip)": {},
	}
	for q, want := range search {
		got := e.names("ann", "q="+url.QueryEscape(q))
		slices.Sort(got)
		if len(got) == 0 {
			got = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("q=%q: %v want %v", q, got, want)
		}
	}

	// Tags are searchable and filterable, case-insensitively.
	if res, _ := e.doJSON("PUT", fmt.Sprintf("/api/v1/items/%d/tags", id(txt)), map[string]any{"tags": []string{"groceries"}}, as("ann")); res.StatusCode != 200 {
		t.Fatalf("tag: %d", res.StatusCode)
	}
	if got := e.names("ann", "q=grocer"); !reflect.DeepEqual(got, []string{"notes.txt"}) {
		t.Errorf("tag search: %v", got)
	}
	if got := e.names("ann", "tag=Groceries"); !reflect.DeepEqual(got, []string{"notes.txt"}) {
		t.Errorf("tag filter: %v", got)
	}
	if got := e.names("bob", "tag=groceries"); len(got) != 0 {
		t.Errorf("bob tag filter: %v", got)
	}

	// Album membership.
	got := e.names("ann", fmt.Sprintf("album=%d", id(al)))
	slices.Sort(got)
	if !reflect.DeepEqual(got, []string{"clip.mp4", "sunset.png"}) {
		t.Errorf("album: %v", got)
	}
	if got := e.names("bob", fmt.Sprintf("album=%d", id(al))); len(got) != 0 {
		t.Errorf("bob album filter: %v", got)
	}

	// Visibility and password.
	e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%d", id(mp4)), map[string]any{"visibility": "private"}, as("ann"))
	e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%d", id(gop)), map[string]any{"password": "pw-123456"}, as("ann"))
	if got := e.names("ann", "visibility=private"); !reflect.DeepEqual(got, []string{"clip.mp4"}) {
		t.Errorf("private: %v", got)
	}
	if got := e.names("ann", "visibility=password"); !reflect.DeepEqual(got, []string{"main.go"}) {
		t.Errorf("password: %v", got)
	}
	if got := e.names("ann", "visibility=unlisted"); len(got) != 8 {
		t.Errorf("unlisted: %v", got)
	}

	// Expiring soon: within 7 days and not yet expired.
	now := time.Now()
	e.exec(`UPDATE items SET expires_at = ? WHERE id = ?`, db.Millis(now.Add(48*time.Hour)), id(png))
	e.exec(`UPDATE items SET expires_at = ? WHERE id = ?`, db.Millis(now.Add(30*24*time.Hour)), id(txt))
	e.exec(`UPDATE items SET expires_at = ? WHERE id = ?`, db.Millis(now.Add(-time.Hour)), id(lnk))
	if got := e.names("ann", "expiring=1"); !reflect.DeepEqual(got, []string{"sunset.png"}) {
		t.Errorf("expiring: %v", got)
	}

	// Opened.
	e.exec(`UPDATE items SET first_opened_at = ?, last_opened_at = ? WHERE id = ?`, db.Millis(now.Add(-30*24*time.Hour)), db.Millis(now.Add(-time.Hour)), id(png))
	e.exec(`UPDATE items SET first_opened_at = ?, last_opened_at = ? WHERE id = ?`, db.Millis(now.Add(-30*24*time.Hour)), db.Millis(now.Add(-20*24*time.Hour)), id(mp4))
	if got := e.names("ann", "opened=7d"); !reflect.DeepEqual(got, []string{"sunset.png"}) {
		t.Errorf("opened 7d: %v", got)
	}
	if got := e.names("ann", "opened=no"); len(got) != 7 || slices.Contains(got, "clip.mp4") || slices.Contains(got, "sunset.png") {
		t.Errorf("not opened: %v", got)
	}

	// Date range (UTC days, inclusive).
	jan := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	e.exec(`UPDATE items SET created_at = ? WHERE id = ?`, db.Millis(jan), id(png))
	e.exec(`UPDATE items SET created_at = ? WHERE id = ?`, db.Millis(jan.AddDate(0, 0, 5)), id(mp4))
	if got := e.names("ann", "from=2026-01-10&to=2026-01-10"); !reflect.DeepEqual(got, []string{"sunset.png"}) {
		t.Errorf("one day: %v", got)
	}
	if got := e.names("ann", "from=2026-01-01&to=2026-01-31"); !reflect.DeepEqual(got, []string{"clip.mp4", "sunset.png"}) {
		t.Errorf("january: %v", got)
	}
	if got := e.names("ann", "to=2026-01-12"); !reflect.DeepEqual(got, []string{"sunset.png"}) {
		t.Errorf("until: %v", got)
	}

	// Filters combine.
	if got := e.names("ann", "type=image&from=2026-01-01&q=sun&expiring=1&opened=7d"); !reflect.DeepEqual(got, []string{"sunset.png"}) {
		t.Errorf("combined: %v", got)
	}
	if got := e.names("ann", "type=video&visibility=unlisted"); len(got) != 0 {
		t.Errorf("combined empty: %v", got)
	}

	// Invalid values are refused by the API.
	for _, q := range []string{"type=exe", "sort=random", "from=yesterday", "album=x", "visibility=public", "opened=1"} {
		if res, _ := e.doJSON("GET", "/api/v1/items?"+q, nil, as("ann")); res.StatusCode != 400 {
			t.Errorf("%s: %d", q, res.StatusCode)
		}
	}
}

func TestListSortAndPages(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	type spec struct {
		name    string
		size    int
		created int // days after base
		views   int
		opened  int // days after base; 0: never
		expires int // days from now; 0: never
	}
	specs := []spec{
		{"bravo.txt", 30, 1, 5, 3, 10},
		{"alpha.txt", 10, 3, 9, 0, 2},
		{"delta.txt", 40, 2, 1, 9, 0},
		{"charlie.txt", 20, 4, 7, 5, 30},
	}
	for _, s := range specs {
		it := e.put("ann", s.name, fmt.Sprintf("%0*d", s.size, 0))
		e.exec(`UPDATE items SET created_at = ?, views = ? WHERE id = ?`, db.Millis(base.AddDate(0, 0, s.created)), s.views, id(it))
		if s.opened != 0 {
			e.exec(`UPDATE items SET first_opened_at = ?, last_opened_at = ? WHERE id = ?`, db.Millis(base), db.Millis(base.AddDate(0, 0, s.opened)), id(it))
		}
		if s.expires != 0 {
			e.exec(`UPDATE items SET expires_at = ? WHERE id = ?`, db.Millis(time.Now().AddDate(0, 0, s.expires)), id(it))
		}
	}
	sorts := map[string][]string{
		"":        {"charlie.txt", "alpha.txt", "delta.txt", "bravo.txt"},
		"new":     {"charlie.txt", "alpha.txt", "delta.txt", "bravo.txt"},
		"old":     {"bravo.txt", "delta.txt", "alpha.txt", "charlie.txt"},
		"name":    {"alpha.txt", "bravo.txt", "charlie.txt", "delta.txt"},
		"size":    {"delta.txt", "bravo.txt", "charlie.txt", "alpha.txt"},
		"views":   {"alpha.txt", "charlie.txt", "bravo.txt", "delta.txt"},
		"opened":  {"delta.txt", "charlie.txt", "bravo.txt", "alpha.txt"},
		"expires": {"alpha.txt", "bravo.txt", "charlie.txt", "delta.txt"},
	}
	for s, want := range sorts {
		if got := e.names("ann", "sort="+s); !reflect.DeepEqual(got, want) {
			t.Errorf("sort=%s: %v want %v", s, got, want)
		}
	}
	// Offset pagination with the total.
	res, m := e.doJSON("GET", "/api/v1/items?sort=name&limit=3&offset=0", nil, as("ann"))
	if res.StatusCode != 200 || num(m, "total") != 4 || len(m["items"].([]any)) != 3 {
		t.Errorf("page 1: %v", m)
	}
	_, m = e.doJSON("GET", "/api/v1/items?sort=name&limit=3&offset=3", nil, as("ann"))
	list := m["items"].([]any)
	if num(m, "total") != 4 || len(list) != 1 || list[0].(map[string]any)["name"] != "delta.txt" {
		t.Errorf("page 2: %v", m)
	}

	// The dashboard pages by 50.
	for i := range 52 {
		e.put("ann", fmt.Sprintf("bulk-%02d.txt", i), "x")
	}
	_, page := e.do("GET", "/dashboard?q=bulk&page=2", nil, as("ann"))
	if !containsAll(page, "51–52 of 52", "bulk-01.txt", "bulk-00.txt") || containsAny(page, "bulk-02.txt") {
		t.Errorf("dashboard page 2")
	}
	_, page = e.do("GET", "/dashboard?q=bulk", nil, as("ann"))
	if !containsAll(page, "1–50 of 52", "bulk-51.txt", "bulk-02.txt", `aria-current="page">1 / 2<`) || containsAny(page, "bulk-01.txt") {
		t.Errorf("dashboard page 1")
	}
}

func TestPreviewedFilter(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	seen := e.put("ann", "previewed.txt", "a")
	both := e.put("ann", "both.txt", "b")
	e.put("ann", "none.txt", "c")
	e.exec(`UPDATE items SET previewed_at = 1 WHERE id IN (?, ?)`, id(seen), id(both))
	e.exec(`UPDATE items SET first_opened_at = 2, last_opened_at = 2 WHERE id = ?`, id(both))
	if got := e.names("ann", "opened=previewed"); !reflect.DeepEqual(got, []string{"previewed.txt"}) {
		t.Errorf("previewed: %v", got)
	}
	_, page := e.do("GET", "/dashboard", nil, as("ann"))
	if !strings.Contains(page, ">previewed<") || !strings.Contains(page, `value="previewed"`) {
		t.Error("previewed badge or filter option missing")
	}
}
