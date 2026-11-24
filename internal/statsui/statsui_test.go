package statsui_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/testutil"
)

const (
	annIP    = "203.0.113.77"
	bobIP    = "198.51.100.9"
	evilUA   = `=HYPERLINK("https://evil.example","x")`
	browser  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 15.1; rv:140.0) Gecko/20100101 Firefox/140.0"
	secretID = "QUJDREVGR0hJSktMTU5PUA"
)

type env struct {
	t     *testing.T
	s     *testutil.Server
	ctx   context.Context
	today time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := testutil.NewServer(t, testutil.WithSMTP())
	now := time.Now().UTC()
	e := &env{t: t, s: s, ctx: context.Background(), today: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)}
	e.exec(`INSERT INTO users (id, username, email, email_verified_at, role, created_at, updated_at) VALUES
  (1, 'ann', 'ann@example.test', 1, 'user', 1, 1), (2, 'bob', 'bob@example.test', 1, 'user', 1, 1), (3, 'root', 'root@example.test', 1, 'admin', 1, 1)`)
	s.Auth.Add("ann", auth.Principal{UserID: 1, EmailVerified: true})
	s.Auth.Add("bob", auth.Principal{UserID: 2, EmailVerified: true})
	s.Auth.Add("root", auth.Principal{UserID: 3, EmailVerified: true, Role: auth.RoleAdmin})
	s.Auth.Add("ann-read", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 101, Scopes: []auth.Scope{auth.ScopeItemsRead}})
	s.Auth.Add("ann-write", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 102, Scopes: []auth.Scope{auth.ScopeItemsWrite, auth.ScopeItemsDelete}})
	s.Auth.Add("bob-read", auth.Principal{UserID: 2, EmailVerified: true, KeyID: 201, Scopes: []auth.Scope{auth.ScopeItemsRead}})
	s.Auth.Add("root-read", auth.Principal{UserID: 3, EmailVerified: true, Role: auth.RoleAdmin, KeyID: 301, Scopes: auth.AllScopes})

	first := e.day(-20).Add(9 * time.Hour).UnixMilli()
	e.exec(`INSERT INTO items (id, owner_id, kind, slug, title, filename, mime, size, first_opened_at, last_opened_at, opened_count, created_at, updated_at)
VALUES (10, 1, 'file', 'pic', '', 'photo.jpg', 'image/jpeg', 10, ?, ?, 9, 1, 1)`, first, e.today.UnixMilli())
	e.exec(`INSERT INTO items (id, owner_id, kind, slug, target_url, created_at, updated_at) VALUES (11, 1, 'link', 'lnk', 'https://example.org/', 1, 1)`)
	e.exec(`INSERT INTO items (id, owner_id, kind, slug, filename, mime, created_at, updated_at) VALUES (12, 2, 'file', 'bobs', 'b.txt', 'text/plain', 1, 1)`)

	// Daily totals for ann's photo: today, 5, 20 and 60 days ago.
	for _, d := range []struct{ off, views, uniques, newv, previews, downloads int }{
		{0, 3, 2, 2, 1, 1}, {-5, 2, 1, 1, 0, 0}, {-20, 4, 1, 1, 0, 0}, {-60, 10, 3, 3, 0, 0},
	} {
		e.exec(`INSERT INTO item_stats_daily (item_id, day, views, uniques, new_visitors, previews, downloads) VALUES (10, ?, ?, ?, ?, ?, ?)`,
			e.day(d.off).UnixMilli(), d.views, d.uniques, d.newv, d.previews, d.downloads)
	}
	// Raw events.
	ev := func(item int64, off int, kind, ip, ua, sum, bot, ref, key string) {
		at := e.day(off).Add(time.Hour)
		if at.After(now) {
			at = now.Add(-time.Second) // today's events are in the past
		}
		e.exec(`INSERT INTO item_events (item_id, at, kind, ip, user_agent, ua_summary, bot_name, referrer_host, visitor_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item, at.UnixMilli(), kind, ip, ua, sum, bot, ref, key)
	}
	ev(10, -20, "view", annIP, browser, "Firefox · macOS", "", "news.example", "v1")
	ev(10, -5, "view", annIP, browser, "Firefox · macOS", "", "", "v1")
	ev(10, 0, "preview", "192.0.2.1", "Discordbot/2.0", "Discord bot", "Discord", "", "")
	ev(10, 0, "view", annIP, evilUA, "=cmd", "", "-2+3.example", "v2")
	ev(10, 0, "download", "2001:db8::1", "@SUM(1)", "+SUM", "", "", "v3")
	ev(12, 0, "view", bobIP, "BobsBrowser/1.0", "BobsBrowser", "", "bob.example", "b1")
	return e
}

func (e *env) day(off int) time.Time { return e.today.AddDate(0, 0, off) }

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.s.App.DB.W.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

// result is a finished response; testutil.Server.Do has read and closed the body.
type result struct {
	StatusCode int
	Header     http.Header
}

func (e *env) do(method, path string, body io.Reader, opts ...func(*http.Request)) (result, string) {
	e.t.Helper()
	req := e.s.Request(e.t, method, path, body)
	for _, o := range opts {
		o(req)
	}
	res, b := e.s.Do(e.t, req) //nolint:bodyclose // Do reads and closes the body
	return result{StatusCode: res.StatusCode, Header: res.Header}, b
}

func as(name string) func(*http.Request) { return func(r *http.Request) { authtest.Session(r, name) } }
func key(name string) func(*http.Request) {
	return func(r *http.Request) {
		authtest.Bearer(r, name)
		r.Header.Del("Sec-Fetch-Site")
		r.Header.Del("Origin")
	}
}
func form() func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }
}

type statsJSON struct {
	Item struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"item"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Opened struct {
		FirstOpenedAt *time.Time `json:"first_opened_at"`
		OpenedCount   int64      `json:"opened_count"`
	} `json:"opened"`
	Totals struct {
		Views, Uniques, Previews, Downloads int64
	} `json:"totals"`
	Daily []struct {
		Day   string `json:"day"`
		Views int64  `json:"views"`
	} `json:"daily"`
	Referrers []struct {
		Key   string `json:"key"`
		Count int64  `json:"count"`
	} `json:"referrers"`
	Bots []struct {
		Key string `json:"key"`
	} `json:"bots"`
}

func (e *env) stats(q string) (int, statsJSON, string) {
	e.t.Helper()
	res, body := e.do("GET", "/api/v1/items/10/stats"+q, nil, key("ann-read"))
	var st statsJSON
	if res.StatusCode == 200 {
		if err := json.Unmarshal([]byte(body), &st); err != nil {
			e.t.Fatalf("%s: %v", body, err)
		}
	}
	return res.StatusCode, st, body
}

func TestStatsByRange(t *testing.T) {
	e := newEnv(t)
	date := func(off int) string { return e.day(off).Format(time.DateOnly) }

	code, st, body := e.stats("?from=" + date(-6) + "&to=" + date(0))
	if code != 200 || st.Totals.Views != 5 || st.Totals.Previews != 1 || st.Totals.Downloads != 1 || len(st.Daily) != 7 {
		t.Fatalf("7 days: %d %s", code, body)
	}
	if st.Daily[1].Day != date(-5) || st.Daily[1].Views != 2 || st.Daily[6].Views != 3 || st.Daily[0].Views != 0 {
		t.Fatalf("daily %+v", st.Daily)
	}
	// Uniques in a bounded range are distinct visitors among the raw opens.
	if st.Totals.Uniques != 3 || st.Item.Name != "photo.jpg" || st.Opened.OpenedCount != 9 || st.Opened.FirstOpenedAt == nil {
		t.Fatalf("uniques %d item %+v opened %+v", st.Totals.Uniques, st.Item, st.Opened)
	}
	if len(st.Bots) != 1 || st.Bots[0].Key != "Discord" {
		t.Fatalf("bots %+v", st.Bots)
	}
	// The default is the last 30 days.
	if code, st, body = e.stats(""); code != 200 || st.Totals.Views != 9 || len(st.Daily) != 30 || len(st.Referrers) != 2 {
		t.Fatalf("default: %d %s", code, body)
	}
	if code, st, _ = e.stats("?from=" + date(-89)); code != 200 || st.Totals.Views != 19 {
		t.Fatalf("90 days: %d %+v", code, st.Totals)
	}
	// RFC 3339 bounds are exact instants.
	q := "?from=" + url.QueryEscape(e.day(-20).Format(time.RFC3339)) + "&to=" + url.QueryEscape(e.day(-19).Format(time.RFC3339))
	if code, st, body = e.stats(q); code != 200 || st.Totals.Views != 4 || len(st.Daily) != 1 || len(st.Referrers) != 1 || st.Referrers[0].Key != "news.example" {
		t.Fatalf("one day: %d %s", code, body)
	}
	for _, bad := range []string{"?from=" + date(0) + "&to=" + date(-3), "?from=yesterday", "?to=2026-13-01", "?from=" + e.day(-1500).Format(time.DateOnly)} {
		if code, _, body := e.stats(bad); code != http.StatusBadRequest || !strings.Contains(body, `"error"`) {
			t.Errorf("%s: %d %s", bad, code, body)
		}
	}

	// The page shows the chosen window.
	for _, tc := range []struct {
		days  string
		views string
	}{{"7", "5"}, {"30", "9"}, {"90", "19"}} {
		res, page := e.do("GET", "/dashboard/items/10?days="+tc.days, nil, as("ann"))
		want := `<span class="pb-tile__label">views</span><span class="pb-tile__value">` + tc.views + `</span>`
		if res.StatusCode != 200 || !strings.Contains(page, want) {
			t.Fatalf("days=%s: %d, want %s", tc.days, res.StatusCode, want)
		}
		if !strings.Contains(page, `<input type="radio" name="days" value="`+tc.days+`" checked>`) {
			t.Fatalf("days=%s not selected", tc.days)
		}
	}
	_, page := e.do("GET", "/dashboard/items/10", nil, as("ann"))
	for _, want := range []string{
		`<figure class="pb-ts pb-chart--panel app-ts" data-ts aria-label="views per day, last 30 days, 9 in total">`, `<span class="pb-eyebrow">views · 30d · per day</span>`, `first opened <time class="pb-reltime"`,
		"Your own visits are not counted.", `<span class="pb-tile__label">downloads</span>`, `<span class="pb-tile__label">embeds</span>`, `<span class="pb-tile__label">visitors</span>`, "Firefox · macOS", "news.example",
		"Discord", annIP, "Export CSV",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, `<span class="pb-tile__label">clicks</span>`) || strings.Contains(page, `<span class="pb-tile__label">plays</span>`) {
		t.Error("tiles that don't apply to an image")
	}
	// The chart is the kit's time-series: one JSON block, noon-UTC points for every day, views and visitors.
	ts := chartJSON(t, page)
	if ts.XFormat != "date" || ts.Unit != "/day" || len(ts.Series) != 2 || ts.Series[0].Name != "Views" || ts.Series[1].Name != "Visitors" {
		t.Fatalf("chart %+v", ts)
	}
	for i, s := range ts.Series {
		if len(s.Data) != 30 {
			t.Fatalf("series %d has %d days, want 30", i, len(s.Data))
		}
		for _, p := range s.Data {
			if p[0]%86400000 != 12*3600000 {
				t.Fatalf("point %d is not at noon UTC", p[0])
			}
		}
	}
	// Views today 3 (2 visitors), 5 days ago 2 (1), 20 days ago 4 (1).
	last := func(s tsSeries, back int) [2]int64 { return s.Data[len(s.Data)-1-back] }
	if v, u := ts.Series[0], ts.Series[1]; last(v, 0)[1] != 3 || last(u, 0)[1] != 2 || last(v, 5)[1] != 2 || last(u, 5)[1] != 1 || last(v, 20)[1] != 4 || last(v, 1)[1] != 0 || last(v, 0)[0] != e.today.Add(12*time.Hour).UnixMilli() {
		t.Fatalf("series %+v", ts.Series)
	}
	// A link shows clicks.
	if _, page := e.do("GET", "/dashboard/items/11", nil, as("ann")); !strings.Contains(page, `<span class="pb-tile__label">clicks</span>`) || !strings.Contains(page, "first opened —") {
		t.Error("link page")
	}
}

// visitorData is what must never reach anyone but the owner.
var visitorData = []string{annIP, "2001:db8::1", browser, "HYPERLINK", "news.example", "Firefox · macOS", "Discord"}

func TestVisitorDataOnlyForTheOwner(t *testing.T) {
	e := newEnv(t)
	// Other users get a 404 for every route, as if the item did not exist.
	for _, tc := range []struct {
		path string
		opt  func(*http.Request)
	}{
		{"/dashboard/items/10", as("bob")},
		{"/dashboard/items/10/events.csv", as("bob")},
		{"/api/v1/items/10/stats", key("bob-read")},
		{"/api/v1/items/10/events", key("bob-read")},
		{"/dashboard/items/10/events.csv", as("root")},
		{"/api/v1/items/10/stats", key("root-read")},
		{"/api/v1/items/10/events", key("root-read")},
		{"/dashboard/items/999", as("ann")},
		{"/dashboard/items/x", as("ann")},
	} {
		res, body := e.do("GET", tc.path, nil, tc.opt)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", tc.path, res.StatusCode)
		}
		for _, v := range visitorData {
			if strings.Contains(body, v) {
				t.Errorf("%s leaks %q", tc.path, v)
			}
		}
	}
	// Admins see totals for other users' items, and nothing about visitors.
	res, body := e.do("GET", "/dashboard/items/10", nil, as("root"))
	if res.StatusCode != 200 || !strings.Contains(body, "Totals only") || !strings.Contains(body, `views</span><span class="pb-tile__value">9<`) {
		t.Fatalf("admin page: %d", res.StatusCode)
	}
	for _, v := range visitorData {
		if strings.Contains(body, v) {
			t.Errorf("admin page leaks %q", v)
		}
	}
	if strings.Contains(body, "Export CSV") || strings.Contains(body, "Latest events") {
		t.Error("admin page offers events")
	}
	// The owner sees bob's visitors on none of their pages.
	for _, p := range []string{"/dashboard/items/10", "/dashboard/items/10/events.csv"} {
		if _, body := e.do("GET", p, nil, as("ann")); strings.Contains(body, bobIP) || strings.Contains(body, "BobsBrowser") {
			t.Errorf("%s shows another item's visitors", p)
		}
	}
	// Signed out: sign in first; public pages carry no visitor data.
	if res, _ := e.do("GET", "/dashboard/items/10", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous: %d", res.StatusCode)
	}
	if res, _ := e.do("GET", "/api/v1/items/10/stats", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous API: %d", res.StatusCode)
	}
	for _, p := range []string{"/pic", "/pic/raw", "/nothing-here", "/"} {
		_, body := e.do("GET", p, nil, func(r *http.Request) { r.Header.Set("User-Agent", browser) })
		for _, v := range []string{annIP, "2001:db8::1", "HYPERLINK", "news.example"} {
			if strings.Contains(body, v) {
				t.Errorf("public %s leaks %q", p, v)
			}
		}
	}
}

func TestEventsCSV(t *testing.T) {
	e := newEnv(t)
	// Enough events for several batches.
	tx, err := e.s.App.DB.W.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2100 {
		if _, err := tx.Exec(`INSERT INTO item_events (item_id, at, kind, ip, user_agent, ua_summary) VALUES (10, ?, 'view', ?, 'bulk', 'bulk')`,
			e.day(-1).UnixMilli()+int64(i), fmt.Sprintf("192.0.2.%d", i%250)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	res, body := e.do("GET", "/dashboard/items/10/events.csv", nil, as("ann"))
	h := res.Header
	if res.StatusCode != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/csv") || h.Get("Content-Disposition") != `attachment; filename="pic-events.csv"` ||
		h.Get("Cache-Control") != "no-store" {
		t.Fatalf("csv: %d %v", res.StatusCode, h)
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1+2100+5 || strings.Join(rows[0], ",") != "time,kind,ip,user_agent,client,bot,referrer" {
		t.Fatalf("%d rows, header %v", len(rows), rows[0])
	}
	// Find the fixture events by content.
	var evil, sum []string
	for _, r := range rows[1:] {
		switch {
		case strings.Contains(r[3], "HYPERLINK"):
			evil = r
		case strings.Contains(r[3], "SUM"):
			sum = r
		}
	}
	if evil == nil || evil[3] != `'`+evilUA || evil[4] != "'=cmd" || evil[6] != "'-2+3.example" || evil[2] != annIP {
		t.Fatalf("formula cells not defused: %q", evil)
	}
	if sum == nil || sum[3] != "'@SUM(1)" || sum[4] != "'+SUM" || sum[2] != "2001:db8::1" {
		t.Fatalf("formula cells not defused: %q", sum)
	}
	if _, err := time.Parse(time.RFC3339, rows[1][0]); err != nil {
		t.Fatalf("time %q", rows[1][0])
	}
}

