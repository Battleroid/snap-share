package stats

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Writer defaults.
const (
	DefaultBatchSize     = 200
	DefaultFlushInterval = 5 * time.Second
	DefaultMaxBuffered   = 10_000
	DefaultFlushTimeout  = 10 * time.Second
)

const dayMillis = 24 * 60 * 60 * 1000

// WriterOptions configures a Writer. Zero values use the defaults.
type WriterOptions struct {
	// BatchSize buffered events trigger a flush before the interval.
	BatchSize int
	// FlushInterval is the longest an event waits in the buffer.
	FlushInterval time.Duration
	// MaxBuffered caps the buffer; Record drops events beyond it.
	MaxBuffered int
	// FlushTimeout bounds each background flush.
	FlushTimeout time.Duration

	// OnOpened, if set, runs inside the flush transaction once for every
	// item event that Opens(), in time order, with the item id as
	// Subject. The integrator uses it to maintain items.first_opened_at,
	// last_opened_at and opened_count. An error rolls back the whole
	// flush, and its events are counted as failed.
	OnOpened func(tx *sql.Tx, subject string, at time.Time) error

	Logger *slog.Logger
	Now    func() time.Time
}

// WriterStats are counters since the Writer started.
type WriterStats struct {
	Written uint64 // events committed
	Dropped uint64 // buffer full, or recorded after Close
	Invalid uint64 // unknown subject type or kind, bad item id
	Failed  uint64 // lost to a failed flush
}

// Writer is a Recorder that buffers events in memory and writes them in
// batches, one transaction per flush, maintaining item_stats_daily and
// item_visitors as it goes. Record never blocks. Close flushes what is
// left.
type Writer struct {
	db   *sql.DB
	opts WriterOptions
	log  *slog.Logger

	mu     sync.Mutex
	buf    []Event
	closed bool

	flushMu sync.Mutex // one flush at a time
	kick    chan struct{}
	stop    chan struct{}
	done    chan struct{}

	written, dropped, invalid, failed atomic.Uint64
}

var _ Recorder = (*Writer)(nil)

