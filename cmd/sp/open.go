package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/e2e"
)

func init() {
	register(command{name: "open", summary: "open a one-time secret link", run: func(ctx context.Context, args []string) error {
		return runOpen(ctx, args, defaultSecretIO())
	}})
}

var errVerify = errors.New("the server's copy failed verification, so it was not opened")

func runOpen(ctx context.Context, args []string, sio secretIO) error {
	fl := flag.NewFlagSet("open", flag.ContinueOnError)
	fl.SetOutput(sio.err)
	out := fl.String("o", "", "save to `file` (- for stdout); never overwrites")
	passFile := fl.String("passphrase-file", "", "read the passphrase from `file`")
	fl.Usage = func() {
		_, _ = fmt.Fprintln(sio.err, "usage: sp open <link> [-o file] [--passphrase-file F]")
		_, _ = fmt.Fprintln(sio.err, "\nText goes to stdout; files are saved under their own name in the current directory.")
		fl.PrintDefaults()
	}
	pos, err := secretParseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fl.Usage()
		return errors.New("give one secret link")
	}
	base, id, key, err := e2e.ParseLink(pos[0])
	if err != nil {
		return errors.New("that is not a whole secret link (it needs the part after #)")
	}
	defer clear(key[:])
	if *out != "" && *out != "-" {
		if _, err := os.Lstat(*out); err == nil {
			return fmt.Errorf("%s exists; refusing to overwrite it", *out)
		}
	}
	// The link's own server, without your API key.
	c, err := newSecretClient(base, "", sio.hc)
	if err != nil {
		return err
	}
	ids := e2e.Encode(id[:])
	probeKey := e2e.ProbeKey(key, id)
	probe := e2e.Encode(probeKey[:])

	info, err := c.info(ctx, ids, probe)
	if err != nil {
		return err
	}
	if info.Kind != e2e.KindText && info.Kind != e2e.KindFile {
		return errVerify
	}
	salt, err := e2e.Decode16(info.Salt)
	if err != nil {
		return errVerify
	}
	var pass []byte
	var params e2e.KDFParams
	if info.KDF != nil {
		// A malicious server could weaken the passphrase cost, or ask for
		// gigabytes of memory.
		if err := info.KDF.Validate(); err != nil {
			return fmt.Errorf("refusing to open: the server asks for unsafe passphrase settings (%w)", err)
		}
		params = *info.KDF
		if *passFile != "" {
			pass, err = secretPassphraseFile(*passFile)
		} else {
			pass, err = sio.ask("Passphrase: ", false)
		}
		if err != nil {
			return err
		}
		if len(pass) == 0 {
			return errors.New("the passphrase is empty")
		}
		defer clear(pass)
		_, _ = fmt.Fprintln(sio.err, "Deriving the key…")
	}
	keys, err := e2e.DeriveKeys(key, id, salt, pass, params)
	if err != nil {
		return err
	}
	defer func() { keys = e2e.Keys{} }()

	cl, err := c.claim(ctx, ids, probe, e2e.Encode(keys.Auth[:]))
	if err != nil {
		return err
	}
	// From here on a view is used. Check the key commitment before
	// decrypting anything.
	commit, err := e2e.Decode32(cl.Commit)
	if err != nil || keys.CheckCommit(commit[:]) != nil {
		return errVerify
	}
	metaCT, err := base64.RawURLEncoding.Strict().DecodeString(cl.MetaCT)
	if err != nil {
		return errVerify
	}
	meta, err := e2e.OpenMeta(keys.Meta, metaCT)
	if err != nil || meta.Kind != info.Kind || cl.Kind != info.Kind {
		return errVerify
	}

	if meta.Kind == e2e.KindText {
		ct, err := base64.RawURLEncoding.Strict().DecodeString(cl.Content)
		if err != nil {
			return errVerify
		}
		pt, err := io.ReadAll(e2e.NewDecryptor(bytes.NewReader(ct), keys.Enc))
		if err != nil {
			return errVerify
		}
		defer clear(pt)
		text, err := e2e.UnpadText(pt, meta.Size)
		if err != nil {
			return errVerify
		}
		if *out == "" || *out == "-" {
			_, err = sio.out.Write(text)
		} else {
			var name string
			if name, err = secretSave(filepath.Dir(*out), filepath.Base(*out), bytes.NewReader(text), int64(len(text)), true); err == nil {
				_, _ = fmt.Fprintf(sio.err, "Saved %s\n", name)
			}
		}
		if err != nil {
			return err
		}
	} else {
		if err := secretDownload(ctx, c, ids, cl, keys, meta, *out, sio); err != nil {
			return err
		}
	}
	if cl.ViewsLeft == 0 {
		_, _ = fmt.Fprintln(sio.err, "That was the last view; the secret is destroyed.")
	} else {
		_, _ = fmt.Fprintf(sio.err, "%d %s left.\n", cl.ViewsLeft, secretPlural(cl.ViewsLeft, "view", "views"))
	}
	return nil
}

