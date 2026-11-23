// Package jobs runs background work: durable queued jobs stored in the jobs
// table, and in-process periodic tasks (sweepers).
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// Handler processes one job payload. Returning an error retries with backoff
// until max attempts; wrap with Permanent to fail immediately.
type Handler func(ctx context.Context, payload json.RawMessage) error

type permanent struct{ error }

// Permanent marks err as not worth retrying.
func Permanent(err error) error { return permanent{err} }

func (p permanent) Unwrap() error { return p.error }

type kind struct {
	handler     Handler
	concurrency int
	timeout     time.Duration
	wake        chan struct{}
}

type periodic struct {
	name     string
	interval time.Duration
	fn       func(context.Context) error
}

// Queue is the job runner.
type Queue struct {
	db    *db.DB
	log   *slog.Logger
	now   func() time.Time
	lease time.Duration
	poll  time.Duration

	mu       sync.Mutex
	kinds    map[string]*kind
	periodic []periodic
	running  bool
	obs      Observer
}

// Observer records job attempts and periodic task runs (internal/metrics
// implements it). result is ok, retry or failed.
type Observer interface {
	JobRun(kind, result string, d time.Duration)
	TaskRun(task string, ok bool, d time.Duration)
}

// Observe sets the observer. Call before Run.
func (q *Queue) Observe(o Observer) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.obs = o
}

// Kinds returns the registered job kinds, sorted.
func (q *Queue) Kinds() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]string, 0, len(q.kinds))
	for k := range q.kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// New creates a queue. Register handlers with Handle and tasks with Every, then call Run.
func New(d *db.DB, log *slog.Logger) *Queue {
	return &Queue{db: d, log: log, now: time.Now, lease: 10 * time.Minute, poll: 5 * time.Second, kinds: map[string]*kind{}}
}

// Handle registers a handler for kind with a concurrency limit and per-job timeout.
func (q *Queue) Handle(name string, concurrency int, timeout time.Duration, h Handler) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running {
		panic("jobs: Handle after Run")
	}
	if concurrency < 1 {
		concurrency = 1
	}
	q.kinds[name] = &kind{handler: h, concurrency: concurrency, timeout: timeout, wake: make(chan struct{}, 1)}
}

// Every runs fn at interval (first run after one interval) while Run is active.
func (q *Queue) Every(name string, interval time.Duration, fn func(context.Context) error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running {
		panic("jobs: Every after Run")
	}
	q.periodic = append(q.periodic, periodic{name, interval, fn})
}

// Option adjusts an enqueued job.
type Option func(*enqueueOpts)

type enqueueOpts struct {
	delay       time.Duration
	maxAttempts int
}

// Delay postpones the first attempt.
func Delay(d time.Duration) Option { return func(o *enqueueOpts) { o.delay = d } }

// MaxAttempts overrides the default of 3.
func MaxAttempts(n int) Option { return func(o *enqueueOpts) { o.maxAttempts = n } }

// Enqueue adds a job in its own transaction.
func (q *Queue) Enqueue(ctx context.Context, name string, payload any, opts ...Option) (int64, error) {
	var id int64
	err := q.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = q.EnqueueTx(ctx, tx, name, payload, opts...)
		return err
	})
	return id, err
}

