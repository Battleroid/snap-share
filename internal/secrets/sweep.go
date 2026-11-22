package secrets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// sweepBatch bounds each sweeper query so the write lock is held briefly.
const sweepBatch = 500

// Sweep is the periodic cleanup (every minute):
//
//   - ready secrets past their expiry become expired;
//   - unfinalized secrets older than UploadTTL are deleted;
//   - expired tickets are deleted;
//   - files of burned, expired and revoked secrets, and of secrets whose row
//     is gone, are deleted once no live ticket needs them;
//   - tombstones older than TombstoneTTL are deleted with their events;
//   - events of secrets that no longer exist are deleted.
//
// It never deletes a file that a live ticket still needs.
func (s *Service) Sweep(ctx context.Context) error {
	return s.SweepAt(ctx, s.now())
}

// SweepAt runs Sweep as of now.
func (s *Service) SweepAt(ctx context.Context, now time.Time) error {
	ms := db.Millis(now)
	var errs []error

	// Expire. Their files go below, once no ticket needs them.
	if err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE secrets SET state = 'expired', burned_at = expires_at, burn_reason = ?,
  updated_at = ? WHERE state = 'ready' AND expires_at <= ?`, reasonExpired, ms, ms)
		return err
	}); err != nil {
		errs = append(errs, fmt.Errorf("expire: %w", err))
	}

	// Abandoned uploads: files first, then the row.
	ids, err := s.ids(ctx, `SELECT id FROM secrets WHERE state = 'uploading' AND created_at <= ? LIMIT ?`,
		db.Millis(now.Add(-UploadTTL)), sweepBatch)
	if err != nil {
		errs = append(errs, err)
	}
	for _, id := range ids {
		if err := s.purgeBlobs(ctx, id, purgeAll); err != nil {
			continue
		}
		if err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `DELETE FROM secrets WHERE id = ? AND state = 'uploading'`, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			return deleteRow(ctx, tx, id)
		}); err != nil {
			errs = append(errs, fmt.Errorf("abandoned upload: %w", err))
		}
	}

	if err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM secret_tickets WHERE expires_at <= ?`, ms)
		return err
	}); err != nil {
		errs = append(errs, fmt.Errorf("tickets: %w", err))
	}

	// Files nobody can read any more.
	ids, err = s.ids(ctx, `SELECT DISTINCT b.secret_id FROM secret_blobs b
LEFT JOIN secrets s ON s.id = b.secret_id
WHERE (s.id IS NULL OR s.state IN ('burned', 'expired', 'revoked'))
  AND NOT EXISTS (SELECT 1 FROM secret_tickets t WHERE t.secret_id = b.secret_id AND t.expires_at > ?)
LIMIT ?`, ms, sweepBatch)
	if err != nil {
		errs = append(errs, err)
	}
	for _, id := range ids {
		_ = s.purgeBlobs(ctx, id, purgeAll) // logged inside; retried next run
	}

	// Old tombstones, once their files are gone.
	ids, err = s.ids(ctx, `SELECT s.id FROM secrets s
WHERE s.state IN ('burned', 'expired', 'revoked') AND coalesce(s.burned_at, s.updated_at) <= ?
  AND NOT EXISTS (SELECT 1 FROM secret_blobs b WHERE b.secret_id = s.id)
LIMIT ?`, db.Millis(now.Add(-TombstoneTTL)), sweepBatch)
	if err != nil {
		errs = append(errs, err)
	}
	if len(ids) > 0 {
		if err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			for _, id := range ids {
				if err := deleteRow(ctx, tx, id); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("tombstones: %w", err))
		}
	}

	// Events left behind by secrets deleted with their owner.
	if err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM secret_events WHERE id IN (
  SELECT e.id FROM secret_events e WHERE NOT EXISTS (SELECT 1 FROM secrets s WHERE s.id = e.secret_id) LIMIT ?)`, sweepBatch)
		return err
	}); err != nil {
		errs = append(errs, fmt.Errorf("orphan events: %w", err))
	}

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("secrets: sweep: %w", err)
	}
	return nil
}

func (s *Service) ids(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.d.DB.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("secrets: sweep query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("secrets: sweep query: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("secrets: sweep query: %w", err)
	}
	return out, nil
}