// secretDownload fetches, decrypts and saves a file secret, then tells the
// server it may delete the ciphertext.
func secretDownload(ctx context.Context, c *secretClient, ids string, cl secretClaim, keys e2e.Keys, meta e2e.Meta, out string, sio secretIO) error {
	body, err := c.blob(ctx, ids, cl.Ticket)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	// Decrypt into a private temporary file; only a stream that verified to
	// its end is kept.
	dir, name, exact := sio.dir, secretLocalName(meta.Name), false
	switch {
	case out == "-":
		dir = ""
	case out != "":
		dir, name, exact = filepath.Dir(out), filepath.Base(out), true
	case dir == "":
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".sp-open-*.partial")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, e2e.NewDecryptor(body, keys.Enc))
	if err == nil && n != meta.Size {
		err = errVerify
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, e2e.ErrDecrypt) {
		err = errVerify
	}
	if err != nil {
		return err
	}
	_ = c.done(ctx, ids, cl.Ticket) // the final chunk verified

	if out == "-" {
		f, err := os.Open(tmp.Name())
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(sio.out, f)
		return err
	}
	saved, err := secretPlace(tmp.Name(), dir, name, exact)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(sio.err, "Saved %s (%s)\n", saved, secretHumanBytes(meta.Size))
	return nil
}

// secretSave writes r to a new file in dir (see secretPlace).
func secretSave(dir, name string, r io.Reader, size int64, exact bool) (string, error) {
	tmp, err := os.CreateTemp(dir, ".sp-open-*.partial")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != size {
		err = io.ErrShortWrite
	}
	if err != nil {
		return "", err
	}
	return secretPlace(tmp.Name(), dir, name, exact)
}

// secretPlace moves a finished temp file to dir/name without overwriting
// anything: it claims the name with O_EXCL first. If the name is taken it
// tries "name (1).ext" and so on, because the secret can't be fetched again.
func secretPlace(tmp, dir, name string, exact bool) (string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 100; i++ {
		cand := name
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, cand)
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- a sanitized name in the chosen dir
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_ = f.Close()
		if err := os.Rename(tmp, p); err != nil {
			_ = os.Remove(p)
			return "", err
		}
		if i > 0 && exact {
			return p, fmt.Errorf("%s appeared meanwhile; saved as %s instead", name, p)
		}
		return p, nil
	}
	return "", fmt.Errorf("could not find a free name for %s", name)
}

// secretLocalName turns the sender's file name into a safe name in the
// current directory: no directories, no control, invisible or bidi
// characters, no leading dots (no surprise dotfiles), nothing Windows
// refuses.
func secretLocalName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError, unicode.IsControl(r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
		case strings.ContainsRune(`:*?"<>|/`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	s := strings.TrimRight(strings.TrimLeft(b.String(), ". "), ". ")
	for len(s) > 200 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	if s == "" {
		return "secret.bin"
	}
	base := strings.ToLower(strings.SplitN(s, ".", 2)[0])
	switch base {
	case "con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		s = "_" + s
	}
	return s
}

func secretHumanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
