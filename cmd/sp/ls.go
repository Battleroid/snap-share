package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"strconv"
	"text/tabwriter"
)

func init() {
	register(command{name: "ls", summary: "list your newest items", run: runLs})
}

// itemList is GET /api/v1/items.
type itemList struct {
	Items []item `json:"items"`
	Total int    `json:"total"`
}

// runLs: sp ls [--limit 20].
func runLs(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	limit := fs.Int("limit", 20, "how many items to show (1-200)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: sp ls [--limit N]")
	}
	if *limit < 1 || *limit > 200 {
		return errors.New("--limit must be 1-200")
	}
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	var list itemList
	if err := c.call(ctx, http.MethodGet, "/api/v1/items?limit="+strconv.Itoa(*limit), nil, &list); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fprintf(tw, "%s\n", "id\tslug\tkind\tsize\tcreated\texpires\tname")
	for _, it := range list.Items {
		size := "—"
		if it.Kind != "link" {
			size = humanBytes(it.Size)
		}
		name := it.Name
		if it.Visibility == "private" {
			name += " (private)"
		}
		fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", it.ID, it.Slug, it.Kind, size, stamp(&it.CreatedAt), stamp(it.ExpiresAt), name)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if list.Total > len(list.Items) {
		fprintf(e.stderr, "%d of %d items\n", len(list.Items), list.Total)
	}
	return nil
}
