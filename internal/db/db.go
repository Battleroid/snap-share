// Package db opens the SQLite database and applies migrations.
//
// SQLite allows one writer at a time, so writes go through a single-connection
// pool (W) whose transactions start with BEGIN IMMEDIATE; reads use a separate
// read-only pool (R). Never hold a transaction or open rows across client I/O.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB holds the write and read pools.
type DB struct {
	W *sql.DB // single connection; all writes
	R *sql.DB // read-only pool
}

// Open opens (creating if needed) the database file at path.
func Open(ctx context.Context, file string) (*DB, error) {
	common := []string{
		"busy_timeout(10000)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
		"secure_delete(1)",
	}
	// journal_size_limit: after a checkpoint resets the WAL, it is cut back
	// to 4 MiB instead of keeping its largest size (and the old frames, which
	// hold deleted rows, in the tail).
	w, err := open(ctx, file, append([]string{"journal_mode(WAL)", "journal_size_limit(4194304)"}, common...), true)
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)

	r, err := open(ctx, file, append(common, "query_only(1)"), false)
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	r.SetMaxIdleConns(8)
	return &DB{W: w, R: r}, nil
}

func open(ctx context.Context, file string, pragmas []string, immediate bool) (*sql.DB, error) {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	if immediate {
		q.Set("_txlock", "immediate")
	}
	dsn := "file:" + file + "?" + q.Encode()
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	if err := d.PingContext(ctx); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	return d, nil
}

// Close closes both pools.
func (d *DB) Close() error { return errors.Join(d.R.Close(), d.W.Close()) }

// Tx runs fn in a write transaction (BEGIN IMMEDIATE). fn must not do client I/O.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// ErrCheckpointBusy means a reader or writer kept TruncateWAL from copying
// and truncating the whole WAL; old frames may remain until the next try.
var ErrCheckpointBusy = errors.New("db: wal checkpoint incomplete: database busy")

// TruncateWAL copies the WAL into the database file and truncates the WAL
// to zero bytes (PRAGMA wal_checkpoint(TRUNCATE)), so rows deleted with
// secure_delete no longer survive in old WAL frames. It waits at most about
// a second for readers, holding the single write connection meanwhile.
// Callers treat it as best effort.
func (d *DB) TruncateWAL(ctx context.Context) error {
	c, err := d.W.Conn(ctx)
	if err != nil {
		return fmt.Errorf("db: checkpoint: %w", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.ExecContext(ctx, "PRAGMA busy_timeout = 1000"); err != nil {
		return fmt.Errorf("db: checkpoint: %w", err)
	}
	defer func() { _, _ = c.ExecContext(context.WithoutCancel(ctx), "PRAGMA busy_timeout = 10000") }()
	var busy, logFrames, done int64
	if err := c.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &done); err != nil {
		return fmt.Errorf("db: checkpoint: %w", err)
	}
	if busy != 0 {
		return ErrCheckpointBusy
	}
	return nil
}

// Backup writes a consistent copy of the database to dst (which must not exist).
func (d *DB) Backup(ctx context.Context, dst string) error {
	_, err := d.W.ExecContext(ctx, "VACUUM INTO ?", dst)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// Migration is one embedded schema file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations returns the embedded migrations in version order.
func Migrations() ([]Migration, error) {
	return parseMigrations(migrationFS, "migrations")
}

func parseMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, _, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || len(num) != 4 {
			return nil, fmt.Errorf("migration %s: name must be NNNN_name.sql", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrations %s and %s share version %d", prev, name, v)
		}
		seen[v] = name
		b, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: name, SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate applies every embedded migration not yet recorded, each in its own transaction.
func (d *DB) Migrate(ctx context.Context) (applied []string, err error) {
	ms, err := Migrations()
	if err != nil {
		return nil, err
	}
	return d.apply(ctx, ms)
}

func (d *DB) apply(ctx context.Context, ms []Migration) ([]string, error) {
	if _, err := d.W.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL) STRICT`); err != nil {
		return nil, fmt.Errorf("schema_migrations: %w", err)
	}
	done, err := d.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	var applied []string
	for _, m := range ms {
		if done[m.Version] {
			continue
		}
		run := func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				m.Version, m.Name, Millis(time.Now()))
			return err
		}
		var err error
		if strings.HasPrefix(m.SQL, ForeignKeysOff) {
			err = d.txForeignKeysOff(ctx, run)
		} else {
			err = d.Tx(ctx, run)
		}
		if err != nil {
			return applied, fmt.Errorf("migration %s: %w", m.Name, err)
		}
		applied = append(applied, m.Name)
	}
	return applied, nil
}

// ForeignKeysOff, as a migration's first line, runs it with foreign keys
// off, as SQLite's table rebuild procedure needs (https://sqlite.org/lang_altertable.html#otheralter):
// the pragma is set outside the transaction, and PRAGMA foreign_key_check
// must come back empty before the commit.
const ForeignKeysOff = "-- snap: foreign_keys=off"

func (d *DB) txForeignKeysOff(ctx context.Context, fn func(*sql.Tx) error) error {
	c, err := d.W.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if _, err := c.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer func() { _, _ = c.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON") }()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	var table string
	var rowid sql.NullInt64
	var parent string
	var fkid int64
	err = tx.QueryRowContext(ctx, "PRAGMA foreign_key_check").Scan(&table, &rowid, &parent, &fkid)
	if err == nil {
		_ = tx.Rollback()
		return fmt.Errorf("foreign key check: %s row %d references a missing %s row", table, rowid.Int64, parent)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return fmt.Errorf("foreign key check: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func (d *DB) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := d.W.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	done := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		done[v] = true
	}
	return done, rows.Err()
}

// Millis converts t to the schema's timestamp format (unix milliseconds UTC).
func Millis(t time.Time) int64 { return t.UnixMilli() }

// Time converts a schema timestamp back to time.Time (UTC).
func Time(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// NullTime converts a nullable timestamp.
func NullTime(ms sql.NullInt64) *time.Time {
	if !ms.Valid {
		return nil
	}
	t := Time(ms.Int64)
	return &t
}
