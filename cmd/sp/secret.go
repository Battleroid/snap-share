package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/Battleroid/snap-share/internal/e2e"
)

func init() {
	register(command{name: "secret", summary: "share a one-time, end-to-end encrypted secret", run: func(ctx context.Context, args []string) error {
		return runSecret(ctx, args, defaultSecretIO())
	}})
}

// secretIO is what the secret commands read from and write to. Tests swap
// every part.
type secretIO struct {
	in       io.Reader
	out, err io.Writer
	hc       *http.Client // nil: default transport
	// ask reads a passphrase without echo; confirm asks twice.
	ask func(prompt string, confirm bool) ([]byte, error)
	dir string // where sp open saves files; "" is the current directory
	// outTTY: out is a terminal, so sp open escapes control characters.
	outTTY bool
}

func defaultSecretIO() secretIO {
	return secretIO{in: os.Stdin, out: os.Stdout, err: os.Stderr, ask: secretAskTerminal,
		outTTY: term.IsTerminal(int(os.Stdout.Fd()))} //nolint:gosec // a file descriptor fits in an int
}

// secretAskTerminal prompts on the controlling terminal, so the secret itself can
// come from stdin.
func secretAskTerminal(prompt string, confirm bool) ([]byte, error) {
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONIN$"
	}
	tty, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return nil, errors.New("asking for a passphrase needs a terminal; use --passphrase-file")
	}
	defer func() { _ = tty.Close() }()
	read := func(p string) ([]byte, error) {
		_, _ = fmt.Fprint(os.Stderr, p)
		b, err := term.ReadPassword(int(tty.Fd())) //nolint:gosec // a file descriptor fits in an int
		_, _ = fmt.Fprintln(os.Stderr)
		return b, err
	}
	pass, err := read(prompt)
	if err != nil {
		return nil, err
	}
	if confirm {
		again, err := read("Repeat: ")
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(pass, again) {
			return nil, errors.New("the passphrases don't match")
		}
	}
	return pass, nil
}

// secretPassphraseFile reads a passphrase, dropping one trailing line ending.
func secretPassphraseFile(name string) ([]byte, error) {
	b, err := os.ReadFile(name) // #nosec G304 -- the user's own file
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	if len(b) == 0 {
		return nil, fmt.Errorf("%s is empty", name)
	}
	return b, nil
}

// secretParseFlags parses flags given before or after the positional arguments.
func secretParseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// secretParseExpiry reads 5m, 1h, 1d, 7d, 30d or any Go duration.
func secretParseExpiry(v string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		d, err := strconv.Atoi(n)
		if err != nil || d < 1 {
			return 0, fmt.Errorf("bad expiry %q", v)
		}
		return time.Duration(d) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("bad expiry %q (use 5m, 1h, 1d, 7d, 30d)", v)
	}
	return d, nil
}

const secretTextMax = e2e.ChunkSize // padded text limit on the server