func TestEventsAPI(t *testing.T) {
	e := newEnv(t)
	var page struct {
		Events []struct {
			ID        int64  `json:"id"`
			Kind      string `json:"kind"`
			IP        string `json:"ip"`
			UserAgent string `json:"user_agent"`
			Referrer  string `json:"referrer"`
		} `json:"events"`
		NextBefore *int64 `json:"next_before"`
	}
	get := func(q string) {
		t.Helper()
		res, body := e.do("GET", "/api/v1/items/10/events"+q, nil, key("ann-read"))
		page.Events, page.NextBefore = nil, nil
		if res.StatusCode != 200 || json.Unmarshal([]byte(body), &page) != nil {
			t.Fatalf("%s: %d %s", q, res.StatusCode, body)
		}
	}
	get("?limit=2")
	if len(page.Events) != 2 || page.NextBefore == nil || page.Events[0].Kind != "download" || page.Events[0].UserAgent != "@SUM(1)" ||
		page.Events[1].IP != annIP || page.Events[0].ID <= page.Events[1].ID {
		t.Fatalf("page 1 %+v", page)
	}
	get(fmt.Sprintf("?limit=2&before=%d", *page.NextBefore))
	if len(page.Events) != 2 || page.Events[1].Referrer != "" || page.Events[0].Kind != "preview" {
		t.Fatalf("page 2 %+v", page)
	}
	get(fmt.Sprintf("?limit=2&before=%d", *page.NextBefore))
	if len(page.Events) != 1 || page.NextBefore != nil || page.Events[0].Referrer != "news.example" {
		t.Fatalf("page 3 %+v", page)
	}
	for _, bad := range []string{"?limit=0", "?limit=1001", "?limit=x", "?before=-1", "?before=x"} {
		if res, _ := e.do("GET", "/api/v1/items/10/events"+bad, nil, key("ann-read")); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, res.StatusCode)
		}
	}
}

