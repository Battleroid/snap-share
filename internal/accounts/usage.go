package accounts

import (
	"context"

	"github.com/Battleroid/snap-share/internal/usageview"
)

// UsageView is the read-only Usage section of Settings › Profile: four
// StatTiles, the storage bar by kind with the max file size, and the
// items-by-type list. It is drawn with the stats.* partials, from the same
// usageview constructors as "Your stats". internal/app builds it (Deps.Usage).
type UsageView struct {
	Tiles   []usageview.Tile
	Storage usageview.StorageView
	MaxFile string // the "Max file size" row
	ByKind  []usageview.KindRow
}

// usage returns the Usage section, or nil when none is wired.
func (s *Service) usage(ctx context.Context, userID int64) (*UsageView, error) {
	if s.d.Usage == nil {
		return nil, nil
	}
	return s.d.Usage(ctx, userID)
}
