package stats

// Paged raw events for the stats panel, its CSV export and the events API
// (statsui), and the open lookups behind notification messages.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// StoredEvent is a raw item event with its row id, which pages and the
// events API use as a cursor.
type StoredEvent struct {
	ID int64
	Event
}

// EventPage selects raw events newest first (by row id). BeforeID > 0
// returns only events with a smaller id; Offset skips that many more.
// Limit is clamped to MaxListLimit.
type EventPage struct {
	BeforeID int64
	Offset   int
	Limit    int
}

const itemEventCols = `id, item_id, at, kind, ip, user_agent, ua_summary, bot_name, referrer_host, visitor_key`

// ItemEvents returns a page of an item's raw events, newest first.
func ItemEvents(ctx context.Context, db *sql.DB, itemID int64, p EventPage) ([]StoredEvent, error) {
	before := p.BeforeID
	if before <= 0 {
		before = math.MaxInt64
	}
	rows, err := db.QueryContext(ctx, `SELECT `+itemEventCols+` FROM item_events
WHERE item_id = ? AND id < ? ORDER BY id DESC LIMIT ? OFFSET ?`, itemID, before, clampLimit(p.Limit), max(p.Offset, 0))
	if err != nil {
		return nil, fmt.Errorf("stats: item events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []StoredEvent
	for rows.Next() {
		e, err := scanStored(rows)
		if err != nil {
			return nil, fmt.Errorf("stats: item events: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: item events: %w", err)
	}
	return out, nil
}

// CountItemEvents counts an item's raw events (those within retention).
func CountItemEvents(ctx context.Context, db *sql.DB, itemID int64) (int64, error) {
	var n int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM item_events WHERE item_id = ?`, itemID).Scan(&n); err != nil {
		return 0, fmt.Errorf("stats: count item events: %w", err)
	}
	return n, nil
}

// FirstItemOpen returns the item's earliest raw event whose kind Opens().
// ok is false when there is none (not yet written, or past retention).
func FirstItemOpen(ctx context.Context, db *sql.DB, itemID int64) (e StoredEvent, ok bool, err error) {
	return oneOpen(ctx, db, `SELECT `+itemEventCols+` FROM item_events
WHERE item_id = ? AND kind IN `+openKinds+` ORDER BY at, id LIMIT 1`, itemID)
}

// LastItemOpen returns the item's latest raw event whose kind Opens() at
// or before notAfter (zero: any time).
func LastItemOpen(ctx context.Context, db *sql.DB, itemID int64, notAfter time.Time) (e StoredEvent, ok bool, err error) {
	_, hi := bounds(time.Time{}, notAfter)
	if hi != math.MaxInt64 {
		hi++ // inclusive
	}
	return oneOpen(ctx, db, `SELECT `+itemEventCols+` FROM item_events
WHERE item_id = ? AND kind IN `+openKinds+` AND at < ? ORDER BY at DESC, id DESC LIMIT 1`, itemID, hi)
}

func oneOpen(ctx context.Context, db *sql.DB, q string, args ...any) (StoredEvent, bool, error) {
	e, err := scanStored(db.QueryRowContext(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredEvent{}, false, nil
	}
	if err != nil {
		return StoredEvent{}, false, fmt.Errorf("stats: open event: %w", err)
	}
	return e, true, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanStored(sc rowScanner) (StoredEvent, error) {
	var (
		e        StoredEvent
		item, at int64
		kind     string
	)
	if err := sc.Scan(&e.ID, &item, &at, &kind, &e.IP, &e.UA, &e.UASummary, &e.Bot, &e.RefererHost, &e.VisitorKey); err != nil {
		return StoredEvent{}, err
	}
	e.Subject = strconv.FormatInt(item, 10)
	e.SubjectType = SubjectItem
	e.At = time.UnixMilli(at).UTC()
	e.Kind = Kind(kind)
	return e, nil
}
