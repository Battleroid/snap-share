package statsui_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
)

const (
	annSecret = "QUJDREVGR0hJSktMTU5PUA"
	bobSecret = "Ym9ic2VjcmV0Ym9ic2Vjcg"
	otherUA   = "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"
)

// seedUser adds what the Your stats page reads on top of newEnv's fixture:
// bob's own daily rollups, the accounts' storage, and secrets with events
// for both. Ann's item 10 has 9 views in 30 days, bob's item 12 has 50.
func (e *env) seedUser() {
	e.t.Helper()
	e.exec(`INSERT INTO item_stats_daily (item_id, day, views, downloads) VALUES (12, ?, 50, 7)`, e.today.UnixMilli())
	e.exec(`UPDATE users SET used_bytes = 1500, quota_bytes = 10000 WHERE id = 1`)
	e.exec(`UPDATE users SET used_bytes = 99000 WHERE id = 2`)
	now := time.Now()
	for _, s := range []struct {
		id    string
		owner int
		note  string
	}{{annSecret, 1, "ann's secret"}, {bobSecret, 2, "bob's secret"}} {
		e.exec(`INSERT INTO secrets (id, owner_id, kind, mode, state, ct_size, chunk_count, views_left, max_views, max_failures, expires_at, note, created_at, updated_at)
VALUES (?, ?, 'text', 'e2e', 'ready', 64, 1, 1, 2, 3, ?, ?, ?, ?)`, s.id, s.owner, now.Add(time.Hour).UnixMilli(), s.note, now.UnixMilli(), now.UnixMilli())
	}
	sev := func(secret string, kind string, n int) {
		for range n {
			e.exec(`INSERT INTO secret_events (secret_id, at, kind, ip, ua_summary) VALUES (?, ?, ?, '203.0.113.5', 'Safari · iOS')`,
				secret, now.Add(-time.Minute).UnixMilli(), kind)
		}
	}
	sev(annSecret, "link_opened", 3)
	sev(annSecret, "revealed", 2)
	sev(annSecret, "failed", 1)
	sev(bobSecret, "link_opened", 11)
	sev(bobSecret, "revealed", 8)
}

// tileHas is the markup of one StatTile.
func tileHas(label, value string) string {
	return `<span class="pb-tile__label">` + label + `</span><span class="pb-tile__value">` + value + `</span>`
}

func TestUserStatsPage(t *testing.T) {
	e := newEnv(t)
	e.seedUser()
	// A fourth account with nothing at all.
	e.exec(`INSERT INTO users (id, username, email, email_verified_at, role, created_at, updated_at) VALUES (4, 'cy', 'cy@example.test', 1, 'user', 1, 1)`)
	e.s.Auth.Add("cy", auth.Principal{UserID: 4, EmailVerified: true})

	// bobData is everything of bob's (and of the deleted or unrelated rows) that must never show for ann.
	bobData := []string{"b.txt", "bob.example", "BobsBrowser", "bob's secret", bobIP, tileHas("views · 30d", "59"), tileHas("downloads · 30d", "8")}
	annData := []string{"photo.jpg", "news.example", "Firefox · macOS", annIP}

	for _, tc := range []struct {
		name    string
		who     string
		path    string
		want    []string
		notWant []string
	}{
		{"the owner sees their own items only", "ann", "/dashboard/stats",
			append([]string{
				tileHas("views · 30d", "9"), tileHas("downloads · 30d", "1"), tileHas("previews · 30d", "1"),
				tileHas("plays · 30d", "0"), tileHas("clicks · 30d", "0"), tileHas("embeds · 30d", "0"),
				`class="pb-line pb-s1"`, "Your own visits are not counted.",
				// top items link to the item's stats page
				`href="/dashboard/items/10/stats"`, "Top items", "Top referrers", "Clients", "news.example",
				// secrets: ann's three numbers, not bob's
				"Secrets · 30d", `<span class="pb-metric__value app-metric__value">3</span>`, `<span class="pb-metric__value app-metric__value">2</span>`,
				"2 of 3 opened links were revealed", `<a class="pb-link" href="/dashboard/secrets">All secrets</a>`,
				// storage under a quota, split by kind, and the items by type
				"<b>1.5 KB</b> of 10 KB · 15%", "images 10 B", "other 1.5 KB", `aria-label="1 image"`, `aria-label="1 link"`,
				"raw events",
			}, annData[:3]...),
			append([]string{`aria-label="1 text"`}, bobData...)},
		{"a range of seven days", "ann", "/dashboard/stats?days=7",
			[]string{tileHas("views · 7d", "5"), `<input type="radio" name="days" value="7" checked>`}, bobData},
		{"ninety days", "ann", "/dashboard/stats?days=90",
			[]string{tileHas("views · 90d", "19"), `<input type="radio" name="days" value="90" checked>`}, nil},
		{"an unknown range falls back to thirty days", "ann", "/dashboard/stats?days=14",
			[]string{tileHas("views · 30d", "9"), `<input type="radio" name="days" value="30" checked>`}, nil},
		{"junk falls back too", "ann", "/dashboard/stats?days=x",
			[]string{tileHas("views · 30d", "9")}, nil},
		{"bob sees his own", "bob", "/dashboard/stats",
			[]string{tileHas("views · 30d", "50"), tileHas("downloads · 30d", "7"), "b.txt", "bob.example", "BobsBrowser", `<span class="pb-metric__value app-metric__value">11</span>`, "8 of 11 opened links were revealed", `aria-label="1 text"`},
			append([]string{"ann's secret"}, annData...)},
		{"an empty account shows the empty state", "cy", "/dashboard/stats",
			[]string{"No stats yet", `class="pb-empty"`, `href="/"`}, []string{"pb-tile", "pb-chart", "Top items", "Secrets · "}},
		{"an admin sees their own numbers, not everyone's", "root", "/dashboard/stats",
			[]string{"No stats yet"}, append(append([]string{}, annData...), bobData...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, body := e.do("GET", tc.path, nil, as(tc.who))
			if res.StatusCode != http.StatusOK {
				t.Fatalf("%d", res.StatusCode)
			}
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("page lacks %q", w)
				}
			}
			for _, n := range tc.notWant {
				if strings.Contains(body, n) {
					t.Errorf("page has %q", n)
				}
			}
			if strings.Contains(body, "pb-fieldset") {
				t.Error("figures are not fieldsets")
			}
		})
	}

	// The nav entry for the page is Stats.
	if _, body := e.do("GET", "/dashboard/stats", nil, as("ann")); !strings.Contains(body, `href="/dashboard/stats" aria-current="page"`) {
		t.Error("Stats is not the current nav entry")
	}
	// A visitor without a session signs in first; a key is refused (browser sessions only).
	if res, _ := e.do("GET", "/dashboard/stats", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("anonymous: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/dashboard/stats", nil, key("ann-read")); res.StatusCode != http.StatusForbidden {
		t.Errorf("with a key: %d", res.StatusCode)
	}
}

