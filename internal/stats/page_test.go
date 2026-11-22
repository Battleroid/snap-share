package stats

import (
	"context"
	"testing"
	"time"
)

func TestItemEventsAndOpens(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	add := func(item int64, mins int, kind, ip string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO item_events (item_id, at, kind, ip) VALUES (?, ?, ?, ?)`,
			item, base.Add(time.Duration(mins)*time.Minute).UnixMilli(), kind, ip); err != nil {
			t.Fatal(err)
		}
	}
	add(1, 0, "preview", "a")
	add(1, 1, "view", "b")
	add(2, 2, "view", "other")
	add(1, 3, "embed", "c")
	add(1, 4, "download", "d")
	add(1, 5, "preview", "e")

	all, err := ItemEvents(ctx, db, 1, EventPage{})
	if err != nil || len(all) != 5 || all[0].IP != "e" || all[4].IP != "a" || all[0].Subject != "1" || all[0].SubjectType != SubjectItem {
		t.Fatalf("all %+v %v", all, err)
	}
	page, _ := ItemEvents(ctx, db, 1, EventPage{BeforeID: all[1].ID, Limit: 2})
	if len(page) != 2 || page[0].IP != "c" || page[1].IP != "b" {
		t.Fatalf("before %+v", page)
	}
	page, _ = ItemEvents(ctx, db, 1, EventPage{Offset: 4, Limit: 2})
	if len(page) != 1 || page[0].IP != "a" {
		t.Fatalf("offset %+v", page)
	}
	if n, err := CountItemEvents(ctx, db, 1); n != 5 || err != nil {
		t.Fatalf("count %d %v", n, err)
	}

	first, ok, err := FirstItemOpen(ctx, db, 1)
	if !ok || err != nil || first.IP != "b" || first.Kind != KindView {
		t.Fatalf("first %+v %v %v", first, ok, err)
	}
	last, ok, _ := LastItemOpen(ctx, db, 1, time.Time{})
	if !ok || last.IP != "d" {
		t.Fatalf("last %+v", last)
	}
	last, ok, _ = LastItemOpen(ctx, db, 1, base.Add(3*time.Minute))
	if !ok || last.IP != "b" {
		t.Fatalf("last before 10:03 %+v", last)
	}
	if _, ok, err := FirstItemOpen(ctx, db, 9); ok || err != nil {
		t.Fatalf("missing item: %v %v", ok, err)
	}
}
