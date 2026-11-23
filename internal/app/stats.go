package app

// media+stats: item visitor events share the stats writer and visitor
// key with secrets; raw events are purged after the retention period.

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"time"

	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/stats"
)

// StatsRetentionDays is how long raw visitor events (with IPs and user
// agents) are kept. Daily totals stay for the item's lifetime.
var StatsRetentionDays = settings.Define("stats.retention_days", 90, settings.Range(1, 3650))

// visitorSecret is the stats visitor-key secret, shared by item and secret
// events (the secrets package derives the same key).
func visitorSecret(key []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, key, nil, "snap stats visitor key", 32)
}

// PurgeStats deletes raw visitor events past the retention period.
func (a *App) PurgeStats(ctx context.Context) error {
	days := StatsRetentionDays.Get(a.Settings)
	n, err := stats.Purge(ctx, a.DB.W, time.Now().AddDate(0, 0, -days))
	if n > 0 {
		a.Log.Info("stats purged", "events", n, "retention_days", days)
	}
	return err
}