// Without a quota the storage bar has no "of"; with none of anything it has no segments.
func TestUserStatsStorageWithoutQuota(t *testing.T) {
	e := newEnv(t)
	e.seedUser()
	e.exec(`UPDATE users SET quota_bytes = NULL WHERE id = 1`)
	_, body := e.do("GET", "/dashboard/stats", nil, as("ann"))
	if !strings.Contains(body, "<b>1.5 KB</b> used") || strings.Contains(body, " of 10 KB") || strings.Contains(body, "<li data-free>") {
		t.Error("storage without a quota")
	}
}

// The owner's own requests record nothing, so they don't show up here.
func TestUserStatsOwnRequestsAreNotCounted(t *testing.T) {
	e := newEnv(t)
	visit := func(who, ua string) {
		t.Helper()
		req, err := http.NewRequestWithContext(e.ctx, "GET", e.s.URL+"/lnk", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "none")
		if who != "" {
			authtest.Session(req, who)
		}
		res, err := e.s.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode >= 400 {
			t.Fatalf("/lnk as %q: %d", who, res.StatusCode)
		}
	}
	clicks := func() string {
		t.Helper()
		if err := e.s.App.Stats.Flush(e.ctx); err != nil {
			t.Fatal(err)
		}
		_, body := e.do("GET", "/dashboard/stats", nil, as("ann"))
		for _, v := range []string{"0", "1", "2", "3"} {
			if strings.Contains(body, tileHas("clicks · 30d", v)) {
				return v
			}
		}
		t.Fatal("no clicks tile")
		return ""
	}
	if got := clicks(); got != "0" {
		t.Fatalf("before any visit: %s", got)
	}
	// Ann follows her own link, twice, with two browsers: nothing.
	visit("ann", browser)
	visit("ann", otherUA)
	if got := clicks(); got != "0" {
		t.Fatalf("owner's clicks counted: %s", got)
	}
	// Two other visitors, one signed in as someone else: two clicks.
	visit("", browser)
	visit("bob", otherUA)
	if got := clicks(); got != "2" {
		t.Fatalf("visitors' clicks: %s", got)
	}
	// And none of it shows on bob's page: it is ann's item.
	if _, body := e.do("GET", "/dashboard/stats", nil, as("bob")); strings.Contains(body, tileHas("clicks · 30d", "2")) {
		t.Fatal("ann's clicks on bob's page")
	}
}

