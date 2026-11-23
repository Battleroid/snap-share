package statsui

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/usageview"
	"github.com/Battleroid/snap-share/internal/web"
)

// UserStatsPath is the "Your stats" page.
const UserStatsPath = "/dashboard/stats"

// userTopItems is how many rows the Top items table shows.
const userTopItems = 10

type topItemRow struct {
	ID          int64
	Name        string
	Kind        string // items.TypeLabel
	Picture     string // thumbnail or image URL; "" shows Icon
	Icon        string // kit icon name
	Views       int64
	Downloads   int64
	PlaysClicks int64
	LastOpened  *time.Time // nil: never
	Path        string     // the item's stats page
}

// secretMetric is one number of the Secrets strip.
type secretMetric struct {
	Label string
	Value string
	Warn  bool // tinted: wrong passphrases happened
	Spark usageview.Spark
}

type secretsBlock struct {
	Metrics []secretMetric
	Context string // "7 of 9 opened links were revealed"
}

// userView is the data of stats/user.
type userView struct {
	Days      int
	Ranges    []int
	Empty     bool // no items and no secrets
	Tiles     []tile
	Chart     Chart
	TopItems  []topItemRow
	Referrers []stats.Count
	Clients   []stats.Count
	Retention string // what to say about raw events' age
	Secrets   *secretsBlock
	Storage   usageview.StorageView
	ByKind    []usageview.KindRow
}

// userData is everything both the page and the API show.
type userData struct {
	From, To  time.Time // the window: whole UTC days, To is the start of the last day
	Days      int
	Totals    stats.Totals
	Daily     []stats.Day
	Top       []stats.ItemCount
	TopItems  []*items.Item // parallel to topRows: the items still there
	Referrers []stats.Count
	Clients   []stats.Count
	Secrets   []stats.SecretDay // nil without secrets (or without permission)
	HasSecret bool
	Used      int64
	Quota     int64
	Kinds     stats.StorageKinds
	Counts    []stats.Count
	NItems    int64
}

