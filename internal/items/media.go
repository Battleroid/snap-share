package items

// Hooks for media+stats: content replacement (JPEG location
// stripping) and media meta for share pages.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// ReplaceContent swaps a file item's content for a rewritten copy already
// committed under newKey (JPEG location stripping). Size and SHA-256 change,
// the owner's usage moves by the size difference, and the old blob is
// deleted. If the item is gone or its content is no longer oldKey, newKey is
// deleted and ErrNotFound returned.
func (s *Service) ReplaceContent(ctx context.Context, id int64, oldKey, newKey string, size int64, sum string) error {
	ctx = context.WithoutCancel(ctx)
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var (
			owner, oldSize int64
			kind           Kind
		)
		err := tx.QueryRowContext(ctx, `SELECT owner_id, kind, size FROM items WHERE id = ? AND blob_key = ?`, id, oldKey).
			Scan(&owner, &kind, &oldSize)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		now := db.Millis(s.now())
		if _, err := tx.ExecContext(ctx, `UPDATE items SET blob_key = ?, size = ?, sha256 = ?, updated_at = ? WHERE id = ?`,
			newKey, size, nullStr(sum), now, id); err != nil {
			return err
		}
		if (kind == KindFile || kind == KindPaste) && size != oldSize {
			_, err = tx.ExecContext(ctx, `UPDATE users SET used_bytes = max(used_bytes + ?, 0), updated_at = ? WHERE id = ?`,
				size-oldSize, now, owner)
		}
		return err
	})
	if err != nil {
		_ = s.d.Blobs.Delete(ctx, newKey)
		return fmt.Errorf("replace content: %w", err)
	}
	if err := s.d.Blobs.Delete(ctx, oldKey); err != nil {
		s.log.Warn("delete replaced blob", "item", id, "err", err)
	}
	return nil
}

// mediaMeta is appended to the share page's type: " · 1920×1080 · 1:02".
func mediaMeta(it *Item) string {
	var b strings.Builder
	if it.Width > 0 && it.Height > 0 {
		fmt.Fprintf(&b, " · %d×%d", it.Width, it.Height)
	}
	if it.DurationMS > 0 {
		b.WriteString(" · " + clock(time.Duration(it.DurationMS)*time.Millisecond))
	}
	return b.String()
}

// clock formats a duration as m:ss or h:mm:ss, rounding to the second
// (never down to 0:00).
func clock(d time.Duration) string {
	sec := max(int64(math.Round(d.Seconds())), 1)
	h, m, ss := sec/3600, sec/60%60, sec%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, ss)
	}
	return fmt.Sprintf("%d:%02d", m, ss)
}
