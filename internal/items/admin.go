package items

// Read helpers for the admin pages: items across owners, per-owner counts
// and site totals.

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// ListFilter selects items across owners. Zero fields match everything.
type ListFilter struct {
	Owner  string // owner username
	Kind   Kind
	Query  string // substring of the title, filename, slug or link target
	Limit  int
	Offset int
}

// OwnedItem is an item with its owner's username.
type OwnedItem struct {
	*Item
	OwnerName string
}

// withExtra scans itemCols followed by extra columns.
type withExtra struct {
	sc    scanner
	extra []any
}

func (w withExtra) Scan(dest ...any) error { return w.sc.Scan(append(dest, w.extra...)...) }

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ListAll returns items of every owner, newest first, and the number matching.
func (s *Service) ListAll(ctx context.Context, f ListFilter) ([]OwnedItem, int, error) {
	var conds []string
	var args []any
	if f.Owner != "" {
		conds, args = append(conds, `owner_id = (SELECT id FROM users WHERE username = ?)`), append(args, f.Owner)
	}
	if f.Kind != "" {
		conds, args = append(conds, `kind = ?`), append(args, string(f.Kind))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		p := "%" + likeEscaper.Replace(q) + "%"
		conds = append(conds, `(title LIKE ? ESCAPE '\' OR filename LIKE ? ESCAPE '\' OR slug LIKE ? ESCAPE '\' OR coalesce(target_url, '') LIKE ? ESCAPE '\')`)
		args = append(args, p, p, p, p)
	}
	where := "1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	var total int
	if err := s.d.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM items WHERE `+where, args...).Scan(&total); err != nil { // #nosec G202 -- conditions are constants from this function
		return nil, 0, err
	}
	out, err := queryAll(ctx, s.d.DB.R, `SELECT `+itemCols+`, coalesce((SELECT username FROM users WHERE users.id = items.owner_id), '')
		FROM items WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, // #nosec G202 -- conditions are constants from this function
		func(rows *sql.Rows) (OwnedItem, error) {
			var o OwnedItem
			var err error
			o.Item, err = scanItem(withExtra{rows, []any{&o.OwnerName}})
			return o, err
		}, append(args, f.Limit, f.Offset)...)
	return out, total, err
}

// CountByOwner returns the number of items each of owners has.
func (s *Service) CountByOwner(ctx context.Context, owners []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(owners))
	if len(owners) == 0 {
		return out, nil
	}
	args := make([]any, len(owners))
	for i, id := range owners {
		args[i] = id
	}
	q := `SELECT owner_id, count(*) FROM items WHERE owner_id IN (?` + strings.Repeat(", ?", len(owners)-1) + `) GROUP BY owner_id` // #nosec G202 -- only placeholders are added
	rows, err := s.d.DB.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// Totals returns the number of items and the bytes charged to quotas.
func (s *Service) Totals(ctx context.Context) (count int, bytes int64, err error) {
	err = s.d.DB.R.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(CASE WHEN kind IN ('file', 'paste') THEN size END), 0) FROM items`).
		Scan(&count, &bytes)
	return count, bytes, err
}

// CreatedPerDay counts items created on each of the last days UTC days,
// oldest first, ending with today. Deleted items are not counted.
func (s *Service) CreatedPerDay(ctx context.Context, days int, now time.Time) ([]int, error) {
	const day = int64(24 * time.Hour / time.Millisecond)
	today := db.Millis(now) / day
	first := today - int64(days) + 1
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT created_at / ? AS d, count(*) FROM items WHERE created_at >= ? GROUP BY d`, day, first*day)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]int, days)
	for rows.Next() {
		var d int64
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		if i := d - first; i >= 0 && i < int64(days) {
			out[i] = n
		}
	}
	return out, rows.Err()
}
