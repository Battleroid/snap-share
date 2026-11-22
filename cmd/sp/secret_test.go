package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/secrets/secretstest"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// spEnv runs sp secret and sp open against a live test server.
type spEnv struct {
	t   *testing.T
	s   *testutil.Server
	dir string
}

func newSPEnv(t *testing.T) *spEnv {
	t.Helper()
	s, _ := secretstest.Server(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	_, err := s.App.DB.W.ExecContext(context.Background(),
		`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at) VALUES (1, 'ann', 'ann@example.test', 1, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	s.Auth.Add("ann-key", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 1, Scopes: []auth.Scope{auth.ScopeSecretsWrite}})
	t.Setenv("SNAP_URL", s.URL)
	t.Setenv("SNAP_KEY", "test-ann-key")
	return &spEnv{t: t, s: s, dir: t.TempDir()}
}

func (e *spEnv) io(stdin string, pass string) (secretIO, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return secretIO{
		in: strings.NewReader(stdin), out: &out, err: &errb, hc: e.s.Client(), dir: e.dir,
		ask: func(string, bool) ([]byte, error) { return []byte(pass), nil },
	}, &out, &errb
}

func (e *spEnv) secret(stdin, pass string, args ...string) string {
	e.t.Helper()
	sio, out, errb := e.io(stdin, pass)
	if err := runSecret(context.Background(), args, sio); err != nil {
		e.t.Fatalf("sp secret %v: %v\n%s", args, err, errb)
	}
	link := strings.TrimSpace(out.String())
	if !strings.HasPrefix(link, e.s.URL+"/s/") || strings.Count(link, "\n") != 0 {
		e.t.Fatalf("sp secret printed %q", out)
	}
	return link
}

func (e *spEnv) open(link, pass string, args ...string) (string, string, error) {
	e.t.Helper()
	sio, out, errb := e.io("", pass)
	err := runOpen(context.Background(), append([]string{link}, args...), sio)
	return out.String(), errb.String(), err
}

func TestSPTextRoundTrip(t *testing.T) {
	e := newSPEnv(t)
	link := e.secret("the launch codes\n", "", "--views", "2", "--note", "for bob")
	for range 2 {
		out, _, err := e.open(link, "")
		if err != nil || out != "the launch codes\n" {
			t.Fatalf("open: %q %v", out, err)
		}
	}
	_, _, err := e.open(link, "")
	var gone *secretGone
	if !errors.As(err, &gone) || !strings.Contains(err.Error(), "already opened") {
		t.Fatalf("third open: %v", err)
	}
	var note string
	_ = e.s.App.DB.R.QueryRow(`SELECT note FROM secrets`).Scan(&note)
	if note != "for bob" {
		t.Fatalf("note %q", note)
	}
}

func TestSPFileWithPassphrase(t *testing.T) {
	e := newSPEnv(t)
	data := make([]byte, 2*e2e.ChunkSize+777)
	for i := range data {
		data[i] = byte(i * 7)
	}
	src := filepath.Join(t.TempDir(), "backup.tar")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}
	passFile := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(passFile, []byte("tr0ub4dor&3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := e.secret("", "tr0ub4dor&3", src, "-p", "--views", "3", "--expire", "1h")

	// Wrong passphrase: refused, and no view used.
	_, _, err := e.open(link, "nope")
	var wp *secretWrongPass
	if !errors.As(err, &wp) || wp.left != 9 {
		t.Fatalf("wrong passphrase: %v", err)
	}
	// Saved under the sender's name in the current directory.
	_, msg, err := e.open(link, "", "--passphrase-file", passFile)
	if err != nil {
		t.Fatalf("open: %v %s", err, msg)
	}
	got, err := os.ReadFile(filepath.Join(e.dir, "backup.tar"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("saved file: %v", err)
	}
	if st, _ := os.Stat(filepath.Join(e.dir, "backup.tar")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	// The name is taken now: never overwritten, saved alongside.
	if _, msg, err = e.open(link, "tr0ub4dor&3"); err != nil {
		t.Fatalf("second open: %v", err)
	}
	if !strings.Contains(msg, "backup (1).tar") {
		t.Fatalf("second open saved as: %s", msg)
	}
	// -o refuses an existing file before using a view.
	if _, _, err = e.open(link, "tr0ub4dor&3", "-o", filepath.Join(e.dir, "backup.tar")); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("-o existing: %v", err)
	}
	out := filepath.Join(e.dir, "copy.bin")
	if _, _, err = e.open(link, "tr0ub4dor&3", "-o", out); err != nil {
		t.Fatalf("-o: %v", err)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, data) {
		t.Fatal("-o content differs")
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) != 3 {
		t.Fatalf("left %d files (temp files not cleaned?)", len(entries))
	}
	if _, _, err = e.open(link, "tr0ub4dor&3"); err == nil {
		t.Fatal("a fourth view")
	}
}

func TestSPStdinLimits(t *testing.T) {
	e := newSPEnv(t)
	sio, _, _ := e.io("", "")
	if err := runSecret(context.Background(), nil, sio); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty stdin: %v", err)
	}
	sio, _, _ = e.io("\xff\xfe", "")
	if err := runSecret(context.Background(), []string{"-"}, sio); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("binary stdin: %v", err)
	}
	sio, _, _ = e.io(strings.Repeat("x", e2e.ChunkSize+1), "")
	if err := runSecret(context.Background(), nil, sio); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("big stdin: %v", err)
	}
	if _, _, err := e.open("https://example.test/s/AAAAAAAAAAAAAAAAAAAAAA", ""); err == nil {
		t.Fatal("a link without a key")
	}
}

func TestSPSecretLocalName(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":             "report.pdf",
		"../../etc/passwd":       "passwd",
		`C:\Users\x\evil.exe`:    "evil.exe",
		".bashrc":                "bashrc",
		"..":                     "secret.bin",
		"":                       "secret.bin",
		"invoice\u202efdp.exe":   "invoicefdp.exe",
		"a\x00b\nc.txt":          "abc.txt",
		"con.txt":                "_con.txt",
		`what?<>|*:".txt`:        "what_______.txt",
		"trailing. . ":           "trailing",
		"zero\u200bwidth.txt":    "zerowidth.txt",
		strings.Repeat("é", 150): strings.Repeat("é", 100),
	} {
		if got := secretLocalName(in); got != want {
			t.Errorf("secretLocalName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A malicious server: sp must refuse weak KDF parameters before asking for
// a passphrase, and refuse a wrong key commitment before decrypting.
func TestSPRefusesAMaliciousServer(t *testing.T) {
	salt := e2e.Encode(make([]byte, 16))
	var (
		mu     sync.Mutex
		kdf    any
		claims int
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/info"):
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "ready", "kind": "text", "salt": salt, "kdf": kdf, "size": 1024, "ct_size": 1040, "views_left": 1})
		case strings.HasSuffix(r.URL.Path, "/claim"):
			claims++
			_ = json.NewEncoder(w).Encode(map[string]any{"kind": "text", "commit": e2e.Encode(make([]byte, 32)), "meta_ct": e2e.Encode(make([]byte, e2e.SealedMetaSize)), "content": "", "views_left": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	id, _ := e2e.NewID()
	key, _ := e2e.NewLinkKey()
	link := e2e.FormatLink(srv.URL, id, key)
	asked := false
	run := func() error {
		var out, errb bytes.Buffer
		return runOpen(context.Background(), []string{link}, secretIO{
			in: strings.NewReader(""), out: &out, err: &errb, hc: srv.Client(),
			ask: func(string, bool) ([]byte, error) { asked = true; return []byte("pw"), nil },
		})
	}
	for _, bad := range []e2e.KDFParams{{MemKiB: 1024, Time: 3, Threads: 1}, {MemKiB: 1 << 30, Time: 3, Threads: 1}, {MemKiB: 65536, Time: 3, Threads: 4}} {
		mu.Lock()
		kdf = bad
		mu.Unlock()
		if err := run(); err == nil || !strings.Contains(err.Error(), "unsafe passphrase settings") {
			t.Fatalf("kdf %+v: %v", bad, err)
		}
		mu.Lock()
		n := claims
		mu.Unlock()
		if asked || n != 0 {
			t.Fatal("asked for a passphrase or claimed despite unsafe parameters")
		}
	}
	mu.Lock()
	kdf = nil
	mu.Unlock()
	if err := run(); !errors.Is(err, errVerify) {
		t.Fatalf("wrong commitment: %v", err)
	}
}

func TestSPUserAgentAndNoKeyOnOpen(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, r.UserAgent()+"|"+r.Header.Get("Authorization"))
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("SNAP_URL", "https://mine.example")
	t.Setenv("SNAP_KEY", "snap_secret_key")
	id, _ := e2e.NewID()
	key, _ := e2e.NewLinkKey()
	var out, errb bytes.Buffer
	err := runOpen(context.Background(), []string{e2e.FormatLink(srv.URL, id, key)}, secretIO{out: &out, err: &errb, hc: srv.Client()})
	if !errors.Is(err, errNoSecret) {
		t.Fatalf("open: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || !strings.HasPrefix(seen[0], "sp/") || strings.Contains(seen[0], "snap_secret_key") {
		t.Fatalf("requests: %q", seen)
	}
}