func TestAPIScopes(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/v1/items/10/stats", "/api/v1/items/10/events"} {
		res, body := e.do("GET", p, nil, key("ann-write"))
		if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "items:read") || strings.Contains(body, annIP) {
			t.Errorf("%s without items:read: %d %s", p, res.StatusCode, body)
		}
		if res, _ := e.do("GET", p, nil, key("ann-read")); res.StatusCode != 200 {
			t.Errorf("%s with items:read: %d", p, res.StatusCode)
		}
		// Browser sessions use the API too (same-origin).
		if res, _ := e.do("GET", p, nil, as("ann")); res.StatusCode != 200 {
			t.Errorf("%s with a session: %d", p, res.StatusCode)
		}
	}
	// The pages are for browser sessions only.
	if res, _ := e.do("GET", "/dashboard/items/10", nil, key("ann-read")); res.StatusCode != http.StatusForbidden {
		t.Errorf("page with a key: %d", res.StatusCode)
	}
}

func TestItemNotifyOverride(t *testing.T) {
	e := newEnv(t)
	post := func(who, ev string, opts ...func(*http.Request)) (result, string) {
		return e.do("POST", "/dashboard/items/10/notify", strings.NewReader(url.Values{"event": {ev}}.Encode()),
			append([]func(*http.Request){as(who), form()}, opts...)...)
	}
	// No channel on: no override control.
	if _, page := e.do("GET", "/dashboard/items/10", nil, as("ann")); strings.Contains(page, "notify me on") {
		t.Fatal("override shown with every channel off")
	}
	if err := e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true); err != nil {
		t.Fatal(err)
	}
	if _, page := e.do("GET", "/dashboard/items/10", nil, as("ann")); !strings.Contains(page, `name="event" value="default" checked`) {
		t.Fatal("override not shown")
	}
	res, body := post("ann", "every", func(r *http.Request) { r.Header.Set("Accept", "application/json") })
	if res.StatusCode != 200 || !strings.Contains(body, `"event":"every"`) {
		t.Fatalf("set: %d %s", res.StatusCode, body)
	}
	if m, _ := e.s.App.Notify.Override(e.ctx, 1, 10); m != notify.ModeEvery {
		t.Fatalf("override %q", m)
	}
	if _, page := e.do("GET", "/dashboard/items/10", nil, as("ann")); !strings.Contains(page, `name="event" value="every" checked`) {
		t.Fatal("override not checked")
	}
	if res, _ := post("ann", "default"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("reset: %d", res.StatusCode)
	}
	if m, _ := e.s.App.Notify.Override(e.ctx, 1, 10); m != "" {
		t.Fatalf("after reset %q", m)
	}
	if res, _ := post("ann", "often"); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad event: %d", res.StatusCode)
	}
	if res, _ := post("bob", "none"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("bob: %d", res.StatusCode)
	}
	if res, _ := post("ann", "none", func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "https://evil.example")
	}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site: %d", res.StatusCode)
	}
	if m, _ := e.s.App.Notify.Override(e.ctx, 1, 10); m != "" {
		t.Fatalf("override changed by a refused request: %q", m)
	}
}

