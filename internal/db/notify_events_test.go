package db

import (
	"context"
	"strings"
	"testing"
)

// 0640 on a database that already has notification settings and inbox
// rows (and later migrations): the old item-open setting becomes the
// item-open rows for email and ntfy, inbox rows and ids survive, ids are not
// reused, and the inbox takes account rows but nothing else new.
func TestNotifyEventsMigration(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	all, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []Migration
	for _, m := range all {
		if m.Version != 640 {
			before = append(before, m)
		}
	}
	if _, err := d.apply(ctx, before); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.W.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id, username, created_at, updated_at) VALUES (1, 'ann', 1, 1), (2, 'bob', 1, 1), (3, 'cat', 1, 1), (4, 'dan', 1, 1)`)
	exec(`INSERT INTO notify_prefs (user_id, email_enabled, default_event, updated_at) VALUES (1, 1, 'none', 1), (2, 1, 'first', 1), (3, 0, 'every', 1)`)
	exec(`INSERT INTO inbox (id, user_id, kind, status, title, subject_type, subject_id, created_at) VALUES
  (1, 1, 'item_opened', 'info', 'a opened', 'item', '7', 1), (5, 2, 'secret_burned', 'down', 'b burned', 'secret', 'x', 2)`)
	exec(`DELETE FROM inbox WHERE id = 5`) // the high-water mark stays 5

	applied, err := d.apply(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(applied, ",") != "0640_notify_events.sql" {
		t.Fatalf("applied %v", applied)
	}
	rows, err := d.R.QueryContext(ctx, `SELECT user_id || ' ' || event || ' ' || channel || ' ' || enabled FROM notify_event_prefs ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"1 item.first_open email 0", "1 item.first_open ntfy 0", "1 item.open email 0", "1 item.open ntfy 0",
		"2 item.first_open email 1", "2 item.first_open ntfy 1", "2 item.open email 0", "2 item.open ntfy 0",
		"3 item.first_open email 1", "3 item.first_open ntfy 1", "3 item.open email 1", "3 item.open ntfy 1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("event rows:\n%s", strings.Join(got, "\n"))
	}

	var title string
	if err := d.R.QueryRowContext(ctx, `SELECT title FROM inbox WHERE id = 1 AND user_id = 1 AND subject_id = '7'`).Scan(&title); err != nil || title != "a opened" {
		t.Fatalf("inbox row: %q %v", title, err)
	}
	res, err := d.W.ExecContext(ctx, `INSERT INTO inbox (user_id, kind, status, title, subject_type, subject_id, created_at) VALUES (4, 'quota', 'warn', 'q', 'account', '4', 3)`)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 6 {
		t.Errorf("next inbox id %d, want 6", id)
	}
	if _, err := d.W.ExecContext(ctx, `INSERT INTO inbox (user_id, kind, status, title, subject_type, subject_id, created_at) VALUES (4, 'x', 'info', 'x', 'album', '1', 3)`); err == nil {
		t.Error("inbox accepted subject_type album")
	}
	if _, err := d.W.ExecContext(ctx, `INSERT INTO notify_event_prefs (user_id, event, channel, enabled) VALUES (4, 'item.open', 'sms', 1)`); err == nil {
		t.Error("notify_event_prefs accepted channel sms")
	}
	// Rows go with their user.
	exec(`DELETE FROM users WHERE id = 3`)
	var n int
	if err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM notify_event_prefs WHERE user_id = 3`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows of a deleted user: %d %v", n, err)
	}
	for _, idx := range []string{"inbox_user_created", "inbox_unread", "inbox_created", "inbox_subject"} {
		if err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n); err != nil || n != 1 {
			t.Errorf("index %s: %d %v", idx, n, err)
		}
	}
}
