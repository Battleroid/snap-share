package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func init() {
	register(command{name: "stats", summary: "show who opened an item", run: runStats})
}

type statsCount struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type statsResp struct {
	Item struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
		Kind string `json:"kind"`
		URL  string `json:"url"`
	} `json:"item"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Opened struct {
		FirstOpenedAt *time.Time `json:"first_opened_at"`
		LastOpenedAt  *time.Time `json:"last_opened_at"`
		OpenedCount   int64      `json:"opened_count"`
		PreviewedAt   *time.Time `json:"previewed_at"`
	} `json:"opened"`
	Totals struct {
		Views, Uniques, Previews, Embeds, Downloads, Clicks, Plays int64
	} `json:"totals"`
	Daily []struct {
		Day   string `json:"day"`
		Views int64  `json:"views"`
	} `json:"daily"`
	Referrers []statsCount `json:"referrers"`
	Bots      []statsCount `json:"bots"`
	Browsers  []statsCount `json:"browsers"`
}

type statsEvent struct {
	At        time.Time `json:"at"`
	Kind      string    `json:"kind"`
	IP        string    `json:"ip"`
	UASummary string    `json:"ua_summary"`
	Bot       string    `json:"bot"`
	Referrer  string    `json:"referrer"`
}

// runStats: sp stats <slug|url|id> [--days 30] [--events].
func runStats(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	days := fs.Int("days", 30, "days to show, ending today (1-1000)")
	events := fs.Bool("events", false, "also list the latest events, with IPs")
	limit := fs.Int("limit", 20, "events to list with --events (1-1000)")
	if err := fs.Parse(interleaved(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: sp stats <slug|url|id> [--days 30] [--events]")
	}
	if *days < 1 || *days > 1000 {
		return errors.New("--days must be 1 to 1000")
	}
	if *limit < 1 || *limit > 1000 {
		return errors.New("--limit must be 1 to 1000")
	}
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	id, err := c.itemID(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	today := time.Now().UTC()
	q := url.Values{
		"from": {today.AddDate(0, 0, -(*days - 1)).Format(time.DateOnly)},
		"to":   {today.Format(time.DateOnly)},
	}
	path := "/api/v1/items/" + strconv.FormatInt(id, 10)
	var st statsResp
	if err := c.call(ctx, http.MethodGet, path+"/stats?"+q.Encode(), nil, &st); err != nil {
		return err
	}
	printStats(e.stdout, &st, *days)
	if !*events {
		return nil
	}
	var evs struct {
		Events []statsEvent `json:"events"`
	}
	if err := c.call(ctx, http.MethodGet, path+"/events?limit="+strconv.Itoa(*limit), nil, &evs); err != nil {
		return err
	}
	fprintf(e.stdout, "\nlatest events\n")
	if len(evs.Events) == 0 {
		fprintf(e.stdout, "  —\n")
	}
	for _, ev := range evs.Events {
		client := ev.UASummary
		if client == "" {
			client = ev.Bot
		}
		fprintf(e.stdout, "  %s  %-8s  %-39s  %-24s  %s\n", ev.At.UTC().Format("2006-01-02 15:04:05"), ev.Kind,
			dash(ev.IP), dash(clip(client, 24)), dash(ev.Referrer))
	}
	return nil
}

// itemID takes an item id, slug or link.
func (c *client) itemID(ctx context.Context, arg string) (int64, error) {
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil && id > 0 {
		return id, nil
	}
	slug, err := c.slugOf(arg)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", arg, err)
	}
	ids, err := c.findSlugs(ctx, map[string]bool{strings.ToLower(slug): true})
	if err != nil {
		return 0, err
	}
	id := ids[strings.ToLower(slug)]
	if id == 0 {
		return 0, fmt.Errorf("%s: no such item", arg)
	}
	return id, nil
}

func printStats(w io.Writer, st *statsResp, days int) {
	opened := "not opened"
	switch {
	case st.Opened.FirstOpenedAt != nil:
		opened = "opened · first " + stamp(st.Opened.FirstOpenedAt) + " · last " + stamp(st.Opened.LastOpenedAt)
	case st.Opened.PreviewedAt != nil:
		opened = "previewed · " + stamp(st.Opened.PreviewedAt)
	}
	fprintf(w, "%s · %s\n%s\n\n", st.Item.Name, st.Item.URL, opened)
	t := st.Totals
	fprintf(w, "last %d %s\n", days, plural(days, "day", "days"))
	fprintf(w, "  views %d · unique visitors %d · previews %d", t.Views, t.Uniques, t.Previews)
	switch st.Item.Kind {
	case "link":
		fprintf(w, " · clicks %d", t.Clicks)
	case "file":
		fprintf(w, " · downloads %d · plays %d · embeds %d", t.Downloads, t.Plays, t.Embeds)
	}
	fprintf(w, "\n")
	views := make([]int64, len(st.Daily))
	var peak int64
	for i, d := range st.Daily {
		views[i] = d.Views
		peak = max(peak, d.Views)
	}
	if len(st.Daily) > 0 {
		fprintf(w, "  views/day %s  %s … %s · peak %d\n", sparkText(views), st.Daily[0].Day, st.Daily[len(st.Daily)-1].Day, peak)
	}
	for _, l := range []struct {
		name string
		list []statsCount
	}{{"referrers", st.Referrers}, {"bots", st.Bots}, {"browsers", st.Browsers}} {
		parts := make([]string, 0, len(l.list))
		for _, c := range l.list {
			parts = append(parts, fmt.Sprintf("%s %d", c.Key, c.Count))
		}
		fprintf(w, "  %-9s %s\n", l.name, dash(strings.Join(parts, " · ")))
	}
}

// sparkText draws values with block characters, one per day; long ranges
// are folded into at most 60 columns (each the sum of its days).
func sparkText(values []int64) string {
	const maxCols = 60
	if n := len(values); n > maxCols {
		cols := make([]int64, maxCols)
		for i, v := range values {
			cols[i*maxCols/n] += v
		}
		values = cols
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	var peak int64
	for _, v := range values {
		peak = max(peak, v)
	}
	var b strings.Builder
	for _, v := range values {
		i := 0
		if peak > 0 && v > 0 {
			i = 1 + int(v*int64(len(bars)-2)/peak)
		}
		b.WriteRune(bars[min(i, len(bars)-1)])
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
