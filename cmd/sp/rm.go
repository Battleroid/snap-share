package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func init() {
	register(command{name: "rm", summary: "delete items by name, link or id", run: runRm})
}

// runRm: sp rm <slug|url|id…>. A bare number is an item id (as sp ls shows);
// use the link for an item whose name is all digits.
func runRm(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: sp rm <slug|url|id…>")
	}
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	type target struct {
		arg, slug string
		id        int64
	}
	targets := make([]target, 0, fs.NArg())
	want := map[string]bool{}
	for _, a := range fs.Args() {
		t := target{arg: a}
		if id, err := strconv.ParseInt(a, 10, 64); err == nil && id > 0 {
			t.id = id
		} else if t.slug, err = c.slugOf(a); err != nil {
			return fmt.Errorf("%s: %w", a, err)
		} else {
			want[strings.ToLower(t.slug)] = true
		}
		targets = append(targets, t)
	}
	ids, err := c.findSlugs(ctx, want)
	if err != nil {
		return err
	}
	failed := 0
	for _, t := range targets {
		id := t.id
		if id == 0 {
			if id = ids[strings.ToLower(t.slug)]; id == 0 {
				failed++
				fprintf(e.stderr, "sp: %s: no such item\n", t.arg)
				continue
			}
		}
		if err := c.call(ctx, http.MethodDelete, "/api/v1/items/"+strconv.FormatInt(id, 10), nil, nil); err != nil {
			failed++
			fprintf(e.stderr, "sp: %s: %v\n", t.arg, err)
			continue
		}
		fprintf(e.stdout, "deleted %s\n", t.arg)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d deletions failed", failed, len(targets))
	}
	return nil
}

// slugOf takes a slug or a link on this server (/slug, /slug.ext, /slug/raw…).
func (c *client) slugOf(arg string) (string, error) {
	if strings.Contains(arg, "://") {
		u, err := url.Parse(arg)
		if err != nil {
			return "", errors.New("not a valid URL")
		}
		if u.Scheme+"://"+u.Host != c.base {
			return "", fmt.Errorf("that link is not on %s", c.base)
		}
		arg, _, _ = strings.Cut(strings.TrimPrefix(u.Path, "/"), "/")
		if i := strings.LastIndexByte(arg, '.'); i > 0 {
			arg = arg[:i]
		}
	}
	if arg == "" || strings.ContainsFunc(arg, notSlugChar) {
		return "", errors.New("not an item name, link or id")
	}
	return arg, nil
}

// notSlugChar reports whether r cannot appear in a slug (A-Z a-z 0-9 - _).
func notSlugChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		return false
	}
	return true
}

// findSlugs pages through your items until every wanted slug (lowercase) is
// found, and returns their ids. The API has no lookup by name.
func (c *client) findSlugs(ctx context.Context, want map[string]bool) (map[string]int64, error) {
	found := map[string]int64{}
	for offset := 0; len(found) < len(want); offset += 200 {
		var list itemList
		if err := c.call(ctx, http.MethodGet, "/api/v1/items?limit=200&offset="+strconv.Itoa(offset), nil, &list); err != nil {
			return nil, err
		}
		for _, it := range list.Items {
			if s := strings.ToLower(it.Slug); want[s] {
				found[s] = it.ID
			}
		}
		if len(list.Items) < 200 || offset+len(list.Items) >= list.Total {
			break
		}
	}
	return found, nil
}