// EnqueueTx adds a job inside the caller's transaction, so it exists only if the
// caller commits. Workers are woken after the commit by the poll interval or Wake.
func (q *Queue) EnqueueTx(ctx context.Context, tx *sql.Tx, name string, payload any, opts ...Option) (int64, error) {
	o := enqueueOpts{maxAttempts: 3}
	for _, f := range opts {
		f(&o)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("job %s payload: %w", name, err)
	}
	now := q.now()
	res, err := tx.ExecContext(ctx, `INSERT INTO jobs (kind, payload, max_attempts, run_after, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, name, string(b), o.maxAttempts, db.Millis(now.Add(o.delay)), db.Millis(now), db.Millis(now))
	if err != nil {
		return 0, fmt.Errorf("enqueue %s: %w", name, err)
	}
	q.Wake(name)
	return res.LastInsertId()
}

// Wake nudges the workers for kind to look for work now.
func (q *Queue) Wake(name string) {
	q.mu.Lock()
	k := q.kinds[name]
	q.mu.Unlock()
	if k == nil {
		return
	}
	select {
	case k.wake <- struct{}{}:
	default:
	}
}

// Run processes jobs and periodic tasks until ctx is cancelled.
func (q *Queue) Run(ctx context.Context) {
	q.mu.Lock()
	q.running = true
	kinds := q.kinds
	periodics := q.periodic
	q.mu.Unlock()

	if err := q.requeueStale(ctx); err != nil {
		q.log.Error("requeue stale jobs", "err", err)
	}
	var wg sync.WaitGroup
	for name, k := range kinds {
		for range k.concurrency {
			wg.Add(1)
			go func() { defer wg.Done(); q.worker(ctx, name, k) }()
		}
	}
	for _, p := range periodics {
		wg.Add(1)
		go func() { defer wg.Done(); q.tick(ctx, p) }()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		q.tick(ctx, periodic{"jobs.maintenance", time.Hour, q.maintain})
	}()
	wg.Wait()
}

func (q *Queue) tick(ctx context.Context, p periodic) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			start := time.Now()
			err := p.fn(ctx)
			d := time.Since(start)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				q.log.Error("periodic task failed", "task", p.name, "ms", d.Milliseconds(), "err", err)
			} else {
				q.log.Debug("periodic task done", "task", p.name, "ms", d.Milliseconds())
			}
			if o := q.observer(); o != nil {
				o.TaskRun(p.name, err == nil, d)
			}
		}
	}
}

func (q *Queue) worker(ctx context.Context, name string, k *kind) {
	t := time.NewTicker(q.poll)
	defer t.Stop()
	for {
		for {
			ran, err := q.runOne(ctx, name, k)
			if err != nil && ctx.Err() == nil {
				q.log.Error("job claim failed", "kind", name, "err", err)
			}
			if !ran {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-k.wake:
		case <-t.C:
		}
	}
}

func (q *Queue) runOne(ctx context.Context, name string, k *kind) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	now := q.now()
	var (
		id                 int64
		payload            string
		attempts, maxTries int
	)
	err := q.db.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `UPDATE jobs SET state = 'running', attempts = attempts + 1, locked_until = ?, updated_at = ?
			WHERE id = (SELECT id FROM jobs WHERE kind = ? AND state = 'queued' AND run_after <= ? ORDER BY run_after, id LIMIT 1)
			RETURNING id, payload, attempts, max_attempts`,
			db.Millis(now.Add(q.lease)), db.Millis(now), name, db.Millis(now)).Scan(&id, &payload, &attempts, &maxTries)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	jctx := ctx
	if k.timeout > 0 {
		var cancel context.CancelFunc
		jctx, cancel = context.WithTimeout(ctx, k.timeout)
		defer cancel()
	}
	start := time.Now()
	herr := safeCall(jctx, k.handler, json.RawMessage(payload))
	d := time.Since(start)
	done := q.now()
	obs := q.observer()
	// Record the outcome even if ctx was cancelled mid-job.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	switch {
	case herr == nil:
		q.log.Info("job done", "kind", name, "id", id, "attempt", attempts, "ms", d.Milliseconds())
		if obs != nil {
			obs.JobRun(name, "ok", d)
		}
		_, err = q.db.W.ExecContext(fctx, `UPDATE jobs SET state = 'done', locked_until = NULL, last_error = NULL, updated_at = ? WHERE id = ?`, db.Millis(done), id)
	case ctx.Err() != nil:
		// Shutting down: put it back without counting the attempt.
		_, err = q.db.W.ExecContext(fctx, `UPDATE jobs SET state = 'queued', attempts = attempts - 1, locked_until = NULL, updated_at = ? WHERE id = ?`, db.Millis(done), id)
	default:
		var perm permanent
		final := attempts >= maxTries || errors.As(herr, &perm)
		q.log.Warn("job failed", "kind", name, "id", id, "attempt", attempts, "final", final, "ms", d.Milliseconds(), "err", herr)
		if obs != nil {
			res := "retry"
			if final {
				res = "failed"
			}
			obs.JobRun(name, res, d)
		}
		if final {
			_, err = q.db.W.ExecContext(fctx, `UPDATE jobs SET state = 'failed', locked_until = NULL, last_error = ?, updated_at = ? WHERE id = ?`, herr.Error(), db.Millis(done), id)
		} else {
			backoff := time.Duration(1<<min(attempts, 10)) * 10 * time.Second
			_, err = q.db.W.ExecContext(fctx, `UPDATE jobs SET state = 'queued', locked_until = NULL, last_error = ?, run_after = ?, updated_at = ? WHERE id = ?`,
				herr.Error(), db.Millis(done.Add(backoff)), db.Millis(done), id)
		}
	}
	return true, err
}

func (q *Queue) observer() Observer {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.obs
}

func safeCall(ctx context.Context, h Handler, p json.RawMessage) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = Permanent(fmt.Errorf("panic: %v", r))
		}
	}()
	return h(ctx, p)
}

// requeueStale returns jobs whose lease expired (process died mid-job) to the queue.
func (q *Queue) requeueStale(ctx context.Context) error {
	now := db.Millis(q.now())
	_, err := q.db.W.ExecContext(ctx, `UPDATE jobs SET state = 'queued', locked_until = NULL, updated_at = ?
		WHERE state = 'running' AND (locked_until IS NULL OR locked_until < ?)`, now, now)
	return err
}

// maintain requeues stale jobs and deletes finished ones older than a week.
func (q *Queue) maintain(ctx context.Context) error {
	if err := q.requeueStale(ctx); err != nil {
		return err
	}
	_, err := q.db.W.ExecContext(ctx, `DELETE FROM jobs WHERE state IN ('done', 'failed') AND updated_at < ?`,
		db.Millis(q.now().Add(-7*24*time.Hour)))
	return err
}
