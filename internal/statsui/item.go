package statsui

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/web"
)

// Page sizes.
const (
	eventsPerPage = 50
	topN          = 10
	csvBatch      = 1000
)

// Ranges offered on the page, in days.
var ranges = []int{7, 30, 90}

type tile struct {
	Label string
	Value int64
	Spark Spark
}

type eventRow struct {
	When     string // UTC, to the second
	Kind     string
	IP       string
	Client   string
	Referrer string
	Bot      string
}

type overrideOption struct {
	Value, Label string
}

var overrideOptions = []overrideOption{
	{"default", "default"}, {"none", "off"}, {"first", "first open"}, {"every", "every open"},
}

type itemView struct {
	Item      *items.Item
	Name      string
	Kind      string
	URL       string
	Owner     bool // false: an admin viewing someone else's item (totals only)
	Days      int
	Ranges    []int
	Tiles     []tile
	Chart     Chart
	Referrers []stats.Count
	Bots      []stats.Count
	Browsers  []stats.Count
	Events    []eventRow
	Total     int64
	Page      int
	Pages     int
	First     int
	Last      int
	PageHref  string
	CSV       string

	Notify       bool   // a channel is on: show the override
	Override     string // default | none | first | every
	Overrides    []overrideOption
	NotifyAction string
}

func kindLabel(it *items.Item) string {
	switch {
	case it.Kind != items.KindFile:
		return string(it.Kind)
	case strings.HasPrefix(it.MIME, "image/"):
		return "image"
	case strings.HasPrefix(it.MIME, "video/"):
		return "video"
	case strings.HasPrefix(it.MIME, "audio/"):
		return "audio"
	}
	return "file"
}

// tilesFor picks the counters that mean something for the item's kind.
func tilesFor(it *items.Item, days int, t stats.Totals, daily []stats.Day) []tile {
	series := func(f func(stats.Day) int64) Spark {
		v := make([]int64, len(daily))
		for i, d := range daily {
			v[i] = f(d)
		}
		return sparkline(v)
	}
	w := " · " + strconv.Itoa(days) + "d"
	out := []tile{
		{"views" + w, t.Views, series(func(d stats.Day) int64 { return d.Views })},
		{"visitors" + w, t.Uniques, series(func(d stats.Day) int64 { return d.Uniques })},
		{"previews" + w, t.Previews, series(func(d stats.Day) int64 { return d.Previews })},
	}
	av := strings.HasPrefix(it.MIME, "video/") || strings.HasPrefix(it.MIME, "audio/")
	switch it.Kind {
	case items.KindLink:
		out = append(out, tile{"clicks" + w, t.Clicks, series(func(d stats.Day) int64 { return d.Clicks })})
	case items.KindGist: // its files and the zip
		out = append(out, tile{"downloads" + w, t.Downloads, series(func(d stats.Day) int64 { return d.Downloads })})
	case items.KindFile:
		out = append(out, tile{"downloads" + w, t.Downloads, series(func(d stats.Day) int64 { return d.Downloads })})
		if av {
			out = append(out, tile{"plays" + w, t.Plays, series(func(d stats.Day) int64 { return d.Plays })})
		}
		if av || strings.HasPrefix(it.MIME, "image/") {
			out = append(out, tile{"embeds" + w, t.Embeds, series(func(d stats.Day) int64 { return d.Embeds })})
		}
	}
	return out
}

func parseDays(r *http.Request) int {
	d, _ := strconv.Atoi(r.URL.Query().Get("days"))
	for _, v := range ranges {
		if d == v {
			return d
		}
	}
	return 30
}