// loadUser reads everything for the signed-in owner. Reads are short queries
// on db.R; no rows are held across the other calls. withSecrets false leaves
// the secret counts out (an API key without secrets:read).
func (s *Service) loadUser(r *http.Request, userID int64, withSecrets bool) (*userData, error) {
	ctx := r.Context()
	db := s.d.DB.R
	d := &userData{Days: parseDays(r)}
	now := s.now().UTC()
	d.To = midnight(now)
	d.From = d.To.AddDate(0, 0, -(d.Days - 1))
	var err error
	if d.Totals, err = stats.UserTotals(ctx, db, userID, d.From, time.Time{}); err != nil {
		return nil, err
	}
	if d.Daily, err = stats.UserDaily(ctx, db, userID, d.From, now); err != nil {
		return nil, err
	}
	if d.Top, err = stats.UserTopItems(ctx, db, userID, d.From, time.Time{}, userTopItems); err != nil {
		return nil, err
	}
	for _, c := range d.Top {
		it, err := s.d.Items.Get(ctx, c.ItemID)
		if errors.Is(err, items.ErrNotFound) || (err == nil && it.OwnerID != userID) {
			it = nil // deleted since the query
		} else if err != nil {
			return nil, err
		}
		d.TopItems = append(d.TopItems, it)
	}
	if d.Referrers, err = stats.UserTopReferrers(ctx, db, userID, d.From, time.Time{}, topN); err != nil {
		return nil, err
	}
	if d.Clients, err = stats.UserTopUserAgents(ctx, db, userID, d.From, time.Time{}, topN); err != nil {
		return nil, err
	}
	all, _, err := stats.UserSecretCount(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	if withSecrets && all > 0 {
		d.HasSecret = true
		if d.Secrets, err = stats.UserSecretDaily(ctx, db, userID, d.From, now); err != nil {
			return nil, err
		}
	}
	if d.Used, d.Quota, err = s.d.Items.Usage(ctx, userID); err != nil {
		return nil, err
	}
	if d.Kinds, err = stats.UserStorageKinds(ctx, db, userID, d.Used); err != nil {
		return nil, err
	}
	if d.Counts, err = stats.UserItemCounts(ctx, db, userID); err != nil {
		return nil, err
	}
	for _, c := range d.Counts {
		d.NItems += c.N
	}
	return d, nil
}

// itemLead is the row lead for it, as on the dashboard: its thumbnail, an
// image itself, or the type icon (albums.Icon).
func itemLead(it *items.Item) (picture, icon string) {
	switch {
	case it.ThumbKey != "":
		picture = "/" + it.Slug + "/thumb"
	case items.ViewKind(it) == "image":
		picture = "/" + it.Slug + "/raw"
	}
	return picture, albums.Icon(it)
}

// userTiles are the six counters, summed over the owner's items. Visitors are
// left out: a visitor key belongs to one item, so they can't be added up.
func userTiles(d *userData) []tile {
	series := func(f func(stats.Day) int64) Spark {
		v := make([]int64, len(d.Daily))
		for i, day := range d.Daily {
			v[i] = f(day)
		}
		return sparkline(v)
	}
	t := d.Totals
	return []tile{
		mkTile("views", t.Views, series(func(x stats.Day) int64 { return x.Views })),
		mkTile("downloads", t.Downloads, series(func(x stats.Day) int64 { return x.Downloads })),
		mkTile("plays", t.Plays, series(func(x stats.Day) int64 { return x.Plays })),
		mkTile("clicks", t.Clicks, series(func(x stats.Day) int64 { return x.Clicks })),
		mkTile("previews", t.Previews, series(func(x stats.Day) int64 { return x.Previews })),
		mkTile("embeds", t.Embeds, series(func(x stats.Day) int64 { return x.Embeds })),
	}
}

func secretsFor(d *userData) *secretsBlock {
	if !d.HasSecret {
		return nil
	}
	sum := stats.SumSecrets(d.Secrets)
	series := func(f func(stats.SecretDay) int64) usageview.Spark {
		v := make([]int64, len(d.Secrets))
		for i, day := range d.Secrets {
			v[i] = f(day)
		}
		return usageview.NewMiniSpark(v)
	}
	b := &secretsBlock{Metrics: []secretMetric{
		{Label: "opened", Value: usageview.Count(sum.Opened), Spark: series(func(x stats.SecretDay) int64 { return x.Opened })},
		{Label: "revealed", Value: usageview.Count(sum.Revealed), Spark: series(func(x stats.SecretDay) int64 { return x.Revealed })},
		{Label: "failed", Value: usageview.Count(sum.Failed), Warn: sum.Failed > 0, Spark: series(func(x stats.SecretDay) int64 { return x.Failed })},
	}}
	switch {
	case sum.Opened > 0:
		b.Context = fmt.Sprintf("%d of %d opened %s %s revealed", sum.Revealed, sum.Opened, plural(sum.Opened, "link", "links"), plural(sum.Revealed, "was", "were"))
	default:
		b.Context = "No secret links were opened in this range."
	}
	return b
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// retentionNote says how far back raw events reach.
func (s *Service) retentionNote() string {
	if s.d.RetentionDays != nil {
		if n := s.d.RetentionDays(); n > 0 {
			return "Referrers, clients and secret events come from raw events, which are kept for " + strconv.Itoa(n) + " " + plural(int64(n), "day", "days") + "."
		}
	}
	return "Referrers, clients and secret events come from raw events, which are deleted after the retention period."
}

// userPage is GET /dashboard/stats: the owner's numbers across all their
// items and secrets, for the chosen range.
func (s *Service) userPage(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	d, err := s.loadUser(r, p.UserID, true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := userView{
		Days: d.Days, Ranges: ranges, Empty: d.NItems == 0 && !d.HasSecret,
		Tiles: userTiles(d), Referrers: d.Referrers, Clients: d.Clients, Retention: s.retentionNote(),
		Secrets: secretsFor(d), Storage: usageview.NewStorage(d.Used, d.Quota, d.Kinds), ByKind: usageview.NewKindRows(d.Counts),
	}
	views := make([]int64, len(d.Daily))
	days := make([]time.Time, len(d.Daily))
	for i, day := range d.Daily {
		days[i], views[i] = day.Day, day.Views
	}
	v.Chart = lineChart(days, views, fmt.Sprintf("views per day across all your items, last %d days, %d in total", d.Days, d.Totals.Views))
	for i, c := range d.Top {
		it := d.TopItems[i]
		if it == nil {
			continue
		}
		pic, icon := itemLead(it)
		row := topItemRow{
			ID: it.ID, Name: it.Name(), Kind: items.TypeLabel(it), Picture: pic, Icon: icon, Views: c.Views, Downloads: c.Downloads, PlaysClicks: c.PlaysClicks,
			LastOpened: it.LastOpened, Path: ItemStatsPath(it.ID),
		}
		v.TopItems = append(v.TopItems, row)
	}
	s.d.Render.Page(w, r, http.StatusOK, "stats/user", web.Page{Title: "Stats", NoIndex: true, Scripts: []string{"js/stats.js"}, Data: v})
}

type apiUserTotals struct {
	Views     int64 `json:"views"`
	Downloads int64 `json:"downloads"`
	Plays     int64 `json:"plays"`
	Clicks    int64 `json:"clicks"`
	Previews  int64 `json:"previews"`
	Embeds    int64 `json:"embeds"`
}

type apiUserDay struct {
	Day       string `json:"day"` // YYYY-MM-DD (UTC)
	Views     int64  `json:"views"`
	Downloads int64  `json:"downloads"`
	Plays     int64  `json:"plays"`
	Clicks    int64  `json:"clicks"`
	Previews  int64  `json:"previews"`
	Embeds    int64  `json:"embeds"`
}

type apiTopItem struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Views       int64  `json:"views"`
	Downloads   int64  `json:"downloads"`
	PlaysClicks int64  `json:"plays_clicks"`
}

type apiSecrets struct {
	Opened   int64 `json:"opened"`
	Revealed int64 `json:"revealed"`
	Failed   int64 `json:"failed"`
}

type apiStorage struct {
	Used   int64 `json:"used"`
	Quota  int64 `json:"quota"` // 0: no quota
	Images int64 `json:"images"`
	Video  int64 `json:"video"`
	Audio  int64 `json:"audio"`
	Other  int64 `json:"other"`
}

type apiUserStats struct {
	From        time.Time     `json:"from"`
	To          time.Time     `json:"to"`
	Days        int           `json:"days"`
	Totals      apiUserTotals `json:"totals"`
	Daily       []apiUserDay  `json:"daily"`
	TopItems    []apiTopItem  `json:"top_items"`
	Referrers   []apiCount    `json:"referrers"`
	Clients     []apiCount    `json:"clients"`
	Secrets     *apiSecrets   `json:"secrets"` // null without secrets:read, or with no secrets
	Storage     apiStorage    `json:"storage"`
	ItemsByType []apiCount    `json:"items_by_type"`
}

// apiUser is GET /api/v1/stats?days=7|30|90: the data of the Your stats page
// as JSON, for the principal's own items. Secret counts need secrets:read as
// well, because they describe secrets, not items.
func (s *Service) apiUser(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	d, err := s.loadUser(r, p.UserID, p.Can(auth.ScopeSecretsRead))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := apiUserStats{
		From: d.From, To: d.To.AddDate(0, 0, 1), Days: d.Days,
		Totals: apiUserTotals{Views: d.Totals.Views, Downloads: d.Totals.Downloads, Plays: d.Totals.Plays, Clicks: d.Totals.Clicks, Previews: d.Totals.Previews, Embeds: d.Totals.Embeds},
		Daily:  make([]apiUserDay, len(d.Daily)), TopItems: []apiTopItem{}, Referrers: apiCounts(d.Referrers), Clients: apiCounts(d.Clients),
		Storage:     apiStorage{Used: d.Used, Quota: d.Quota, Images: d.Kinds.Images, Video: d.Kinds.Video, Audio: d.Kinds.Audio, Other: d.Kinds.Other},
		ItemsByType: apiCounts(d.Counts),
	}
	for i, day := range d.Daily {
		out.Daily[i] = apiUserDay{Day: day.Day.Format(time.DateOnly), Views: day.Views, Downloads: day.Downloads, Plays: day.Plays, Clicks: day.Clicks, Previews: day.Previews, Embeds: day.Embeds}
	}
	for i, c := range d.Top {
		if it := d.TopItems[i]; it != nil {
			out.TopItems = append(out.TopItems, apiTopItem{ID: it.ID, Slug: it.Slug, Name: it.Name(), Kind: kindLabel(it), Views: c.Views, Downloads: c.Downloads, PlaysClicks: c.PlaysClicks})
		}
	}
	if d.HasSecret {
		sum := stats.SumSecrets(d.Secrets)
		out.Secrets = &apiSecrets{Opened: sum.Opened, Revealed: sum.Revealed, Failed: sum.Failed}
	}
	web.JSON(w, http.StatusOK, out)
}

func apiCounts(cs []stats.Count) []apiCount {
	out := make([]apiCount, len(cs))
	for i, c := range cs {
		out[i] = apiCount{Key: c.Key, Count: c.N}
	}
	return out
}
