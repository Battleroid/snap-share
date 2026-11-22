package stats

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestDB opens a temp-file SQLite database in WAL mode with the stats
// migration applied, as the server's migration runner would.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snap.db")
	db, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migration, err := os.ReadFile("../db/migrations/0600_stats.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigration(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	for _, table := range []string{"item_events", "item_stats_daily", "item_visitors", "secret_events"} {
		var strict int
		err := db.QueryRowContext(ctx, `SELECT strict FROM pragma_table_list WHERE name = ?`, table).Scan(&strict)
		if err != nil || strict != 1 {
			t.Errorf("%s: strict = %d, %v", table, strict, err)
		}
	}
	// STRICT and CHECK constraints reject bad rows.
	bad := []string{
		`INSERT INTO item_events (item_id, at, kind) VALUES (1, 0, 'revealed')`,
		`INSERT INTO secret_events (secret_id, at, kind) VALUES ('s', 0, 'view')`,
		`INSERT INTO item_events (item_id, at, kind) VALUES ('x', 0, 'view')`,
		`INSERT INTO item_events (item_id, at, kind) VALUES (1, 'yesterday', 'view')`,
	}
	for _, q := range bad {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("accepted: %s", q)
		}
	}
	// Applying it again on the same database is harmless.
	migration, err := os.ReadFile("../db/migrations/0600_stats.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Errorf("re-apply: %v", err)
	}
}