// itemPage is GET /dashboard/items/{id}/stats.
func (s *Service) itemPage(w http.ResponseWriter, r *http.Request) {
	it, acc, ok := s.loadItem(w, r, true)
	if !ok {
		return
	}
	ctx := r.Context()
	p := auth.FromContext(ctx)
	days := parseDays(r)
	now := s.now().UTC()
	from := midnight(now).AddDate(0, 0, -(days - 1))
	totals, err := stats.ItemTotals(ctx, s.d.DB.R, it.ID, from, time.Time{})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	daily, err := stats.ItemDaily(ctx, s.d.DB.R, it.ID, from, now)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	dayList := make([]time.Time, len(daily))
	views := make([]int64, len(daily))
	for i, d := range daily {
		dayList[i], views[i] = d.Day, d.Views
	}
	base := ItemStatsPath(it.ID)
	v := itemView{
		Item: it, Name: it.Name(), Kind: kindLabel(it), URL: s.d.Items.PublicURL(it.Slug), Owner: acc == accessOwner,
		Days: days, Ranges: ranges, Tiles: tilesFor(it, days, totals, daily),
		Chart: lineChart(dayList, views, fmt.Sprintf("views per day, last %d days, %d in total", days, totals.Views)),
	}
	if acc != accessOwner {
		// Totals only: the name and the link (the slug is all an unlisted
		// item's protection) stay with the owner.
		v.Name, v.URL = "Item "+strconv.FormatInt(it.ID, 10), ""
	}
	if acc == accessOwner {
		if err := s.ownerDetails(r, it, from, days, p.UserID, base, &v); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.d.Render.Page(w, r, http.StatusOK, "stats/item", web.Page{
		Title: "Stats · " + v.Name, Nav: "dashboard", NoIndex: true, Scripts: []string{"js/stats.js"}, Data: v,
	})
}

// ownerDetails adds what only the owner sees: top lists, raw events and
// the notification override.
func (s *Service) ownerDetails(r *http.Request, it *items.Item, from time.Time, days int, userID int64, base string, v *itemView) error {
	ctx := r.Context()
	var err error
	if v.Referrers, err = stats.TopReferrers(ctx, s.d.DB.R, it.ID, from, time.Time{}, topN); err != nil {
		return err
	}
	if v.Bots, err = stats.TopBots(ctx, s.d.DB.R, it.ID, from, time.Time{}, topN); err != nil {
		return err
	}
	if v.Browsers, err = stats.TopUserAgents(ctx, s.d.DB.R, it.ID, from, time.Time{}, topN); err != nil {
		return err
	}
	if v.Total, err = stats.CountItemEvents(ctx, s.d.DB.R, it.ID); err != nil {
		return err
	}
	v.Pages = max(1, int((v.Total+eventsPerPage-1)/eventsPerPage))
	v.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	v.Page = min(max(v.Page, 1), v.Pages)
	evs, err := stats.ItemEvents(ctx, s.d.DB.R, it.ID, stats.EventPage{Offset: (v.Page - 1) * eventsPerPage, Limit: eventsPerPage})
	if err != nil {
		return err
	}
	for _, e := range evs {
		v.Events = append(v.Events, eventRow{When: e.At.UTC().Format(time.DateTime), Kind: string(e.Kind), IP: e.IP, Client: e.UASummary, Referrer: e.RefererHost, Bot: e.Bot})
	}
	if v.Total > 0 {
		v.First = (v.Page-1)*eventsPerPage + 1
		v.Last = v.First + len(v.Events) - 1
	}
	v.PageHref = base + "?days=" + strconv.Itoa(days) + "&page="
	v.CSV = "/dashboard/items/" + strconv.FormatInt(it.ID, 10) + "/events.csv"
	if s.d.Notify != nil && s.d.Notify.Enabled(ctx, userID) {
		m, err := s.d.Notify.Override(ctx, userID, it.ID)
		if err != nil {
			return err
		}
		v.Notify, v.Override, v.Overrides = true, "default", overrideOptions
		if m != "" {
			v.Override = string(m)
		}
		v.NotifyAction = "/dashboard/items/" + strconv.FormatInt(it.ID, 10) + "/notify"
	}
	return nil
}

// itemNotify is the per-item override: event=default|none|first|every.
// JSON for fetch; plain posts go back to the stats page.
func (s *Service) itemNotify(w http.ResponseWriter, r *http.Request) {
	it, acc, ok := s.loadItem(w, r, false)
	if !ok || acc != accessOwner {
		return
	}
	if s.d.Notify == nil {
		s.d.Errors.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	ev := r.PostFormValue("event")
	m := notify.Mode(ev)
	if ev == "default" {
		m = ""
	} else if !m.Valid() {
		s.d.Errors.Error(w, r, http.StatusBadRequest, "Choose default, off, first open or every open.")
		return
	}
	p := auth.FromContext(r.Context())
	if err := s.d.Notify.SetOverride(r.Context(), p.UserID, it.ID, m); err != nil {
		s.serverError(w, r, err)
		return
	}
	if web.IsAPI(r) {
		web.JSON(w, http.StatusOK, map[string]string{"event": ev})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, ItemStatsPath(it.ID), http.StatusSeeOther)
}

// itemCSV is GET /dashboard/items/{id}/events.csv: every raw event still
// within retention, newest first, owner only. Rows are read in batches so
// no query stays open while the response is written.
func (s *Service) itemCSV(w http.ResponseWriter, r *http.Request) {
	it, acc, ok := s.loadItem(w, r, false)
	if !ok || acc != accessOwner {
		return
	}
	ctx := r.Context()
	first, err := stats.ItemEvents(ctx, s.d.DB.R, it.ID, stats.EventPage{Limit: csvBatch})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+it.Slug+`-events.csv"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time", "kind", "ip", "user_agent", "client", "bot", "referrer"})
	batch := first
	for len(batch) > 0 {
		for _, e := range batch {
			_ = cw.Write([]string{
				e.At.UTC().Format(time.RFC3339), string(e.Kind), csvCell(e.IP), csvCell(e.UA),
				csvCell(e.UASummary), csvCell(e.Bot), csvCell(e.RefererHost),
			})
		}
		cw.Flush()
		if cw.Error() != nil || len(batch) < csvBatch {
			return
		}
		if batch, err = stats.ItemEvents(ctx, s.d.DB.R, it.ID, stats.EventPage{BeforeID: batch[len(batch)-1].ID, Limit: csvBatch}); err != nil {
			s.log.Error("statsui: csv", "err", err)
			return
		}
	}
	cw.Flush()
}

// csvCell defuses spreadsheet formulas: a cell starting with = + - @ (or a
// tab or carriage return) gets a leading apostrophe, so a visitor's
// User-Agent can't run as a formula when the owner opens the file.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