func TestSecretTimeline(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	e.exec(`INSERT INTO secrets (id, owner_id, kind, mode, state, ct_size, chunk_count, views_left, max_views, failures, max_failures, expires_at, note, notify,
  opened_at, burned_at, burn_reason, created_at, updated_at)
VALUES (?, 1, 'text', 'e2e', 'burned', 64, 1, 1, 2, 3, 3, ?, 'prod db', 0, ?, ?, 'failed attempts', ?, ?)`,
		secretID, now.Add(time.Hour).UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.Add(-time.Hour).UnixMilli(), now.UnixMilli())
	sev := func(mins int, kind, ip, sum string) {
		e.exec(`INSERT INTO secret_events (secret_id, at, kind, ip, ua_summary) VALUES (?, ?, ?, ?, ?)`, secretID, now.Add(time.Duration(mins)*time.Minute).UnixMilli(), kind, ip, sum)
	}
	sev(-50, "link_opened", "203.0.113.5", "Safari · iOS")
	sev(-49, "revealed", "203.0.113.5", "Safari · iOS")
	sev(-10, "failed", "198.51.100.20", "curl")
	sev(-9, "failed", "198.51.100.20", "curl")
	sev(-8, "failed", "198.51.100.20", "curl")
	sev(-8, "burned", "198.51.100.20", "curl")

	path := "/dashboard/secrets/" + secretID + "/timeline"
	res, page := e.do("GET", path, nil, as("ann"))
	if res.StatusCode != 200 || res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("timeline: %d", res.StatusCode)
	}
	for _, want := range []string{"prod db", ">burned<", ">revealed<", `<span class="pb-mono">1 of 2</span>`, ">wrong passphrase<",
		`<span class="pb-mono">3 of 3</span>`, `<span class="pb-mono">failed attempts</span>`, ">link opened<", "198.51.100.20", "Safari · iOS", "6 events"} {
		if !strings.Contains(page, want) {
			t.Errorf("timeline lacks %q", want)
		}
	}
	// Newest first.
	if strings.Index(page, ">burned<") > strings.Index(page, ">link opened<") {
		t.Error("not newest first")
	}
	if strings.Contains(page, "Notify me when it is revealed") {
		t.Error("notify switch shown with every channel off")
	}
	// Two people viewed it: a Viewers strip with a chip each, labelled in order of
	// first appearance, and each event names its viewer. Visitor keys stay on the server.
	e.exec(`UPDATE secret_events SET visitor_key = ? WHERE ip = '198.51.100.20'`, "LEAKKEY-0123456789")
	_, page = e.do("GET", path, nil, as("ann"))
	for _, want := range []string{`data-viewers`, `data-viewer="all"`, `data-viewer="v1"`, `data-viewer="v2"`, ">Visitor 1<", ">Visitor 2<",
		`<li class="pb-timeline__day"`, `class="pb-event" data-viewer="v2" data-status="down"`, "1 opened · 1 revealed · 0 failed", "0 opened · 0 revealed · 3 failed"} {
		if !strings.Contains(page, want) {
			t.Errorf("timeline lacks %q", want)
		}
	}
	if strings.Contains(page, "LEAKKEY") {
		t.Error("the page shows a visitor key")
	}
	if !regexp.MustCompile(`class="app-viewer__mark" data-hue="[1-6]"`).MatchString(page) {
		t.Error("viewer markers have no hue")
	}
	// htmx gets the body only, a page at a time; counts cover every event.
	for i := 0; i < 20; i++ {
		sev(-40+i, "link_opened", "192.0.2.1", "Firefox · Linux")
	}
	hx := func(r *http.Request) { r.Header.Set("HX-Request", "true") }
	_, frag := e.do("GET", path, nil, as("ann"), hx)
	if strings.Contains(frag, "<html") || !strings.Contains(frag, "1–20 of 26 events") || !strings.Contains(frag, `href="`+path+`?page=2"`) ||
		!strings.Contains(frag, ">burned<") {
		t.Errorf("fragment page 1: %s", frag)
	}
	// The dashboard's dialog gets head, body and footer.
	_, dlg := e.do("GET", path, nil, as("ann"), hx, func(r *http.Request) { r.Header.Set("HX-Target", "secret-timeline-dialog") })
	if !strings.Contains(dlg, `class="pb-dialog__head"`) || !strings.Contains(dlg, `class="pb-dialog__foot"`) || !strings.Contains(dlg, "1–20 of 26 events") || !strings.Contains(dlg, "data-viewers") || !strings.Contains(dlg, ">Visitor 3<") ||
		// not the page's own body: its heading and pager line
		strings.Contains(dlg, "pb-pagehead") || strings.Contains(dlg, "app-tl-foot") {
		t.Errorf("dialog fragment: %s", dlg)
	}
	_, frag = e.do("GET", path+"?page=2", nil, as("ann"), hx)
	if !strings.Contains(frag, "21–26 of 26 events") || !strings.Contains(frag, ">link opened<") || strings.Contains(frag, ">burned<") {
		t.Errorf("fragment page 2: %s", frag)
	}
	// Someone else's secret, or a bad id, is not found.
	for _, tc := range []struct{ who, path string }{{"bob", path}, {"root", path}, {"ann", "/dashboard/secrets/nope/timeline"}} {
		res, body := e.do("GET", tc.path, nil, as(tc.who))
		if res.StatusCode != http.StatusNotFound || strings.Contains(body, "198.51.100.20") || strings.Contains(body, "prod db") {
			t.Errorf("%s %s: %d", tc.who, tc.path, res.StatusCode)
		}
	}

	// With a channel on, the secret's notify flag is a Switch.
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true)
	if _, page := e.do("GET", path, nil, as("ann")); !strings.Contains(page, "Notify me when it is revealed") || !strings.Contains(page, `data-secret-notify="/dashboard/secrets/`+secretID+`/notify">`) {
		t.Fatal("notify switch missing")
	}
	set := func(who, on string, opts ...func(*http.Request)) result {
		res, _ := e.do("POST", "/dashboard/secrets/"+secretID+"/notify", strings.NewReader(url.Values{"enabled": {on}}.Encode()),
			append([]func(*http.Request){as(who), form(), func(r *http.Request) { r.Header.Set("Accept", "application/json") }}, opts...)...)
		return res
	}
	flag := func() int {
		var n int
		_ = e.s.App.DB.R.QueryRow(`SELECT notify FROM secrets WHERE id = ?`, secretID).Scan(&n)
		return n
	}
	if res := set("ann", "1"); res.StatusCode != 200 || flag() != 1 {
		t.Fatalf("on: %d %d", res.StatusCode, flag())
	}
	if res := set("bob", "0"); res.StatusCode != http.StatusNotFound || flag() != 1 {
		t.Fatalf("bob: %d %d", res.StatusCode, flag())
	}
	if res := set("ann", "0", func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "https://evil.example")
	}); res.StatusCode != http.StatusForbidden || flag() != 1 {
		t.Fatalf("cross-site: %d", res.StatusCode)
	}
	if res := set("ann", "0"); res.StatusCode != 200 || flag() != 0 {
		t.Fatalf("off: %d %d", res.StatusCode, flag())
	}
}

