package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var (
	day1 = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	day2 = day1.AddDate(0, 0, 1)
	day3 = day1.AddDate(0, 0, 2)
)

// quietWriter flushes only when told to.
func quietWriter(t *testing.T, db *sql.DB, opts WriterOptions) *Writer {
	t.Helper()
	opts.BatchSize = 1 << 20
	opts.FlushInterval = time.Hour
	w := NewWriter(db, opts)
	t.Cleanup(func() { _ = w.Close(context.Background()) })
	return w
}

func itemEvent(id string, at time.Time, kind Kind, visitor string) Event {
	return Event{Subject: id, SubjectType: SubjectItem, At: at, Kind: kind, VisitorKey: visitor,
		IP: "192.168.1.1", UA: "ua-" + visitor, UASummary: "Firefox · Linux"}
}

func flush(t *testing.T, w *Writer) {
	t.Helper()
	if err := w.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func daily(t *testing.T, db *sql.DB, item int64, day time.Time) dayCounts {
	t.Helper()
	var c dayCounts
	err := db.QueryRow(`SELECT views, uniques, new_visitors, previews, embeds, downloads, clicks, plays
FROM item_stats_daily WHERE item_id = ? AND day = ?`, item, day.UnixMilli()).
		Scan(&c.views, &c.uniques, &c.newVisitors, &c.previews, &c.embeds, &c.downloads, &c.clicks, &c.plays)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return c
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriterRollups(t *testing.T) {
	db := openTestDB(t)
	w := quietWriter(t, db, WriterOptions{})
	ctx := context.Background()
	at := day1.Add(9 * time.Hour)

	for _, e := range []Event{
		itemEvent("42", at, KindView, "v1"),
		itemEvent("42", at.Add(time.Minute), KindView, "v1"),
		itemEvent("42", at, KindView, "v2"),
		itemEvent("42", at, KindDownload, "v1"),
		itemEvent("42", at, KindPlay, "v3"),
		itemEvent("42", at, KindEmbed, "v9"),   // embeds don't make uniques
		itemEvent("42", at, KindPreview, "v8"), // nor do previews
		itemEvent("42", at, KindView, ""),      // no key: counted, not unique
		itemEvent("7", at, KindClick, "v1"),
		{Subject: "s1", SubjectType: SubjectSecret, At: at, Kind: KindLinkOpened, IP: "203.0.113.9"},
		{Subject: "s1", SubjectType: SubjectSecret, At: at, Kind: KindRevealed},
	} {
		w.Record(ctx, e)
	}
	flush(t, w)

	if got, want := daily(t, db, 42, day1), (dayCounts{views: 4, uniques: 3, newVisitors: 3, previews: 1, embeds: 1, downloads: 1, plays: 1}); got != want {
		t.Errorf("day1 = %+v, want %+v", got, want)
	}
	if got := daily(t, db, 7, day1); got != (dayCounts{clicks: 1, uniques: 1, newVisitors: 1}) {
		t.Errorf("item 7 = %+v", got)
	}
	if n := count(t, db, `SELECT count(*) FROM item_events`); n != 9 {
		t.Errorf("item_events = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM secret_events WHERE secret_id = 's1'`); n != 2 {
		t.Errorf("secret_events = %d", n)
	}

	// Later the same day: a returning visitor is not unique again; a new one is.
	w.Record(ctx, itemEvent("42", at.Add(5*time.Hour), KindView, "v1"))
	w.Record(ctx, itemEvent("42", at.Add(5*time.Hour), KindView, "v4"))
	flush(t, w)
	if got := daily(t, db, 42, day1); got.views != 6 || got.uniques != 4 || got.newVisitors != 4 {
		t.Errorf("day1 after second flush = %+v", got)
	}

	// Next day, out of order within the batch: v1 returns (unique, not
	// new), v5 is new.
	w.Record(ctx, itemEvent("42", day2.Add(3*time.Hour), KindView, "v5"))
	w.Record(ctx, itemEvent("42", day2.Add(1*time.Hour), KindView, "v1"))
	w.Record(ctx, itemEvent("42", day2.Add(2*time.Hour), KindPlay, "v1"))
	flush(t, w)
	if got := daily(t, db, 42, day2); got != (dayCounts{views: 2, plays: 1, uniques: 2, newVisitors: 1}) {
		t.Errorf("day2 = %+v", got)
	}

	tot, err := ItemTotals(ctx, db, 42, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Totals{Views: 8, Uniques: 5, Previews: 1, Embeds: 1, Downloads: 1, Plays: 2}); tot != want {
		t.Errorf("lifetime totals = %+v, want %+v", tot, want)
	}
	tot, err = ItemTotals(ctx, db, 42, day2, day3)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Totals{Views: 2, Uniques: 2, Plays: 1}); tot != want {
		t.Errorf("day2 totals = %+v, want %+v", tot, want)
	}
	tot, err = ItemTotals(ctx, db, 42, day1, day3)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Uniques != 5 || tot.Views != 8 {
		t.Errorf("two-day totals = %+v", tot)
	}
	if s := w.Stats(); s != (WriterStats{Written: 16}) {
		t.Errorf("Stats = %+v", s)
	}
}

func TestWriterOnOpened(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE opened (subject TEXT, at INTEGER) STRICT`); err != nil {
		t.Fatal(err)
	}
	fail := false
	w := quietWriter(t, db, WriterOptions{OnOpened: func(tx *sql.Tx, subject string, at time.Time) error {
		if fail {
			return errors.New("boom")
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO opened VALUES (?, ?)`, subject, at.UnixMilli())
		return err
	}})

	at := day1.Add(time.Hour)
	w.Record(ctx, itemEvent("42", at.Add(2*time.Second), KindDownload, "a"))
	w.Record(ctx, itemEvent("42", at, KindView, "a"))
	w.Record(ctx, itemEvent("42", at, KindPreview, "b"))
	w.Record(ctx, itemEvent("42", at, KindEmbed, "c"))
	w.Record(ctx, Event{Subject: "s1", SubjectType: SubjectSecret, At: at, Kind: KindRevealed})
	flush(t, w)

	rows, err := db.QueryContext(ctx, `SELECT subject, at FROM opened ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var s string
		var ms int64
		if err := rows.Scan(&s, &ms); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s@%d", s, ms-at.UnixMilli()))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := "[42@0 42@2000]"; fmt.Sprint(got) != want {
		t.Errorf("OnOpened calls = %v, want %s", got, want)
	}

	// A failing hook rolls the whole flush back.
	fail = true
	w.Record(ctx, itemEvent("42", at, KindView, "z"))
	w.Record(ctx, Event{Subject: "s2", SubjectType: SubjectSecret, At: at, Kind: KindBurned})
	if err := w.Flush(ctx); err == nil {
		t.Fatal("flush succeeded with a failing hook")
	}
	if n := count(t, db, `SELECT count(*) FROM item_events WHERE visitor_key = 'z'`); n != 0 {
		t.Error("event committed despite hook error")
	}
	if n := count(t, db, `SELECT count(*) FROM secret_events WHERE secret_id = 's2'`); n != 0 {
		t.Error("secret event committed despite hook error")
	}
	if s := w.Stats(); s.Failed != 2 {
		t.Errorf("Failed = %d, want 2", s.Failed)
	}
}

func TestWriterDropsAndInvalid(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w := quietWriter(t, db, WriterOptions{MaxBuffered: 3})
	for i := range 5 {
		w.Record(ctx, itemEvent("1", day1, KindView, fmt.Sprint(i)))
	}
	for _, e := range []Event{
		{Subject: "1", SubjectType: "album", Kind: KindView},
		{Subject: "abc", SubjectType: SubjectItem, Kind: KindView},
		{Subject: "0", SubjectType: SubjectItem, Kind: KindView},
		{Subject: "1", SubjectType: SubjectItem, Kind: KindRevealed},
		{Subject: "s", SubjectType: SubjectSecret, Kind: KindView},
		{Subject: "", SubjectType: SubjectSecret, Kind: KindRevealed},
		{Subject: "1", SubjectType: SubjectItem, Kind: "visit"},
	} {
		w.Record(ctx, e)
	}
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	w.Record(ctx, itemEvent("1", day1, KindView, "late"))
	if got, want := w.Stats(), (WriterStats{Written: 3, Dropped: 3, Invalid: 7}); got != want {
		t.Errorf("Stats = %+v, want %+v", got, want)
	}
	if err := w.Close(ctx); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestWriterCleansFields(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	clock := newFakeClock()
	w := quietWriter(t, db, WriterOptions{Now: clock.Now})
	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'a'
	}
	w.Record(ctx, Event{Subject: "1", SubjectType: SubjectItem, Kind: KindView, UA: string(long) + "\xff", IP: "\xff1.2.3.4"})
	flush(t, w)
	var ua, ip string
	var at int64
	if err := db.QueryRowContext(ctx, `SELECT user_agent, ip, at FROM item_events`).Scan(&ua, &ip, &at); err != nil {
		t.Fatal(err)
	}
	if len(ua) != maxUALen {
		t.Errorf("user_agent length = %d", len(ua))
	}
	if ip != "�1.2.3.4" {
		t.Errorf("ip = %q", ip)
	}
	if at != clock.Now().UnixMilli() {
		t.Errorf("at = %d, want the writer's clock", at)
	}
}

func TestWriterBackground(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// By count.
	w := NewWriter(db, WriterOptions{BatchSize: 2, FlushInterval: time.Hour})
	w.Record(ctx, itemEvent("1", day1, KindView, "a"))
	w.Record(ctx, itemEvent("1", day1, KindView, "b"))
	waitWritten(t, w, 2)
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// By interval.
	w = NewWriter(db, WriterOptions{BatchSize: 100, FlushInterval: 10 * time.Millisecond})
	w.Record(ctx, itemEvent("1", day1, KindView, "c"))
	waitWritten(t, w, 1)

	// Close flushes the rest.
	w.Record(ctx, itemEvent("1", day1, KindView, "d"))
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, db, `SELECT count(*) FROM item_events`); n != 4 {
		t.Errorf("item_events = %d, want 4", n)
	}
}

func waitWritten(t *testing.T, w *Writer, n uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for w.Stats().Written < n {
		if time.Now().After(deadline) {
			t.Fatalf("written = %d, want %d", w.Stats().Written, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWriterConcurrent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w := NewWriter(db, WriterOptions{BatchSize: 50, FlushInterval: 5 * time.Millisecond, MaxBuffered: 100_000})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 250 {
				w.Record(ctx, itemEvent("5", day1.Add(time.Duration(i)*time.Second), KindView, fmt.Sprintf("g%d-%d", g, i%10)))
			}
		})
	}
	wg.Wait()
	if err := w.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if s := w.Stats(); s.Written != 2000 || s.Dropped != 0 || s.Failed != 0 {
		t.Errorf("Stats = %+v", s)
	}
	if got := daily(t, db, 5, day1); got.views != 2000 || got.uniques != 80 || got.newVisitors != 80 {
		t.Errorf("daily = %+v, want 2000 views, 80 uniques", got)
	}
}

func TestDayOf(t *testing.T) {
	tests := map[int64]int64{
		0:                0,
		dayMillis - 1:    0,
		dayMillis:        dayMillis,
		-1:               -dayMillis,
		-dayMillis:       -dayMillis,
		day1.UnixMilli(): day1.UnixMilli(),
		day1.Add(23*time.Hour + 59*time.Minute).UnixMilli(): day1.UnixMilli(),
	}
	for in, want := range tests {
		if got := dayOf(in); got != want {
			t.Errorf("dayOf(%d) = %d, want %d", in, got, want)
		}
	}
}
