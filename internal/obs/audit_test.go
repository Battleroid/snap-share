package obs_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/obs"
)

type lines struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lines) all(t *testing.T) []map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, s := range strings.Split(strings.TrimSpace(l.b.String()), "\n") {
		if s == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("not one JSON object per line: %q", s)
		}
		out = append(out, m)
	}
	return out
}

func openDB(t *testing.T) *db.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStreamEmitsOnlyCommittedEntries(t *testing.T) {
	ctx := context.Background()
	d := openDB(t)
	store := accounts.NewStore(d)
	// An entry from before the stream started is not emitted again.
	if err := store.Audit(ctx, accounts.AuditEntry{Action: "old.entry"}); err != nil {
		t.Fatal(err)
	}
	out := &lines{}
	s, err := obs.NewStream(ctx, d, obs.JSONLines{W: out}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	// Rolled back after the audit insert: nothing may be emitted.
	boom := errors.New("boom")
	err = d.Tx(ctx, func(tx *sql.Tx) error {
		if err := accounts.AuditTx(ctx, tx, accounts.AuditEntry{Action: "rolled.back"}); err != nil {
			return err
		}
		// Still open: the entry is not visible to the stream yet.
		if n, err := s.Poll(ctx); err != nil || n != 0 {
			t.Errorf("poll inside the transaction: %d %v", n, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if n, err := s.Poll(ctx); err != nil || n != 0 {
		t.Fatalf("poll after rollback: %d %v", n, err)
	}

	for i := range 3 {
		e := accounts.AuditEntry{ActorUserID: int64(i + 1), IP: "192.0.2.1", Action: "user.status", Target: "user:7",
			Details: map[string]any{"status": "disabled"}}
		if err := store.Audit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.Poll(ctx); err != nil || n != 3 {
		t.Fatalf("poll: %d %v", n, err)
	}
	if n, _ := s.Poll(ctx); n != 0 {
		t.Fatalf("second poll re-emitted %d", n)
	}
	got := out.all(t)
	var rows int
	if err := d.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action != 'old.entry'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if len(got) != rows {
		t.Fatalf("%d lines for %d committed rows", len(got), rows)
	}
	for _, m := range got {
		if m["action"] == "rolled.back" || m["action"] == "old.entry" {
			t.Errorf("emitted %v", m)
		}
		if m["v"] != float64(obs.AuditVersion) || m["target"] != "user:7" || m["ip"] != "192.0.2.1" || m["actor_key"] != nil {
			t.Errorf("line %v", m)
		}
	}
}

func TestStreamRetriesAfterSinkFailure(t *testing.T) {
	ctx := context.Background()
	d := openDB(t)
	store := accounts.NewStore(d)
	sink := &flaky{fail: true}
	s, err := obs.NewStream(ctx, d, sink, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Audit(ctx, accounts.AuditEntry{Action: "a.one"})
	_ = store.Audit(ctx, accounts.AuditEntry{Action: "a.two"})
	if _, err := s.Poll(ctx); err == nil {
		t.Fatal("no error from a failing sink")
	}
	sink.fail = false
	if n, err := s.Poll(ctx); err != nil || n != 2 {
		t.Fatalf("after recovery: %d %v", n, err)
	}
	if len(sink.got) != 2 || sink.got[0].Action != "a.one" {
		t.Fatalf("got %+v", sink.got)
	}
}

type flaky struct {
	fail bool
	got  []obs.Line
}

func (f *flaky) WriteAudit(l obs.Line) error {
	if f.fail {
		return errors.New("disk full")
	}
	f.got = append(f.got, l)
	return nil
}

func ptr[T any](v T) *T { return &v }

func TestRedact(t *testing.T) {
	l := obs.Redact(obs.Line{
		Target: ptr("secret:AAAAAAAAAAAAAAAAAAAAAA"),
		Details: map[string]any{
			"token": "t0k3n", "upload_token": "x", "password": "hunter2", "Authorization": "Bearer abc",
			"to":     []any{"alice@example.com", "bob@example.org"},
			"url":    "https://ntfy.example/topic?auth=abc#frag",
			"nested": map[string]any{"secret_id": "s", "ok": "kept"},
			"status": "disabled", "password_set": true, "via": "cli",
			"secrets": float64(3), "items": float64(2), "secret_ids": []any{"SIDsidSIDsid"}, "credential": map[string]any{"inner": "CREDcred"},
		},
	})
	b, _ := json.Marshal(l)
	s := string(b)
	for _, leak := range []string{"AAAAAAAAAAAAAAAAAAAAAA", "t0k3n", "hunter2", "Bearer", "alice@", "bob@", "auth=abc", "frag", "SIDsidSIDsid", "CREDcred"} {
		if strings.Contains(s, leak) {
			t.Errorf("%q survived: %s", leak, s)
		}
	}
	for _, keep := range []string{`"secret:[redacted]"`, `"status":"disabled"`, `"password_set":true`, `"ok":"kept"`, `"via":"cli"`, `"secrets":3`, `"items":2`, `"https://ntfy.example/topic"`} {
		if !strings.Contains(s, keep) {
			t.Errorf("%s missing: %s", keep, s)
		}
	}
	if u := obs.Redact(obs.Line{Target: ptr("user:3")}); *u.Target != "user:3" {
		t.Errorf("user target changed: %q", *u.Target)
	}
}

// The line formats are a contract with log shippers: pin them.
func TestAuditLineGolden(t *testing.T) {
	l := obs.Line{V: 1, ID: 42, TS: "2026-11-23T10:00:00.123Z", Action: "user.role_change", ActorUser: ptr(int64(1)),
		IP: ptr("192.0.2.7"), Target: ptr("user:9"), Details: map[string]any{"from": "user", "to": "admin"}}
	var file bytes.Buffer
	if err := (obs.JSONLines{W: &file}).WriteAudit(l); err != nil {
		t.Fatal(err)
	}
	wantFile := `{"v":1,"id":42,"ts":"2026-11-23T10:00:00.123Z","action":"user.role_change","actor_user":1,"actor_key":null,"ip":"192.0.2.7","target":"user:9","details":{"from":"user","to":"admin"}}` + "\n"
	if file.String() != wantFile {
		t.Errorf("file line:\n got %s\nwant %s", file.String(), wantFile)
	}
	var std bytes.Buffer
	if err := (obs.LogRecords{H: obs.AuditHandler(&std)}).WriteAudit(l); err != nil {
		t.Fatal(err)
	}
	wantStd := `{"level":"INFO","msg":"audit","log":"audit","v":1,"id":42,"ts":"2026-11-23T10:00:00.123Z","action":"user.role_change","actor_user":1,"actor_key":null,"ip":"192.0.2.7","target":"user:9","details":{"from":"user","to":"admin"}}` + "\n"
	if std.String() != wantStd {
		t.Errorf("stdout line:\n got %s\nwant %s", std.String(), wantStd)
	}
}

// Run polls on its interval and drains once more when its context ends.
func TestStreamRunDrainsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := openDB(t)
	store := accounts.NewStore(d)
	out := &lines{}
	s, err := obs.NewStream(ctx, d, obs.JSONLines{W: out}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Run(ctx, time.Hour); close(done) }()
	if err := store.Audit(context.Background(), accounts.AuditEntry{Action: "late.entry"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if got := out.all(t); len(got) != 1 || got[0]["action"] != "late.entry" {
		t.Fatalf("after shutdown: %v", got)
	}
}