// TestItemPageControls: the owner's item page carries the preview, the
// details and the controls; an admin's totals page carries none of them
// (no link, name, tags or the item's JSON); the old stats address redirects.
func TestItemPageControls(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO tags (id, owner_id, name, created_at) VALUES (1, 1, 'travel', 1)`)
	e.exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (10, 1)`)
	e.exec(`INSERT INTO items (id, owner_id, kind, slug, title, created_at, updated_at) VALUES (13, 1, 'album', 'trip', 'Trip <2026>', 1, 1)`)
	e.exec(`INSERT INTO album_items (album_id, item_id, position, added_at) VALUES (13, 10, 0, 1)`)

	_, page := e.do("GET", "/dashboard/items/10", nil, as("ann"))
	for _, want := range []string{
		`data-item-edit=""`, `data-item="{&#34;id&#34;:10,`, `&#34;tags&#34;:[&#34;travel&#34;]`, `id="item-edit"`,
		`action="/dashboard/items/10/delete"`, `data-confirm="Delete photo.jpg?"`, "Copy link", `popovertarget="share-qr"`,
		"<dt>link</dt>", "/pic</span>", "<dt>tags</dt><dd>travel</dd>",
		`<a class="pb-link" href="/dashboard/items/13">Trip &lt;2026&gt;</a>`, `href="/dashboard">Items</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("owner page lacks %q", want)
		}
	}

	res, admin := e.do("GET", "/dashboard/items/10", nil, as("root"))
	if res.StatusCode != 200 {
		t.Fatalf("admin page: %d", res.StatusCode)
	}
	for _, never := range []string{"data-item-edit", "item-edit", "/delete", "Copy link", "<dt>link</dt>", "photo.jpg", "/pic", "travel", "Trip"} {
		if strings.Contains(admin, never) {
			t.Errorf("admin page shows %q", never)
		}
	}

	// The old stats address keeps its query.
	res, _ = e.do("GET", "/dashboard/items/10/stats?days=7&page=2", nil, as("ann"))
	if res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != "/dashboard/items/10?days=7&page=2" {
		t.Errorf("old stats address: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
}
