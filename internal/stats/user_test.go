package stats

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// userFixture is a migrated database with two users, ann (1) and bob (2),
// and stats rows for both, plus rows whose item or secret is gone.
type userFixture struct {
	t     *testing.T
	ctx   context.Context
	db    *db.DB
	today time.Time
}

func newUserFixture(t *testing.T) *userFixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	f := &userFixture{t: t, ctx: ctx, db: d, today: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)}
	f.exec(`INSERT INTO users (id, username, email, email_verified_at, role, created_at, updated_at) VALUES
  (1, 'ann', 'ann@example.test', 1, 'user', 1, 1), (2, 'bob', 'bob@example.test', 1, 'user', 1, 1)`)
	return f
}

func (f *userFixture) day(off int) time.Time { return f.today.AddDate(0, 0, off) }

func (f *userFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.W.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

func (f *userFixture) item(id, owner int64, kind, mime string, size, render int64, created time.Time) {
	f.t.Helper()
	f.exec(`INSERT INTO items (id, owner_id, kind, slug, mime, size, render_size, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, owner, kind, fmt.Sprintf("s%d", id), mime, size, render, created.UnixMilli(), created.UnixMilli())
}

func (f *userFixture) daily(item int64, off, views, previews, downloads, plays, clicks int) {
	f.t.Helper()
	f.exec(`INSERT INTO item_stats_daily (item_id, day, views, previews, downloads, plays, clicks) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item, f.day(off).UnixMilli(), views, previews, downloads, plays, clicks)
}

func (f *userFixture) event(item int64, off int, kind, ref, ua string) {
	f.t.Helper()
	f.exec(`INSERT INTO item_events (item_id, at, kind, referrer_host, ua_summary) VALUES (?, ?, ?, ?, ?)`,
		item, f.day(off).Add(time.Hour).UnixMilli(), kind, ref, ua)
}

// sid pads a short name to the 22 characters a secret id has.
func sid(name string) string { return name + strings.Repeat("x", 22-len(name)) }

func (f *userFixture) secret(id string, owner int64, state string) {
	f.t.Helper()
	now := time.Now().UnixMilli()
	f.exec(`INSERT INTO secrets (id, owner_id, kind, mode, state, ct_size, chunk_count, views_left, max_views, max_failures, expires_at, created_at, updated_at)
VALUES (?, ?, 'text', 'e2e', ?, 64, 1, 1, 1, 3, ?, ?, ?)`, sid(id), owner, state, now+3600_000, now, now)
}

func (f *userFixture) secretEvent(secret string, off int, kind string, n int) {
	f.t.Helper()
	for range n {
		f.exec(`INSERT INTO secret_events (secret_id, at, kind) VALUES (?, ?, ?)`, sid(secret), f.day(off).Add(2*time.Hour).UnixMilli(), kind)
	}
}

// seed gives ann and bob distinctive data in every table the helpers read.
func (f *userFixture) seed() {
	f.item(10, 1, "file", "image/png", 100, 5, f.day(-40).Add(time.Hour))
	f.item(11, 1, "file", "video/mp4", 1000, 0, f.day(-3).Add(time.Hour))
	f.item(12, 1, "file", "audio/mpeg", 50, 0, f.day(-3).Add(2*time.Hour))
	f.item(13, 1, "file", "text/plain", 7, 0, f.day(0).Add(time.Hour))
	f.item(14, 1, "file", "application/zip", 70, 0, f.day(0).Add(2*time.Hour))
	f.item(15, 1, "paste", "text/plain", 3, 2, f.day(0).Add(3*time.Hour))
	f.item(16, 1, "link", "", 0, 0, f.day(0).Add(4*time.Hour))
	f.item(17, 1, "album", "", 0, 0, f.day(0).Add(5*time.Hour))
	f.item(20, 2, "file", "image/png", 99999, 0, f.day(-1))

	// ann: 10 gets views and a download; 11 downloads and plays; 16 clicks.
	f.daily(10, 0, 3, 1, 1, 0, 0)
	f.daily(10, -5, 2, 0, 0, 0, 0)
	f.daily(10, -20, 4, 0, 0, 0, 0)
	f.daily(10, -60, 10, 0, 0, 0, 0)
	f.daily(11, -1, 3, 0, 2, 4, 0)
	f.daily(16, 0, 0, 0, 0, 0, 5)
	// bob's, and a deleted item's (99) that is still in the rollups.
	f.daily(20, 0, 500, 500, 500, 500, 500)
	f.daily(99, 0, 777, 0, 0, 0, 0)

	f.event(10, 0, "view", "news.example", "Firefox · macOS")
	f.event(10, -5, "view", "news.example", "Firefox · macOS")
	f.event(10, 0, "view", "forum.example", "curl")
	f.event(10, 0, "preview", "preview.example", "Discord bot")
	f.event(11, -1, "play", "", "Firefox · macOS")
	f.event(10, -100, "view", "old.example", "Opera") // before every range
	f.event(20, 0, "view", "bob.example", "BobsBrowser")
	f.event(99, 0, "view", "ghost.example", "GhostBrowser")

	f.secret("A1", 1, "ready")
	f.secret("A2", 1, "burned")
	f.secret("B1", 2, "ready")
	f.secretEvent("A1", 0, "link_opened", 3)
	f.secretEvent("A1", 0, "revealed", 2)
	f.secretEvent("A2", -2, "failed", 1)
	f.secretEvent("A2", -2, "burned", 1)
	f.secretEvent("A1", -50, "link_opened", 4) // before a 30 day range
	f.secretEvent("B1", 0, "link_opened", 9)
	f.secretEvent("B1", 0, "revealed", 9)
	f.secretEvent("GONE", 0, "revealed", 9)
}

func keys(cs []Count) string {
	s := ""
	for _, c := range cs {
		s += fmt.Sprintf("%s=%d ", c.Key, c.N)
	}
	return s
}

func TestUserQueriesAreScopedToTheOwner(t *testing.T) {
	f := newUserFixture(t)
	f.seed()
	r, ctx := f.db.R, f.ctx
	from, now := f.day(-29), time.Now().UTC()

	t.Run("totals", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			user int64
			from time.Time
			want Totals
		}{
			{"ann 30 days", 1, from, Totals{Views: 3 + 2 + 4 + 3, Previews: 1, Downloads: 1 + 2, Plays: 4, Clicks: 5}},
			{"ann 7 days", 1, f.day(-6), Totals{Views: 3 + 2 + 3, Previews: 1, Downloads: 3, Plays: 4, Clicks: 5}},
			{"ann since the start", 1, time.Time{}, Totals{Views: 3 + 2 + 4 + 10 + 3, Previews: 1, Downloads: 3, Plays: 4, Clicks: 5}},
			{"bob", 2, from, Totals{Views: 500, Previews: 500, Downloads: 500, Plays: 500, Clicks: 500, Embeds: 0}},
			{"nobody", 3, from, Totals{}},
		} {
			got, err := UserTotals(ctx, r, tc.user, tc.from, time.Time{})
			if err != nil || got != tc.want {
				t.Errorf("%s: %+v, %v; want %+v", tc.name, got, err, tc.want)
			}
		}
	})

	t.Run("daily", func(t *testing.T) {
		days, err := UserDaily(ctx, r, 1, from, now)
		if err != nil || len(days) != 30 {
			t.Fatalf("%d days, %v", len(days), err)
		}
		var views int64
		for _, d := range days {
			views += d.Views
			if d.Uniques != 0 {
				t.Errorf("%v: uniques %d, want 0 (they can't be summed over items)", d.Day, d.Uniques)
			}
		}
		if views != 12 || days[29].Views != 3 || days[28].Views != 3 || days[24].Views != 2 || days[9].Views != 4 || days[0].Views != 0 {
			t.Errorf("series %+v", days)
		}
		if !days[0].Day.Equal(from) || !days[29].Day.Equal(f.today) {
			t.Errorf("days run %v to %v", days[0].Day, days[29].Day)
		}
		for _, rg := range [][2]time.Time{{{}, now}, {from, {}}, {now, from}, {from, from.AddDate(0, 0, MaxSeriesDays)}} {
			if _, err := UserDaily(ctx, r, 1, rg[0], rg[1]); !errors.Is(err, ErrRange) {
				t.Errorf("UserDaily(%v, %v): %v, want ErrRange", rg[0], rg[1], err)
			}
		}
	})

	t.Run("top items", func(t *testing.T) {
		got, err := UserTopItems(ctx, r, 1, from, time.Time{}, 10)
		want := []ItemCount{{10, 9, 1, 0}, {11, 3, 2, 4}, {16, 0, 0, 5}}
		if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("ann: %+v, %v; want %+v", got, err, want)
		}
		if got, _ := UserTopItems(ctx, r, 1, from, time.Time{}, 1); len(got) != 1 || got[0].ItemID != 10 {
			t.Errorf("limit 1: %+v", got)
		}
		if got, _ := UserTopItems(ctx, r, 2, from, time.Time{}, 10); len(got) != 1 || got[0].ItemID != 20 {
			t.Errorf("bob: %+v", got)
		}
	})

	t.Run("referrers and clients", func(t *testing.T) {
		refs, err := UserTopReferrers(ctx, r, 1, from, time.Time{}, 10)
		if err != nil || keys(refs) != "news.example=2 forum.example=1 " {
			t.Errorf("referrers %q, %v", keys(refs), err)
		}
		uas, err := UserTopUserAgents(ctx, r, 1, from, time.Time{}, 10)
		if err != nil || keys(uas) != "Firefox · macOS=3 curl=1 " {
			t.Errorf("clients %q, %v", keys(uas), err)
		}
		if refs, _ := UserTopReferrers(ctx, r, 2, from, time.Time{}, 10); keys(refs) != "bob.example=1 " {
			t.Errorf("bob referrers %q", keys(refs))
		}
	})

	t.Run("secrets", func(t *testing.T) {
		days, err := UserSecretDaily(ctx, r, 1, from, now)
		if err != nil || len(days) != 30 {
			t.Fatalf("%d days, %v", len(days), err)
		}
		if got, want := SumSecrets(days), (SecretCounts{Opened: 3, Revealed: 2, Failed: 1}); got != want {
			t.Errorf("sum %+v, want %+v", got, want)
		}
		if days[29].Revealed != 2 || days[27].Failed != 1 {
			t.Errorf("series %+v", days)
		}
		days, _ = UserSecretDaily(ctx, r, 1, f.day(-60), now)
		if got := SumSecrets(days); got.Opened != 7 {
			t.Errorf("60 days: %+v", got)
		}
		days, _ = UserSecretDaily(ctx, r, 2, from, now)
		if got := SumSecrets(days); got != (SecretCounts{Opened: 9, Revealed: 9}) {
			t.Errorf("bob: %+v", got)
		}
		all, active, err := UserSecretCount(ctx, r, 1)
		if err != nil || all != 2 || active != 1 {
			t.Errorf("count %d active %d, %v", all, active, err)
		}
	})
}

