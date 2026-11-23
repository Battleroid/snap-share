package stats

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
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

// ---- user-level queries ----
//
// Everything below answers for one account, across all the items and
// secrets it owns. "Your stats" (GET /dashboard/stats, GET /api/v1/stats)
// and Settings > Profile "Usage" share these helpers. All are read-only:
// pass db.R. Each is scoped by an inner join on items.owner_id or
// secrets.owner_id, so rows left behind by a deleted item or secret, and
// other users' rows, never count.
//
// Two limits come from what snap records, not from these queries:
//   - Unique visitors can't be summed over items (visitor keys are per
//     item), so Totals.Uniques and Day.Uniques are always zero here.
//   - Raw events (referrers, clients, secret events) only reach back as far
//     as the retention setting; the daily rollups and the items table don't.

// ItemTypes are the keys UserItemCounts returns, in the order a list shows
// them when counts tie.
var ItemTypes = []string{"image", "video", "audio", "text", "other", "paste", "gist", "link", "album"}

// itemTypeSQL classifies an items row (alias i) into one of ItemTypes: files
// by MIME prefix, every other kind by its own name.
const itemTypeSQL = `CASE WHEN i.kind <> 'file' THEN i.kind
  WHEN i.mime LIKE 'image/%' THEN 'image' WHEN i.mime LIKE 'video/%' THEN 'video'
  WHEN i.mime LIKE 'audio/%' THEN 'audio' WHEN i.mime LIKE 'text/%' THEN 'text' ELSE 'other' END`

// UserTotals sums the counts of all of userID's items over [from, to), with
// the same day semantics as ItemTotals. Uniques is always zero.
func UserTotals(ctx context.Context, db *sql.DB, userID int64, from, to time.Time) (Totals, error) {
	lo, hi := dayBounds(from, to)
	var t Totals
	err := db.QueryRowContext(ctx, `SELECT
  coalesce(sum(d.views), 0), coalesce(sum(d.previews), 0), coalesce(sum(d.embeds), 0),
  coalesce(sum(d.downloads), 0), coalesce(sum(d.clicks), 0), coalesce(sum(d.plays), 0)
FROM item_stats_daily d JOIN items i ON i.id = d.item_id
WHERE i.owner_id = ? AND d.day >= ? AND d.day < ?`, userID, lo, hi).
		Scan(&t.Views, &t.Previews, &t.Embeds, &t.Downloads, &t.Clicks, &t.Plays)
	if err != nil {
		return Totals{}, fmt.Errorf("stats: user totals: %w", err)
	}
	return t, nil
}

// dayBounds is bounds for the daily rollups: the lower bound is the start
// of its day.
func dayBounds(from, to time.Time) (lo, hi int64) {
	lo, hi = bounds(from, to)
	if !from.IsZero() {
		lo = dayOf(lo)
	}
	return lo, hi
}

