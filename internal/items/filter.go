package items

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/web"
)

// List filters: the query parameters GET /api/v1/items and the dashboard
// accept. The album and tag filters use the dashboard package's album_items and
// item_tags tables.

// Types are the item types lists filter on, in display order.
var Types = []string{"image", "video", "audio", "text", "paste", "gist", "link", "album", "other"}

// Sorts are the list orders, the first being the default.
var Sorts = []string{"new", "old", "name", "size", "views", "opened", "expires"}

// Filter values.
const (
	OpenedNever  = "no" // never opened
	OpenedRecent = "7d" // opened in the last 7 days
	// OpenedPreviewed is previewed by a chat or social app but never opened.
	OpenedPreviewed = "previewed"
	ByPassword      = "password"

	// ExpiringWindow is what "expiring soon" means.
	ExpiringWindow = 7 * 24 * time.Hour
	recentWindow   = 7 * 24 * time.Hour

	maxQueryLen = 200
	maxTerms    = 12
	minTrigram  = 3 // the trigram tokenizer cannot match shorter terms
)

// Filter narrows an owner's item list. The zero value lists everything, newest first.
type Filter struct {
	Query         string     // search text
	Type          string     // one of Types; "" for all
	Visibility    string     // unlisted | private | password; "" for all
	Expiring      bool       // expires within ExpiringWindow (and has not yet)
	Album         int64      // member of this album
	Tag           string     // has this tag (case-insensitive)
	From, To      *time.Time // created on or after From's day / on or before To's day (UTC dates)
	Opened        string     // OpenedNever | OpenedRecent | OpenedPreviewed | ""
	Sort          string     // one of Sorts; "" for new
	CreatedBefore *time.Time // created at or before (the "select all" bound)
}

func badFilter(msg string) error { return web.Errorf(http.StatusBadRequest, "%s", msg) }

// ParseFilter reads a filter from query parameters (q, type, visibility,
// expiring, album, tag, from, to, opened, sort, created_before). Invalid values
// are left out of the returned filter, and the first one is reported as a 400
// error, so pages can ignore it and APIs can refuse it.
func ParseFilter(v url.Values) (Filter, error) {
	var f Filter
	var first error
	fail := func(msg string) {
		if first == nil {
			first = badFilter(msg)
		}
	}
	f.Query = cleanQuery(v.Get("q"))
	switch t := v.Get("type"); {
	case t == "" || t == "all":
	case slices.Contains(Types, t):
		f.Type = t
	default:
		fail("Type must be one of " + strings.Join(Types, ", ") + ".")
	}
	switch vis := v.Get("visibility"); vis {
	case "", "all":
	case string(Unlisted), string(Private), ByPassword:
		f.Visibility = vis
	default:
		fail("Visibility must be unlisted, private or password.")
	}
	switch e := v.Get("expiring"); e {
	case "", "0", "false":
	case "1", "true", "soon":
		f.Expiring = true
	default:
		fail("Expiring must be 1 or 0.")
	}
	if a := v.Get("album"); a != "" {
		if id, err := strconv.ParseInt(a, 10, 64); err == nil && id > 0 {
			f.Album = id
		} else {
			fail("Album must be an album id.")
		}
	}
	if t := strings.TrimSpace(v.Get("tag")); t != "" {
		if n, err := TagName(t); err == nil {
			f.Tag = n
		} else {
			fail(err.Error())
		}
	}
	for _, d := range []struct {
		key string
		dst **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		s := v.Get(d.key)
		if s == "" {
			continue
		}
		t, err := time.Parse(time.DateOnly, s)
		if err != nil {
			fail("Dates are YYYY-MM-DD.")
			continue
		}
		*d.dst = &t
	}
	switch o := v.Get("opened"); o {
	case "", "all":
	case OpenedNever, OpenedRecent, OpenedPreviewed:
		f.Opened = o
	default:
		fail("Opened must be no, 7d or previewed.")
	}
	switch s := v.Get("sort"); {
	case s == "" || s == Sorts[0]:
	case slices.Contains(Sorts, s):
		f.Sort = s
	default:
		fail("Sort must be one of " + strings.Join(Sorts, ", ") + ".")
	}
	if cb := v.Get("created_before"); cb != "" {
		t, err := time.Parse(time.RFC3339Nano, cb)
		if err != nil {
			fail("created_before must be an RFC 3339 time.")
		} else {
			t = t.UTC()
			f.CreatedBefore = &t
		}
	}
	return f, first
}

// Values is the filter as query parameters, leaving out defaults. ParseFilter
// reads it back to the same filter.
func (f Filter) Values() url.Values {
	v := url.Values{}
	set := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	set("q", f.Query)
	set("type", f.Type)
	set("visibility", f.Visibility)
	if f.Expiring {
		v.Set("expiring", "1")
	}
	if f.Album != 0 {
		v.Set("album", strconv.FormatInt(f.Album, 10))
	}
	set("tag", f.Tag)
	if f.From != nil {
		v.Set("from", f.From.Format(time.DateOnly))
	}
	if f.To != nil {
		v.Set("to", f.To.Format(time.DateOnly))
	}
	set("opened", f.Opened)
	set("sort", f.Sort)
	if f.CreatedBefore != nil {
		v.Set("created_before", f.CreatedBefore.UTC().Format(time.RFC3339Nano))
	}
	return v
}

