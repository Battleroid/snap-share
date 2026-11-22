package accounts_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

const pw = "correct horse battery"

func newServer(t *testing.T, opts ...testutil.Option) *testutil.Server {
	t.Helper()
	return testutil.NewServer(t, append([]testutil.Option{testutil.RealAuth()}, opts...)...)
}

func createUser(t *testing.T, s *testutil.Server, name, email string, role auth.Role) *accounts.User {
	t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword(ctx, pw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.App.Accounts.Store().CreateUser(ctx, accounts.NewUser{Username: name, Email: email, EmailVerified: email != "", PasswordHash: hash, Role: role}, accounts.AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// response is what tests look at; the body is read and closed by browser.do.
type response struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r *response) Cookies() []*http.Cookie { return r.cookies }

// browser is one cookie jar talking to the test server like a browser would.
type browser struct {
	t *testing.T
	s *testutil.Server
	c *http.Client
}

func newBrowser(t *testing.T, s *testutil.Server) *browser {
	// s.Client() hands out one shared *http.Client, so build our own around its
	// transport to give every browser its own cookie jar.
	base := s.Client()
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, s: s, c: &http.Client{Transport: base.Transport, Jar: jar, CheckRedirect: base.CheckRedirect}}
}

func (b *browser) do(req *http.Request) (*response, string) {
	b.t.Helper()
	res, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	return &response{StatusCode: res.StatusCode, Header: res.Header, cookies: res.Cookies()}, string(body)
}

func (b *browser) get(path string) (*response, string) {
	b.t.Helper()
	return b.do(b.s.Request(b.t, "GET", path, nil))
}

// getURL fetches an absolute URL (the stub provider) without same-origin headers.
func (b *browser) getURL(u string) (*response, string) {
	b.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "GET", u, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	return b.do(req)
}

func (b *browser) post(path string, form url.Values, mut ...func(*http.Request)) (*response, string) {
	b.t.Helper()
	req := b.s.Request(b.t, "POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, m := range mut {
		m(req)
	}
	return b.do(req)
}

// postJSON posts a form the way token-page.js does and decodes the JSON answer.
func (b *browser) postJSON(path string, form url.Values) (int, map[string]string) {
	b.t.Helper()
	res, body := b.post(path, form, func(r *http.Request) { r.Header.Set("Accept", "application/json") })
	var out map[string]string
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		b.t.Fatalf("%s: not JSON (%d): %s", path, res.StatusCode, body)
	}
	return res.StatusCode, out
}

func (b *browser) login(login, password string) *response {
	b.t.Helper()
	res, _ := b.post("/login", url.Values{"login": {login}, "password": {password}})
	return res
}

func (b *browser) mustLogin(login string) {
	b.t.Helper()
	if res := b.login(login, pw); res.StatusCode != http.StatusSeeOther {
		b.t.Fatalf("login %s: %d", login, res.StatusCode)
	}
}

// cookie returns the jar's value for name ("" when absent).
func (b *browser) cookie(name string) string {
	u, _ := url.Parse(b.s.URL)
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// whoami returns the signed-in username, or "" when anonymous.
func (b *browser) whoami() string {
	b.t.Helper()
	res, body := b.get("/settings/profile")
	if res.StatusCode != http.StatusOK {
		return ""
	}
	m := regexp.MustCompile(`name="username" type="text" value="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		b.t.Fatalf("no username on the profile page")
	}
	return m[1]
}

var linkRE = regexp.MustCompile(`https://[^\s"]+?#t=([A-Za-z0-9_-]{43})`)

// lastLink returns the newest emailed link's token for path (e.g. "/verify").
// Some mail is sent in the background, so it waits a little.
func lastLink(t *testing.T, s *testutil.Server, to, path string) string {
	t.Helper()
	for range 100 {
		if tok := findLink(t, s, to, path); tok != "" {
			return tok
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %s link mailed to %s (%d messages)", path, to, len(s.Mail.Sent()))
	return ""
}

func findLink(t *testing.T, s *testutil.Server, to, path string) string {
	t.Helper()
	sent := s.Mail.Sent()
	for i := len(sent) - 1; i >= 0; i-- {
		m := sent[i]
		if len(m.To) == 0 || m.To[0] != to {
			continue
		}
		if l := linkRE.FindStringSubmatch(m.Text); l != nil && strings.Contains(l[0], path+"#t=") {
			if !strings.Contains(m.HTML, l[0]) {
				t.Fatalf("HTML body lacks the link")
			}
			return l[1]
		}
	}
	return ""
}

func countAudit(t *testing.T, s *testutil.Server, action string) int {
	t.Helper()
	var n int
	if err := s.App.DB.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