type userStatsJSON struct {
	Days   int `json:"days"`
	Totals struct {
		Views, Downloads, Previews, Plays, Clicks, Embeds int64
	} `json:"totals"`
	Daily []struct {
		Day   string
		Views int64
	} `json:"daily"`
	TopItems []struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Views int64  `json:"views"`
	} `json:"top_items"`
	Referrers []struct {
		Key   string `json:"key"`
		Count int64  `json:"count"`
	} `json:"referrers"`
	Clients []struct{ Key string } `json:"clients"`
	Secrets *struct {
		Opened, Revealed, Failed int64
	} `json:"secrets"`
	Storage struct {
		Used, Quota, Images, Video, Audio, Other int64
	} `json:"storage"`
	ItemsByType []struct {
		Key   string `json:"key"`
		Count int64  `json:"count"`
	} `json:"items_by_type"`
}

func TestUserStatsAPI(t *testing.T) {
	e := newEnv(t)
	e.seedUser()
	e.s.Auth.Add("ann-both", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 103, Scopes: []auth.Scope{auth.ScopeItemsRead, auth.ScopeSecretsRead}})

	get := func(path string, opt func(*http.Request)) (int, userStatsJSON, string) {
		t.Helper()
		var opts []func(*http.Request)
		if opt != nil {
			opts = append(opts, opt)
		}
		res, body := e.do("GET", path, nil, opts...)
		var st userStatsJSON
		if res.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &st); err != nil {
				t.Fatalf("%s: %v", body, err)
			}
		}
		return res.StatusCode, st, body
	}

	code, st, body := get("/api/v1/stats", key("ann-read"))
	if code != 200 || st.Days != 30 || st.Totals.Views != 9 || st.Totals.Downloads != 1 || st.Totals.Previews != 1 || len(st.Daily) != 30 {
		t.Fatalf("ann: %d %s", code, body)
	}
	if len(st.TopItems) != 1 || st.TopItems[0].ID != 10 || st.TopItems[0].Name != "photo.jpg" || st.TopItems[0].Views != 9 {
		t.Errorf("top items %+v", st.TopItems)
	}
	if len(st.Referrers) != 2 || st.Referrers[0].Count != 1 || st.Referrers[1].Key != "news.example" || len(st.Clients) == 0 || st.Clients[0].Key != "Firefox · macOS" {
		t.Errorf("referrers %+v clients %+v", st.Referrers, st.Clients)
	}
	if st.Storage.Used != 1500 || st.Storage.Quota != 10000 || st.Storage.Images != 10 || st.Storage.Other != 1490 {
		t.Errorf("storage %+v", st.Storage)
	}
	if len(st.ItemsByType) != 2 || st.ItemsByType[0].Count != 1 {
		t.Errorf("items by type %+v", st.ItemsByType)
	}
	for _, leak := range []string{"b.txt", "bob.example", "BobsBrowser", "bob's secret"} {
		if strings.Contains(body, leak) {
			t.Errorf("ann's stats show %q", leak)
		}
	}
	// Secret counts are about secrets: a key needs secrets:read as well. A session has both.
	if st.Secrets != nil || strings.Contains(body, `"secrets":{`) {
		t.Errorf("an items:read key sees secret counts: %s", body)
	}
	for _, opt := range []func(*http.Request){key("ann-both"), as("ann")} {
		if code, st, body = get("/api/v1/stats", opt); code != 200 || st.Secrets == nil || st.Secrets.Opened != 3 || st.Secrets.Revealed != 2 || st.Secrets.Failed != 1 {
			t.Errorf("with secrets:read: %d %s", code, body)
		}
	}
	// The range follows the page's: 7, 30 or 90 days.
	if code, st, _ = get("/api/v1/stats?days=7", key("ann-read")); code != 200 || st.Days != 7 || st.Totals.Views != 5 || len(st.Daily) != 7 {
		t.Errorf("7 days: %d %+v", code, st.Totals)
	}
	if _, st, _ = get("/api/v1/stats?days=90", key("ann-read")); st.Days != 90 || st.Totals.Views != 19 {
		t.Errorf("90 days: %+v", st.Totals)
	}
	if _, st, _ = get("/api/v1/stats?days=5", key("ann-read")); st.Days != 30 {
		t.Errorf("unknown range: %d", st.Days)
	}
	// Bob's key answers for bob; an admin's, for the admin.
	if _, st, body = get("/api/v1/stats", key("bob-read")); st.Totals.Views != 50 || len(st.TopItems) != 1 || st.TopItems[0].ID != 12 || strings.Contains(body, "photo.jpg") {
		t.Errorf("bob: %s", body)
	}
	if _, st, body = get("/api/v1/stats", key("root-read")); st.Totals.Views != 0 || len(st.TopItems) != 0 || strings.Contains(body, "photo.jpg") {
		t.Errorf("root: %s", body)
	}
	// Scopes and sign-in.
	if code, _, body = get("/api/v1/stats", key("ann-write")); code != http.StatusForbidden || !strings.Contains(body, "items:read") || strings.Contains(body, "photo.jpg") {
		t.Errorf("without items:read: %d %s", code, body)
	}
	if code, _, _ = get("/api/v1/stats", nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", code)
	}
}
