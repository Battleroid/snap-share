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
	register(command{name: "paste", summary: "create a paste from a file or stdin, or a gist from several files", run: runPaste})
}

// maxPaste matches the server's paste limit, which also caps a gist's files
// together.
const maxPaste = 16 << 20

// maxGistFiles matches the server's limit.
const maxGistFiles = 50

// gistFile is one file of POST /api/v1/gists.
type gistFile struct {
	Name    string `json:"name,omitempty"`
	Lang    string `json:"lang,omitempty"`
	Content string `json:"content"`
}

// runPaste: sp paste [file|-]... [--lang go] [--name main.go] [--title T] [--expire 1d].
// One file (or stdin) makes a paste; several make one gist.
func runPaste(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("paste", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	lang := fs.String("lang", "", "language for highlighting (default: from the name); in a gist, of stdin")
	name := fs.String("name", "", "file name shown with the paste (default: the file's name); in a gist, of stdin")
	title := fs.String("title", "", "title of the paste or gist")
	expire := fs.String("expire", "", "expiry: never, 1h, 1d, 7d, 30d, 90d or an RFC 3339 time")
	fs.Usage = func() {
		fprintf(e.stderr, "%s\n", "usage: sp paste [flags] [file|-]...")
		fprintf(e.stderr, "%s\n", "Several files make one gist; - (stdin) may be one of them.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(interleaved(fs, args)); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return runGist(ctx, fs.Args(), *name, *lang, *title, *expire)
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
	for k, v := range map[string]string{"lang": *lang, "name": *name, "title": *title, "expires": *expire} {
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

// runGist creates one gist from several files; name and lang belong to
// stdin ("-"), which may be given once.
func runGist(ctx context.Context, paths []string, name, lang, title, expire string) error {
	e := envFrom(ctx)
	if len(paths) > maxGistFiles {
		return fmt.Errorf("a gist holds at most %d files", maxGistFiles)
	}
	stdin := 0
	for _, p := range paths {
		if p == "-" {
			stdin++
		}
	}
	if stdin > 1 {
		return errors.New("give - (stdin) at most once")
	}
	if stdin == 0 && (name != "" || lang != "") {
		return errors.New("in a gist, --name and --lang are for stdin (-)")
	}
	var total int64
	files := make([]gistFile, 0, len(paths))
	for _, p := range paths {
		f, label := gistFile{Name: name, Lang: lang}, "stdin"
		var b []byte
		var err error
		if p == "-" {
			b, err = io.ReadAll(io.LimitReader(e.stdin, maxPaste+1-total))
		} else {
			f, label = gistFile{Name: filepath.Base(p)}, p
			b, err = readUpTo(p, maxPaste+1-total)
		}
		if err != nil {
			return err
		}
		if total += int64(len(b)); total > maxPaste {
			return fmt.Errorf("a gist is limited to %s in all; use sp up for larger files", humanBytes(maxPaste))
		}
		if !utf8.Valid(b) {
			return fmt.Errorf("%s is not UTF-8 text; use sp up for binary files", label)
		}
		f.Content = string(b)
		files = append(files, f)
	}
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	req := map[string]any{"files": files}
	for k, v := range map[string]string{"title": title, "expires": expire} {
		if v != "" {
			req[k] = v
		}
	}
	var it item
	if err := c.call(ctx, http.MethodPost, "/api/v1/gists", req, &it); err != nil {
		return err
	}
	fprintf(e.stdout, "%s\n", it.URL)
	return nil
}

// readUpTo reads at most n bytes of the file at p.
func readUpTo(p string, n int64) ([]byte, error) {
	f, err := os.Open(p) // #nosec G304 -- a file the user named
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, n))
}
