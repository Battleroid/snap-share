package app

import (
	"context"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/usageview"
	"github.com/Battleroid/snap-share/internal/web"
)

// usageDays is the window of the Usage tiles' sparklines and counts.
const usageDays = 30

// profileUsage builds the Usage section of Settings › Profile for userID
// from the user-level stats queries (short reads on db.R) and the items
// service's usage and limits.
func (a *App) profileUsage(ctx context.Context, userID int64) (*accounts.UsageView, error) {
	db := a.DB.R
	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(usageDays - 1))

	used, quota, maxFile, err := a.Items.Limits(ctx, userID)
	if err != nil {
		return nil, err
	}
	kinds, err := stats.UserStorageKinds(ctx, db, userID, used)
	if err != nil {
		return nil, err
	}
	counts, err := stats.UserItemCounts(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	added, err := stats.UserBytesAddedDaily(ctx, db, userID, from, now)
	if err != nil {
		return nil, err
	}
	created, err := stats.UserItemsCreatedDaily(ctx, db, userID, from, now)
	if err != nil {
		return nil, err
	}
	_, active, err := stats.UserSecretCount(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	secrets, err := stats.UserSecretDaily(ctx, db, userID, from, now)
	if err != nil {
		return nil, err
	}
	totals, err := stats.UserTotals(ctx, db, userID, from, time.Time{})
	if err != nil {
		return nil, err
	}
	daily, err := stats.UserDaily(ctx, db, userID, from, now)
	if err != nil {
		return nil, err
	}

	var items, newItems, opened int64
	for _, c := range counts {
		items += c.N
	}
	series := func(n int, f func(int) int64) usageview.Spark {
		v := make([]int64, n)
		for i := range v {
			v[i] = f(i)
		}
		return usageview.NewSpark(v)
	}
	for _, d := range created {
		newItems += d.N
	}
	for _, d := range secrets {
		opened += d.Opened
	}
	w := " · " + itoa(usageDays) + "d"

	storage := usageview.Tile{Label: "storage used", Spark: series(len(added), func(i int) int64 { return added[i].N })}
	storage.Value, storage.Small, _ = strings.Cut(web.HumanBytes(used), " ")
	if quota > 0 {
		storage.Delta = "of " + web.HumanBytes(quota)
	}
	return &accounts.UsageView{
		Tiles: []usageview.Tile{
			storage,
			{Label: "items", Value: usageview.Count(items), Delta: "+" + usageview.Count(newItems) + w,
				Spark: series(len(created), func(i int) int64 { return created[i].N })},
			{Label: "secrets · active", Value: usageview.Count(active), Delta: usageview.Count(opened) + " opened" + w,
				Spark: series(len(secrets), func(i int) int64 { return secrets[i].Opened })},
			{Label: "views" + w, Value: usageview.Count(totals.Views),
				Spark: series(len(daily), func(i int) int64 { return daily[i].Views })},
		},
		Storage: usageview.NewStorage(used, quota, kinds),
		MaxFile: web.HumanBytes(maxFile),
		ByKind:  usageview.NewKindRows(counts),
	}, nil
}

func itoa(n int) string { return usageview.Count(int64(n)) }
