package stats

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestItemDaily(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w := quietWriter(t, db, WriterOptions{})
	w.Record(ctx, itemEvent("3", day1.Add(time.Hour), KindView, "a"))
	w.Record(ctx, itemEvent("3", day3.Add(time.Hour), KindView, "a"))
	w.Record(ctx, itemEvent("3", day3.Add(2*time.Hour), KindDownload, "b"))
	w.Record(ctx, itemEvent("4", day2, KindView, "a"))
	flush(t, w)

	days, err := ItemDaily(ctx, db, 3, day1.Add(5*time.Hour), day3.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := []Day{
		{Day: day1, Views: 1, Uniques: 1, NewVisitors: 1},
		{Day: day2},
		{Day: day3, Views: 1, Downloads: 1, Uniques: 2, NewVisitors: 1},
	}
	if fmt.Sprint(days) != fmt.Sprint(want) {
		t.Errorf("ItemDaily =\n%v\nwant\n%v", days, want)
	}
	for _, r := range [][2]time.Time{
		{{}, day1},
		{day1, {}},
		{day2, day1},
		{day1, day1.AddDate(0, 0, MaxSeriesDays)},
	} {
		if _, err := ItemDaily(ctx, db, 3, r[0], r[1]); !errors.Is(err, ErrRange) {
			t.Errorf("ItemDaily(%v, %v): err = %v, want ErrRange", r[0], r[1], err)
		}
	}
}

func TestTopLists(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w := quietWriter(t, db, WriterOptions{})
	add := func(kind Kind, ref, bot, ua string, n int) {
		for range n {
			w.Record(ctx, Event{Subject: "9", SubjectType: SubjectItem, At: day1.Add(time.Hour), Kind: kind,
				RefererHost: ref, Bot: bot, UASummary: ua})
		}
	}
	add(KindView, "news.example", "", "Firefox · Linux", 3)
	add(KindView, "chat.example", "", "Safari · iOS", 1)
	add(KindDownload, "", "", "curl", 2)
	add(KindPreview, "discord.com", "Discord", "Discord bot", 4)
	add(KindPreview, "", "Slack", "Slack bot", 1)
	// Outside the range, and another item.
	w.Record(ctx, Event{Subject: "9", SubjectType: SubjectItem, At: day3, Kind: KindView, RefererHost: "late.example"})
	w.Record(ctx, Event{Subject: "10", SubjectType: SubjectItem, At: day1, Kind: KindView, RefererHost: "other.example"})
	flush(t, w)

	tests := []struct {
		name string
		fn   func() ([]Count, error)
		want string
	}{
		{"referrers", func() ([]Count, error) { return TopReferrers(ctx, db, 9, day1, day2, 10) }, "[{news.example 3} {chat.example 1}]"},
		{"bots", func() ([]Count, error) { return TopBots(ctx, db, 9, day1, day2, 10) }, "[{Discord 4} {Slack 1}]"},
		{"user agents", func() ([]Count, error) { return TopUserAgents(ctx, db, 9, day1, day2, 10) }, "[{Firefox · Linux 3} {curl 2} {Safari · iOS 1}]"},
		{"limit", func() ([]Count, error) { return TopUserAgents(ctx, db, 9, day1, day2, 1) }, "[{Firefox · Linux 3}]"},
		{"open range", func() ([]Count, error) { return TopReferrers(ctx, db, 9, time.Time{}, time.Time{}, 0) }, "[{news.example 3} {chat.example 1} {late.example 1}]"},
	}
	for _, tt := range tests {
		got, err := tt.fn()
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if fmt.Sprint(got) != tt.want {
			t.Errorf("%s = %v, want %s", tt.name, got, tt.want)
		}
	}
}

func TestLatestEvents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w := quietWriter(t, db, WriterOptions{})
	for i := range 5 {
		e := itemEvent("8", day1.Add(time.Duration(i)*time.Minute), KindView, fmt.Sprint(i))
		e.RefererHost = "ref.example"
		w.Record(ctx, e)
	}
	w.Record(ctx, Event{Subject: "abc_-1", SubjectType: SubjectSecret, At: day1, Kind: KindLinkOpened, IP: "203.0.113.1", UA: "curl/8"})
	w.Record(ctx, Event{Subject: "abc_-1", SubjectType: SubjectSecret, At: day1.Add(time.Second), Kind: KindRevealed, IP: "203.0.113.1"})
	flush(t, w)

	items, err := LatestItemEvents(ctx, db, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].VisitorKey != "4" || items[2].VisitorKey != "2" {
		t.Fatalf("LatestItemEvents = %+v", items)
	}
	want := itemEvent("8", day1.Add(4*time.Minute), KindView, "4")
	want.RefererHost = "ref.example"
	if items[0] != want {
		t.Errorf("event =\n%+v\nwant\n%+v", items[0], want)
	}

	secrets, err := LatestSecretEvents(ctx, db, "abc_-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 2 || secrets[0].Kind != KindRevealed || secrets[1].UA != "curl/8" ||
		secrets[1].Subject != "abc_-1" || secrets[1].SubjectType != SubjectSecret {
		t.Errorf("LatestSecretEvents = %+v", secrets)
	}
}

func TestPurge(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w := quietWriter(t, db, WriterOptions{})
	old := day1.Add(time.Hour)
	for i := range purgeBatch + 10 { // more than one delete batch
		w.Record(ctx, itemEvent("1", old, KindView, fmt.Sprint(i%3)))
	}
	w.Record(ctx, Event{Subject: "s", SubjectType: SubjectSecret, At: old, Kind: KindRevealed, IP: "203.0.113.1"})
	w.Record(ctx, itemEvent("1", day3, KindView, "fresh"))
	w.Record(ctx, Event{Subject: "s", SubjectType: SubjectSecret, At: day3, Kind: KindBurned})
	flush(t, w)

	n, err := Purge(ctx, db, day2)
	if err != nil {
		t.Fatal(err)
	}
	if n != purgeBatch+11 {
		t.Errorf("purged %d, want %d", n, purgeBatch+11)
	}
	if c := count(t, db, `SELECT count(*) FROM item_events`); c != 1 {
		t.Errorf("item_events left = %d", c)
	}
	if c := count(t, db, `SELECT count(*) FROM secret_events`); c != 1 {
		t.Errorf("secret_events left = %d", c)
	}
	if c := count(t, db, `SELECT count(*) FROM item_visitors`); c != 1 {
		t.Errorf("item_visitors left = %d, want only the fresh visitor", c)
	}
	// Rollups stay.
	if got := daily(t, db, 1, day1); got.views != purgeBatch+10 || got.uniques != 3 {
		t.Errorf("day1 rollup after purge = %+v", got)
	}
	tot, err := ItemTotals(ctx, db, 1, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if tot.Views != purgeBatch+11 || tot.Uniques != 4 {
		t.Errorf("lifetime totals after purge = %+v", tot)
	}
	// Nothing left to purge.
	if n, err := Purge(ctx, db, day2); err != nil || n != 0 {
		t.Errorf("second purge = %d, %v", n, err)
	}
}

func TestDeleteSubject(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w := quietWriter(t, db, WriterOptions{})
	w.Record(ctx, itemEvent("1", day1, KindView, "a"))
	w.Record(ctx, itemEvent("2", day1, KindView, "a"))
	w.Record(ctx, Event{Subject: "s", SubjectType: SubjectSecret, At: day1, Kind: KindRevealed})
	flush(t, w)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeleteItemStats(ctx, tx, 1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSecretEvents(ctx, db, "s"); err != nil {
		t.Fatal(err)
	}
	for q, want := range map[string]int64{
		`SELECT count(*) FROM item_events WHERE item_id = 1`:      0,
		`SELECT count(*) FROM item_stats_daily WHERE item_id = 1`: 0,
		`SELECT count(*) FROM item_visitors WHERE item_id = 1`:    0,
		`SELECT count(*) FROM item_events WHERE item_id = 2`:      1,
		`SELECT count(*) FROM secret_events`:                      0,
	} {
		if got := count(t, db, q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
}
