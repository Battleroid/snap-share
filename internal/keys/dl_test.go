package keys_test

import (
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestCLIDownloads(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "cli")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cli, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(cli, "snap-cli-linux-arm64"), 0o755); err != nil { // a directory, not a build
		t.Fatal(err)
	}
	write("snap-cli-linux-amd64", "ELF linux build")
	write("snap-cli-windows-amd64.exe", "MZ windows build")
	write("snap-cli-linux-amd64.exe", "misnamed")
	write("notes.txt", "not a build")
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "secret"), filepath.Join(cli, "snap-cli-darwin-arm64")); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, testutil.Env("CLI_DIR", cli))

	res, body := e.do("GET", "/dl/snap-cli-linux-amd64", nil)
	if res.StatusCode != 200 || body != "ELF linux build" {
		t.Fatalf("linux build: %d %q", res.StatusCode, body)
	}
	for k, v := range map[string]string{
		"Content-Type":           "application/octet-stream",
		"Content-Disposition":    `attachment; filename="snap-cli-linux-amd64"`,
		"X-Content-Type-Options": "nosniff",
	} {
		if got := res.Header.Get(k); got != v {
			t.Errorf("%s: %q", k, got)
		}
	}
	if res, body := e.do("GET", "/dl/snap-cli-windows-amd64.exe", nil); res.StatusCode != 200 || body != "MZ windows build" {
		t.Errorf("windows build: %d", res.StatusCode)
	}
	if res, _ := e.do("HEAD", "/dl/snap-cli-linux-amd64", nil); res.StatusCode != 200 || res.ContentLength != int64(len("ELF linux build")) {
		t.Errorf("HEAD: %d %d", res.StatusCode, res.ContentLength)
	}
	res, body = e.do("GET", "/dl/snap-cli-linux-amd64", nil, header("Range", "bytes=0-2"))
	if res.StatusCode != 206 || body != "ELF" {
		t.Errorf("range: %d %q", res.StatusCode, body)
	}
	for _, p := range []string{
		"/dl/snap-cli-linux-amd64.exe",  // .exe only for windows
		"/dl/snap-cli-windows-amd64",    // windows needs .exe
		"/dl/snap-cli-freebsd-amd64",    // valid name, absent
		"/dl/snap-cli-linux-arm64",      // a directory
		"/dl/snap-cli-darwin-arm64",     // a symlink out of the directory
		"/dl/notes.txt",                 // not a release name
		"/dl/snap-cli-linux-mips",       // unknown arch
		"/dl/SNAP-CLI-LINUX-AMD64",      // case matters
		"/dl/snap-cli-linux-amd64%00",   // NUL
		"/dl/..%2fsecret",               // traversal
		"/dl/snap-cli-linux-amd64/",     // trailing slash
		"/dl/%2e%2e%2fcli%2fnotes.txt",  // encoded traversal
		"/dl/snap-cli-linux-amd64.sig",  // other suffixes
		"/dl/x/snap-cli-linux-amd64",    // nested
		"/dl/snap-cli-linux-amd64%5c..", // backslash
	} {
		res, body := e.do("GET", p, nil)
		if res.StatusCode != 404 && res.StatusCode != 400 && res.StatusCode != 301 && res.StatusCode != 307 {
			t.Errorf("%s: %d %q", p, res.StatusCode, body)
		}
		if strings.Contains(body, "outside") || strings.Contains(body, "not a build") || strings.Contains(body, "misnamed") {
			t.Errorf("%s leaked a file", p)
		}
	}

	// The integrations page lists the builds and the install line.
	ann := e.user("ann", auth.RoleUser)
	res, body = e.do("GET", "/settings/integrations", nil, withCookie(e.session(ann)))
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("integrations: %d", res.StatusCode)
	}
	body = html.UnescapeString(body)
	for _, want := range []string{
		"curl -fLo sp " + e.s.URL + "/dl/snap-cli-linux-amd64 && chmod +x sp && ./sp login " + e.s.URL,
		`href="/dl/snap-cli-windows-amd64.exe"`,
		"/api/v1/upload/backup.tar.zst?format=url",
		"--data-binary @-",
		`action="/settings/integrations/sharex/files"`, `action="/settings/integrations/sharex/text"`, `action="/settings/integrations/sharex/links"`,
		// Upload options: the section, the table, and examples with this origin.
		`href="#options"`, `<section id="options">`, "expiry_action", "{inputbox:Password, or leave blank}", `"Body": "FormURLEncoded"`,
		`-F expires=7d -F visibility=private -F naming=words "` + e.s.URL + `/api/v1/upload?format=url"`,
		`-H "X-Snap-Password: $ITEM_PASSWORD"`, "sp up --expire 7d --private --name words photo.png",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("integrations page lacks %q", want)
		}
	}
	if strings.Contains(body, "snap-cli-linux-amd64.exe") || strings.Contains(body, "darwin") {
		t.Error("page lists files that are not served")
	}
}

func TestCLIDownloadsMissingDir(t *testing.T) {
	e := newEnv(t, testutil.Env("CLI_DIR", filepath.Join(t.TempDir(), "nope")))
	if res, _ := e.do("GET", "/dl/snap-cli-linux-amd64", nil); res.StatusCode != 404 {
		t.Errorf("missing dir: %d", res.StatusCode)
	}
	ann := e.user("ann", auth.RoleUser)
	_, body := e.do("GET", "/settings/integrations", nil, withCookie(e.session(ann)))
	if !strings.Contains(body, "No sp builds on this server.") {
		t.Error("no notice without builds")
	}
}
