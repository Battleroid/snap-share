package statsui

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/web"
)

const (
	defaultRangeDays = 30
	maxEventsLimit   = 1000
)

type apiItem struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

type apiOpened struct {
	FirstOpenedAt *time.Time `json:"first_opened_at"`
	LastOpenedAt  *time.Time `json:"last_opened_at"`
	OpenedCount   int64      `json:"opened_count"`
	PreviewedAt   *time.Time `json:"previewed_at"`
}

type apiTotals struct {
	Views     int64 `json:"views"`
	Uniques   int64 `json:"uniques"`
	Previews  int64 `json:"previews"`
	Embeds    int64 `json:"embeds"`
	Downloads int64 `json:"downloads"`
	Clicks    int64 `json:"clicks"`
	Plays     int64 `json:"plays"`
}

type apiDay struct {
	Day         string `json:"day"` // YYYY-MM-DD (UTC)
	Views       int64  `json:"views"`
	Uniques     int64  `json:"uniques"`
	NewVisitors int64  `json:"new_visitors"`
	Previews    int64  `json:"previews"`
	Embeds      int64  `json:"embeds"`
	Downloads   int64  `json:"downloads"`
	Clicks      int64  `json:"clicks"`
	Plays       int64  `json:"plays"`
}

type apiCount struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type apiStatsResp struct {
	Item      apiItem    `json:"item"`
	From      time.Time  `json:"from"`
	To        time.Time  `json:"to"`
	Opened    apiOpened  `json:"opened"`
	Totals    apiTotals  `json:"totals"`
	Daily     []apiDay   `json:"daily"`
	Referrers []apiCount `json:"referrers"`
	Bots      []apiCount `json:"bots"`
	Browsers  []apiCount `json:"browsers"`
}

type apiEvent struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	UASummary string    `json:"ua_summary"`
	Bot       string    `json:"bot"`
	Referrer  string    `json:"referrer"`
}

var errBadRange = errors.New("bad range")

// parseBound reads a date (YYYY-MM-DD, UTC) or an RFC 3339 time. A date
// as the upper bound includes that whole day.
func parseBound(v string, upper bool) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		if upper {
			t = t.AddDate(0, 0, 1)
		}
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, errBadRange
	}
	return t.UTC(), nil
}

// parseRange returns [from, to). Defaults: the last 30 days up to now.
func parseRange(r *http.Request, now time.Time) (from, to time.Time, err error) {
	q := r.URL.Query()
	to = now.UTC()
	if v := q.Get("to"); v != "" {
		if to, err = parseBound(v, true); err != nil {
			return
		}
	}
	from = midnight(to.Add(-time.Millisecond)).AddDate(0, 0, -(defaultRangeDays - 1))
	if v := q.Get("from"); v != "" {
		if from, err = parseBound(v, false); err != nil {
			return
		}
	}
	if !from.Before(to) || to.Sub(from) > stats.MaxSeriesDays*24*time.Hour {
		err = errBadRange
	}
	return
}

func counts(in []stats.Count) []apiCount {
	out := make([]apiCount, 0, len(in))
	for _, c := range in {
		out = append(out, apiCount{Key: c.Key, Count: c.N})
	}
	return out
}

func (s *Service) badRange(w http.ResponseWriter, r *http.Request) {
	s.d.Errors.Error(w, r, http.StatusBadRequest,
		"from and to must be dates (YYYY-MM-DD) or RFC 3339 times, from before to, at most 1000 days apart.")
}

// apiStats is GET /api/v1/items/{id}/stats?from&to (items:read, owner only).
func (s *Service) apiStats(w http.ResponseWriter, r *http.Request) {
	it, acc, ok := s.loadItem(w, r, false)
	if !ok || acc != accessOwner {
		return
	}
	from, to, err := parseRange(r, s.now())
	if err != nil {
		s.badRange(w, r)
		return
	}
	ctx := r.Context()
	db := s.d.DB.R
	t, err := stats.ItemTotals(ctx, db, it.ID, from, to)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	daily, err := stats.ItemDaily(ctx, db, it.ID, from, to.Add(-time.Millisecond))
	if errors.Is(err, stats.ErrRange) {
		s.badRange(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	refs, err := stats.TopReferrers(ctx, db, it.ID, from, to, topN)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	bots, err := stats.TopBots(ctx, db, it.ID, from, to, topN)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	uas, err := stats.TopUserAgents(ctx, db, it.ID, from, to, topN)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := apiStatsResp{
		Item: apiItem{ID: it.ID, Slug: it.Slug, Name: it.Name(), Kind: string(it.Kind), URL: s.d.Items.PublicURL(it.Slug)},
		From: from, To: to,
		Opened: apiOpened{FirstOpenedAt: it.FirstOpened, LastOpenedAt: it.LastOpened, OpenedCount: it.OpenedCount, PreviewedAt: it.PreviewedAt},
		Totals: apiTotals{Views: t.Views, Uniques: t.Uniques, Previews: t.Previews, Embeds: t.Embeds,
			Downloads: t.Downloads, Clicks: t.Clicks, Plays: t.Plays},
		Daily:     make([]apiDay, 0, len(daily)),
		Referrers: counts(refs), Bots: counts(bots), Browsers: counts(uas),
	}
	for _, d := range daily {
		out.Daily = append(out.Daily, apiDay{Day: d.Day.Format(time.DateOnly), Views: d.Views, Uniques: d.Uniques,
			NewVisitors: d.NewVisitors, Previews: d.Previews, Embeds: d.Embeds, Downloads: d.Downloads, Clicks: d.Clicks, Plays: d.Plays})
	}
	web.JSON(w, http.StatusOK, out)
}

// apiEvents is GET /api/v1/items/{id}/events?before&limit (items:read,
// owner only): raw events, newest first, with IPs and User-Agents.
// next_before is the cursor for the next page, null at the end.
func (s *Service) apiEvents(w http.ResponseWriter, r *http.Request) {
	it, acc, ok := s.loadItem(w, r, false)
	if !ok || acc != accessOwner {
		return
	}
	q := r.URL.Query()
	limit := 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxEventsLimit {
			s.d.Errors.Error(w, r, http.StatusBadRequest, "limit must be 1 to 1000.")
			return
		}
		limit = n
	}
	var before int64
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			s.d.Errors.Error(w, r, http.StatusBadRequest, "before must be an event id.")
			return
		}
		before = n
	}
	evs, err := stats.ItemEvents(r.Context(), s.d.DB.R, it.ID, stats.EventPage{BeforeID: before, Limit: limit})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := make([]apiEvent, 0, len(evs))
	for _, e := range evs {
		out = append(out, apiEvent{ID: e.ID, At: e.At, Kind: string(e.Kind), IP: e.IP, UserAgent: e.UA,
			UASummary: e.UASummary, Bot: e.Bot, Referrer: e.RefererHost})
	}
	var next *int64
	if len(evs) == limit {
		id := evs[len(evs)-1].ID
		next = &id
	}
	web.JSON(w, http.StatusOK, map[string]any{"events": out, "next_before": next})
}
