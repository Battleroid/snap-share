package statsui_test

// Regression (M3): item ids are AUTOINCREMENT and never reused; visitor
// events still buffered when an item is deleted are dropped at the flush
// instead of being written under the dead id, and the deleted item's
// batched opens are forgotten, so nothing of the old item's visitors
// reaches the next item's owner (stats page, CSV, events API, open
// notifications).

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

type reuseEnv struct {
	t *testing.T
	s *testutil.Server
}

func newReuseEnv(t *testing.T, opts ...testutil.Option) *reuseEnv {
	s := testutil.NewServer(t, opts...)
	e := &reuseEnv{t: t, s: s}
	e.exec(`INSERT INTO users (id, username, email, email_verified_at, role, created_at, updated_at) VALUES
 (1, 'victim', 'victim@example.test', 1, 'user', 1, 1), (2, 'attacker', 'attacker@example.test', 1, 'user', 1, 1)`)
	s.Auth.Add("victim", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 11, Scopes: auth.AllScopes[:3]})
	s.Auth.Add("attacker", auth.Principal{UserID: 2, EmailVerified: true, KeyID: 22, Scopes: auth.AllScopes[:3]})
	return e
}

func (e *reuseEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.s.App.DB.W.Exec(q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *reuseEnv) count(q string, args ...any) int64 {
	e.t.Helper()
	var n int64
	if err := e.s.App.DB.R.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *reuseEnv) req(method, path, who string) *http.Request {
	req := e.s.Request(e.t, method, path, nil)
	req.Header.Del("Sec-Fetch-Site")
	req.Header.Del("Origin")
	if who != "" {
		req.Header.Set("Authorization", "Bearer test-"+who)
	}
	return req
}

func (e *reuseEnv) paste(who string) (int64, string) {
	e.t.Helper()
	req := e.s.Request(e.t, http.MethodPost, "/api/v1/pastes", strings.NewReader(`{"content":"hello from `+who+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-"+who)
	req.Header.Del("Sec-Fetch-Site")
	req.Header.Del("Origin")
	res, body := e.s.Do(e.t, req) //nolint:bodyclose // Do reads and closes the body
	if res.StatusCode != http.StatusCreated {
		e.t.Fatalf("create paste: %d %s", res.StatusCode, body)
	}
	var it struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal([]byte(body), &it); err != nil {
		e.t.Fatal(err, body)
	}
	return it.ID, it.Slug
}

// visitThenDelete has a visitor open the victim's paste and the victim
// delete it before the stats writer and the counters flush.
func (e *reuseEnv) visitThenDelete(ua string) int64 {
	e.t.Helper()
	vid, slug := e.paste("victim")
	v := e.req(http.MethodGet, "/"+slug, "")
	v.Header.Set("User-Agent", ua)
	v.Header.Set("Referer", "https://private-chat.example/room/42")
	if res, body := e.s.Do(e.t, v); res.StatusCode != http.StatusOK { //nolint:bodyclose // Do reads and closes the body
		e.t.Fatalf("visit: %d %s", res.StatusCode, body)
	}
	if res, body := e.s.Do(e.t, e.req(http.MethodDelete, "/api/v1/items/"+strconv.FormatInt(vid, 10), "victim")); res.StatusCode != http.StatusNoContent { //nolint:bodyclose // Do reads and closes the body
		e.t.Fatalf("delete: %d %s", res.StatusCode, body)
	}
	return vid
}

func TestRedteamItemIDReuseLeaksVisitorEvents(t *testing.T) {
	e := newReuseEnv(t)
	ctx := context.Background()
	vid := e.visitThenDelete("Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0 victims-visitor-fingerprint")

	// The periodic flush runs after the delete: the event is dropped.
	if err := e.s.App.Stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM item_events WHERE item_id = ?`, vid); n != 0 {
		t.Fatalf("item_events rows left under deleted item %d: %d", vid, n)
	}
	if n := e.count(`SELECT count(*) FROM item_stats_daily WHERE item_id = ?`, vid); n != 0 {
		t.Fatalf("item_stats_daily rows left under deleted item %d: %d", vid, n)
	}
	if st := e.s.App.Stats.Stats(); st.Orphaned == 0 {
		t.Errorf("writer stats: %+v, want the event counted as orphaned", st)
	}

	// The next item gets a new id.
	aid, _ := e.paste("attacker")
	if aid == vid {
		t.Fatalf("item id %d reused", aid)
	}
	res, body := e.s.Do(t, e.req(http.MethodGet, "/api/v1/items/"+strconv.FormatInt(aid, 10)+"/events", "attacker")) //nolint:bodyclose // Do reads and closes the body
	if res.StatusCode != http.StatusOK || strings.Contains(body, "victims-visitor-fingerprint") || strings.Contains(body, "private-chat.example") {
		t.Fatalf("attacker's events: %d %s", res.StatusCode, body)
	}

	// Rows orphaned some other way (written before this fix) are swept.
	e.exec(`INSERT INTO item_events (item_id, at, kind) VALUES (?, 1, 'view')`, vid)
	e.exec(`INSERT INTO item_stats_daily (item_id, day, views) VALUES (?, 0, 1)`, vid)
	e.exec(`INSERT INTO item_visitors (item_id, visitor_key, last_day) VALUES (?, 'k', 0)`, vid)
	if err := e.s.App.Items.SweepOrphans(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"item_events", "item_stats_daily", "item_visitors"} {
		if n := e.count(`SELECT count(*) FROM `+tbl+` WHERE item_id = ?`, vid); n != 0 {
			t.Errorf("%s rows for deleted item after the sweep: %d", tbl, n)
		}
	}
}

func TestRedteamItemIDReuseNotifiesNewOwner(t *testing.T) {
	e := newReuseEnv(t, testutil.WithSMTP())
	ctx := context.Background()
	e.exec(`INSERT INTO notify_prefs (user_id, email_enabled, default_event, updated_at) VALUES (2, 1, 'first', 1)`)
	vid := e.visitThenDelete("Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0")
	if err := e.s.App.Stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	aid, _ := e.paste("attacker")
	if aid == vid {
		t.Fatalf("item id %d reused", aid)
	}
	if err := e.s.App.Items.FlushCounters(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT opened_count FROM items WHERE id = ?`, aid); n != 0 {
		t.Fatalf("attacker's never-shared item has opened_count=%d", n)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = 'notify.open'`); n != 0 {
		t.Fatalf("%d open notifications queued for a deleted item's visit", n)
	}
}
