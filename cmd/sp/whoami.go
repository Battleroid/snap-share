package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"strings"
	"text/tabwriter"
)

func init() {
	register(command{name: "whoami", summary: "show the account and key in use", run: runWhoami})
}

// runWhoami: sp whoami. Needs a key with account:read.
func runWhoami(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: sp whoami")
	}
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	var who me
	if err := c.call(ctx, http.MethodGet, "/api/v1/me", nil, &who); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fprintf(tw, "user\t%s (%s)\n", who.Username, who.Role)
	fprintf(tw, "server\t%s\n", c.base)
	if !who.EmailVerified {
		fprintf(tw, "email\tnot verified (uploads are refused until it is)\n")
	}
	if k := who.Key; k != nil {
		fprintf(tw, "key\t%s… (%s)\n", k.Prefix, k.Name)
		fprintf(tw, "scopes\t%s\n", strings.Join(k.Scopes, " "))
		fprintf(tw, "expires\t%s\n", stamp(k.ExpiresAt))
	}
	q := who.Quota
	if q.QuotaBytes > 0 {
		fprintf(tw, "storage\t%s of %s\n", humanBytes(q.UsedBytes), humanBytes(q.QuotaBytes))
	} else {
		fprintf(tw, "storage\t%s used\n", humanBytes(q.UsedBytes))
	}
	fprintf(tw, "max upload\t%s\n", humanBytes(q.MaxUploadBytes))
	return tw.Flush()
}
