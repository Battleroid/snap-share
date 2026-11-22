package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"unicode/utf8"
)

func init() {
	register(command{name: "paste", summary: "create a paste from a file or stdin", run: runPaste})
}

// maxPaste matches the server's paste limit.
const maxPaste = 16 << 20

// runPaste: sp paste [file|-] [--lang go] [--name main.go] [--expire 1d].
func runPaste(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("paste", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	lang := fs.String("lang", "", "language for highlighting (default: from the name)")
	name := fs.String("name", "", "file name shown with the paste (default: the file's name)")
	expire := fs.String("expire", "", "expiry: never, 1h, 1d, 7d, 30d, 90d or an RFC 3339 time")
	fs.Usage = func() {
		fprintf(e.stderr, "%s\n", "usage: sp paste [flags] [file|-]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(interleaved(fs, args)); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return errors.New("paste one file at a time")
	}
	src := e.stdin
	if p := fs.Arg(0); p != "" && p != "-" {
		f, err := os.Open(p) // #nosec G304 -- a file the user named
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		src = f
		if *name == "" {
			*name = filepath.Base(p)
		}
	}
	b, err := io.ReadAll(io.LimitReader(src, maxPaste+1))
	if err != nil {
		return err
	}
	if len(b) > maxPaste {
		return fmt.Errorf("pastes are limited to %s; use sp up for larger files", humanBytes(maxPaste))
	}
	if !utf8.Valid(b) {
		return errors.New("that is not UTF-8 text; use sp up for binary files")
	}
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	req := map[string]string{"content": string(b)}
	for k, v := range map[string]string{"lang": *lang, "name": *name, "expires": *expire} {
		if v != "" {
			req[k] = v
		}
	}
	var it item
	if err := c.call(ctx, http.MethodPost, "/api/v1/pastes", req, &it); err != nil {
		return err
	}
	fprintf(e.stdout, "%s\n", it.URL)
	return nil
}
