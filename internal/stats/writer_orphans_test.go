package stats

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// Events of items that no longer exist at the flush are dropped, counted
// as orphaned, and leave no rows (raw, daily or visitors) behind.
func TestWriterDropsDeletedItems(t *testing.T) {
	db := openTestDB(t)
	live := map[int64]bool{7: true}
	asked := 0
	w := quietWriter(t, db, WriterOptions{ItemExists: func(_ context.Context, _ *sql.Tx, id int64) (bool, error) {
		asked++
		return live[id], nil
	}})
	at := day1.Add(9 * time.Hour)
	for _, e := range []Event{
		itemEvent("7", at, KindView, "v1"),
		itemEvent("8", at, KindView, "v1"),
		itemEvent("8", at, KindDownload, "v2"),
		{Subject: "s1", SubjectType: SubjectSecret, At: at, Kind: KindRevealed},
	} {
		w.Record(context.Background(), e)
	}
	flush(t, w)
	if n := count(t, db, `SELECT count(*) FROM item_events WHERE item_id = 8`) +
		count(t, db, `SELECT count(*) FROM item_stats_daily WHERE item_id = 8`) +
		count(t, db, `SELECT count(*) FROM item_visitors WHERE item_id = 8`); n != 0 {
		t.Fatalf("rows for a deleted item: %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM item_events WHERE item_id = 7`); n != 1 {
		t.Fatalf("live item's events: %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM secret_events`); n != 1 {
		t.Fatalf("secret events: %d", n)
	}
	if st := w.Stats(); st.Written != 2 || st.Orphaned != 2 {
		t.Fatalf("stats %+v, want 2 written and 2 orphaned", st)
	}
	if asked != 2 {
		t.Errorf("ItemExists asked %d times, want once per item", asked)
	}
}