func runSecret(ctx context.Context, args []string, sio secretIO) error {
	fs := flag.NewFlagSet("secret", flag.ContinueOnError)
	fs.SetOutput(sio.err)
	views := fs.Int("views", 1, "views before the secret is destroyed")
	expire := fs.String("expire", "1d", "expiry: 5m, 1h, 1d, 7d or 30d")
	ask := fs.Bool("p", false, "ask for a passphrase (not echoed)")
	passFile := fs.String("passphrase-file", "", "read the passphrase from `file`")
	note := fs.String("note", "", "a note for your secrets page (the server can read it; the recipient can't)")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(sio.err, "usage: sp secret [file|-] [--views N] [--expire 1d] [-p | --passphrase-file F] [--note T]")
		_, _ = fmt.Fprintln(sio.err, "\nEncrypts the file (or text from stdin) on this machine and prints a one-time link.")
		fs.PrintDefaults()
	}
	pos, err := secretParseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		fs.Usage()
		return errors.New("give one file, or - for stdin")
	}
	expiry, err := secretParseExpiry(*expire)
	if err != nil {
		return err
	}
	if *ask && *passFile != "" {
		return errors.New("use -p or --passphrase-file, not both")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// What to send: a file, or text from stdin.
	var (
		kind   = e2e.KindText
		meta   e2e.Meta
		size   int64
		text   []byte
		upload *os.File
	)
	if len(pos) == 1 && pos[0] != "-" {
		f, err := os.Open(pos[0]) // #nosec G703 -- the user's own file argument
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", pos[0])
		}
		typ := mime.TypeByExtension(filepath.Ext(st.Name()))
		if typ == "" {
			typ = "application/octet-stream"
		}
		kind, upload, size = e2e.KindFile, f, st.Size()
		meta = e2e.Meta{Kind: kind, Name: st.Name(), Type: typ, Size: size}
	} else {
		b, err := io.ReadAll(io.LimitReader(sio.in, secretTextMax+1))
		if err != nil {
			return err
		}
		if len(b) > secretTextMax {
			return errors.New("stdin holds more than 1 MiB; save it to a file and pass the file")
		}
		if len(b) == 0 {
			return errors.New("nothing to share: stdin is empty")
		}
		if !utf8.Valid(b) {
			return errors.New("stdin is not UTF-8 text; pass it as a file instead")
		}
		meta = e2e.Meta{Kind: kind, Type: "text/plain; charset=utf-8", Size: int64(len(b))}
		text = e2e.PadText(b)
		clear(b)
		size = int64(len(text))
		defer clear(text)
	}

	var pass []byte
	switch {
	case *ask:
		if pass, err = sio.ask("Passphrase: ", true); err != nil {
			return err
		}
	case *passFile != "":
		if pass, err = secretPassphraseFile(*passFile); err != nil {
			return err
		}
	}
	defer clear(pass)
	if (*ask || *passFile != "") && len(pass) == 0 {
		return errors.New("the passphrase is empty")
	}

	c, err := newSecretClient(cfg.URL, cfg.Key, sio.hc)
	if err != nil {
		return err
	}
	created, err := c.create(ctx, secretCreateReq{Kind: kind, Size: size, Views: *views, ExpiresIn: int64(expiry / time.Second), Note: *note})
	if err != nil {
		return err
	}
	id, err := e2e.Decode16(created.ID)
	if err != nil {
		return errors.New("the server sent a malformed secret id")
	}
	if created.Chunks != e2e.NumChunks(size) {
		return errors.New("the server disagrees about the chunk count")
	}

	// A fresh key and salt for this attempt.
	key, err := e2e.NewLinkKey()
	if err != nil {
		return err
	}
	defer clear(key[:])
	salt, err := e2e.NewSalt()
	if err != nil {
		return err
	}
	var kdf *e2e.KDFParams
	params := e2e.DefaultKDF()
	if pass != nil {
		kdf = &params
		_, _ = fmt.Fprintln(sio.err, "Deriving the key…")
	}
	keys, err := e2e.DeriveKeys(key, id, salt, pass, params)
	if err != nil {
		return err
	}
	defer func() { keys = e2e.Keys{} }()

	// Encrypt and upload chunk by chunk. Each chunk is encrypted once; a
	// retry resends the same ciphertext.
	n := e2e.NumChunks(size)
	buf := make([]byte, e2e.ChunkSize)
	defer clear(buf)
	var src io.Reader = bytes.NewReader(text)
	if upload != nil {
		src = upload
	}
	for i := int64(0); i < n; i++ {
		want := min(int64(e2e.ChunkSize), size-i*e2e.ChunkSize)
		if _, err := io.ReadFull(src, buf[:want]); err != nil {
			return fmt.Errorf("reading the input: %w (did it change while uploading?)", err)
		}
		ct, err := e2e.EncryptChunk(keys.Enc, uint64(i), i == n-1, buf[:want]) //nolint:gosec // i >= 0
		if err != nil {
			return err
		}
		if err := c.putChunk(ctx, created.ID, created.UploadToken, i, ct); err != nil {
			return fmt.Errorf("uploading chunk %d of %d: %w", i+1, n, err)
		}
		if n > 1 {
			_, _ = fmt.Fprintf(sio.err, "\rUploaded %d of %d", i+1, n)
		}
	}
	if n > 1 {
		_, _ = fmt.Fprintln(sio.err)
	}
	if upload != nil {
		if extra, _ := upload.Read(buf[:1]); extra != 0 {
			return errors.New("the file grew while uploading")
		}
	}

	probe := e2e.ProbeKey(key, id)
	hProbe, hAuth := e2e.Hash(probe[:]), e2e.Hash(keys.Auth[:])
	metaCT, err := e2e.SealMeta(keys.Meta, meta)
	if err != nil {
		return err
	}
	fin, err := c.finalize(ctx, created.ID, created.UploadToken, secretFinalizeReq{
		Salt: e2e.Encode(salt[:]), KDF: kdf, HProbe: e2e.Encode(hProbe[:]), HAuth: e2e.Encode(hAuth[:]),
		Commit: e2e.Encode(keys.Commit[:]), MetaCT: e2e.Encode(metaCT),
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(sio.out, e2e.FormatLink(cfg.URL, id, key))
	when := fin.ExpiresAt
	if t, err := time.Parse(time.RFC3339, fin.ExpiresAt); err == nil {
		when = t.Local().Format("2006-01-02 15:04")
	}
	_, _ = fmt.Fprintf(sio.err, "%d %s · expires %s\n", fin.Views, secretPlural(fin.Views, "view", "views"), when)
	if pass != nil {
		_, _ = fmt.Fprintln(sio.err, "Send the passphrase another way.")
	}
	return nil
}
