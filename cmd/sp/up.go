package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func init() {
	register(command{name: "up", summary: "upload files and print their links", run: runUp})
}

type upOptions struct {
	expire, passwordFile, naming, slug string
	private                            bool
}

// runUp: sp up <files…> [--expire 7d] [--private] [--password-file f]
// [--name random|words|original] [--slug name]. Prints one URL per file.
func runUp(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	var o upOptions
	fs.StringVar(&o.expire, "expire", "", "expiry: never, 1h, 1d, 7d, 30d, 90d or an RFC 3339 time")
	fs.BoolVar(&o.private, "private", false, "only you can open it")
	fs.StringVar(&o.passwordFile, "password-file", "", "read a password visitors must enter from this file")
	fs.StringVar(&o.naming, "name", "", "naming: random, words or original")
	fs.StringVar(&o.slug, "slug", "", "a custom name (one file only)")
	fs.Usage = func() {
		fprintf(e.stderr, "%s\n", "usage: sp up [flags] <files…>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(interleaved(fs, args)); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		fs.Usage()
		return errors.New("no files given")
	}
	if o.slug != "" && len(files) > 1 {
		return errors.New("--slug works with one file at a time")
	}
	fields, err := o.fields()
	if err != nil {
		return err
	}
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	failed := 0
	for _, path := range files {
		it, err := c.upload(ctx, path, fields)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed++
			fprintf(e.stderr, "sp: %s: %v\n", path, err)
			continue
		}
		fprintf(e.stdout, "%s\n", it.URL)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d uploads failed", failed, len(files))
	}
	return nil
}

func (o upOptions) fields() ([][2]string, error) {
	var f [][2]string
	if o.expire != "" {
		f = append(f, [2]string{"expires", o.expire})
	}
	if o.private {
		f = append(f, [2]string{"visibility", "private"})
	}
	if o.naming != "" {
		f = append(f, [2]string{"naming", o.naming})
	}
	if o.slug != "" {
		f = append(f, [2]string{"slug", o.slug})
	}
	if o.passwordFile != "" {
		pw, err := readPasswordFile(o.passwordFile)
		if err != nil {
			return nil, err
		}
		f = append(f, [2]string{"password", pw})
	}
	return f, nil
}

func readPasswordFile(path string) (string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the user's own file
	if err != nil {
		return "", err
	}
	pw := strings.TrimRight(string(b), "\r\n")
	if pw == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return pw, nil
}

// upload streams one file as multipart/form-data with an exact Content-Length,
// so the server reserves only what the file needs.
func (c *client) upload(ctx context.Context, path string, fields [][2]string) (*item, error) {
	f, err := os.Open(path) // #nosec G304 -- a file the user named
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	for _, kv := range fields {
		if err := mw.WriteField(kv[0], kv[1]); err != nil {
			return nil, err
		}
	}
	name := filepath.Base(path)
	if _, err := mw.CreateFormFile("file", name); err != nil {
		return nil, err
	}
	tail := "\r\n--" + mw.Boundary() + "--\r\n" // what mw.Close would write
	var src io.Reader = f
	var prog *progress
	if isTerminal(c.env.stderr) {
		prog = &progress{w: c.env.stderr, name: name, total: info.Size()}
		src = io.TeeReader(f, prog)
		defer prog.done()
	}
	body := io.MultiReader(bytes.NewReader(head.Bytes()), src, strings.NewReader(tail))
	req, err := c.newRequest(ctx, http.MethodPost, "/api/v1/upload", body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = int64(head.Len()) + info.Size() + int64(len(tail))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var it item
	if err := c.do(req, &it); err != nil {
		return nil, err
	}
	return &it, nil
}

// progress prints "name  1.2 MB / 40.0 MB  3%" on one terminal line.
type progress struct {
	w     io.Writer
	name  string
	total int64

	mu   sync.Mutex
	n    int64
	last time.Time
}

func (p *progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n += int64(len(b))
	if now := time.Now(); now.Sub(p.last) >= 100*time.Millisecond || p.n == p.total {
		p.last = now
		pct := 100
		if p.total > 0 {
			pct = int(p.n * 100 / p.total)
		}
		fprintf(p.w, "\r\033[K%s  %s / %s  %d%%", p.name, humanBytes(p.n), humanBytes(p.total), pct)
	}
	return len(b), nil
}

func (p *progress) done() { fprintf(p.w, "%s", "\r\033[K") }

// interleaved moves flags that follow positional arguments to the front, so
// "sp up a.png --private" works like "sp up --private a.png". "--" ends flags.
func interleaved(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil {
			if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue
			}
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	return append(append(flags, "--"), pos...)
}
