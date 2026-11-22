package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestStatsPurge(t *testing.T) {
	s := testutil.NewServer(t)
	ctx := context.Background()
	now := time.Now()
	add := func(age time.Duration) {
		t.Helper()
		if _, err := s.App.DB.W.Exec(`INSERT INTO item_events (item_id, at, kind, ip, visitor_key) VALUES (1, ?, 'view', '203.0.113.4', 'k')`,
			now.Add(-age).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.App.DB.W.Exec(`INSERT INTO item_stats_daily (item_id, day, views) VALUES (1, 0, 3)`); err != nil {
		t.Fatal(err)
	}
	day := 24 * time.Hour
	add(100 * day)
	add(40 * day)
	add(time.Hour)
	count := func(q string) int {
		t.Helper()
		var n int
		if err := s.App.DB.R.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := s.App.PurgeStats(ctx); err != nil { // default: 90 days
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM item_events`); n != 2 {
		t.Errorf("90-day retention kept %d events, want 2", n)
	}
	if err := app.StatsRetentionDays.Set(ctx, s.App.Settings, 30); err != nil {
		t.Fatal(err)
	}
	if err := s.App.PurgeStats(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM item_events`); n != 1 {
		t.Errorf("30-day retention kept %d events, want 1", n)
	}
	if n := count(`SELECT views FROM item_stats_daily WHERE item_id = 1`); n != 3 {
		t.Errorf("daily totals changed: %d", n)
	}
}
