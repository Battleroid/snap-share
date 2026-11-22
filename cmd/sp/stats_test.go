package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
)

func TestStats(t *testing.T) {
	e := newCLIEnv(t)
	link := strings.TrimSpace(e.mustSP("", "short", "https://example.com/report", "--slug", "report"))
	var id int64
	if err := e.s.App.DB.R.QueryRow(`SELECT id FROM items WHERE slug = 'report'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := e.s.App.DB.W.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE items SET first_opened_at = ?, last_opened_at = ?, opened_count = 4 WHERE id = ?`, today.AddDate(0, 0, -3).UnixMilli(), now.UnixMilli(), id)
	for _, d := range []struct{ off, clicks int }{{0, 3}, {-3, 1}, {-20, 5}} {
		exec(`INSERT INTO item_stats_daily (item_id, day, views, clicks, new_visitors) VALUES (?, ?, ?, ?, 1)`, id, today.AddDate(0, 0, d.off).UnixMilli(), d.clicks, d.clicks)
	}
	at := now.Add(-time.Minute).UnixMilli()
	exec(`INSERT INTO item_events (item_id, at, kind, ip, user_agent, ua_summary, referrer_host, visitor_key) VALUES (?, ?, 'click', '203.0.113.4', 'Mozilla', 'Firefox · macOS', 'chat.example', 'k1')`, id, at)
	exec(`INSERT INTO item_events (item_id, at, kind, ip, user_agent, ua_summary, bot_name) VALUES (?, ?, 'preview', '192.0.2.9', 'Slackbot', 'Slack bot', 'Slack')`, id, at+1)

	out := e.mustSP("", "stats", link, "--days", "7")
	for _, want := range []string{
		"https://example.com/report · " + link, "opened · first ", "last 7 days", "views 4 · unique visitors 1 · previews 0 · clicks 4",
		"views/day ▁▁▁▄▁▁█  " + today.AddDate(0, 0, -6).Format(time.DateOnly) + " … " + today.Format(time.DateOnly) + " · peak 3",
		"referrers chat.example 1", "bots      Slack 1", "browsers  Firefox · macOS 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "203.0.113.4") {
		t.Error("IPs without --events")
	}
	// By id, with the latest events and a longer window.
	out = e.mustSP("", "stats", "--events", "--days", "30", strconv.FormatInt(id, 10))
	if !strings.Contains(out, "views 9") || !strings.Contains(out, "latest events") ||
		!strings.Contains(out, "preview   192.0.2.9") || !strings.Contains(out, "click     203.0.113.4") || !strings.Contains(out, "chat.example") {
		t.Errorf("--events:\n%s", out)
	}
	// Errors.
	for _, args := range [][]string{{"stats"}, {"stats", "nope"}, {"stats", "report", "--days", "0"}, {"stats", "https://elsewhere.example/report"}} {
		if _, _, err := e.sp("", args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	// A key without items:read is refused by the server.
	t.Setenv("SNAP_KEY", e.mint(auth.ScopeItemsWrite))
	if _, _, err := e.sp("", "stats", strconv.FormatInt(id, 10)); err == nil || !strings.Contains(err.Error(), "items:read") {
		t.Errorf("without items:read: %v", err)
	}
}

func TestSparkText(t *testing.T) {
	if got := sparkText([]int64{0, 1, 2, 4, 8}); got != "▁▂▃▅█" {
		t.Errorf("sparkText = %q", got)
	}
	if got := sparkText(make([]int64, 90)); len([]rune(got)) != 60 || strings.Trim(got, "▁") != "" {
		t.Errorf("folded %q", got)
	}
}
