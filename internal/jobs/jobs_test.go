package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

func newQueue(t *testing.T) (*Queue, *db.DB) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	q := New(d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.poll = 20 * time.Millisecond
	return q, d
}

func waitState(t *testing.T, d *db.DB, id int64, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var s string
		if err := d.R.QueryRow(`SELECT state FROM jobs WHERE id = ?`, id).Scan(&s); err == nil && s == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %d never reached %s", id, want)
}

func TestRunSuccessRetryPermanent(t *testing.T) {
	q, d := newQueue(t)
	var calls, flaky atomic.Int32
	q.Handle("ok", 2, time.Second, func(_ context.Context, p json.RawMessage) error {
		var v struct{ N int }
		if err := json.Unmarshal(p, &v); err != nil || v.N != 7 {
			return Permanent(errors.New("bad payload"))
		}
		calls.Add(1)
		return nil
	})
	q.Handle("flaky", 1, time.Second, func(context.Context, json.RawMessage) error {
		flaky.Add(1)
		return errors.New("try again")
	})
	q.Handle("perm", 1, time.Second, func(context.Context, json.RawMessage) error {
		return Permanent(errors.New("nope"))
	})
	q.Handle("boom", 1, time.Second, func(context.Context, json.RawMessage) error { panic("x") })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)

	okID, _ := q.Enqueue(ctx, "ok", map[string]int{"N": 7})
	permID, _ := q.Enqueue(ctx, "perm", nil)
	boomID, _ := q.Enqueue(ctx, "boom", nil)
	flakyID, _ := q.Enqueue(ctx, "flaky", nil, MaxAttempts(1))

	waitState(t, d, okID, "done")
	waitState(t, d, permID, "failed")
	waitState(t, d, boomID, "failed")
	waitState(t, d, flakyID, "failed")
	if calls.Load() != 1 || flaky.Load() != 1 {
		t.Fatalf("calls=%d flaky=%d", calls.Load(), flaky.Load())
	}
}

func TestRetryBackoffAndStale(t *testing.T) {
	q, d := newQueue(t)
	now := time.Now()
	q.now = func() time.Time { return now }
	ctx := context.Background()
	id, err := q.Enqueue(ctx, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	k := &kind{handler: func(context.Context, json.RawMessage) error { return errors.New("fail") }, wake: make(chan struct{}, 1)}
	if ran, err := q.runOne(ctx, "x", k); !ran || err != nil {
		t.Fatalf("runOne: %v %v", ran, err)
	}
	var state string
	var runAfter int64
	if err := d.R.QueryRow(`SELECT state, run_after FROM jobs WHERE id = ?`, id).Scan(&state, &runAfter); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || runAfter <= db.Millis(now) {
		t.Fatalf("expected backoff, got %s %d", state, runAfter)
	}
	if ran, _ := q.runOne(ctx, "x", k); ran {
		t.Fatal("job ran before its backoff elapsed")
	}

	// A running job whose lease expired is requeued.
	if _, err := d.W.Exec(`UPDATE jobs SET state = 'running', locked_until = ? WHERE id = ?`, db.Millis(now.Add(-time.Minute)), id); err != nil {
		t.Fatal(err)
	}
	if err := q.requeueStale(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.R.QueryRow(`SELECT state FROM jobs WHERE id = ?`, id).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("stale not requeued: %s %v", state, err)
	}
}
