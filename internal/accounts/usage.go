package accounts

import "context"

// The Usage section of Settings › Profile is read-only: four StatTiles, a
// joined StorageBar by kind and the items-by-type list.
//
// TODO(integration): Family F6 builds the shared queries and view types
// (internal/usageview on ui/f6-stats: Tile, StorageView, KindRow) and the
// partials stats.tiles, stats.storage and stats.bykind. The types below
// have the same fields so the wiring is one function: set Deps.Usage in
// internal/app to build a UsageView from usageview and the stats queries,
// and swap the markup in settings/profile.html for those partials. Until
// Deps.Usage is set the page shows no Usage section.

// UsageSpark is a StatTile background sparkline as two SVG path strings
// (viewBox "0 0 200 48"); both are empty for no data.
type UsageSpark struct{ Line, Area string }

// UsageTile is one StatTile: a label, a value with an optional small unit,
// an optional delta line and the sparkline.
type UsageTile struct {
	Label, Value, Small, Delta string
	Spark                      UsageSpark
}

// UsageSegment is one part of the storage bar. W is a percentage of the
// bar ("4.2"), Color a kit token ("var(--chart-1)").
type UsageSegment struct{ Key, Label, Size, W, Color string }

// UsageStorage is the joined StorageBar: against Quota when Limited, else
// against what is used.
type UsageStorage struct {
	Used, Quota, Pct, Free, Aria string
	Limited                      bool
	Segments                     []UsageSegment
	MaxFile                      string // the "Max file size" row
}

// UsageKind is one row of "Items by type"; Pct is the meter fill, 1-100.
type UsageKind struct {
	Label string
	N     int64
	Pct   int
}

// UsageView is everything the Usage section shows.
type UsageView struct {
	Tiles   []UsageTile
	Storage UsageStorage
	ByKind  []UsageKind
}

// usage returns the Usage section, or nil when none is wired.
func (s *Service) usage(ctx context.Context, userID int64) (*UsageView, error) {
	if s.d.Usage == nil {
		return nil, nil
	}
	return s.d.Usage(ctx, userID)
}
