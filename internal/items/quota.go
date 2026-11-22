package items

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// Site settings for uploads.
var (
	MaxUploadBytes    = settings.Define("uploads.max_bytes", int64(1_000_000_000), settings.Range[int64](1, 1<<50))
	DefaultQuotaBytes = settings.Define("uploads.default_quota_bytes", int64(0), settings.Range[int64](0, 1<<60))
	BlockedExtensions = settings.Define("uploads.blocked_extensions", []string{}, validList)
	BlockedTypes      = settings.Define("uploads.blocked_types", []string{}, validList)
	MaxExpiryDays     = settings.Define("items.max_expiry_days", 0, settings.Range(0, 36500))
)

func validList(l []string) error {
	if len(l) > 500 {
		return errors.New("at most 500 entries")
	}
	for _, s := range l {
		if s == "" || len(s) > 128 {
			return fmt.Errorf("entry %q must be 1-128 characters", s)
		}
	}
	return nil
}

// PartSize is the chunk size the browser upload API asks for.
const PartSize = 8 << 20

// maxPasteBytes caps pastes, which are read into memory.
const maxPasteBytes = 16 << 20

// UploadTTL is how long an upload may sit idle before it is reaped.
const UploadTTL = 24 * time.Hour

type limits struct {
	maxUpload int64 // per file
	quota     int64 // 0 = unlimited
	used      int64
	reserved  int64
}

func (l limits) remaining() int64 {
	if l.quota <= 0 {
		return 1 << 62
	}
	return max(l.quota-l.used-l.reserved, 0)
}

// userLimits reads a user's size limit, quota and usage.
func (s *Service) userLimits(ctx context.Context, q querier, userID int64) (limits, error) {
	var (
		quota, maxUp   sql.NullInt64
		used, reserved int64
		status         string
	)
	err := q.QueryRowContext(ctx, `SELECT quota_bytes, max_upload_bytes, used_bytes, reserved_bytes, status FROM users WHERE id = ?`, userID).
		Scan(&quota, &maxUp, &used, &reserved, &status)
	if errors.Is(err, sql.ErrNoRows) || status == "disabled" {
		return limits{}, errNoAccount
	}
	if err != nil {
		return limits{}, err
	}
	l := limits{maxUpload: MaxUploadBytes.Get(s.d.Settings), quota: DefaultQuotaBytes.Get(s.d.Settings), used: used, reserved: reserved}
	if maxUp.Valid && maxUp.Int64 > 0 && maxUp.Int64 < l.maxUpload {
		l.maxUpload = maxUp.Int64
	}
	if quota.Valid {
		l.quota = quota.Int64
	}
	// keys track: an API key's upload cap lowers the limit for the requests it makes.
	if p := auth.FromContext(ctx); p != nil && p.UserID == userID {
		l.maxUpload = p.Constraints().UploadLimit(l.maxUpload)
	}
	return l, nil
}

func tooLarge(max int64) error {
	return web.Errorf(http.StatusRequestEntityTooLarge, "The file is over the %s limit.", web.HumanBytes(max))
}

// reserveTx charges want bytes to the user's reservation. With exact false it
// grants what is left of the quota (up to want) instead of failing.
func (s *Service) reserveTx(ctx context.Context, tx *sql.Tx, userID, want int64, exact bool) (int64, error) {
	l, err := s.userLimits(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	left := l.remaining()
	grant := want
	if want > left {
		if exact || left <= 0 {
			return 0, errQuota
		}
		grant = left
	}
	if grant > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET reserved_bytes = reserved_bytes + ? WHERE id = ?`, grant, userID); err != nil {
			return 0, err
		}
	}
	return grant, nil
}

// settleTx turns a reservation into usage of actual bytes.
func settleTx(ctx context.Context, tx *sql.Tx, userID, reserved, actual int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE users SET reserved_bytes = max(reserved_bytes - ?, 0), used_bytes = used_bytes + ?, updated_at = ? WHERE id = ?`,
		reserved, actual, db.Millis(now), userID)
	return err
}

func releaseTx(ctx context.Context, tx *sql.Tx, userID, reserved int64) error {
	if reserved <= 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE users SET reserved_bytes = max(reserved_bytes - ?, 0) WHERE id = ?`, reserved, userID)
	return err
}

func normList(l []string, trim string) []string {
	out := make([]string, 0, len(l))
	for _, s := range l {
		s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), trim)))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// blockedName reports whether the filename's extension is blocked.
func (s *Service) blockedName(name string) bool {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	return ext != "" && slices.Contains(normList(BlockedExtensions.Get(s.d.Settings), "."), ext)
}

// blockedType reports whether a MIME type (or its "type/*" family) is blocked.
func (s *Service) blockedType(mime string) bool {
	mime = strings.ToLower(mime)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	for _, b := range normList(BlockedTypes.Get(s.d.Settings), "") {
		if b == mime || (strings.HasSuffix(b, "/*") && strings.HasPrefix(mime, strings.TrimSuffix(b, "*"))) {
			return true
		}
	}
	return false
}

// Reconcile recomputes every user's used and reserved bytes from items and
// uploads, plus what live one-time secrets hold (secrets.quota_used and
// quota_reserved, kept by internal/secrets), and drops search rows whose
// item is gone.
func (s *Service) Reconcile(ctx context.Context) error {
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET
			used_bytes = (SELECT coalesce(sum(size), 0) FROM items WHERE items.owner_id = users.id AND kind IN ('file', 'paste'))
				+ (SELECT coalesce(sum(quota_used), 0) FROM secrets WHERE secrets.owner_id = users.id),
			reserved_bytes = (SELECT coalesce(sum(reserved), 0) FROM uploads WHERE uploads.owner_id = users.id)
				+ (SELECT coalesce(sum(quota_reserved), 0) FROM secrets WHERE secrets.owner_id = users.id)`); err != nil {
			return fmt.Errorf("reconcile usage: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM items_fts WHERE rowid NOT IN (SELECT id FROM items)`); err != nil {
			return fmt.Errorf("reconcile search: %w", err)
		}
		return nil
	})
}