// Narrowed reports whether anything other than the sort is set.
func (f Filter) Narrowed() bool {
	f.Sort, f.CreatedBefore = "", nil
	return f != (Filter{})
}

// TagName validates a tag name: trimmed, 1-40 characters, no control characters.
func TagName(s string) (string, error) {
	n := cleanText(s, 1<<10)
	n = strings.Join(strings.Fields(n), " ")
	switch {
	case n == "":
		return "", badFilter("A tag needs a name.")
	case utf8.RuneCountInString(n) > 40:
		return "", badFilter("Tags are at most 40 characters.")
	case strings.ContainsAny(n, ","):
		return "", badFilter("Tags cannot contain commas.")
	}
	return n, nil
}

// MatchQuery turns search text into an FTS5 MATCH expression: every term of
// three or more characters is wrapped in double quotes, with embedded quotes
// doubled, so FTS operators (AND, OR, NOT, NEAR, *, ^, column:) in the input
// are matched as plain text. Terms are ANDed. Shorter terms cannot match with
// the trigram tokenizer and are returned in short, for a LIKE search on the
// title and filename instead.
func MatchQuery(q string) (match string, short []string) {
	terms := strings.Fields(cleanQuery(q))
	if len(terms) > maxTerms {
		terms = terms[:maxTerms]
	}
	var parts []string
	for _, t := range terms {
		if utf8.RuneCountInString(t) < minTrigram {
			if !slices.Contains(short, t) {
				short = append(short, t)
			}
			continue
		}
		p := `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
		if !slices.Contains(parts, p) {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " "), short
}

// cleanQuery turns control characters (tabs, newlines) into spaces, trims,
// and caps the length.
func cleanQuery(q string) string {
	q = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || isBidiControl(r) {
			return ' '
		}
		return r
	}, q))
	if r := []rune(q); len(r) > maxQueryLen {
		q = strings.TrimSpace(string(r[:maxQueryLen]))
	}
	return q
}

// likePattern is %s% with LIKE wildcards escaped (ESCAPE '\').
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// textMIMEs are application types listed as text (text/* and +json/+xml are too).
var textMIMEs = []string{"application/json", "application/xml", "application/javascript", "application/x-sh",
	"application/yaml", "application/toml", "application/sql", "application/x-yaml"}

// typeSQL classifies a row into one of Types; TypeOf is the same in Go.
var typeSQL = `(CASE
	WHEN kind = 'link' THEN 'link'
	WHEN kind = 'album' THEN 'album'
	WHEN kind = 'paste' THEN 'paste'
	WHEN kind = 'gist' THEN 'gist'
	WHEN mime LIKE 'image/%' THEN 'image'
	WHEN mime LIKE 'video/%' THEN 'video'
	WHEN mime LIKE 'audio/%' THEN 'audio'
	WHEN mime LIKE 'text/%' OR mime LIKE '%+json' OR mime LIKE '%+xml' OR mime IN ('` + strings.Join(textMIMEs, "', '") + `') THEN 'text'
	ELSE 'other' END)`

// nameSQL is Item.Name in SQL.
const nameSQL = `(CASE WHEN title != '' THEN title WHEN filename != '' THEN filename WHEN kind = 'link' THEN coalesce(target_url, '') ELSE slug END)`

// TypeOf is the list type of it (one of Types).
func TypeOf(it *Item) string {
	switch it.Kind {
	case KindLink, KindAlbum, KindPaste, KindGist:
		return string(it.Kind)
	}
	m := it.MIME
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "video/"):
		return "video"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	case strings.HasPrefix(m, "text/"), strings.HasSuffix(m, "+json"), strings.HasSuffix(m, "+xml"), slices.Contains(textMIMEs, m):
		return "text"
	}
	return "other"
}

// where builds the condition selecting owner's items that match f. withType
// false leaves out the type filter (for per-type counts).
func (f Filter) where(owner int64, now time.Time, withType bool) (string, []any) {
	conds := []string{"owner_id = ?"}
	args := []any{owner}
	add := func(c string, a ...any) {
		conds = append(conds, c)
		args = append(args, a...)
	}
	if f.Query != "" {
		match, short := MatchQuery(f.Query)
		if match != "" {
			add(`id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)`, match)
		}
		for _, t := range short {
			p := likePattern(t)
			add(`(title LIKE ? ESCAPE '\' OR filename LIKE ? ESCAPE '\')`, p, p)
		}
	}
	if withType && f.Type != "" {
		add(typeSQL+` = ?`, f.Type)
	}
	switch f.Visibility {
	case string(Unlisted), string(Private):
		add(`visibility = ?`, f.Visibility)
	case ByPassword:
		add(`password_hash IS NOT NULL`)
	}
	if f.Expiring {
		n := db.Millis(now)
		add(`expires_at > ? AND expires_at <= ?`, n, n+ExpiringWindow.Milliseconds())
	}
	if f.Album != 0 {
		add(`id IN (SELECT item_id FROM album_items WHERE album_id = ?)`, f.Album)
	}
	if f.Tag != "" {
		add(`id IN (SELECT it.item_id FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE t.owner_id = ? AND t.name = ?)`, owner, f.Tag)
	}
	if f.From != nil {
		add(`created_at >= ?`, db.Millis(*f.From))
	}
	if f.To != nil {
		add(`created_at < ?`, db.Millis(f.To.AddDate(0, 0, 1)))
	}
	switch f.Opened {
	case OpenedNever:
		add(`first_opened_at IS NULL`)
	case OpenedRecent:
		add(`last_opened_at >= ?`, db.Millis(now.Add(-recentWindow)))
	case OpenedPreviewed:
		add(`previewed_at IS NOT NULL AND first_opened_at IS NULL`)
	}
	if f.CreatedBefore != nil {
		add(`created_at <= ?`, db.Millis(*f.CreatedBefore))
	}
	return strings.Join(conds, " AND "), args
}