func TestUserUsageHelpers(t *testing.T) {
	f := newUserFixture(t)
	f.seed()
	r, ctx := f.db.R, f.ctx
	from, now := f.day(-29), time.Now().UTC()

	counts, err := UserItemCounts(ctx, r, 1)
	if err != nil || keys(counts) != "image=1 video=1 audio=1 text=1 other=1 paste=1 link=1 album=1 " {
		t.Errorf("counts %q, %v", keys(counts), err)
	}
	f.item(30, 1, "file", "image/jpeg", 1, 0, f.day(0))
	f.item(31, 1, "file", "text/markdown", 1, 0, f.day(0))
	f.item(32, 1, "file", "text/x-go", 1, 0, f.day(0))
	counts, _ = UserItemCounts(ctx, r, 1)
	if keys(counts) != "text=3 image=2 video=1 audio=1 other=1 paste=1 link=1 album=1 " {
		t.Errorf("sorted by count, then type order: %q", keys(counts))
	}
	if counts, _ := UserItemCounts(ctx, r, 3); len(counts) != 0 {
		t.Errorf("an account with nothing: %+v", counts)
	}

	k, err := UserStorageKinds(ctx, r, 1, 2000)
	if err != nil || k != (StorageKinds{Images: 106, Video: 1000, Audio: 50, Other: 2000 - 106 - 1000 - 50}) {
		t.Errorf("kinds %+v, %v", k, err)
	}
	if k, _ = UserStorageKinds(ctx, r, 1, 10); k.Other != 0 || k.Video != 1000 {
		t.Errorf("a stale counter: %+v", k)
	}
	if k, _ = UserStorageKinds(ctx, r, 2, 99999); k != (StorageKinds{Images: 99999}) {
		t.Errorf("bob: %+v", k)
	}

	created, err := UserItemsCreatedDaily(ctx, r, 1, from, now)
	if err != nil || len(created) != 30 {
		t.Fatalf("created %d, %v", len(created), err)
	}
	// Today: 13, 14, 15, 16, 17, 30, 31, 32. Three days ago: 11 and 12. 10 is too old.
	if created[29].N != 8 || created[26].N != 2 || created[0].N != 0 {
		t.Errorf("created %+v", created)
	}
	bytes, err := UserBytesAddedDaily(ctx, r, 1, from, now)
	if err != nil || len(bytes) != 30 {
		t.Fatalf("bytes %d, %v", len(bytes), err)
	}
	// Item 10 (100+5) predates the range; links and albums hold nothing.
	if bytes[0].N != 105 || bytes[25].N != 105 || bytes[26].N != 105+1050 || bytes[29].N != 105+1050+7+70+5+3 {
		t.Errorf("cumulative bytes %+v", bytes)
	}
	if _, err := UserBytesAddedDaily(ctx, r, 1, now, from); !errors.Is(err, ErrRange) {
		t.Errorf("bad range: %v", err)
	}
	// Deleting an item takes its bytes out of the past.
	f.exec(`DELETE FROM items WHERE id = 11`)
	if bytes, _ = UserBytesAddedDaily(ctx, r, 1, from, now); bytes[29].N != 105+50+7+70+5+3 {
		t.Errorf("after delete %+v", bytes[29])
	}
}
