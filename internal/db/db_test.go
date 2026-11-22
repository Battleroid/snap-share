package db

import (
	"context"
	"database/sql"
	"path/filepath"
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
