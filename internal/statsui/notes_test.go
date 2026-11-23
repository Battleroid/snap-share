package statsui

import (
	"fmt"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
)

func TestEndNote(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	old, recent := secrets.Summary{Created: now.AddDate(0, 0, -100)}, secrets.Summary{Created: now.AddDate(0, 0, -3)}
	for _, tc := range []struct {
		name      string
		sm        secrets.Summary
		retention int
		total     int
		want      string
	}{
		{"older than retention", old, 90, 12, "Events older than 90 days are removed."},
		{"one day", secrets.Summary{Created: now.AddDate(0, 0, -5)}, 1, 3, "Events older than 1 day are removed."},
		{"within retention", recent, 90, 12, ""},
		{"retention unknown", old, 0, 12, ""},
		{"the cap", recent, 90, maxTimeline, "Only the latest 500 events are shown."},
		{"both", old, 30, maxTimeline, "Events older than 30 days are removed. Only the latest 500 events are shown."},
	} {
		if got := endNote(tc.sm, now, tc.retention, tc.total); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRetentionNote(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func() int
		want string
	}{
		{"unset", nil, "Referrers, clients and secret events come from raw events, which are deleted after the retention period."},
		{"zero", func() int { return 0 }, "Referrers, clients and secret events come from raw events, which are deleted after the retention period."},
		{"set", func() int { return 90 }, "Referrers, clients and secret events come from raw events, which are kept for 90 days."},
		{"one day", func() int { return 1 }, "Referrers, clients and secret events come from raw events, which are kept for 1 day."},
	} {
		s := &Service{d: Deps{RetentionDays: tc.fn}}
		if got := s.retentionNote(); got != tc.want {
			t.Errorf("%s: %q", tc.name, got)
		}
	}
}

// Viewers are told apart by bot name, then visitor key, then IP and client;
// two with the same hash hue still get different markers.
func TestTimelineViewers(t *testing.T) {
	// Two keys whose hash gives the same hue.
	var a, b string
	for i := 0; b == ""; i++ {
		k := fmt.Sprintf("key-%d", i)
		switch {
		case a == "":
			a = k
		case hueOf("key:"+k) == hueOf("key:"+a):
			b = k
		}
	}
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	ev := func(min int, kind stats.Kind, key, ip, bot string) stats.Event {
		return stats.Event{At: at.Add(time.Duration(min) * time.Minute), Kind: kind, VisitorKey: key, IP: ip, UASummary: "Firefox · Linux", Bot: bot}
	}
	// Newest first, as stored.
	evs := []stats.Event{
		ev(5, stats.KindRevealed, b, "198.51.100.2", ""),
		ev(4, stats.KindLinkOpened, "", "192.0.2.9", "Slack"),
		ev(3, stats.KindLinkOpened, b, "198.51.100.2", ""),
		ev(2, stats.KindFailed, a, "203.0.113.1", ""),
		ev(1, stats.KindLinkOpened, "", "192.0.2.9", "Slack"),
		ev(0, stats.KindLinkOpened, a, "203.0.113.1", ""),
	}
	rows, viewers, reveals, fails := timeline(secrets.Summary{MaxViews: 2, MaxFailures: 3}, evs)
	if len(rows) != 6 || reveals != 1 || fails != 1 || len(viewers) != 3 {
		t.Fatalf("%d rows, %d reveals, %d fails, %d viewers", len(rows), reveals, fails, len(viewers))
	}
	want := []struct {
		label                       string
		opened, revealed, failed, n int
	}{{"Visitor 1", 1, 0, 1, 2}, {"Slack", 2, 0, 0, 2}, {"Visitor 2", 1, 1, 0, 2}}
	hues := map[int]bool{}
	for i, w := range want {
		v := viewers[i]
		if v.Label != w.label || v.Opened != w.opened || v.Revealed != w.revealed || v.Failed != w.failed || v.Total != w.n || v.ID != fmt.Sprintf("v%d", i+1) {
			t.Errorf("viewer %d: %+v", i, v)
		}
		if v.Hue < 1 || v.Hue > 6 || hues[v.Hue] {
			t.Errorf("viewer %d hue %d (used %v)", i, v.Hue, hues)
		}
		hues[v.Hue] = true
	}
	// Each row names its viewer; a visitor key never appears in the data the page gets.
	if rows[0].Viewer != "v3" || rows[0].VLabel != "Visitor 2" || rows[5].Viewer != "v1" || rows[1].VLabel != "Slack" {
		t.Errorf("rows %+v", rows)
	}
	for _, r := range rows {
		if r.Viewer == a || r.Viewer == b || r.VLabel == a || r.VLabel == b {
			t.Errorf("visitor key in a row: %+v", r)
		}
	}
	if rows[0].Day != "2026-11-23" || rows[0].Clock != "09:05:00" {
		t.Errorf("when: %s %s", rows[0].Day, rows[0].Clock)
	}
}
