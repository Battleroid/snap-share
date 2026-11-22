package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// Query limits.
const (
	MaxSeriesDays = 1000 // ItemDaily refuses longer ranges
	MaxListLimit  = 1000 // Top* and Latest* cap their limit here
)

// ErrRange is returned for a missing, inverted or too long range.
var ErrRange = errors.New("stats: bad range")

// Execer is satisfied by *sql.DB and *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Totals are an item's counts over a range.
type Totals struct {
	Views, Uniques, Previews, Embeds, Downloads, Clicks, Plays int64
}

// Day is one day of an item's series. Day is 00:00 UTC.
type Day struct {
	Day                                                                     time.Time
	Views, Uniques, NewVisitors, Previews, Embeds, Downloads, Clicks, Plays int64
}

// Count is one row of a top-N list.
type Count struct {
	Key string
	N   int64
}

// openKinds is the SQL list of kinds where Kind.Opens is true.
const openKinds = `('view', 'download', 'click', 'play')`

// bounds turns [from, to) into unix ms, with zero from meaning the start
// of time and zero to meaning no upper bound.
func bounds(from, to time.Time) (lo, hi int64) {
	lo, hi = math.MinInt64, math.MaxInt64
	if !from.IsZero() {
		lo = from.UnixMilli()
	}
	if !to.IsZero() {
		hi = to.UnixMilli()
	}
	return lo, hi
}

// ItemTotals sums an item's counts over [from, to). Zero from means since
// the start, zero to means up to now.
//
// Counts come from the daily rollups and cover every UTC day that overlaps
// the range; pass midnight-aligned bounds for exact days. Uniques for the
// whole lifetime (zero from) is the sum of new visitors per day. For a
// bounded range it is the number of distinct visitors among the raw
// events in [from, to), so it only reaches back as far as retention.
func ItemTotals(ctx context.Context, db *sql.DB, itemID int64, from, to time.Time) (Totals, error) {
	lo, hi := bounds(from, to)
	dayLo := lo
	if !from.IsZero() {
		dayLo = dayOf(lo)
	}
	var t Totals
	var lifetimeUniques int64
	err := db.QueryRowContext(ctx, `SELECT
  coalesce(sum(views), 0), coalesce(sum(new_visitors), 0), coalesce(sum(previews), 0),
  coalesce(sum(embeds), 0), coalesce(sum(downloads), 0), coalesce(sum(clicks), 0), coalesce(sum(plays), 0)
FROM item_stats_daily WHERE item_id = ? AND day >= ? AND day < ?`,
		itemID, dayLo, hi).Scan(&t.Views, &lifetimeUniques, &t.Previews, &t.Embeds, &t.Downloads, &t.Clicks, &t.Plays)
	if err != nil {
		return Totals{}, fmt.Errorf("stats: totals: %w", err)
	}
	if from.IsZero() {
		t.Uniques = lifetimeUniques
		return t, nil
	}
	err = db.QueryRowContext(ctx, `SELECT count(DISTINCT visitor_key) FROM item_events
WHERE item_id = ? AND at >= ? AND at < ? AND visitor_key <> '' AND kind IN `+openKinds,
		itemID, lo, hi).Scan(&t.Uniques)
	if err != nil {
		return Totals{}, fmt.Errorf("stats: uniques: %w", err)
	}
	return t, nil
}

