package notify_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/testutil"
)

const firefox = "Mozilla/5.0 (Macintosh; Intel Mac OS X 15.1; rv:140.0) Gecko/20100101 Firefox/140.0"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// syncBuffer is a log sink safe for concurrent writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	t     *testing.T
	s     *testutil.Server
	ctx   context.Context
	clk   *clock
	q     *jobs.Queue
	own   *notify.Service // with the fake clock, its own queue and the test ntfy's TLS roots
	logs  *syncBuffer
	ntfy  *ntfyServer
	link  int64 // ann's short link "abc"
	photo int64 // ann's file "photo.jpg"
}

// ntfyServer records publishes.
type ntfyServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []*http.Request
	bodies []string
	status int
}

func (n *ntfyServer) last() (*http.Request, string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.reqs) == 0 {
		return nil, ""
	}
	return n.reqs[len(n.reqs)-1], n.bodies[len(n.bodies)-1]
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := testutil.NewServer(t, testutil.WithSMTP())
	e := &env{t: t, s: s, ctx: context.Background(), clk: &clock{t: time.Date(2026, 11, 22, 14, 3, 0, 0, time.UTC)}, logs: &syncBuffer{}}
	e.exec(`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at) VALUES
  (1, 'ann', 'ann@example.test', 1, 1, 1), (2, 'bob', 'bob@example.test', 1, 1, 1), (3, 'cat', 'cat@example.test', NULL, 1, 1)`)
	s.Auth.Add("ann", auth.Principal{UserID: 1, EmailVerified: true})
	s.Auth.Add("bob", auth.Principal{UserID: 2, EmailVerified: true})
	s.Auth.Add("cat", auth.Principal{UserID: 3})
	s.Auth.Add("ann-key", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 100, Scopes: auth.AllScopes[:8]})
	e.exec(`INSERT INTO items (id, owner_id, kind, slug, target_url, created_at, updated_at) VALUES (10, 1, 'link', 'abc', 'https://example.org/', 1, 1)`)
	e.exec(`INSERT INTO items (id, owner_id, kind, slug, filename, mime, created_at, updated_at) VALUES (11, 1, 'file', 'pic', 'photo.jpg', 'image/jpeg', 1, 1)`)
	e.link, e.photo = 10, 11

	e.ntfy = &ntfyServer{status: http.StatusOK}
	e.ntfy.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.ntfy.mu.Lock()
		e.ntfy.reqs = append(e.ntfy.reqs, r)
		e.ntfy.bodies = append(e.ntfy.bodies, string(b))
		st := e.ntfy.status
		e.ntfy.mu.Unlock()
		w.WriteHeader(st)
		_, _ = io.WriteString(w, `{"id":"x"}`)
	}))
	t.Cleanup(e.ntfy.srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(e.ntfy.srv.Certificate())

	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.q = jobs.New(s.App.DB, log)
	own, err := notify.New(notify.Deps{
		Cfg: s.App.Cfg, Log: log, DB: s.App.DB, Settings: s.App.Settings, Jobs: e.q, Mail: s.Mail, Items: s.App.Items,
		Render: s.App.Render, Errors: s.App.Errors, Now: e.clk.Now, TLS: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.own = own
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.s.App.DB.W.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.s.App.DB.R.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

// jobs returns the payloads of queued jobs of kind, oldest first.
func (e *env) jobs(kind string) []string {
	e.t.Helper()
	rows, err := e.s.App.DB.R.Query(`SELECT payload FROM jobs WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func (e *env) clearJobs() { e.exec(`DELETE FROM jobs`) }

// sendAll runs every queued notify.send job through the own service.
func (e *env) sendAll() []error {
	e.t.Helper()
	var errs []error
	for _, p := range e.jobs(notify.JobSend) {
		errs = append(errs, e.own.HandleSend(e.ctx, json.RawMessage(p)))
	}
	e.exec(`DELETE FROM jobs WHERE kind = ?`, notify.JobSend)
	return errs
}

func (e *env) open(j map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(j)
	if err := e.own.HandleOpen(e.ctx, b); err != nil {
		e.t.Fatalf("open %s: %v", b, err)
	}
}

// result is a finished response; testutil.Server.Do has read and closed the body.
type result struct {
	StatusCode int
	Header     http.Header
}

func (e *env) send(req *http.Request) (result, string) {
	e.t.Helper()
	res, body := e.s.Do(e.t, req) //nolint:bodyclose // Do reads and closes the body
	return result{StatusCode: res.StatusCode, Header: res.Header}, body
}

func (e *env) do(method, path string, body io.Reader, opts ...func(*http.Request)) (result, string) {
	e.t.Helper()
	req := e.s.Request(e.t, method, path, body)
	for _, o := range opts {
		o(req)
	}
	return e.send(req)
}

func as(name string) func(*http.Request) { return func(r *http.Request) { authtest.Session(r, name) } }
func form() func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }
}
func wantJSON() func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Accept", "application/json") }
}

func TestDefaultsOff(t *testing.T) {
	e := newEnv(t)
	p, err := e.s.App.Notify.Prefs(e.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if p.EmailEnabled || p.NtfyEnabled || p.Default != notify.ModeNone || p.NtfyServer != "https://ntfy.sh" || p.NtfyTokenSet || p.AnyChannel() {
		t.Fatalf("defaults %+v", p)
	}
	// Opens and secrets notify nobody who hasn't opted in.
	e.s.App.Notify.ItemsOpened(e.ctx, []items.Opened{{ItemID: e.link, First: true, Count: 1, From: e.clk.Now(), To: e.clk.Now()}})
	e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, Kind: stats.KindBurned, At: e.clk.Now()})
	e.own.Close()
	if n := e.count(`SELECT count(*) FROM jobs`); n != 0 {
		t.Fatalf("%d jobs queued with everything off", n)
	}
	// A row created by one setting keeps the others off.
	if err := e.s.App.Notify.SetDefault(e.ctx, 1, notify.ModeEvery); err != nil {
		t.Fatal(err)
	}
	e.s.App.Notify.ItemsOpened(e.ctx, []items.Opened{{ItemID: e.link, First: true, Count: 1, From: e.clk.Now(), To: e.clk.Now()}})
	if n := e.count(`SELECT count(*) FROM jobs`); n != 0 {
		t.Fatalf("%d jobs queued with no channel on", n)
	}
	res, body := e.do("GET", "/settings/notifications", nil, as("ann"))
	if res.StatusCode != 200 || strings.Contains(body, `data-notify-channel="email" checked`) || strings.Contains(body, `data-notify-channel="ntfy" checked`) {
		t.Fatalf("page: %d", res.StatusCode)
	}
	if !strings.Contains(body, `href="/settings/notifications" aria-current="page"`) {
		t.Fatal("settings tab not current")
	}
	for _, form := range []string{`action="/settings/notifications/events"`, `action="/settings/notifications/ntfy"`, `data-notify-test="email"`, `data-notify-test="ntfy"`} {
		if n := strings.Count(body, form); n != 1 {
			t.Errorf("%d forms with %s, want 1", n, form)
		}
	}
}

func TestNtfyTokenEncryptedAtRest(t *testing.T) {
	e := newEnv(t)
	const token = "tk_AbCdEfGhIjKlMnOpQrStUvWxYz012"
	tok := token
	if err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: "https://NTFY.example.com/sub/", Topic: "snap-ann_1", Token: &tok}); err != nil {
		t.Fatal(err)
	}
	var ct []byte
	var server, topic string
	if err := e.s.App.DB.R.QueryRow(`SELECT ntfy_token_ct, ntfy_server_url, ntfy_topic FROM notify_prefs WHERE user_id = 1`).Scan(&ct, &server, &topic); err != nil {
		t.Fatal(err)
	}
	if len(ct) < len(token)+12+16 || bytes.Contains(ct, []byte(token)) || bytes.Contains(ct, []byte("AbCdEf")) {
		t.Fatalf("token stored as %q", ct)
	}
	if server != "https://ntfy.example.com/sub" || topic != "snap-ann_1" {
		t.Fatalf("server %q topic %q", server, topic)
	}
	// Never sent back to the browser: the page says "set".
	res, body := e.do("GET", "/settings/notifications", nil, as("ann"))
	if res.StatusCode != 200 || strings.Contains(body, token) || strings.Contains(body, "AbCdEf") || !strings.Contains(body, "access token · set") {
		t.Fatalf("page %d leaks or lacks the token state", res.StatusCode)
	}
	// Copied to another user's row, the ciphertext does not open (the user id is bound in).
	e.exec(`INSERT INTO notify_prefs (user_id, ntfy_enabled, ntfy_server_url, ntfy_topic, ntfy_token_ct, updated_at) VALUES (2, 1, ?, 't', ?, 1)`, e.ntfy.srv.URL, ct)
	if err := notify.OutboundAllow.Set(e.ctx, e.s.App.Settings, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"user_id": 2, "channel": "ntfy", "message": map[string]any{"kind": "test", "title": "x"}})
	if err := e.own.HandleSend(e.ctx, b); err == nil || !strings.Contains(err.Error(), "cannot be decrypted") {
		t.Fatalf("bob used ann's token: %v", err)
	}
	if r, _ := e.ntfy.last(); r != nil {
		t.Fatal("published with a token that should not open")
	}
	// An empty token keeps it for the same server; "remove" clears it.
	if err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: "https://ntfy.example.com/sub", Topic: "snap-ann_2"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.s.App.Notify.Prefs(e.ctx, 1); !p.NtfyTokenSet || p.NtfyTopic != "snap-ann_2" {
		t.Fatalf("after save without token: %+v", p)
	}
	empty := ""
	if err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Topic: "snap-ann_1", Token: &empty}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.s.App.Notify.Prefs(e.ctx, 1); p.NtfyTokenSet {
		t.Fatal("token not removed")
	}
	// Bad input is refused field by field.
	bad := "has space"
	err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: "ftp://x", Topic: "no/slash", Token: &bad})
	var fe notify.FieldErrors
	if !errors.As(err, &fe) || fe["server"] == "" || fe["topic"] == "" || fe["token"] == "" {
		t.Fatalf("validation: %v", err)
	}
	for _, s := range []string{"http://ntfy.example.com", "https://u:p@ntfy.example.com", "https://ntfy.example.com/?x=1", "https://ntfy.example.com/#f"} {
		if err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: s, Topic: "t"}); err == nil {
			t.Errorf("server %q accepted", s)
		}
	}
}

func TestFirstOpenHookFiresOnce(t *testing.T) {
	e := newEnv(t)
	if err := e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true); err != nil {
		t.Fatal(err)
	}
	if err := e.s.App.Notify.SetDefault(e.ctx, 1, notify.ModeFirst); err != nil {
		t.Fatal(err)
	}
	visit := func(ua string) {
		t.Helper()
		req := e.s.Request(t, "GET", "/abc", nil)
		req.Header.Del("Sec-Fetch-Site")
		req.Header.Del("Origin")
		req.Header.Set("User-Agent", ua)
		req.Header.Set("X-Forwarded-For", "203.0.113.4")
		if res, _ := e.send(req); res.StatusCode != http.StatusFound {
			t.Fatalf("visit: %d", res.StatusCode)
		}
	}
	// The owner's own visit and a preview bot are not opens.
	req := e.s.Request(t, "GET", "/abc", nil)
	req.Header.Set("User-Agent", firefox)
	authtest.Session(req, "ann")
	e.send(req)
	visit("Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)")
	if err := e.s.App.Items.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.jobs(notify.JobOpen)); n != 0 {
		t.Fatalf("%d open jobs before anyone opened it", n)
	}

	visit(firefox)
	visit("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/159.0.0.0 Safari/537.36")
	if err := e.s.App.Items.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	opens := e.jobs(notify.JobOpen)
	if len(opens) != 1 || !strings.Contains(opens[0], `"first":true`) || !strings.Contains(opens[0], `"count":2`) {
		t.Fatalf("open jobs %v", opens)
	}
	// Later opens, with first-open notifications, queue nothing more.
	visit(firefox + " again")
	if err := e.s.App.Items.FlushCounters(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.jobs(notify.JobOpen)); n != 1 {
		t.Fatalf("%d open jobs after a second flush", n)
	}

	// The job names the first visitor.
	if err := e.s.App.Stats.Flush(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.own.HandleOpen(e.ctx, json.RawMessage(opens[0])); err != nil {
		t.Fatal(err)
	}
	if errs := e.sendAll(); len(errs) != 1 || errs[0] != nil {
		t.Fatalf("sends %v", errs)
	}
	sent := e.s.Mail.Sent()
	if len(sent) != 1 {
		t.Fatalf("%d mails", len(sent))
	}
	m := sent[0]
	if m.To[0] != "ann@example.test" || !strings.HasPrefix(m.Subject, "https://example.org/ opened 2 times · Firefox · macOS · ") ||
		!strings.Contains(m.Text, e.s.URL+"/abc") || !strings.Contains(m.HTML, e.s.URL+"/abc") {
		t.Fatalf("mail %+v", m)
	}
}

func TestEveryOpenIsCoalesced(t *testing.T) {
	e := newEnv(t)
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true)
	_ = e.s.App.Notify.SetDefault(e.ctx, 1, notify.ModeEvery)
	ms := func() int64 { return e.clk.Now().UnixMilli() }

	e.open(map[string]any{"item_id": e.photo, "first": true, "count": 1, "from": ms(), "to": ms()})
	if n := len(e.jobs(notify.JobSend)); n != 1 {
		t.Fatalf("first open: %d sends", n)
	}
	e.clk.Add(time.Minute)
	e.open(map[string]any{"item_id": e.photo, "count": 2, "from": ms(), "to": ms()})
	e.clk.Add(time.Minute)
	e.open(map[string]any{"item_id": e.photo, "count": 1, "from": ms(), "to": ms()})
	e.open(map[string]any{"item_id": e.photo, "first": true, "count": 1, "from": ms(), "to": ms()}) // someone new: held too
	if n := len(e.jobs(notify.JobSend)); n != 1 {
		t.Fatalf("inside the window: %d sends", n)
	}
	summaries := e.jobs(notify.JobOpen)
	if len(summaries) != 1 || !strings.Contains(summaries[0], `"summary":true`) {
		t.Fatalf("summary jobs %v", summaries)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ? AND run_after - created_at > 12 * 60 * 1000`, notify.JobOpen); n != 1 {
		t.Fatal("summary is not scheduled for the end of the window")
	}

	e.clk.Add(13 * time.Minute)
	e.open(map[string]any{"item_id": e.photo, "summary": true})
	e.open(map[string]any{"item_id": e.photo, "summary": true}) // a duplicate finds nothing pending
	sends := e.jobs(notify.JobSend)
	if len(sends) != 2 || !strings.Contains(sends[1], "photo.jpg opened 4 more times") || !strings.Contains(sends[1], "since 14:04 UTC") {
		t.Fatalf("after the window: %v", sends)
	}
	// The summary starts a new window; after it, the next open goes out at once.
	e.clk.Add(10 * time.Minute)
	e.open(map[string]any{"item_id": e.photo, "count": 1, "from": ms(), "to": ms()})
	if n := len(e.jobs(notify.JobSend)); n != 2 {
		t.Fatalf("inside the summary's window: %d sends", n)
	}
	e.clk.Add(6 * time.Minute)
	e.open(map[string]any{"item_id": e.photo, "summary": true})
	e.clk.Add(16 * time.Minute)
	e.open(map[string]any{"item_id": e.photo, "count": 1, "from": ms(), "to": ms()})
	if n := len(e.jobs(notify.JobSend)); n != 4 {
		t.Fatalf("after quiet: %d sends", n)
	}

	// A per-item override wins over the default; "none" silences the item.
	e.clearJobs()
	if err := e.own.SetOverride(e.ctx, 1, e.photo, notify.ModeNone); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(time.Hour)
	e.open(map[string]any{"item_id": e.photo, "first": true, "count": 1, "from": ms(), "to": ms()})
	if n := len(e.jobs(notify.JobSend)); n != 0 {
		t.Fatalf("override none: %d sends", n)
	}
	// An override written by someone who no longer owns the item is ignored.
	e.exec(`UPDATE notify_overrides SET user_id = 2`)
	e.open(map[string]any{"item_id": e.photo, "count": 1, "from": ms(), "to": ms()})
	if n := len(e.jobs(notify.JobSend)); n != 1 {
		t.Fatalf("stale override: %d sends", n)
	}
	if err := e.own.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM notify_overrides`); n != 0 {
		t.Fatalf("sweep left %d stale overrides", n)
	}
}

func TestSecretMessagesHaveNoLinkOrKey(t *testing.T) {
	e := newEnv(t)
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true)
	if err := notify.OutboundAllow.Set(e.ctx, e.s.App.Settings, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	_ = e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: e.ntfy.srv.URL, Topic: "ann"})
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelNtfy, true)
	const id = "QUJDREVGR0hJSktMTU5PUA"
	n := secrets.Notification{OwnerID: 1, SecretID: id, Note: "prod db", Kind: stats.KindRevealed, Requested: true, ViewsLeft: 1, MaxViews: 2,
		At: e.clk.Now(), IP: "203.0.113.4", UASummary: "Firefox · macOS\r\nBcc: x@y"}
	e.own.Notify(e.ctx, n)
	e.own.Close()
	n.Kind, n.Reason = stats.KindBurned, "failed attempts"
	e.own.Notify(e.ctx, n)
	e.own.Close()
	for _, p := range e.jobs(notify.JobSend) {
		if strings.Contains(p, id) || strings.Contains(p, "/s/") {
			t.Fatalf("job payload names the secret: %s", p)
		}
	}
	if errs := e.sendAll(); len(errs) != 4 {
		t.Fatalf("sends %v", errs)
	}
	sent := e.s.Mail.Sent()
	if len(sent) != 2 {
		t.Fatalf("%d mails", len(sent))
	}
	for _, m := range sent {
		for _, part := range []string{m.Subject, m.Text, m.HTML} {
			if strings.Contains(part, id) || strings.Contains(part, "/s/") {
				t.Fatalf("mail names the secret: %q", part)
			}
		}
		if strings.ContainsAny(m.Subject, "\r\n") || !strings.Contains(m.Text, e.s.URL+"/dashboard/secrets") || !strings.Contains(m.Subject, `"prod db"`) {
			t.Fatalf("mail %+v", m)
		}
	}
	if !strings.Contains(sent[0].Subject, "opened · Firefox · macOS Bcc: x@y · 203.0.113.4 · 14:03 UTC · 1 of 2 views left") {
		t.Fatalf("reveal subject %q", sent[0].Subject)
	}
	if !strings.Contains(sent[1].Subject, "destroyed after failed passphrase attempts") {
		t.Fatalf("burn subject %q", sent[1].Subject)
	}
	r, body := e.ntfy.last()
	if r == nil || r.Header.Get("Click") != e.s.URL+"/dashboard/secrets" || strings.Contains(body, id) || r.Header.Get("Priority") != "4" {
		t.Fatalf("ntfy %v %q", r, body)
	}
}

func TestHourlyLimitSparesBurns(t *testing.T) {
	e := newEnv(t)
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true)
	for range notify.MessagesPerHour + 5 {
		e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, Kind: stats.KindRevealed, Requested: true, At: e.clk.Now(), MaxViews: 9})
	}
	e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, Kind: stats.KindBurned, At: e.clk.Now()})
	e.own.Close()
	sends := e.jobs(notify.JobSend)
	burns := 0
	for _, p := range sends {
		if strings.Contains(p, "secret_burned") {
			burns++
		}
	}
	if len(sends) != notify.MessagesPerHour+1 || burns != 1 {
		t.Fatalf("%d sends, %d burns", len(sends), burns)
	}
}

// Every reveal of an owned secret reaches the notifier; email and ntfy
// still report only those of secrets with notify set, and every burn.
func TestPushOnlyRequestedReveals(t *testing.T) {
	e := newEnv(t)
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true)
	n := secrets.Notification{OwnerID: 1, Note: "prod db", Kind: stats.KindRevealed, At: e.clk.Now(), MaxViews: 2}
	for i, step := range []struct {
		kind      stats.Kind
		requested bool
		sends     int
	}{{stats.KindRevealed, false, 0}, {stats.KindRevealed, true, 1}, {stats.KindBurned, false, 2}, {stats.KindBurned, true, 3}} {
		n.Kind, n.Requested = step.kind, step.requested
		e.own.Notify(e.ctx, n)
		e.own.Close()
		if got := len(e.jobs(notify.JobSend)); got != step.sends {
			t.Fatalf("step %d (%s, requested %v): %d sends, want %d", i, step.kind, step.requested, got, step.sends)
		}
	}
}

func TestNtfyDelivery(t *testing.T) {
	e := newEnv(t)
	tok := "tk_deliverytoken"
	if err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: e.ntfy.srv.URL, Topic: "snap-topic", Token: &tok}); err != nil {
		t.Fatal(err)
	}
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelNtfy, true)
	send := func() error {
		b, _ := json.Marshal(map[string]any{"user_id": 1, "channel": "ntfy", "message": map[string]any{
			"kind": "open", "title": "fötø.jpg opened", "detail": "Firefox · macOS · 203.0.113.4 · 14:03 UTC",
			"click": e.s.URL + "/pic", "tags": []string{"snap", "open"},
		}})
		return e.own.HandleSend(e.ctx, b)
	}
	// 127.0.0.1 is refused until the admin allows it.
	if err := send(); err == nil || err.Error() != "ntfy: address not allowed" {
		t.Fatalf("not allowlisted: %v", err)
	}
	if err := notify.OutboundAllow.Set(e.ctx, e.s.App.Settings, []string{"127.0.0.1/32"}); err != nil {
		t.Fatal(err)
	}
	if err := send(); err != nil {
		t.Fatal(err)
	}
	r, body := e.ntfy.last()
	title, err := new(mime.WordDecoder).DecodeHeader(r.Header.Get("Title"))
	if err != nil || title != "fötø.jpg opened" || !strings.HasPrefix(r.Header.Get("Title"), "=?UTF-8?") {
		t.Fatalf("title %q %v", r.Header.Get("Title"), err)
	}
	if r.Method != http.MethodPost || r.URL.Path != "/snap-topic" || r.Header.Get("Authorization") != "Bearer "+tok ||
		r.Header.Get("Tags") != "snap,open" || r.Header.Get("Click") != e.s.URL+"/pic" || body != "Firefox · macOS · 203.0.113.4 · 14:03 UTC" ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("publish %s %s %v %q", r.Method, r.URL, r.Header, body)
	}
	st, _ := e.s.App.Notify.Statuses(e.ctx, 1)
	if st["ntfy"].LastOK == nil || st["ntfy"].Failing() {
		t.Fatalf("status %+v", st["ntfy"])
	}
	// The settings page shows the last failure; no token or topic in logs or errors.
	e.ntfy.mu.Lock()
	e.ntfy.status = http.StatusUnauthorized
	e.ntfy.mu.Unlock()
	e.clk.Add(time.Minute)
	if err := send(); err == nil || err.Error() != "ntfy: server answered 401" {
		t.Fatalf("401: %v", err)
	}
	_, page := e.do("GET", "/settings/notifications", nil, as("ann"))
	if !strings.Contains(page, "last delivery failed") || !strings.Contains(page, "ntfy: server answered 401") || strings.Contains(page, tok) {
		t.Fatal("page does not show the failure, or shows the token")
	}
	for _, secret := range []string{tok, "snap-topic"} {
		if strings.Contains(e.logs.String(), secret) {
			t.Fatalf("logs contain %q:\n%s", secret, e.logs.String())
		}
	}
}

func TestSendJobRetries(t *testing.T) {
	e := newEnv(t)
	_ = notify.OutboundAllow.Set(e.ctx, e.s.App.Settings, []string{"127.0.0.1"})
	_ = e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: e.ntfy.srv.URL, Topic: "retry"})
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelNtfy, true)
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() { defer close(done); e.q.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	state := func(id int64) (string, int, string) {
		var st, last string
		var attempts int
		_ = e.s.App.DB.R.QueryRow(`SELECT state, attempts, coalesce(last_error, '') FROM jobs WHERE id = ?`, id).Scan(&st, &attempts, &last)
		return st, attempts, last
	}
	wait := func(id int64, want func(string, int) bool) (string, int, string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if st, n, last := state(id); want(st, n) {
				return st, n, last
			}
			time.Sleep(20 * time.Millisecond)
		}
		st, n, last := state(id)
		t.Fatalf("job %d: %s after %d attempts (%s)", id, st, n, last)
		return "", 0, ""
	}
	enqueue := func() int64 {
		id, err := e.q.Enqueue(e.ctx, notify.JobSend, map[string]any{"user_id": 1, "channel": "ntfy",
			"message": map[string]any{"kind": "test", "title": "t"}}, jobs.MaxAttempts(5))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	// A server error is retried later.
	e.ntfy.mu.Lock()
	e.ntfy.status = http.StatusBadGateway
	e.ntfy.mu.Unlock()
	id := enqueue()
	st, _, last := wait(id, func(st string, n int) bool { return st == "queued" && n == 1 })
	if st != "queued" || last != "ntfy: server answered 502" {
		t.Fatalf("5xx: %s %q", st, last)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE id = ? AND run_after > ?`, id, time.Now().UnixMilli()); n != 1 {
		t.Fatal("no backoff")
	}
	// A 4xx fails at once.
	e.ntfy.mu.Lock()
	e.ntfy.status = http.StatusForbidden
	e.ntfy.mu.Unlock()
	id = enqueue()
	if st, n, last := wait(id, func(st string, _ int) bool { return st == "failed" }); n != 1 || last != "ntfy: server answered 403" {
		t.Fatalf("4xx: %s %d %q", st, n, last)
	}
	// So does a blocked address.
	_ = notify.OutboundAllow.Set(e.ctx, e.s.App.Settings, []string{})
	id = enqueue()
	if _, n, last := wait(id, func(st string, _ int) bool { return st == "failed" }); n != 1 || last != "ntfy: address not allowed" {
		t.Fatalf("blocked: %d %q", n, last)
	}
	// A channel turned off meanwhile is skipped, not failed.
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelNtfy, false)
	id = enqueue()
	wait(id, func(st string, _ int) bool { return st == "done" })
}

func TestSettingsPage(t *testing.T) {
	e := newEnv(t)
	post := func(path string, v url.Values, opts ...func(*http.Request)) (result, string) {
		return e.do("POST", path, strings.NewReader(v.Encode()), append([]func(*http.Request){form()}, opts...)...)
	}
	// Cross-origin posts are refused (CSRF) and change nothing.
	res, _ := post("/settings/notifications/channel", url.Values{"channel": {"email"}, "enabled": {"1"}}, as("ann"), func(r *http.Request) {
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Origin", "https://evil.example")
	})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site: %d", res.StatusCode)
	}
	for _, path := range []string{"/settings/notifications/ntfy", "/settings/notifications/events", "/settings/notifications/test"} {
		res, _ := post(path, url.Values{"event": {"every"}, "topic": {"x"}, "channel": {"email"}}, as("ann"), func(r *http.Request) {
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			r.Header.Set("Origin", "https://evil.example")
		})
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-site %s: %d", path, res.StatusCode)
		}
	}
	if p, _ := e.s.App.Notify.Prefs(e.ctx, 1); p.EmailEnabled || p.Default != notify.ModeNone {
		t.Fatalf("cross-site changed prefs: %+v", p)
	}
	// API keys can't use the settings.
	res, _ = post("/settings/notifications/channel", url.Values{"channel": {"email"}, "enabled": {"1"}}, func(r *http.Request) {
		authtest.Bearer(r, "ann-key")
		r.Header.Del("Sec-Fetch-Site")
		r.Header.Del("Origin")
	})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("api key: %d", res.StatusCode)
	}

	// The Switch endpoint answers JSON.
	res, body := post("/settings/notifications/channel", url.Values{"channel": {"email"}, "enabled": {"1"}}, as("ann"), wantJSON())
	if res.StatusCode != 200 || !strings.Contains(body, `"email":true`) {
		t.Fatalf("enable email: %d %s", res.StatusCode, body)
	}
	res, body = post("/settings/notifications/channel", url.Values{"channel": {"ntfy"}, "enabled": {"1"}}, as("ann"), wantJSON())
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "topic") {
		t.Fatalf("ntfy without topic: %d %s", res.StatusCode, body)
	}
	res, body = post("/settings/notifications/channel", url.Values{"channel": {"email"}, "enabled": {"1"}}, as("cat"), wantJSON())
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "Verify") {
		t.Fatalf("unverified email: %d %s", res.StatusCode, body)
	}

	// The ntfy form validates and never echoes the token.
	res, body = post("/settings/notifications/ntfy", url.Values{"server": {"https://ntfy.sh"}, "topic": {"bad topic"}, "token": {"tk_x"}}, as("ann"))
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "letters, digits") || strings.Contains(body, "tk_x") {
		t.Fatalf("bad topic: %d", res.StatusCode)
	}
	res, _ = post("/settings/notifications/ntfy", url.Values{"server": {"https://ntfy.sh"}, "topic": {"ann-alerts"}, "token": {"tk_formtoken"}}, as("ann"))
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save ntfy: %d", res.StatusCode)
	}
	_, body = e.do("GET", "/settings/notifications?saved=ntfy", nil, as("ann"))
	if strings.Contains(body, "tk_formtoken") || !strings.Contains(body, "access token · set") || !strings.Contains(body, "ntfy settings saved.") ||
		!strings.Contains(body, "https://ntfy.sh/ann-alerts") {
		t.Fatal("page after saving ntfy")
	}

	// The events grid: the columns posted are saved, the others keep theirs.
	res, _ = post("/settings/notifications/events", url.Values{"channel": {"inbox", "email"}, "on": {"item.open:email", "item.first_open:inbox"}}, as("ann"))
	if p, _ := e.s.App.Notify.Prefs(e.ctx, 1); res.StatusCode != http.StatusSeeOther || !p.Events.On(notify.EventItemOpen, "email") ||
		p.Events.On(notify.EventSecretBurned, "email") || p.Events.On(notify.EventSecretRevealed, "inbox") || !p.Events.On(notify.EventSecretBurned, "ntfy") {
		t.Fatalf("events: %d %+v", res.StatusCode, p.Events)
	}
	for _, bad := range []url.Values{{"channel": {"inbox"}, "on": {"item.sometimes:inbox"}}, {"channel": {"sms"}}, {"on": {"item.open:sms"}}} {
		if res, _ := post("/settings/notifications/events", bad, as("ann")); res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("bad events %v: %d", bad, res.StatusCode)
		}
	}
	// fetch gets JSON.
	res, body = post("/settings/notifications/events", url.Values{"channel": {"inbox"}}, as("ann"), func(r *http.Request) { r.Header.Set("Accept", "application/json") })
	if res.StatusCode != 200 || !strings.Contains(body, `"saved":true`) {
		t.Fatalf("events json: %d %s", res.StatusCode, body)
	}

	// Send test email: delivered now, the result on the page.
	res, body = post("/settings/notifications/test", url.Values{"channel": {"email"}}, as("ann"))
	if res.StatusCode != 200 || !strings.Contains(body, "Test sent to your email address.") || len(e.s.Mail.Sent()) != 1 {
		t.Fatalf("test email: %d", res.StatusCode)
	}
	// A failing test shows why, without details (127.0.0.1 is not allowlisted).
	if res, _ := post("/settings/notifications/ntfy", url.Values{"server": {"https://127.0.0.1:1"}, "topic": {"ann-alerts"}}, as("ann")); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save local ntfy: %d", res.StatusCode)
	}
	res, body = post("/settings/notifications/test", url.Values{"channel": {"ntfy"}}, as("ann"))
	if res.StatusCode != 200 || !strings.Contains(body, "Test not delivered: ntfy: address not allowed.") || strings.Contains(body, "tk_formtoken") {
		t.Fatalf("test ntfy: %d", res.StatusCode)
	}
	// The grid's Test buttons get JSON: a message, or an error.
	res, body = post("/settings/notifications/test", url.Values{"channel": {"email"}}, as("ann"), wantJSON())
	if res.StatusCode != 200 || !strings.Contains(body, `"message":"Test sent to your email address."`) {
		t.Fatalf("test email json: %d %s", res.StatusCode, body)
	}
	res, body = post("/settings/notifications/test", url.Values{"channel": {"ntfy"}}, as("ann"), wantJSON())
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(body, `"error":"Test not delivered: ntfy: address not allowed."`) {
		t.Fatalf("test ntfy json: %d %s", res.StatusCode, body)
	}
	// Tests are rate limited.
	var last int
	for range 6 {
		res, _ = post("/settings/notifications/test", url.Values{"channel": {"email"}}, as("ann"))
		last = res.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("test limit: %d", last)
	}
	if res, body = post("/settings/notifications/test", url.Values{"channel": {"email"}}, as("ann"), wantJSON()); res.StatusCode != http.StatusTooManyRequests ||
		!strings.Contains(body, `"error":"Too many tests.`) {
		t.Fatalf("test limit json: %d %s", res.StatusCode, body)
	}
	// Signed out: sign in first.
	if res, _ := e.do("GET", "/settings/notifications", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous: %d", res.StatusCode)
	}
}
