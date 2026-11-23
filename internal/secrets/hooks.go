package secrets

// Owner hooks for statsui and notify: the secret timeline page
// (/dashboard/secrets/{id}/timeline) and its notify switch.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// ErrNotFound is returned by the owner hooks for a missing secret or one
// that belongs to someone else.
var ErrNotFound = errNotFound

// OwnerSecret returns the owner's view of one of their secrets.
func (s *Service) OwnerSecret(ctx context.Context, owner int64, id string) (Summary, error) {
	if !validID(id) {
		return Summary{}, ErrNotFound
	}
	rw, err := load(ctx, s.d.DB.R, id)
	if err != nil {
		return Summary{}, err
	}
	if !rw.OwnerID.Valid || rw.OwnerID.Int64 != owner {
		return Summary{}, ErrNotFound
	}
	return s.summary(rw), nil
}

// SetNotify turns reveal notifications on or off for one of the owner's
// secrets (the secrets.notify flag; burns after failed passphrase attempts
// are always reported).
func (s *Service) SetNotify(ctx context.Context, owner int64, id string, on bool) error {
	if !validID(id) {
		return ErrNotFound
	}
	flag := 0
	if on {
		flag = 1
	}
	res, err := s.d.DB.W.ExecContext(ctx, `UPDATE secrets SET notify = ?, updated_at = ? WHERE id = ? AND owner_id = ?`,
		flag, db.Millis(time.Now()), id, owner)
	if err != nil {
		return fmt.Errorf("secrets: set notify: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(ErrNotFound, err)
	}
	return nil
}
