package secrets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/web"
)

// Secret ciphertext counts against its owner's storage quota, with the same
// semantics as items (internal/items/quota.go): users.quota_bytes, or
// uploads.default_quota_bytes when NULL, 0 meaning unlimited; usage in
// users.used_bytes and users.reserved_bytes. A secret's own quota_reserved
// and quota_used columns say what it holds, so every path that deletes its
// content releases exactly that once, and the items reconcile job can
// recompute users' usage from them.
//
//   - create (E2E): ct_size is reserved;
//   - sealed: each stored chunk is reserved as it arrives;
//   - finalize: the reservation becomes usage;
//   - content deleted (last view, burn, expiry, revoke, sweep, abandoned
//     upload): released.
//
// Anonymous secrets have no owner; their columns add up to the live
// anonymous total that secrets.anonymous_max_total_bytes caps.

var (
	errQuota       = web.Errorf(http.StatusRequestEntityTooLarge, "That would put you over your storage quota.")
	errNoAccount   = web.Errorf(http.StatusForbidden, "This account can't create secrets.")
	errAnonFull    = web.Errorf(http.StatusServiceUnavailable, "Too many anonymous secrets are stored right now. Try again later, or sign in.")
	errAnonPerHour = web.Errorf(http.StatusTooManyRequests, "Too many anonymous secrets were created in the last hour. Try again later, or sign in.")
)

// userLimits are a user's upload size limit, quota and usage.
type userLimits struct {
	maxUpload int64 // per secret, plaintext; 0: no limit beyond the secret limits
	quota     int64 // 0: unlimited
	used      int64
	reserved  int64
}

func (l userLimits) remaining() int64 {
	if l.quota <= 0 {
		return 1 << 62
	}
	return max(l.quota-l.used-l.reserved, 0)
}

// sizeLimit lowers limit to the user's and the API key's upload limits.
func (l userLimits) sizeLimit(p *auth.Principal, limit int64) int64 {
	if l.maxUpload > 0 && l.maxUpload < limit {
		limit = l.maxUpload
	}
	return p.Constraints().UploadLimit(limit)
}

func (s *Service) userLimits(ctx context.Context, q querier, userID int64) (userLimits, error) {
	var (
		quota, maxUp   sql.NullInt64
		used, reserved int64
		status         string
	)
	err := q.QueryRowContext(ctx, `SELECT quota_bytes, max_upload_bytes, used_bytes, reserved_bytes, status FROM users WHERE id = ?`, userID).
		Scan(&quota, &maxUp, &used, &reserved, &status)
	if errors.Is(err, sql.ErrNoRows) || status == "disabled" {
		return userLimits{}, errNoAccount
	}
	if err != nil {
		return userLimits{}, fmt.Errorf("secrets: user limits: %w", err)
	}
	l := userLimits{used: used, reserved: reserved}
	if s.d.DefaultQuota != nil {
		l.quota = s.d.DefaultQuota()
	}
	if quota.Valid {
		l.quota = quota.Int64
	}
	if maxUp.Valid && maxUp.Int64 > 0 {
		l.maxUpload = maxUp.Int64
	}
	return l, nil
}

// reserveTx charges n more bytes to an uploading secret: to its owner's
// reservation (failing with errQuota past the quota), or to the anonymous
// total. The secret must still be uploading.
func (s *Service) reserveTx(ctx context.Context, tx *sql.Tx, id string, owner sql.NullInt64, n int64) error {
	if owner.Valid {
		l, err := s.userLimits(ctx, tx, owner.Int64)
		if err != nil {
			return err
		}
		if n > l.remaining() {
			return errQuota
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE secrets SET quota_reserved = quota_reserved + ? WHERE id = ? AND state = 'uploading'`, n, id)
	if err != nil {
		return fmt.Errorf("secrets: reserve: %w", err)
	}
	if k, err := res.RowsAffected(); err != nil || k != 1 {
		return errors.Join(err, errNotFound)
	}
	if owner.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET reserved_bytes = reserved_bytes + ? WHERE id = ?`, n, owner.Int64); err != nil {
			return fmt.Errorf("secrets: reserve: %w", err)
		}
	}
	return nil
}

// charge is what a secret holds against its owner's quota.
type charge struct {
	owner          sql.NullInt64
	reserved, used int64
}

func loadCharge(ctx context.Context, tx *sql.Tx, id string) (charge, bool, error) {
	var c charge
	err := tx.QueryRowContext(ctx, `SELECT owner_id, quota_reserved, quota_used FROM secrets WHERE id = ?`, id).
		Scan(&c.owner, &c.reserved, &c.used)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, fmt.Errorf("secrets: charge: %w", err)
	}
	return c, true, nil
}

// settleTx turns a finalized secret's reservation into usage.
func settleTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) error {
	c, ok, err := loadCharge(ctx, tx, id)
	if err != nil || !ok || c.reserved == 0 {
		return err
	}
	if c.owner.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET reserved_bytes = max(reserved_bytes - ?, 0), used_bytes = used_bytes + ?,
  updated_at = ? WHERE id = ?`, c.reserved, c.reserved, db.Millis(now), c.owner.Int64); err != nil {
			return fmt.Errorf("secrets: settle: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE secrets SET quota_used = quota_used + quota_reserved, quota_reserved = 0 WHERE id = ?`, id)
	return err
}

// releaseTx gives back whatever a secret holds. It is idempotent: the
// secret's columns drop to 0 in the same transaction.
func releaseTx(ctx context.Context, tx *sql.Tx, id string) error {
	c, ok, err := loadCharge(ctx, tx, id)
	if err != nil || !ok || (c.reserved == 0 && c.used == 0) {
		return err
	}
	if c.owner.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET reserved_bytes = max(reserved_bytes - ?, 0),
  used_bytes = max(used_bytes - ?, 0) WHERE id = ?`, c.reserved, c.used, c.owner.Int64); err != nil {
			return fmt.Errorf("secrets: release: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE secrets SET quota_reserved = 0, quota_used = 0 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("secrets: release: %w", err)
	}
	return nil
}

// checkAnonymous enforces the site-wide anonymous caps inside the create
// transaction: creates in the last hour, and live ciphertext.
func (s *Service) checkAnonymous(ctx context.Context, tx *sql.Tx, ctSize int64, now time.Time) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM secrets WHERE owner_id IS NULL AND created_at > ?`,
		db.Millis(now.Add(-time.Hour))).Scan(&n); err != nil {
		return fmt.Errorf("secrets: anonymous count: %w", err)
	}
	if n >= AnonymousPerHour.Get(s.d.Settings) {
		return errAnonPerHour
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(quota_reserved + quota_used), 0) FROM secrets
WHERE owner_id IS NULL AND (quota_reserved > 0 OR quota_used > 0)`).Scan(&total); err != nil {
		return fmt.Errorf("secrets: anonymous total: %w", err)
	}
	if total+ctSize > AnonymousMaxTotalBytes.Get(s.d.Settings) {
		return errAnonFull
	}
	return nil
}