// ItemDaily returns one Day per UTC day from the day of from through the
// day of to, inclusive, with zeros for days without events. Both bounds
// are required and the range is capped at MaxSeriesDays.
func ItemDaily(ctx context.Context, db *sql.DB, itemID int64, from, to time.Time) ([]Day, error) {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return nil, ErrRange
	}
	first, last := dayOf(from.UnixMilli()), dayOf(to.UnixMilli())
	n := (last-first)/dayMillis + 1
	if n > MaxSeriesDays {
		return nil, ErrRange
	}
	rows, err := db.QueryContext(ctx, `SELECT day, views, uniques, new_visitors, previews, embeds, downloads, clicks, plays
FROM item_stats_daily WHERE item_id = ? AND day >= ? AND day <= ? ORDER BY day`, itemID, first, last)
	if err != nil {
		return nil, fmt.Errorf("stats: daily: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Day, n)
	for i := range out {
		out[i].Day = time.UnixMilli(first + int64(i)*dayMillis).UTC()
	}
	for rows.Next() {
		var day int64
		var d Day
		if err := rows.Scan(&day, &d.Views, &d.Uniques, &d.NewVisitors, &d.Previews, &d.Embeds, &d.Downloads, &d.Clicks, &d.Plays); err != nil {
			return nil, fmt.Errorf("stats: daily: %w", err)
		}
		i := (day - first) / dayMillis
		d.Day = out[i].Day
		out[i] = d
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: daily: %w", err)
	}
	return out, nil
}

const (
	topReferrersSQL = `SELECT referrer_host, count(*) AS n FROM item_events
WHERE item_id = ? AND at >= ? AND at < ? AND referrer_host <> '' AND kind <> 'preview'
GROUP BY referrer_host ORDER BY n DESC, referrer_host LIMIT ?`
	topBotsSQL = `SELECT bot_name, count(*) AS n FROM item_events
WHERE item_id = ? AND at >= ? AND at < ? AND bot_name <> '' AND kind = 'preview'
GROUP BY bot_name ORDER BY n DESC, bot_name LIMIT ?`
	topUserAgentsSQL = `SELECT ua_summary, count(*) AS n FROM item_events
WHERE item_id = ? AND at >= ? AND at < ? AND ua_summary <> '' AND kind <> 'preview'
GROUP BY ua_summary ORDER BY n DESC, ua_summary LIMIT ?`
)

// TopReferrers counts referring hosts of non-preview events in [from, to).
func TopReferrers(ctx context.Context, db *sql.DB, itemID int64, from, to time.Time, limit int) ([]Count, error) {
	return top(ctx, db, "referrers", topReferrersSQL, itemID, from, to, limit)
}

// TopBots counts preview bots by name in [from, to).
func TopBots(ctx context.Context, db *sql.DB, itemID int64, from, to time.Time, limit int) ([]Count, error) {
	return top(ctx, db, "bots", topBotsSQL, itemID, from, to, limit)
}

// TopUserAgents counts browser/OS summaries of non-preview events in
// [from, to).
func TopUserAgents(ctx context.Context, db *sql.DB, itemID int64, from, to time.Time, limit int) ([]Count, error) {
	return top(ctx, db, "user agents", topUserAgentsSQL, itemID, from, to, limit)
}

func top(ctx context.Context, db *sql.DB, what, query string, itemID int64, from, to time.Time, limit int) ([]Count, error) {
	lo, hi := bounds(from, to)
	rows, err := db.QueryContext(ctx, query, itemID, lo, hi, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("stats: top %s: %w", what, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Key, &c.N); err != nil {
			return nil, fmt.Errorf("stats: top %s: %w", what, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: top %s: %w", what, err)
	}
	return out, nil
}

func clampLimit(n int) int {
	if n <= 0 || n > MaxListLimit {
		return MaxListLimit
	}
	return n
}

// LatestItemEvents returns an item's most recent raw events, newest first.
func LatestItemEvents(ctx context.Context, db *sql.DB, itemID int64, limit int) ([]Event, error) {
	return latest(ctx, db, `SELECT item_id, at, kind, ip, user_agent, ua_summary, bot_name, referrer_host, visitor_key
FROM item_events WHERE item_id = ? ORDER BY at DESC, id DESC LIMIT ?`, SubjectItem, itemID, limit)
}

// LatestSecretEvents returns a secret's most recent events, newest first.
func LatestSecretEvents(ctx context.Context, db *sql.DB, secretID string, limit int) ([]Event, error) {
	return latest(ctx, db, `SELECT secret_id, at, kind, ip, user_agent, ua_summary, bot_name, referrer_host, visitor_key
FROM secret_events WHERE secret_id = ? ORDER BY at DESC, id DESC LIMIT ?`, SubjectSecret, secretID, limit)
}

func latest(ctx context.Context, db *sql.DB, q, subjectType string, subject any, limit int) ([]Event, error) {
	rows, err := db.QueryContext(ctx, q, subject, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("stats: latest: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var (
			e    Event
			subj any
			at   int64
			kind string
		)
		if err := rows.Scan(&subj, &at, &kind, &e.IP, &e.UA, &e.UASummary, &e.Bot, &e.RefererHost, &e.VisitorKey); err != nil {
			return nil, fmt.Errorf("stats: latest: %w", err)
		}
		switch s := subj.(type) {
		case int64:
			e.Subject = strconv.FormatInt(s, 10)
		case string:
			e.Subject = s
		}
		e.SubjectType = subjectType
		e.At = time.UnixMilli(at).UTC()
		e.Kind = Kind(kind)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: latest: %w", err)
	}
	return out, nil
}

// purgeBatch bounds each DELETE so the write lock is held briefly.
const purgeBatch = 5000

// Batched retention deletes. item_visitors is WITHOUT ROWID, so its
// batches are selected by key.
const (
	purgeItemEventsSQL = `DELETE FROM item_events WHERE id IN (SELECT id FROM item_events WHERE at < ? LIMIT ?)`
	//nolint:gosec // G101: SQL, not a credential
	purgeSecretEventsSQL = `DELETE FROM secret_events WHERE id IN (SELECT id FROM secret_events WHERE at < ? LIMIT ?)`
	purgeVisitorsSQL     = `DELETE FROM item_visitors WHERE (item_id, visitor_key) IN
  (SELECT item_id, visitor_key FROM item_visitors WHERE last_day < ? LIMIT ?)`
)

// Purge deletes raw item and secret events older than olderThan, and
// visitor keys last counted before that day. Daily rollups stay. It
// returns the number of events deleted.
func Purge(ctx context.Context, db *sql.DB, olderThan time.Time) (int64, error) {
	cutoff := olderThan.UnixMilli()
	var total int64
	for _, p := range []struct {
		what, query string
		arg         int64
		events      bool
	}{
		{"item_events", purgeItemEventsSQL, cutoff, true},
		{"secret_events", purgeSecretEventsSQL, cutoff, true},
		{"item_visitors", purgeVisitorsSQL, dayOf(cutoff), false},
	} {
		for {
			res, err := db.ExecContext(ctx, p.query, p.arg, purgeBatch)
			if err != nil {
				return total, fmt.Errorf("stats: purge %s: %w", p.what, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return total, fmt.Errorf("stats: purge %s: %w", p.what, err)
			}
			if p.events {
				total += n
			}
			if n < purgeBatch {
				break
			}
		}
	}
	return total, nil
}

// DeleteItemStats removes every stats row for an item. Call it in the
// transaction that deletes the item; there are no foreign keys.
func DeleteItemStats(ctx context.Context, x Execer, itemID int64) error {
	for _, q := range []string{
		`DELETE FROM item_events WHERE item_id = ?`,
		`DELETE FROM item_stats_daily WHERE item_id = ?`,
		`DELETE FROM item_visitors WHERE item_id = ?`,
	} {
		if _, err := x.ExecContext(ctx, q, itemID); err != nil {
			return fmt.Errorf("stats: delete item stats: %w", err)
		}
	}
	return nil
}

// DeleteSecretEvents removes a secret's events.
func DeleteSecretEvents(ctx context.Context, x Execer, secretID string) error {
	if _, err := x.ExecContext(ctx, `DELETE FROM secret_events WHERE secret_id = ?`, secretID); err != nil {
		return fmt.Errorf("stats: delete secret events: %w", err)
	}
	return nil
}
