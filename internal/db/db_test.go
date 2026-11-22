package db

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestMigrateIdempotent(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	first, err := d.Migrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("expected migrations to apply")
	}
	second, err := d.Migrate(ctx)
	if err != nil || len(second) != 0 {
		t.Fatalf("second run: %v %v", second, err)
	}
	var n int
	if err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM settings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
}

func TestParseMigrationsRejectsBadNames(t *testing.T) {
	bad := []fstest.MapFS{
		{"m/1_x.sql": {Data: []byte("")}},
		{"m/0100_a.sql": {Data: []byte("")}, "m/0100_b.sql": {Data: []byte("")}},
		{"m/abcd_x.sql": {Data: []byte("")}},
	}
	for i, fsys := range bad {
		if _, err := parseMigrations(fsys, "m"); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	ok := fstest.MapFS{"m/0200_b.sql": {Data: []byte("b")}, "m/0100_a.sql": {Data: []byte("a")}, "m/README": {}}
	ms, err := parseMigrations(ok, "m")
	if err != nil || len(ms) != 2 || ms[0].Version != 100 {
		t.Fatalf("got %v %v", ms, err)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	_, err := d.apply(ctx, []Migration{{Version: 1, Name: "0001_bad.sql", SQL: "CREATE TABLE a (x INTEGER); SELECT nope FROM missing;"}})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := d.R.ExecContext(ctx, "SELECT * FROM a"); err == nil {
		t.Fatal("table a should not exist after rollback")
	}
}

func TestReadPoolIsReadOnly(t *testing.T) {
	d := openTest(t)
	if _, err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.R.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('x', '1', 0)`); err == nil {
		t.Fatal("read pool accepted a write")
	}
}

func TestConcurrentWritesSerialize(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	if _, err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('n', '0', 0)`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := d.Tx(ctx, func(tx *sql.Tx) error {
				var v int
				if err := tx.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM settings WHERE key='n'`).Scan(&v); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key='n'`, v+1)
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var v int
	if err := d.R.QueryRow(`SELECT CAST(value AS INTEGER) FROM settings WHERE key='n'`).Scan(&v); err != nil || v != 50 {
		t.Fatalf("lost updates: %d %v", v, err)
	}
}

// A deleted row must not survive in snap.db (secure_delete) or in old WAL
// frames once TruncateWAL has run.
func TestTruncateWALDropsDeletedRows(t *testing.T) {
	file := filepath.Join(t.TempDir(), "snap.db")
	d, err := Open(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()
	if _, err := d.W.ExecContext(ctx, `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL) STRICT`); err != nil {
		t.Fatal(err)
	}
	marker := "WALMARKER-" + strings.Repeat("q", 32)
	for i := range 20 {
		if _, err := d.W.ExecContext(ctx, `INSERT INTO notes (body) VALUES (?)`, fmt.Sprintf("%s-%d", marker, i)); err != nil {
			t.Fatal(err)
		}
	}
	// What the auto-checkpoint does: the rows reach the main file and the
	// WAL keeps its frames.
	if _, err := d.W.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.ExecContext(ctx, `DELETE FROM notes`); err != nil {
		t.Fatal(err)
	}
	found := func() (inMain, inWAL bool, walSize int) {
		main, err := os.ReadFile(file) // #nosec G304 -- a test file
		if err != nil {
			t.Fatal(err)
		}
		wal, err := os.ReadFile(file + "-wal") // #nosec G304 -- a test file
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return bytes.Contains(main, []byte(marker)), bytes.Contains(wal, []byte(marker)), len(wal)
	}
	if _, inWAL, _ := found(); !inWAL {
		t.Fatal("setup: the deleted rows should still be in the WAL before truncating")
	}
	if err := d.TruncateWAL(ctx); err != nil {
		t.Fatal(err)
	}
	if inMain, inWAL, size := found(); inMain || inWAL || size != 0 {
		t.Fatalf("after TruncateWAL: in snap.db %t, in snap.db-wal %t, wal %d bytes", inMain, inWAL, size)
	}
	// The write connection's busy timeout is back to normal.
	var ms int
	if err := d.W.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&ms); err != nil || ms != 10000 {
		t.Fatalf("busy_timeout %d %v", ms, err)
	}
}

func TestJournalSizeLimit(t *testing.T) {
	d := openTest(t)
	var n int64
	if err := d.W.QueryRow(`PRAGMA journal_size_limit`).Scan(&n); err != nil || n != 4<<20 {
		t.Fatalf("journal_size_limit %d %v", n, err)
	}
}