func (f Filter) order() string {
	switch f.Sort {
	case "old":
		return `created_at ASC, id ASC`
	case "name":
		return nameSQL + ` COLLATE NOCASE ASC, id DESC`
	case "size":
		return `size DESC, id DESC`
	case "views":
		return `views DESC, id DESC`
	case "opened":
		return `last_opened_at IS NULL, last_opened_at DESC, id DESC`
	case "expires":
		return `expires_at IS NULL, expires_at ASC, id DESC`
	}
	return `created_at DESC, id DESC`
}

// Querier is what list helpers read through: *sql.DB, *sql.Tx or *sql.Conn.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// SelectItems returns the items matching a condition over the items table
// (without WHERE, e.g. "owner_id = ? AND id IN (...)") followed by an optional
// ORDER BY/LIMIT tail. Rows are closed before it returns.
func SelectItems(ctx context.Context, q Querier, cond, tail string, args ...any) ([]*Item, error) {
	return queryAll(ctx, q, `SELECT `+itemCols+` FROM items WHERE `+cond+` `+tail, // #nosec G202 G701 -- callers pass constant SQL; values are bound
		func(rows *sql.Rows) (*Item, error) { return scanItem(rows) }, args...)
}

// Find lists owner's items matching f with the total count.
func (s *Service) Find(ctx context.Context, owner int64, f Filter, limit, offset int) ([]*Item, int, error) {
	cond, args := f.where(owner, s.now(), true)
	var total int
	if err := s.d.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE `+cond, args...).Scan(&total); err != nil { // #nosec G202 G701 -- built from constants; values are bound
		return nil, 0, err
	}
	list, err := SelectItems(ctx, s.d.DB.R, cond, `ORDER BY `+f.order()+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	return list, total, err
}

// Count counts owner's items matching f, reading through q (a transaction, to
// check a "select all" count before acting on it).
func (s *Service) Count(ctx context.Context, q Querier, owner int64, f Filter) (int, error) {
	cond, args := f.where(owner, s.now(), true)
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE `+cond, args...).Scan(&n) // #nosec G202 G701 -- built from constants; values are bound
	return n, err
}

// MatchIDs returns the ids of owner's items matching f, in list order, at most limit.
func (s *Service) MatchIDs(ctx context.Context, q Querier, owner int64, f Filter, limit int) ([]int64, error) {
	cond, args := f.where(owner, s.now(), true)
	return queryAll(ctx, q, `SELECT id FROM items WHERE `+cond+` ORDER BY `+f.order()+` LIMIT ?`, // #nosec G202 G701 -- built from constants; values are bound
		func(rows *sql.Rows) (int64, error) {
			var id int64
			err := rows.Scan(&id)
			return id, err
		}, append(args, limit)...)
}

// TypeCounts counts owner's items matching f per type, ignoring f.Type.
func (s *Service) TypeCounts(ctx context.Context, owner int64, f Filter) (map[string]int, error) {
	cond, args := f.where(owner, s.now(), false)
	type tc struct {
		t string
		n int
	}
	list, err := queryAll(ctx, s.d.DB.R, `SELECT `+typeSQL+` AS t, count(*) FROM items WHERE `+cond+` GROUP BY t`, // #nosec G202 G701 -- built from constants; values are bound
		func(rows *sql.Rows) (tc, error) {
			var x tc
			err := rows.Scan(&x.t, &x.n)
			return x, err
		}, args...)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(Types))
	for _, x := range list {
		out[x.t] = x.n
	}
	return out, nil
}