// UserDaily returns one Day per UTC day from the day of from through the day
// of to, inclusive, summed over all of userID's items, with zeros for days
// without events. Both bounds are required and the range is capped at
// MaxSeriesDays. Uniques and NewVisitors are always zero. The Views of each
// Day are "views per day" for the Usage tiles.
func UserDaily(ctx context.Context, db *sql.DB, userID int64, from, to time.Time) ([]Day, error) {
	first, n, err := seriesRange(from, to)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT d.day, sum(d.views), sum(d.previews), sum(d.embeds), sum(d.downloads), sum(d.clicks), sum(d.plays)
FROM item_stats_daily d JOIN items i ON i.id = d.item_id
WHERE i.owner_id = ? AND d.day >= ? AND d.day < ? GROUP BY d.day`, userID, first, first+n*dayMillis)
	if err != nil {
		return nil, fmt.Errorf("stats: user daily: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Day, n)
	for i := range out {
		out[i].Day = time.UnixMilli(first + int64(i)*dayMillis).UTC()
	}
	for rows.Next() {
		var day int64
		var d Day
		if err := rows.Scan(&day, &d.Views, &d.Previews, &d.Embeds, &d.Downloads, &d.Clicks, &d.Plays); err != nil {
			return nil, fmt.Errorf("stats: user daily: %w", err)
		}
		i := (day - first) / dayMillis
		d.Day = out[i].Day
		out[i] = d
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: user daily: %w", err)
	}
	return out, nil
}

// seriesRange validates a day series: the first day's unix ms and the number
// of days from the day of from through the day of to.
func seriesRange(from, to time.Time) (first, n int64, err error) {
	if from.IsZero() || to.IsZero() || to.Before(from) {
		return 0, 0, ErrRange
	}
	first, last := dayOf(from.UnixMilli()), dayOf(to.UnixMilli())
	n = (last-first)/dayMillis + 1
	if n > MaxSeriesDays {
		return 0, 0, ErrRange
	}
	return first, n, nil
}

// ItemCount is one row of UserTopItems: an item's counts over the range.
type ItemCount struct {
	ItemID      int64
	Views       int64
	Downloads   int64
	PlaysClicks int64
}

// UserTopItems ranks userID's items by views over [from, to) (ties by
// downloads, plays and clicks, then id) and returns the first limit that
// had any activity. Names and kinds are the caller's to look up.
func UserTopItems(ctx context.Context, db *sql.DB, userID int64, from, to time.Time, limit int) ([]ItemCount, error) {
	lo, hi := dayBounds(from, to)
	rows, err := db.QueryContext(ctx, `SELECT d.item_id, sum(d.views) AS v, sum(d.downloads) AS dl, sum(d.plays + d.clicks) AS pc
FROM item_stats_daily d JOIN items i ON i.id = d.item_id
WHERE i.owner_id = ? AND d.day >= ? AND d.day < ?
GROUP BY d.item_id HAVING v + dl + pc > 0
ORDER BY v DESC, dl + pc DESC, d.item_id LIMIT ?`, userID, lo, hi, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("stats: user top items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ItemCount
	for rows.Next() {
		var c ItemCount
		if err := rows.Scan(&c.ItemID, &c.Views, &c.Downloads, &c.PlaysClicks); err != nil {
			return nil, fmt.Errorf("stats: user top items: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: user top items: %w", err)
	}
	return out, nil
}

// userTopSQL counts one raw-event column over all of an owner's items.
const userTopSQL = `SELECT e.%[1]s, count(*) AS n FROM item_events e JOIN items i ON i.id = e.item_id
WHERE i.owner_id = ? AND e.at >= ? AND e.at < ? AND e.%[1]s <> '' AND e.kind <> 'preview'
GROUP BY e.%[1]s ORDER BY n DESC, e.%[1]s LIMIT ?`

// UserTopReferrers counts referring hosts of non-preview events on all of
// userID's items in [from, to). Raw events only: it reaches back as far as
// retention.
func UserTopReferrers(ctx context.Context, db *sql.DB, userID int64, from, to time.Time, limit int) ([]Count, error) {
	return top(ctx, db, "user referrers", fmt.Sprintf(userTopSQL, "referrer_host"), userID, from, to, limit)
}

// UserTopUserAgents counts browser/OS summaries of non-preview events on all
// of userID's items in [from, to). Raw events only.
func UserTopUserAgents(ctx context.Context, db *sql.DB, userID int64, from, to time.Time, limit int) ([]Count, error) {
	return top(ctx, db, "user agents", fmt.Sprintf(userTopSQL, "ua_summary"), userID, from, to, limit)
}

// SecretCounts are the events on a user's secrets over a range.
type SecretCounts struct {
	Opened   int64 // the secret link was loaded (link_opened)
	Revealed int64 // a view was consumed
	Failed   int64 // wrong passphrase
}

// SecretDay is one day of SecretCounts. Day is 00:00 UTC.
type SecretDay struct {
	Day time.Time
	SecretCounts
}

// UserSecretDaily returns one SecretDay per UTC day from the day of from
// through the day of to, inclusive, for the secrets userID owns (zeros for
// quiet days), from raw secret events, so only as far back as retention.
// Same bounds rules as UserDaily. The Revealed of each day is "secret
// reveals per day" for the Usage tiles.
func UserSecretDaily(ctx context.Context, db *sql.DB, userID int64, from, to time.Time) ([]SecretDay, error) {
	first, n, err := seriesRange(from, to)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT e.at / ? * ?, e.kind, count(*)
FROM secret_events e JOIN secrets s ON s.id = e.secret_id
WHERE s.owner_id = ? AND e.at >= ? AND e.at < ? AND e.kind IN ('link_opened', 'revealed', 'failed')
GROUP BY 1, 2`, dayMillis, dayMillis, userID, first, first+n*dayMillis)
	if err != nil {
		return nil, fmt.Errorf("stats: user secret daily: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]SecretDay, n)
	for i := range out {
		out[i].Day = time.UnixMilli(first + int64(i)*dayMillis).UTC()
	}
	for rows.Next() {
		var day, c int64
		var kind string
		if err := rows.Scan(&day, &kind, &c); err != nil {
			return nil, fmt.Errorf("stats: user secret daily: %w", err)
		}
		d := &out[(day-first)/dayMillis]
		switch Kind(kind) {
		case KindLinkOpened:
			d.Opened = c
		case KindRevealed:
			d.Revealed = c
		case KindFailed:
			d.Failed = c
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: user secret daily: %w", err)
	}
	return out, nil
}

// SumSecrets adds up a UserSecretDaily series.
func SumSecrets(days []SecretDay) SecretCounts {
	var t SecretCounts
	for _, d := range days {
		t.Opened += d.Opened
		t.Revealed += d.Revealed
		t.Failed += d.Failed
	}
	return t
}

// UserSecretCount is how many secrets userID has (active ones and the
// tombstones not yet purged), and how many of them are active (state ready).
func UserSecretCount(ctx context.Context, db *sql.DB, userID int64) (all, active int64, err error) {
	err = db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(state = 'ready'), 0) FROM secrets WHERE owner_id = ?`, userID).Scan(&all, &active)
	if err != nil {
		return 0, 0, fmt.Errorf("stats: user secret count: %w", err)
	}
	return all, active, nil
}

// DayCount is one day of a series. Day is 00:00 UTC.
type DayCount struct {
	Day time.Time
	N   int64
}

// UserItemsCreatedDaily returns, for each UTC day from the day of from
// through the day of to, how many of userID's items were created that day,
// counting only items that still exist.
func UserItemsCreatedDaily(ctx context.Context, db *sql.DB, userID int64, from, to time.Time) ([]DayCount, error) {
	return userCreatedDaily(ctx, db, userID, from, to, "count(*)", "")
}

// UserBytesAddedDaily returns, for each UTC day from the day of from through
// the day of to, the cumulative bytes of userID's items (content plus cached
// renders; links and albums hold none) by the day they were uploaded,
// counting only items that still exist. The first day starts from what was
// uploaded before it. Secrets are not included, so the last value can be
// below the account's used bytes.
func UserBytesAddedDaily(ctx context.Context, db *sql.DB, userID int64, from, to time.Time) ([]DayCount, error) {
	first, _, err := seriesRange(from, to)
	if err != nil {
		return nil, err
	}
	var before int64
	if err := db.QueryRowContext(ctx, `SELECT coalesce(sum(size + render_size), 0) FROM items
WHERE owner_id = ? AND kind NOT IN ('link', 'album') AND created_at < ?`, userID, first).Scan(&before); err != nil {
		return nil, fmt.Errorf("stats: user bytes: %w", err)
	}
	days, err := userCreatedDaily(ctx, db, userID, from, to, "sum(size + render_size)", "AND kind NOT IN ('link', 'album')")
	if err != nil {
		return nil, err
	}
	run := before
	for i := range days {
		run += days[i].N
		days[i].N = run
	}
	return days, nil
}

func userCreatedDaily(ctx context.Context, db *sql.DB, userID int64, from, to time.Time, agg, where string) ([]DayCount, error) {
	first, n, err := seriesRange(from, to)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT created_at / ? * ?, `+agg+` FROM items
WHERE owner_id = ? AND created_at >= ? AND created_at < ? `+where+` GROUP BY 1`, dayMillis, dayMillis, userID, first, first+n*dayMillis)
	if err != nil {
		return nil, fmt.Errorf("stats: user items by day: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]DayCount, n)
	for i := range out {
		out[i].Day = time.UnixMilli(first + int64(i)*dayMillis).UTC()
	}
	for rows.Next() {
		var day, c int64
		if err := rows.Scan(&day, &c); err != nil {
			return nil, fmt.Errorf("stats: user items by day: %w", err)
		}
		out[(day-first)/dayMillis].N = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: user items by day: %w", err)
	}
	return out, nil
}

// UserItemCounts counts userID's items by type, most first (ties in the
// order of ItemTypes). Types with no items are left out. image, video, audio,
// text and other are files by MIME; paste, gist, link and album are item
// kinds.
func UserItemCounts(ctx context.Context, db *sql.DB, userID int64) ([]Count, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+itemTypeSQL+` AS t, count(*) FROM items i WHERE i.owner_id = ? GROUP BY t`, userID)
	if err != nil {
		return nil, fmt.Errorf("stats: user item counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Key, &c.N); err != nil {
			return nil, fmt.Errorf("stats: user item counts: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats: user item counts: %w", err)
	}
	order := func(k string) int { return slices.Index(ItemTypes, k) }
	slices.SortFunc(out, func(a, b Count) int {
		if a.N != b.N {
			return cmp.Compare(b.N, a.N)
		}
		return cmp.Compare(order(a.Key), order(b.Key))
	})
	return out, nil
}

// StorageKinds splits an account's used bytes into the four segments of the
// storage bar.
type StorageKinds struct {
	Images, Video, Audio, Other int64
}

// UserStorageKinds splits used (the account's used bytes, as the items
// service reports them in items.Service.Usage) by kind: images, video and
// audio are the content and cached renders of the user's files by MIME
// prefix. Other is the rest of used: text and other files, pastes, gists,
// the fixed charge of links and the ciphertext of secrets. The four add up
// to used, except when the files alone exceed it (a stale counter), where
// Other is 0.
func UserStorageKinds(ctx context.Context, db *sql.DB, userID, used int64) (StorageKinds, error) {
	var k StorageKinds
	err := db.QueryRowContext(ctx, `SELECT
  coalesce(sum(CASE WHEN mime LIKE 'image/%' THEN size + render_size END), 0),
  coalesce(sum(CASE WHEN mime LIKE 'video/%' THEN size + render_size END), 0),
  coalesce(sum(CASE WHEN mime LIKE 'audio/%' THEN size + render_size END), 0)
FROM items WHERE owner_id = ? AND kind = 'file'`, userID).Scan(&k.Images, &k.Video, &k.Audio)
	if err != nil {
		return StorageKinds{}, fmt.Errorf("stats: user storage: %w", err)
	}
	k.Other = max(used-k.Images-k.Video-k.Audio, 0)
	return k, nil
}
