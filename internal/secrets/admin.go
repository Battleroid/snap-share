package secrets

// Admin helpers: secret metadata across owners (never notes, keys or
// ciphertext), counts, and burning everything a user owns.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// Meta is what admins see of a secret. It leaves out the owner's note and
// every envelope value.
type Meta struct {
	ID         string
	OwnerID    int64  // 0: anonymous
	OwnerName  string // "" when anonymous
	Kind       string // text or file
	Mode       string // e2e or sealed
	State      string // uploading, ready, burned, expired or revoked
	Passphrase bool
	Size       int64
	ViewsUsed  int
	ViewsLeft  int
	MaxViews   int
	CreatedAt  time.Time
	ExpiresAt  time.Time
	BurnedAt   *time.Time
	BurnReason string
}

// Burnable reports whether the secret can still be opened, so burning it changes something.
func (m Meta) Burnable() bool { return m.State == stateReady || m.State == stateUploading }

// MetaFilter selects secrets for ListMeta. Zero fields match everything.
type MetaFilter struct {
	Owner     string // owner username
	Anonymous bool   // only secrets without an owner
	Active    bool   // only secrets that can still be opened (or are uploading)
	Limit     int
	Offset    int
}

// ListMeta returns secret metadata across owners, newest first, and the number matching.
func (s *Service) ListMeta(ctx context.Context, f MetaFilter) ([]Meta, int, error) {
	now := s.now()
	var conds []string
	var args []any
	switch {
	case f.Anonymous:
		conds = append(conds, `owner_id IS NULL`)
	case f.Owner != "":
		conds, args = append(conds, `owner_id = (SELECT id FROM users WHERE username = ?)`), append(args, f.Owner)
	}
	if f.Active {
		conds, args = append(conds, `(state = 'uploading' OR (state = 'ready' AND expires_at > ?))`), append(args, db.Millis(now))
	}
	where := "1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	var total int
	if err := s.d.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM secrets WHERE `+where, args...).Scan(&total); err != nil { // #nosec G202 -- conditions are constants from this function
		return nil, 0, fmt.Errorf("secrets: admin count: %w", err)
	}
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT `+rowCols+`, coalesce((SELECT username FROM users WHERE users.id = secrets.owner_id), '')
FROM secrets WHERE `+where+` ORDER BY created_at DESC, id LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...) // #nosec G202 -- conditions are constants from this function
	if err != nil {
		return nil, 0, fmt.Errorf("secrets: admin list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Meta
	for rows.Next() {
		var owner string
		rw, err := scanRow(withOwner{rows, &owner})
		if err != nil {
			return nil, 0, fmt.Errorf("secrets: admin list: %w", err)
		}
		sm := s.summary(rw)
		m := Meta{
			ID: rw.ID, OwnerID: rw.OwnerID.Int64, OwnerName: owner, Kind: rw.Kind, Mode: rw.Mode, State: sm.State,
			Passphrase: sm.Passphrase, Size: sm.Size, ViewsUsed: sm.ViewsUsed, ViewsLeft: rw.ViewsLeft, MaxViews: rw.MaxViews,
			CreatedAt: sm.Created, ExpiresAt: sm.Expires, BurnReason: sm.BurnReason,
		}
		if rw.BurnedAt.Valid {
			t := db.Time(rw.BurnedAt.Int64)
			m.BurnedAt = &t
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("secrets: admin list: %w", err)
	}
	return out, total, nil
}

// withOwner scans rowCols followed by the owner's username.
type withOwner struct {
	sc    scanner
	owner *string
}

func (w withOwner) Scan(dest ...any) error { return w.sc.Scan(append(dest, w.owner)...) }

// CountActive returns the number of secrets that can still be opened.
func (s *Service) CountActive(ctx context.Context) (int, error) {
	var n int
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM secrets WHERE state = 'ready' AND expires_at > ?`, db.Millis(s.now())).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("secrets: count: %w", err)
	}
	return n, nil
}

// CountByOwner returns the number of secrets (in any state) each of owners has.
func (s *Service) CountByOwner(ctx context.Context, owners []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(owners))
	if len(owners) == 0 {
		return out, nil
	}
	args := make([]any, len(owners))
	for i, id := range owners {
		args[i] = id
	}
	q := `SELECT owner_id, count(*) FROM secrets WHERE owner_id IN (?` + strings.Repeat(", ?", len(owners)-1) + `) GROUP BY owner_id` // #nosec G202 -- only placeholders are added
	rows, err := s.d.DB.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("secrets: count: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("secrets: count: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

// RevokeOwnedBy burns every secret the user owns and deletes their files now
// (call before deleting the user; the sweeper retries files it could not
// delete). It returns how many secrets the user had.
func (s *Service) RevokeOwnedBy(ctx context.Context, owner int64) (int, error) {
	ids, err := s.ids(ctx, `SELECT id FROM secrets WHERE owner_id = ?`, owner)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, _, err := s.Revoke(ctx, id, owner); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
