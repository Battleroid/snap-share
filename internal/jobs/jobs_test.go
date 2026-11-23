package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func jobRow(t *testing.T, d *db.DB, id int64) (state string, attempts int, runAfter int64, lastErr string) {
	t.Helper()
	var le *string
	if err := d.R.QueryRow(`SELECT state, attempts, run_after, last_error FROM jobs WHERE id = ?`, id).Scan(&state, &attempts, &runAfter, &le); err != nil {
		t.Fatal(err)
	}
	if le != nil {
		lastErr = *le
	}
	return
}

func TestDeferDoesNotSpendAttempts(t *testing.T) {
	q, d := newQueue(t)
	now := time.Now()
	q.now = func() time.Time { return now }
	ctx := context.Background()
	id, err := q.Enqueue(ctx, "x", nil, MaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	k := &kind{wake: make(chan struct{}, 1), handler: func(context.Context, json.RawMessage) error {
		calls++
		if calls <= 3 {
			// Wrapped, to prove errors.As finds it. With MaxAttempts(1) a counted failure would be final.
			return fmt.Errorf("probe: %w", Defer(errors.New("worker down"), time.Minute))
		}
		return nil
	}}
	for i := 1; i <= 3; i++ {
		if ran, err := q.runOne(ctx, "x", k); !ran || err != nil {
			t.Fatalf("run %d: %v %v", i, ran, err)
		}
		state, attempts, runAfter, lastErr := jobRow(t, d, id)
		if state != "queued" || attempts != 0 {
			t.Fatalf("run %d: state=%s attempts=%d", i, state, attempts)
		}
		if runAfter != db.Millis(now.Add(time.Minute)) {
			t.Fatalf("run %d: run_after=%d", i, runAfter)
		}
		if lastErr != "probe: worker down" {
			t.Fatalf("last_error=%q", lastErr)
		}
		if ran, _ := q.runOne(ctx, "x", k); ran {
			t.Fatal("deferred job ran early")
		}
		now = now.Add(time.Minute)
	}
	if ran, err := q.runOne(ctx, "x", k); !ran || err != nil {
		t.Fatalf("final run: %v %v", ran, err)
	}
	if state, attempts, _, _ := jobRow(t, d, id); state != "done" || attempts != 1 {
		t.Fatalf("state=%s attempts=%d", state, attempts)
	}
}

func TestDeferNilAndNegative(t *testing.T) {
	if got := Defer(nil, time.Second).Error(); got != "deferred for 1s" {
		t.Fatalf("got %q", got)
	}
	q, d := newQueue(t)
	now := time.Now()
	q.now = func() time.Time { return now }
	ctx := context.Background()
	id, _ := q.Enqueue(ctx, "x", nil)
	k := &kind{wake: make(chan struct{}, 1), handler: func(context.Context, json.RawMessage) error { return Defer(nil, -time.Hour) }}
	if ran, err := q.runOne(ctx, "x", k); !ran || err != nil {
		t.Fatal(ran, err)
	}
	state, attempts, runAfter, _ := jobRow(t, d, id)
	if state != "queued" || attempts != 0 || runAfter != db.Millis(now) {
		t.Fatalf("state=%s attempts=%d run_after=%d", state, attempts, runAfter)
	}
}

func TestPlainErrorStillCounts(t *testing.T) {
	q, d := newQueue(t)
	ctx := context.Background()
	id, _ := q.Enqueue(ctx, "x", nil, MaxAttempts(1))
	k := &kind{wake: make(chan struct{}, 1), handler: func(context.Context, json.RawMessage) error { return errors.New("bad input") }}
	if ran, err := q.runOne(ctx, "x", k); !ran || err != nil {
		t.Fatal(ran, err)
	}
	if state, attempts, _, _ := jobRow(t, d, id); state != "failed" || attempts != 1 {
		t.Fatalf("state=%s attempts=%d", state, attempts)
	}
}

func TestDeferWhileShuttingDownRequeuesNow(t *testing.T) {
	q, d := newQueue(t)
	now := time.Now()
	q.now = func() time.Time { return now }
	bg := context.Background()
	id, _ := q.Enqueue(bg, "x", nil)
	ctx, cancel := context.WithCancel(bg)
	k := &kind{wake: make(chan struct{}, 1), handler: func(context.Context, json.RawMessage) error {
		cancel()
		return Defer(errors.New("worker down"), time.Hour)
	}}
	if ran, err := q.runOne(ctx, "x", k); !ran || err != nil {
		t.Fatal(ran, err)
	}
	// Shutdown wins: requeued without the long delay, attempt not counted.
	state, attempts, runAfter, _ := jobRow(t, d, id)
	if state != "queued" || attempts != 0 || runAfter > db.Millis(now) {
		t.Fatalf("state=%s attempts=%d run_after=%d", state, attempts, runAfter)
	}
}

func TestDeferRunsAgainViaRun(t *testing.T) {
	q, d := newQueue(t)
	var calls atomic.Int32
	q.Handle("x", 1, time.Second, func(context.Context, json.RawMessage) error {
		if calls.Add(1) == 1 {
			return Defer(errors.New("starting"), 100*time.Millisecond)
		}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)
	id, _ := q.Enqueue(ctx, "x", nil, MaxAttempts(1))
	waitState(t, d, id, "done")
	if calls.Load() != 2 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestIsDeferred(t *testing.T) {
	d := Defer(errors.New("down"), time.Minute)
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{d, true},
		{fmt.Errorf("teaser: %w", d), true},
		{errors.Join(errors.New("poster: crashed"), d), true},
		{Permanent(d), true},
		{errors.New("plain"), false},
		{Permanent(errors.New("bad input")), false},
		{nil, false},
	} {
		if got := IsDeferred(tc.err); got != tc.want {
			t.Errorf("IsDeferred(%v) = %v", tc.err, got)
		}
	}
}

// A handler learns its job's id and attempt; a deferral does not move the
// attempt on, a counted failure does.
func TestCurrentRun(t *testing.T) {
	q, _ := newQueue(t)
	now := time.Now()
	q.now = func() time.Time { return now }
	ctx := context.Background()
	if _, ok := Current(ctx); ok {
		t.Fatal("Current outside a job")
	}
	id, err := q.Enqueue(ctx, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen []Run
	results := []error{Defer(nil, 0), errors.New("fail"), nil}
	k := &kind{handler: func(ctx context.Context, _ json.RawMessage) error {
		r, ok := Current(ctx)
		if !ok {
			t.Fatal("no run in the handler's context")
		}
		seen = append(seen, r)
		err := results[0]
		results = results[1:]
		return err
	}, timeout: time.Second, wake: make(chan struct{}, 1)}
	for range 3 {
		if ran, err := q.runOne(ctx, "x", k); !ran || err != nil {
			t.Fatalf("runOne: %v %v", ran, err)
		}
		now = now.Add(time.Hour) // past any backoff
	}
	want := []Run{{id, "x", 1}, {id, "x", 1}, {id, "x", 2}}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("runs %v, want %v", seen, want)
	}
}