// NewWriter starts a Writer on db. The caller must Close it.
func NewWriter(db *sql.DB, opts WriterOptions) *Writer {
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultBatchSize
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = DefaultFlushInterval
	}
	if opts.MaxBuffered <= 0 {
		opts.MaxBuffered = DefaultMaxBuffered
	}
	if opts.FlushTimeout <= 0 {
		opts.FlushTimeout = DefaultFlushTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	w := &Writer{
		db:   db,
		opts: opts,
		log:  log,
		kick: make(chan struct{}, 1),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go w.loop()
	return w
}

// Record validates e and queues it. It never blocks: when the buffer is
// full, or after Close, the event is dropped and counted.
func (w *Writer) Record(_ context.Context, e Event) {
	if !valid(e) {
		w.invalid.Add(1)
		return
	}
	if e.At.IsZero() {
		e.At = w.opts.Now()
	}
	if e.SubjectType == SubjectItem {
		id, _ := strconv.ParseInt(e.Subject, 10, 64) // checked by valid
		e.Subject = strconv.FormatInt(id, 10)        // "+07" -> "7"
	}
	clean(&e)

	w.mu.Lock()
	if w.closed || len(w.buf) >= w.opts.MaxBuffered {
		w.mu.Unlock()
		w.dropped.Add(1)
		return
	}
	w.buf = append(w.buf, e)
	full := len(w.buf) >= w.opts.BatchSize
	w.mu.Unlock()

	if full {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
}

// Stats returns the Writer's counters.
func (w *Writer) Stats() WriterStats {
	return WriterStats{
		Written: w.written.Load(),
		Dropped: w.dropped.Load(),
		Invalid: w.invalid.Load(),
		Failed:  w.failed.Load(),
	}
}

// Flush writes everything buffered now, in one transaction.
func (w *Writer) Flush(ctx context.Context) error {
	w.flushMu.Lock()
	defer w.flushMu.Unlock()

	w.mu.Lock()
	batch := w.buf
	w.buf = nil
	w.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	if err := w.write(ctx, batch); err != nil {
		w.failed.Add(uint64(len(batch)))
		w.log.Error("stats: flush failed", "events", len(batch), "err", err)
		return err
	}
	w.written.Add(uint64(len(batch)))
	return nil
}

// Close stops the background flusher and flushes what is buffered.
// Events recorded after Close are dropped.
func (w *Writer) Close(ctx context.Context) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()

	close(w.stop)
	select {
	case <-w.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return w.Flush(ctx)
}

func (w *Writer) loop() {
	defer close(w.done)
	t := time.NewTicker(w.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
		case <-w.kick:
		}
		ctx, cancel := context.WithTimeout(context.Background(), w.opts.FlushTimeout)
		_ = w.Flush(ctx) // logged and counted in Flush
		cancel()
	}
}

func valid(e Event) bool {
	switch e.SubjectType {
	case SubjectItem:
		id, err := strconv.ParseInt(e.Subject, 10, 64)
		return err == nil && id > 0 && e.Kind.ItemKind()
	case SubjectSecret:
		return e.Subject != "" && len(e.Subject) <= 128 && e.Kind.SecretKind()
	}
	return false
}

// clean bounds the stored strings and makes them valid UTF-8.
func clean(e *Event) {
	e.IP = limit(e.IP, 64)
	e.UA = limit(e.UA, maxUALen)
	e.UASummary = limit(e.UASummary, 64)
	e.Bot = limit(e.Bot, 64)
	e.RefererHost = limit(e.RefererHost, 253)
	e.VisitorKey = limit(e.VisitorKey, 64)
}

func limit(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return s
}

// dayOf returns the unix ms of 00:00 UTC on the day of ms.
func dayOf(ms int64) int64 {
	d := ms % dayMillis
	if d < 0 {
		d += dayMillis
	}
	return ms - d
}

type dayKey struct {
	item, day int64
}

type dayCounts struct {
	views, uniques, newVisitors, previews, embeds, downloads, clicks, plays int64
}

func (c *dayCounts) add(k Kind) {
	switch k {
	case KindView:
		c.views++
	case KindPreview:
		c.previews++
	case KindEmbed:
		c.embeds++
	case KindDownload:
		c.downloads++
	case KindClick:
		c.clicks++
	case KindPlay:
		c.plays++
	}
}

const (
	insertItemEvent = `INSERT INTO item_events
  (item_id, at, kind, ip, user_agent, ua_summary, bot_name, referrer_host, visitor_key)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	//nolint:gosec // G101: SQL, not a credential
	insertSecretEvent = `INSERT INTO secret_events
  (secret_id, at, kind, ip, user_agent, ua_summary, bot_name, referrer_host, visitor_key)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	selectVisitor = `SELECT last_day FROM item_visitors WHERE item_id = ? AND visitor_key = ?`
	insertVisitor = `INSERT INTO item_visitors (item_id, visitor_key, last_day) VALUES (?, ?, ?)`
	updateVisitor = `UPDATE item_visitors SET last_day = ? WHERE item_id = ? AND visitor_key = ?`
	upsertDaily   = `INSERT INTO item_stats_daily
  (item_id, day, views, uniques, new_visitors, previews, embeds, downloads, clicks, plays)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  ON CONFLICT (item_id, day) DO UPDATE SET
    views = views + excluded.views,
    uniques = uniques + excluded.uniques,
    new_visitors = new_visitors + excluded.new_visitors,
    previews = previews + excluded.previews,
    embeds = embeds + excluded.embeds,
    downloads = downloads + excluded.downloads,
    clicks = clicks + excluded.clicks,
    plays = plays + excluded.plays`
)

// write commits a batch: raw events, visitor bookkeeping, daily rollups
// and the OnOpened hook, all in one transaction.
func (w *Writer) write(ctx context.Context, batch []Event) (err error) {
	slices.SortStableFunc(batch, func(a, b Event) int { return a.At.Compare(b.At) })

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("stats: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	days := map[dayKey]*dayCounts{}
	for _, e := range batch {
		at := e.At.UnixMilli()
		args := []any{at, string(e.Kind), e.IP, e.UA, e.UASummary, e.Bot, e.RefererHost, e.VisitorKey}
		if e.SubjectType == SubjectSecret {
			if _, err := tx.ExecContext(ctx, insertSecretEvent, append([]any{e.Subject}, args...)...); err != nil {
				return fmt.Errorf("stats: insert secret event: %w", err)
			}
			continue
		}
		id, _ := strconv.ParseInt(e.Subject, 10, 64) // checked by valid
		if _, err := tx.ExecContext(ctx, insertItemEvent, append([]any{id}, args...)...); err != nil {
			return fmt.Errorf("stats: insert item event: %w", err)
		}
		k := dayKey{id, dayOf(at)}
		c := days[k]
		if c == nil {
			c = &dayCounts{}
			days[k] = c
		}
		c.add(e.Kind)
		if !e.Kind.Opens() {
			continue
		}
		if e.VisitorKey != "" {
			if err := visit(ctx, tx, k, e.VisitorKey, c); err != nil {
				return err
			}
		}
		if w.opts.OnOpened != nil {
			if err := w.opts.OnOpened(tx, e.Subject, e.At); err != nil {
				return fmt.Errorf("stats: OnOpened: %w", err)
			}
		}
	}

	keys := make([]dayKey, 0, len(days))
	for k := range days {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b dayKey) int {
		return cmp.Or(cmp.Compare(a.item, b.item), cmp.Compare(a.day, b.day))
	})
	for _, k := range keys {
		c := days[k]
		if _, err := tx.ExecContext(ctx, upsertDaily, k.item, k.day,
			c.views, c.uniques, c.newVisitors, c.previews, c.embeds, c.downloads, c.clicks, c.plays); err != nil {
			return fmt.Errorf("stats: upsert daily: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("stats: commit: %w", err)
	}
	return nil
}

// visit counts a visitor once per item per day: a key never seen for the
// item is a new visitor and a unique; one last counted on an earlier day
// is a unique again.
func visit(ctx context.Context, tx *sql.Tx, k dayKey, key string, c *dayCounts) error {
	var last int64
	err := tx.QueryRowContext(ctx, selectVisitor, k.item, key).Scan(&last)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, insertVisitor, k.item, key, k.day); err != nil {
			return fmt.Errorf("stats: insert visitor: %w", err)
		}
		c.uniques++
		c.newVisitors++
	case err != nil:
		return fmt.Errorf("stats: select visitor: %w", err)
	case k.day > last:
		if _, err := tx.ExecContext(ctx, updateVisitor, k.day, k.item, key); err != nil {
			return fmt.Errorf("stats: update visitor: %w", err)
		}
		c.uniques++
	}
	return nil
}
