package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
)

func init() {
	register(command{name: "short", summary: "create a short link", run: runShort})
}

// runShort: sp short <url> [--slug docs] [--expire 30d].
func runShort(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("short", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	slug := fs.String("slug", "", "a custom name")
	expire := fs.String("expire", "", "expiry: never, 1h, 1d, 7d, 30d, 90d or an RFC 3339 time")
	fs.Usage = func() {
		fprintf(e.stderr, "%s\n", "usage: sp short [flags] <url>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(interleaved(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("give one URL")
	}
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	req := map[string]string{"url": fs.Arg(0)}
	if *slug != "" {
		req["slug"] = *slug
	}
	if *expire != "" {
		req["expires"] = *expire
	}
	var it item
	if err := c.call(ctx, http.MethodPost, "/api/v1/links", req, &it); err != nil {
		return err
	}
	fprintf(e.stdout, "%s\n", it.URL)
	return nil
}
