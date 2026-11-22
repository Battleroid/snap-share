package db

import (
	"context"
	"strings"
	"testing"
)

// The items rebuild (0320, foreign keys off) on a database that already has
// albums, tags, stats and notifications: rows, ids and references survive,
// foreign keys are on again and enforced against the new table, and ids
// are never reused, not even ones freed before the upgrade.
func TestItemsRebuildKeepsData(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	all, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []Migration
	for _, m := range all {
		if m.Version != 320 && m.Version != 330 && m.Version != 620 {
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
	exec(`INSERT INTO users (id, username, created_at, updated_at) VALUES (1, 'ann', 1, 1)`)
	for _, it := range []struct {
		id         int64
		kind, slug string
	}{{1, "file", "one"}, {2, "paste", "two"}, {5, "album", "trip"}} {
		exec(`INSERT INTO items (id, owner_id, kind, slug, size, blob_key, render_key, created_at, updated_at, previewed_at)
			VALUES (?, 1, ?, ?, 3, 'b', 'r', 1, 1, 7)`, it.id, it.kind, it.slug)
		exec(`INSERT INTO items_fts (rowid, title, filename, body, target_url, tags) VALUES (?, ?, '', '', '', '')`, it.id, it.slug)
	}
	exec(`INSERT INTO album_items (album_id, item_id, position, added_at) VALUES (5, 1, 0, 1), (5, 2, 1, 1)`)
	exec(`INSERT INTO tags (id, owner_id, name, created_at) VALUES (1, 1, 'travel', 1)`)
	exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (1, 1)`)
	exec(`INSERT INTO item_events (item_id, at, kind) VALUES (1, 1, 'view'), (7, 1, 'view')`) // 7: deleted before the upgrade
	exec(`INSERT INTO item_stats_daily (item_id, day, views) VALUES (7, 0, 1)`)
	exec(`INSERT INTO notify_overrides (subject_type, subject_id, user_id, event, updated_at) VALUES ('item', '9', 1, 'every', 1)`)

	applied, err := d.apply(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(applied, ",") != "0320_items_rebuild.sql,0330_items_gist.sql,0620_stats_orphans.sql" {
		t.Fatalf("applied %v", applied)
	}
	count := func(q string, args ...any) (n int64) {
		t.Helper()
		if err := d.R.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	var schema string
	if err := d.R.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'items'`).Scan(&schema); err != nil || !strings.Contains(schema, "AUTOINCREMENT") {
		t.Fatalf("items schema: %v %s", err, schema)
	}
	if n := count(`SELECT count(*) FROM items WHERE id IN (1, 2, 5) AND previewed_at = 7 AND render_key = 'r' AND length(nonce) = 32`); n != 3 {
		t.Fatalf("items kept: %d", n)
	}
	if n := count(`SELECT count(DISTINCT nonce) FROM items`); n != 3 {
		t.Errorf("nonces not distinct: %d", n)
	}
	if n := count(`SELECT count(*) FROM album_items`) + count(`SELECT count(*) FROM item_tags`); n != 3 {
		t.Errorf("album and tag rows: %d, want 3", n)
	}
	if n := count(`SELECT count(*) FROM item_events WHERE item_id = 7`) + count(`SELECT count(*) FROM item_stats_daily`); n != 0 {
		t.Errorf("orphaned stats rows: %d", n)
	}
	if n := count(`SELECT count(*) FROM item_events WHERE item_id = 1`); n != 1 {
		t.Errorf("live item's events: %d", n)
	}
	var fk int64
	if err := d.W.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys after the migration: %d %v", fk, err)
	}
	// Ids continue above every id seen, live or not.
	res, err := d.W.ExecContext(ctx, `INSERT INTO items (owner_id, kind, slug, created_at, updated_at) VALUES (1, 'link', 'new', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 10 {
		t.Errorf("next id %d, want 10", id)
	}
	exec(`DELETE FROM items WHERE id = 10`)
	res, err = d.W.ExecContext(ctx, `INSERT INTO items (owner_id, kind, slug, created_at, updated_at) VALUES (1, 'link', 'newer', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 11 {
		t.Errorf("id after deleting the newest: %d, want 11", id)
	}
	// References point at the new table: deleting an item cascades, and a
	// member must exist.
	exec(`DELETE FROM items WHERE id = 1`)
	if n := count(`SELECT count(*) FROM album_items WHERE item_id = 1`) + count(`SELECT count(*) FROM item_tags`); n != 0 {
		t.Errorf("cascade after the rebuild: %d rows left", n)
	}
	if _, err := d.W.ExecContext(ctx, `INSERT INTO album_items (album_id, item_id, position, added_at) VALUES (5, 99, 2, 1)`); err == nil {
		t.Error("album_items accepted a missing item")
	}
}

// The gist rebuild (0330) on a database that is otherwise current: every
// column, reference and index survives, the id sequence keeps its
// high-water mark, kind 'gist' is accepted and gist files go with their item.
func TestItemsGistRebuild(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	all, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []Migration
	for _, m := range all {
		if m.Version != 330 {
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
	count := func(q string, args ...any) (n int64) {
		t.Helper()
		if err := d.R.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	exec(`INSERT INTO users (id, username, created_at, updated_at) VALUES (1, 'ann', 1, 1)`)
	for _, it := range []struct {
		id         int64
		kind, slug string
	}{{1, "file", "one"}, {2, "paste", "two"}, {5, "album", "trip"}, {8, "link", "gone"}} {
		exec(`INSERT INTO items (id, owner_id, kind, slug, size, blob_key, render_key, render_size, created_at, updated_at,
			previewed_at, nonce, created_by_key, expires_at) VALUES (?, 1, ?, ?, 3, 'b', 'r', 9, 1, 1, 7, ?, 4, 99)`,
			it.id, it.kind, it.slug, "nonce-"+it.slug)
		exec(`INSERT INTO items_fts (rowid, title, filename, body, target_url, tags) VALUES (?, ?, '', '', '', '')`, it.id, it.slug)
	}
	exec(`DELETE FROM items WHERE id = 8`) // the newest: the sequence must stay at 8
	exec(`INSERT INTO album_items (album_id, item_id, position, added_at) VALUES (5, 1, 0, 1), (5, 2, 1, 1)`)
	exec(`INSERT INTO tags (id, owner_id, name, created_at) VALUES (1, 1, 'travel', 1)`)
	exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (1, 1)`)

	applied, err := d.apply(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(applied, ",") != "0330_items_gist.sql" {
		t.Fatalf("applied %v", applied)
	}
	if n := count(`SELECT count(*) FROM items WHERE id IN (1, 2, 5) AND previewed_at = 7 AND render_key = 'r' AND render_size = 9
		AND nonce = 'nonce-' || slug AND created_by_key = 4 AND expires_at = 99`); n != 3 {
		t.Fatalf("items kept: %d", n)
	}
	if n := count(`SELECT count(*) FROM items_fts WHERE items_fts MATCH '"two"'`); n != 1 {
		t.Errorf("search rows: %d", n)
	}
	if n := count(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name IN ('items_owner_created', 'items_expires')`); n != 2 {
		t.Errorf("indexes: %d", n)
	}
	var schema string
	if err := d.R.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'items'`).Scan(&schema); err != nil ||
		!strings.Contains(schema, "AUTOINCREMENT") || !strings.Contains(schema, "'gist'") {
		t.Fatalf("items schema: %v %s", err, schema)
	}
	if n := count(`SELECT count(*) FROM sqlite_sequence WHERE name = 'items_new'`); n != 0 {
		t.Error("items_new left in sqlite_sequence")
	}
	var fk int64
	if err := d.W.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys after the migration: %d %v", fk, err)
	}
	res, err := d.W.ExecContext(ctx, `INSERT INTO items (owner_id, kind, slug, created_at, updated_at) VALUES (1, 'gist', 'g', 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := res.LastInsertId(); id != 9 {
		t.Errorf("next id %d, want 9", id)
	}
	if _, err := d.W.ExecContext(ctx, `INSERT INTO items (owner_id, kind, slug, created_at, updated_at) VALUES (1, 'bogus', 'h', 1, 1)`); err == nil {
		t.Error("unknown kind accepted")
	}
	exec(`INSERT INTO gist_files (item_id, position, filename, mime, size, sha256, blob_key) VALUES (9, 0, 'a.go', 'text/plain', 1, 's', 'k0'), (9, 1, 'b.md', 'text/markdown', 1, 's', 'k1')`)
	if _, err := d.W.ExecContext(ctx, `INSERT INTO gist_files (item_id, position, filename, mime, size, sha256, blob_key) VALUES (9, 2, 'A.GO', 'text/plain', 1, 's', 'k2')`); err == nil {
		t.Error("duplicate file name accepted")
	}
	if _, err := d.W.ExecContext(ctx, `INSERT INTO gist_files (item_id, position, filename, mime, size, sha256, blob_key) VALUES (99, 0, 'a', 'text/plain', 1, 's', 'k')`); err == nil {
		t.Error("gist_files accepted a missing item")
	}
	exec(`DELETE FROM items WHERE id = 9`)
	if n := count(`SELECT count(*) FROM gist_files`); n != 0 {
		t.Errorf("gist files outlive their item: %d", n)
	}
	exec(`DELETE FROM items WHERE id = 1`)
	if n := count(`SELECT count(*) FROM album_items WHERE item_id = 1`) + count(`SELECT count(*) FROM item_tags`); n != 0 {
		t.Errorf("cascade after the rebuild: %d rows left", n)
	}
	if _, err := d.W.ExecContext(ctx, `INSERT INTO album_items (album_id, item_id, position, added_at) VALUES (5, 99, 2, 1)`); err == nil {
		t.Error("album_items accepted a missing item")
	}
}

// A foreign-keys-off migration that leaves a dangling reference is rolled
// back.
func TestForeignKeysOffMigrationChecked(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	ms := []Migration{
		{Version: 1, Name: "0001_a.sql", SQL: `CREATE TABLE p (id INTEGER PRIMARY KEY) STRICT;
			CREATE TABLE c (pid INTEGER NOT NULL REFERENCES p (id)) STRICT;`},
		{Version: 2, Name: "0002_b.sql", SQL: ForeignKeysOff + "\nINSERT INTO c (pid) VALUES (42);"},
	}
	if _, err := d.apply(ctx, ms); err == nil || !strings.Contains(err.Error(), "foreign key check") {
		t.Fatalf("apply: %v", err)
	}
	var n int64
	if err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM c`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after the failed migration: %d %v", n, err)
	}
	var fk int64
	if err := d.W.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys after a failed migration: %d %v", fk, err)
	}
}
