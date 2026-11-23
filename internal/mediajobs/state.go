package mediajobs

import (
	"context"
	"fmt"
	"strings"

	"github.com/Battleroid/snap-share/internal/items"
)

// stateBatch bounds the ids in one lookup (SQLite's variable limit is far
// higher; pages hold at most a few dozen items).
const stateBatch = 500

// JobStates is items.MediaJobStates: for each item id it finds the newest
// media.process job and reports MediaPending while it is queued or running
// (a retry waiting out its backoff is queued) and MediaFailed once it has
// failed for good. Items whose newest job is done, or that have none, get no
// entry. It reads the jobs table only, so no column on items is needed.
func (s *Service) JobStates(ctx context.Context, ids []int64) (map[int64]items.MediaState, error) {
	out := map[int64]items.MediaState{}
	for len(ids) > 0 {
		n := min(len(ids), stateBatch)
		if err := s.jobStates(ctx, ids[:n], out); err != nil {
			return nil, err
		}
		ids = ids[n:]
	}
	return out, nil
}

func (s *Service) jobStates(ctx context.Context, ids []int64, out map[int64]items.MediaState) error {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, JobKind)
	for _, id := range ids {
		args = append(args, id)
	}
	// Oldest first, so a later job of the same item overrides an earlier one.
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT json_extract(payload, '$.id'), state FROM jobs
		WHERE kind = ? AND json_extract(payload, '$.id') IN (`+marks+`) ORDER BY id`, args...) // #nosec G202 -- marks is only "?" placeholders
	if err != nil {
		return fmt.Errorf("media job states: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id    int64
			state string
		)
		if err := rows.Scan(&id, &state); err != nil {
			return fmt.Errorf("media job states: %w", err)
		}
		switch state {
		case "queued", "running":
			out[id] = items.MediaPending
		case "failed":
			out[id] = items.MediaFailed
		default:
			delete(out, id)
		}
	}
	return rows.Err()
}
