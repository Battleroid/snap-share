package notify_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/testutil"
)

type inboxRow struct {
	ID                                                    int64
	Kind, Status, Title, Body, Link, SubjectType, Subject string
	At                                                    int64
	Read                                                  bool
}

// inbox returns a user's rows, newest first.
func (e *env) inbox(user int64) []inboxRow {
	e.t.Helper()
	rows, err := e.s.App.DB.R.Query(`SELECT id, kind, status, title, body, link, subject_type, subject_id, created_at, read_at IS NOT NULL
FROM inbox WHERE user_id = ? ORDER BY created_at DESC, id DESC`, user)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []inboxRow
	for rows.Next() {
		var r inboxRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.Status, &r.Title, &r.Body, &r.Link, &r.SubjectType, &r.Subject, &r.At, &r.Read); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) visit(path, ua string) {
	e.t.Helper()
	req := e.s.Request(e.t, "GET", path, nil)
	req.Header.Del("Sec-Fetch-Site")
	req.Header.Del("Origin")
	req.Header.Set("User-Agent", ua)
	e.send(req)
}

func (e *env) flush() {
	e.t.Helper()
	if err := e.s.App.Items.FlushCounters(e.ctx); err != nil {
		e.t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A first open is in the owner's inbox with everything off; later opens,
// the owner's own visits and preview bots are not.
func TestInboxFirstOpen(t *testing.T) {
	e := newEnv(t)
	req := e.s.Request(t, "GET", "/abc", nil)
	req.Header.Set("User-Agent", firefox)
	authtest.Session(req, "ann")
	e.send(req)
	e.visit("/abc", "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)")
	e.flush()
	if rows := e.inbox(1); len(rows) != 0 {
		t.Fatalf("rows before anyone opened it: %+v", rows)
	}

	e.visit("/abc", firefox)
	e.flush()
	rows := e.inbox(1)
	want := inboxRow{Kind: notify.InboxItemOpened, Status: "info", Title: "https://example.org/ opened",
		Body: "Firefox · macOS · 127.0.0.1", Link: "/dashboard/items/10", SubjectType: "item", Subject: "10"}
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	got := rows[0]
	got.ID, got.At = 0, 0
	if got != want {
		t.Fatalf("row %+v, want %+v", got, want)
	}
	e.visit("/abc", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/159.0.0.0 Safari/537.36")
	e.flush()
	if rows := e.inbox(1); len(rows) != 1 {
		t.Fatalf("a later open added a row: %+v", rows)
	}
	if n := len(e.jobs(notify.JobOpen)); n != 0 {
		t.Fatalf("%d open jobs with every channel off", n)
	}

	// Without a job queue the hook still records first opens.
	nojobs, err := notify.New(notify.Deps{Cfg: e.s.App.Cfg, DB: e.s.App.DB, Settings: e.s.App.Settings, Mail: e.s.Mail, Items: e.s.App.Items, Now: e.clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	now := e.clk.Now()
	nojobs.ItemsOpened(e.ctx, []items.Opened{
		{ItemID: e.photo, Count: 1, From: now, To: now, IP: "203.0.113.9", Client: "Chrome · Linux"},
		{ItemID: e.photo, First: true, Count: 2, From: now, To: now, IP: "203.0.113.4", Client: "Safari · iOS"},
		{ItemID: 999, First: true, Count: 1, From: now, To: now}, // deleted since
	})
	rows = e.inbox(1)
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	for _, r := range rows {
		if r.Subject == "11" && (r.Title != "photo.jpg opened" || r.Body != "Safari · iOS · 203.0.113.4" || r.At != now.UnixMilli()) {
			t.Fatalf("row %+v", r)
		}
	}
}

// Every reveal and burn of an owned secret is in the inbox; email and ntfy
// keep their rules: reveals only with notify on, burns always.
func TestInboxSecretsAndPushChannels(t *testing.T) {
	e := newEnv(t)
	if err := e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true); err != nil {
		t.Fatal(err)
	}
	const id = "QUJDREVGR0hJSktMTU5PUA"
	base := secrets.Notification{OwnerID: 1, SecretID: id, SecretKind: "text", Note: "prod db", ViewsLeft: 1, MaxViews: 2,
		At: e.clk.Now(), IP: "203.0.113.4", UASummary: "Firefox · macOS"}
	notifyOne := func(n secrets.Notification) {
		t.Helper()
		e.own.Notify(e.ctx, n)
		e.own.Close()
	}
	sends := func() []string { return e.jobs(notify.JobSend) }

	reveal := base
	reveal.Kind = stats.KindRevealed
	notifyOne(reveal)
	rows := e.inbox(1)
	want := inboxRow{Kind: notify.InboxSecretRevealed, Status: "info", Title: `Secret "prod db" revealed`,
		Body: "Firefox · macOS · 203.0.113.4 · 1 of 2 views left", Link: "/dashboard/secrets/" + id + "/timeline", SubjectType: "secret", Subject: id}
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	if got := rows[0]; got.Kind != want.Kind || got.Status != want.Status || got.Title != want.Title || got.Body != want.Body ||
		got.Link != want.Link || got.SubjectType != want.SubjectType || got.Subject != want.Subject || got.Read {
		t.Fatalf("row %+v, want %+v", got, want)
	}
	if s := sends(); len(s) != 0 {
		t.Fatalf("a reveal without notify was pushed: %v", s)
	}

	reveal.Requested = true
	notifyOne(reveal)
	if s := sends(); len(s) != 1 || !strings.Contains(s[0], "secret_revealed") {
		t.Fatalf("a reveal with notify: %v", s)
	}
	if n := len(e.inbox(1)); n != 2 {
		t.Fatalf("%d rows", n)
	}

	burn := base
	burn.Kind, burn.Reason = stats.KindBurned, "failed attempts"
	notifyOne(burn)
	rows = e.inbox(1)
	if r := rows[0]; r.Kind != notify.InboxSecretBurned || r.Status != "down" || r.Title != `Secret "prod db" burned after wrong passphrases` ||
		r.Body != "last attempt Firefox · macOS · 203.0.113.4" || r.Link != want.Link {
		t.Fatalf("burn row %+v", r)
	}
	if s := sends(); len(s) != 2 || !strings.Contains(s[1], "secret_burned") {
		t.Fatalf("a burn without notify must still be pushed: %v", s)
	}

	// Without a note the kind names it; without an owner nothing happens.
	plain := base
	plain.Kind, plain.Note, plain.SecretKind = stats.KindRevealed, "", "file"
	notifyOne(plain)
	if r := e.inbox(1)[0]; r.Title != "File secret revealed" {
		t.Fatalf("title %q", r.Title)
	}
	anon := reveal
	anon.OwnerID = 0
	notifyOne(anon)
	if n := e.count(`SELECT count(*) FROM inbox`); n != 4 {
		t.Fatalf("%d rows after an anonymous reveal", n)
	}
	if s := sends(); len(s) != 2 {
		t.Fatalf("sends %v", s)
	}
}

func entry(user int64, title, subject string, at time.Time) notify.Entry {
	return notify.Entry{UserID: user, Kind: notify.InboxItemOpened, Status: "info", Title: title, Link: "/dashboard/items/" + subject + "",
		SubjectType: "item", SubjectID: subject, At: at}
}

func TestInboxCapAndRetention(t *testing.T) {
	e := newEnv(t)
	svc := e.s.App.Notify
	now := time.Now()
	var es []notify.Entry
	for i := range notify.InboxPerUser + 5 {
		es = append(es, entry(1, fmt.Sprintf("n%03d", i), "10", now.Add(time.Duration(i-300)*time.Minute)))
	}
	if err := svc.AddInbox(e.ctx, es[:150]...); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddInbox(e.ctx, es[150:]...); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddInbox(e.ctx, entry(2, "bob's", "10", now)); err != nil {
		t.Fatal(err)
	}
	rows := e.inbox(1)
	if len(rows) != notify.InboxPerUser || rows[0].Title != "n204" || rows[len(rows)-1].Title != "n005" {
		t.Fatalf("%d rows, newest %q, oldest %q", len(rows), rows[0].Title, rows[len(rows)-1].Title)
	}
	if n := len(e.inbox(2)); n != 1 {
		t.Fatalf("bob has %d rows", n)
	}

	// Rows are kept as long as raw stats events: the setting decides.
	e.exec(`DELETE FROM inbox`)
	if err := svc.AddInbox(e.ctx, entry(1, "old", "10", now.AddDate(0, 0, -11)), entry(1, "recent", "10", now.AddDate(0, 0, -9))); err != nil {
		t.Fatal(err)
	}
	if err := app.StatsRetentionDays.Set(e.ctx, e.s.App.Settings, 10); err != nil {
		t.Fatal(err)
	}
	if err := svc.SweepInbox(e.ctx); err != nil {
		t.Fatal(err)
	}
	if rows := e.inbox(1); len(rows) != 1 || rows[0].Title != "recent" {
		t.Fatalf("after a 10-day sweep: %+v", rows)
	}
	if err := app.StatsRetentionDays.Set(e.ctx, e.s.App.Settings, 5); err != nil {
		t.Fatal(err)
	}
	if err := svc.SweepInbox(e.ctx); err != nil {
		t.Fatal(err)
	}
	if rows := e.inbox(1); len(rows) != 0 {
		t.Fatalf("after a 5-day sweep: %+v", rows)
	}
}

// Rows whose item or secret is gone, or has another owner, are swept.
func TestInboxOrphanSweep(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	const kept, gone = "QUJDREVGR0hJSktMTU5PUA", "WllYV1ZVVFNSUVBPTk1MSw"
	e.exec(`INSERT INTO secrets (id, owner_id, kind, mode, state, ct_size, chunk_count, views_left, max_views, max_failures, expires_at, created_at, updated_at)
VALUES (?, 1, 'text', 'e2e', 'burned', 64, 1, 0, 1, 10, ?, ?, ?)`, kept, now.Add(time.Hour).UnixMilli(), now.UnixMilli(), now.UnixMilli())
	secret := func(user int64, title, id string) notify.Entry {
		return notify.Entry{UserID: user, Kind: notify.InboxSecretRevealed, Status: "info", Title: title, SubjectType: "secret", SubjectID: id, At: now}
	}
	if err := e.s.App.Notify.AddInbox(e.ctx,
		entry(1, "item", "10", now), entry(1, "gone item", "999", now), entry(2, "not bob's item", "11", now),
		secret(1, "secret", kept), secret(1, "gone secret", gone), secret(2, "not bob's secret", kept),
	); err != nil {
		t.Fatal(err)
	}
	if err := e.s.App.Notify.SweepInbox(e.ctx); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, r := range append(e.inbox(1), e.inbox(2)...) {
		titles = append(titles, r.Title)
	}
	if strings.Join(titles, ",") != "secret,item" {
		t.Fatalf("after the sweep: %v", titles)
	}
	e.exec(`DELETE FROM items WHERE id = 10`)
	if err := e.s.App.Notify.SweepInbox(e.ctx); err != nil {
		t.Fatal(err)
	}
	if rows := e.inbox(1); len(rows) != 1 || rows[0].Title != "secret" {
		t.Fatalf("after deleting the item: %+v", rows)
	}
}

func TestInboxPagesAndIDOR(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	if err := e.s.App.Notify.AddInbox(e.ctx, entry(1, "photo.jpg opened", "11", now.Add(-2*time.Hour)),
		notify.Entry{UserID: 1, Kind: notify.InboxSecretBurned, Status: "down", Title: `Secret "prod db" burned after wrong passphrases`,
			Body: "last attempt Firefox · macOS · 203.0.113.4", Link: "/dashboard/secrets/QUJDREVGR0hJSktMTU5PUA/timeline",
			SubjectType: "secret", SubjectID: "QUJDREVGR0hJSktMTU5PUA", At: now.Add(-3 * time.Minute)},
		entry(2, "bob's link opened", "10", now)); err != nil {
		t.Fatal(err)
	}
	rows := e.inbox(1)
	burned, photo := rows[0], rows[1]

	// The panel and the page list the user's own rows, newest first.
	res, body := e.do("GET", "/notifications/panel", nil, as("ann"))
	if res.StatusCode != 200 || strings.Contains(body, "<html") || strings.Contains(body, "bob's") ||
		strings.Index(body, "burned after wrong passphrases") > strings.Index(body, "photo.jpg opened") {
		t.Fatalf("panel %d: %s", res.StatusCode, body)
	}
	for _, s := range []string{
		`data-id="` + strconv.FormatInt(burned.ID, 10) + `" data-status="down" data-href="/dashboard/secrets/QUJDREVGR0hJSktMTU5PUA/timeline" data-unread`,
		`pb-icon--status-down`, `>3m</time>`, `>2h</time>`, `pb-icon--eye`, `<span class="pb-notif__body">last attempt Firefox · macOS · 203.0.113.4</span>`,
	} {
		if !strings.Contains(body, s) {
			t.Errorf("panel lacks %s", s)
		}
	}
	res, body = e.do("GET", "/notifications", nil, as("ann"))
	if res.StatusCode != 200 || !strings.Contains(body, "Mark all read") || !strings.Contains(body, "photo.jpg opened") || strings.Contains(body, "bob's") {
		t.Fatalf("page %d", res.StatusCode)
	}
	// The bell on every page: the count, and the script that keeps it live.
	if !strings.Contains(body, `aria-label="Notifications, 2 unread" data-unread="2"`) || !strings.Contains(body, `class="pb-bell__count">2</span>`) ||
		!strings.Contains(body, "/static/js/inbox.") {
		t.Fatal("page lacks the bell with its count")
	}
	if _, b := e.do("GET", "/login", nil); strings.Contains(b, "pb-bell") || strings.Contains(b, "inbox.") {
		t.Fatal("signed-out page has a bell")
	}

	// Bob can't read Ann's rows; an unknown or malformed id is the same 404.
	for _, id := range []string{strconv.FormatInt(photo.ID, 10), "999999", "abc", "-1"} {
		if res, _ := e.do("POST", "/notifications/"+id+"/read", nil, as("bob"), wantJSON()); res.StatusCode != http.StatusNotFound {
			t.Fatalf("bob reads %s: %d", id, res.StatusCode)
		}
	}
	if rows := e.inbox(1); rows[1].Read {
		t.Fatal("bob's request marked ann's row read")
	}
	res, body = e.do("POST", "/notifications/"+strconv.FormatInt(photo.ID, 10)+"/read", nil, as("ann"), wantJSON())
	if res.StatusCode != 200 || strings.TrimSpace(body) != `{"unread":1}` {
		t.Fatalf("read: %d %s", res.StatusCode, body)
	}
	if _, body = e.do("POST", "/notifications/"+strconv.FormatInt(photo.ID, 10)+"/read", nil, as("ann"), wantJSON()); strings.TrimSpace(body) != `{"unread":1}` {
		t.Fatalf("read twice: %s", body)
	}
	// Read all is per user; a form post goes back to the list.
	res, _ = e.do("POST", "/notifications/read-all", nil, as("bob"), form())
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/notifications" {
		t.Fatalf("bob read-all: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if rows := e.inbox(1); rows[0].Read || !e.inbox(2)[0].Read {
		t.Fatal("read-all crossed users")
	}
	if _, body = e.do("POST", "/notifications/read-all", nil, as("ann"), wantJSON()); strings.TrimSpace(body) != `{"unread":0}` {
		t.Fatalf("read-all: %s", body)
	}
	if n := e.count(`SELECT count(*) FROM inbox WHERE read_at IS NULL`); n != 0 {
		t.Fatalf("%d unread", n)
	}

	// Browser sessions only, and same-origin POSTs only.
	for _, rt := range [][2]string{{"GET", "/notifications"}, {"GET", "/notifications/panel"}, {"GET", "/notifications/stream"},
		{"POST", "/notifications/read-all"}, {"POST", "/notifications/" + strconv.FormatInt(photo.ID, 10) + "/read"}} {
		req := e.s.Request(t, rt[0], rt[1], nil)
		authtest.Bearer(req, "ann-key")
		if res, _ := e.send(req); res.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with an API key: %d", rt[0], rt[1], res.StatusCode)
		}
	}
	req := e.s.Request(t, "POST", "/notifications/read-all", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.example")
	authtest.Session(req, "ann")
	if res, _ := e.send(req); res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site read-all: %d", res.StatusCode)
	}
}

func TestInboxPagination(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	var es []notify.Entry
	for i := range 120 {
		es = append(es, entry(1, fmt.Sprintf("row %03d", i), "10", now.Add(time.Duration(i-200)*time.Minute)))
	}
	if err := e.s.App.Notify.AddInbox(e.ctx, es...); err != nil {
		t.Fatal(err)
	}
	for page, want := range map[string][2]any{"": {50, "row 119"}, "3": {20, "row 019"}, "99": {20, "row 019"}, "x": {50, "row 119"}} {
		_, body := e.do("GET", "/notifications?page="+page, nil, as("ann"))
		first := body[strings.Index(body, `class="pb-notif__title">`)+len(`class="pb-notif__title">`):]
		if n := strings.Count(body, `<li class="pb-notif app-notif"`); n != want[0] || !strings.HasPrefix(first, want[1].(string)) {
			t.Errorf("page %q: %d rows, first %.10q", page, n, first)
		}
	}
	if _, body := e.do("GET", "/notifications?page=2", nil, as("ann")); !strings.Contains(body, "2 / 3") {
		t.Error("no pager")
	}
	if _, body := e.do("GET", "/notifications/panel", nil, as("ann")); strings.Count(body, `<li class="pb-notif app-notif"`) != 20 {
		t.Error("panel is not the latest 20")
	}
}

// ---- the stream ----

// sse is an open GET /notifications/stream, read frame by frame.
type sse struct {
	t      *testing.T
	res    *http.Response
	frames chan []string
	done   chan struct{}
	cancel context.CancelFunc
}

func (e *env) openStream(opts ...func(*http.Request)) (*sse, int) {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := e.s.Request(e.t, "GET", "/notifications/stream", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	for _, o := range opts {
		o(req)
	}
	res, err := e.s.Client().Do(req) //nolint:bodyclose // closed by sse.close
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		cancel()
		return nil, res.StatusCode
	}
	s := &sse{t: e.t, res: res, frames: make(chan []string, 64), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(s.frames)
		br := bufio.NewReader(res.Body)
		var cur []string
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if line = strings.TrimSuffix(line, "\n"); line != "" {
				cur = append(cur, line)
				continue
			}
			select {
			case s.frames <- cur:
			case <-s.done:
				return
			}
			cur = nil
		}
	}()
	e.t.Cleanup(s.close)
	return s, res.StatusCode
}

func (s *sse) close() {
	select {
	case <-s.done:
		return
	default:
	}
	close(s.done)
	s.cancel()
	_ = s.res.Body.Close()
}

// next returns the next frame, or nil once the server has ended the stream.
func (s *sse) next() []string {
	s.t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			return nil
		}
		return f
	case <-time.After(5 * time.Second):
		s.t.Fatal("no frame within 5 s")
	}
	return nil
}

// drain reads to the end of the stream and returns the last frame.
func (s *sse) drain() (last []string) {
	s.t.Helper()
	for f := s.next(); f != nil; f = s.next() {
		last = f
	}
	return last
}

func frame(lines []string) (name, data string) {
	for _, l := range lines {
		if v, ok := strings.CutPrefix(l, "event: "); ok {
			name = v
		}
		if v, ok := strings.CutPrefix(l, "data: "); ok {
			data = v
		}
	}
	return name, data
}

// event skips comments and returns the data of the next event, which must
// be called name.
func (s *sse) event(name string) string {
	s.t.Helper()
	for {
		f := s.next()
		if f == nil {
			s.t.Fatalf("stream ended before %q", name)
		}
		got, data := frame(f)
		if got == "" {
			continue
		}
		if got != name {
			s.t.Fatalf("event %q (%s), want %q", got, data, name)
		}
		return data
	}
}

func TestInboxStream(t *testing.T) {
	e := newEnv(t)
	svc := e.s.App.Notify
	svc.SetStreamLimits(notify.StreamLimits{Heartbeat: 100 * time.Millisecond})
	if err := svc.AddInbox(e.ctx, entry(1, "earlier", "10", time.Now().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	s, code := e.openStream(as("ann"))
	if code != 200 {
		t.Fatalf("stream: %d", code)
	}
	if h := s.res.Header; h.Get("Cache-Control") != "no-store" || h.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers %v", h)
	}
	if f := s.next(); len(f) != 1 || f[0] != "retry: 5000" {
		t.Fatalf("first frame %q", f)
	}
	if d := s.event("count"); d != `{"unread":1}` {
		t.Fatalf("count %s", d)
	}

	// A first open reaches the open stream; bob's rows don't.
	if err := svc.AddInbox(e.ctx, entry(2, "bob's", "10", time.Now())); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	svc.ItemsOpened(e.ctx, []items.Opened{{ItemID: e.photo, First: true, Count: 1, From: now, To: now, IP: "203.0.113.4", Client: "Firefox · macOS"}})
	var ev struct {
		Unread int `json:"unread"`
		Entry  struct {
			ID                                          int64
			Kind, Status, Icon, Title, Body, Link, Time string
			Unread                                      bool
		} `json:"entry"`
	}
	if err := json.Unmarshal([]byte(s.event("notification")), &ev); err != nil {
		t.Fatal(err)
	}
	if en := ev.Entry; ev.Unread != 2 || en.Title != "photo.jpg opened" || en.Body != "Firefox · macOS · 203.0.113.4" || en.Kind != notify.InboxItemOpened ||
		en.Status != "info" || en.Icon != "eye" || en.Link != "/dashboard/items/11" || en.Time != "now" || !en.Unread || en.ID == 0 {
		t.Fatalf("event %+v", ev)
	}

	// Heartbeats keep proxies from closing it.
	if f := s.next(); len(f) != 1 || f[0] != ": ping" {
		t.Fatalf("heartbeat %q", f)
	}
	// Reads reach the stream too (other tabs follow).
	if res, _ := e.do("POST", "/notifications/"+strconv.FormatInt(ev.Entry.ID, 10)+"/read", nil, as("ann"), wantJSON()); res.StatusCode != 200 {
		t.Fatalf("read: %d", res.StatusCode)
	}
	if d := s.event("read"); d != fmt.Sprintf(`{"unread":1,"id":%d}`, ev.Entry.ID) {
		t.Fatalf("read event %s", d)
	}
	if res, _ := e.do("POST", "/notifications/read-all", nil, as("ann"), wantJSON()); res.StatusCode != 200 {
		t.Fatalf("read-all: %d", res.StatusCode)
	}
	if d := s.event("read"); d != `{"unread":0,"all":true}` {
		t.Fatalf("read-all event %s", d)
	}

	// The client goes away: its handler returns and unsubscribes.
	if n := svc.Streams(); n != 1 {
		t.Fatalf("%d streams", n)
	}
	s.close()
	eventually(t, "the stream to be unregistered", func() bool { return svc.Streams() == 0 })
}

// A stream ends after its lifetime with a retry hint, so the browser
// reconnects and the session is checked again.
func TestInboxStreamLifetime(t *testing.T) {
	e := newEnv(t)
	svc := e.s.App.Notify
	svc.SetStreamLimits(notify.StreamLimits{MaxAge: 300 * time.Millisecond, Heartbeat: time.Hour})
	start := time.Now()
	s, code := e.openStream(as("ann"))
	if code != 200 {
		t.Fatalf("stream: %d", code)
	}
	last := s.drain()
	if took := time.Since(start); took < 300*time.Millisecond || took > 4*time.Second {
		t.Fatalf("stream lasted %v", took)
	}
	if len(last) != 1 || last[0] != "retry: 5000" {
		t.Fatalf("last frame %q", last)
	}
	eventually(t, "the stream to be unregistered", func() bool { return svc.Streams() == 0 })

	// CloseStreams (server shutdown) ends open streams at once.
	svc.SetStreamLimits(notify.StreamLimits{})
	s, _ = e.openStream(as("ann"))
	s.event("count")
	svc.CloseStreams()
	if last := s.drain(); len(last) != 1 || last[0] != "retry: 5000" {
		t.Fatalf("last frame at shutdown %q", last)
	}
	eventually(t, "the stream to be unregistered", func() bool { return svc.Streams() == 0 })
	if _, code := e.openStream(as("ann")); code != http.StatusServiceUnavailable {
		t.Fatalf("stream after shutdown: %d", code)
	}
}

func TestInboxStreamLimits(t *testing.T) {
	e := newEnv(t)
	svc := e.s.App.Notify
	svc.SetStreamLimits(notify.StreamLimits{PerUser: 2, Total: 3})
	a1, _ := e.openStream(as("ann"))
	a2, _ := e.openStream(as("ann"))
	if a1 == nil || a2 == nil {
		t.Fatal("ann's first two streams refused")
	}
	if _, code := e.openStream(as("ann")); code != http.StatusTooManyRequests {
		t.Fatalf("ann's third stream: %d", code)
	}
	b1, _ := e.openStream(as("bob"))
	if b1 == nil {
		t.Fatal("bob's stream refused")
	}
	if _, code := e.openStream(as("bob")); code != http.StatusTooManyRequests {
		t.Fatalf("a fourth stream in all: %d", code)
	}
	a1.close()
	eventually(t, "a slot", func() bool { return svc.Streams() == 2 })
	if b2, code := e.openStream(as("bob")); b2 == nil {
		t.Fatalf("bob's stream after a slot freed: %d", code)
	}

	// Browser sessions only.
	if _, code := e.openStream(func(r *http.Request) { authtest.Bearer(r, "ann-key") }); code != http.StatusForbidden {
		t.Fatalf("API key: %d", code)
	}
	if _, code := e.openStream(); code != http.StatusSeeOther {
		t.Fatalf("signed out: %d", code)
	}
	a2.close()
	b1.close()
	eventually(t, "every stream to be unregistered", func() bool { return svc.Streams() == 1 })
}

// Addresses in SNAP_RATELIMIT_EXEMPT (a LAN behind one router, the e2e
// network) are not held to the per-user cap; the global cap still applies.
func TestInboxStreamCapExempt(t *testing.T) {
	s := testutil.NewServer(t, testutil.Env("RATELIMIT_EXEMPT", "127.0.0.1/32"))
	s.Auth.Add("ann", auth.Principal{UserID: 1})
	s.App.Notify.SetStreamLimits(notify.StreamLimits{PerUser: 1, Total: 3})
	e := &env{t: t, s: s, ctx: context.Background()}
	for i := range 3 {
		if st, code := e.openStream(as("ann")); st == nil {
			t.Fatalf("stream %d: %d", i, code)
		}
	}
	if _, code := e.openStream(as("ann")); code != http.StatusTooManyRequests {
		t.Fatalf("beyond the global cap: %d", code)
	}
}

// The raw origin never serves the inbox: GETs go to the app origin, POSTs
// are refused, and no stream starts there.
func TestInboxNotOnRawOrigin(t *testing.T) {
	s := testutil.NewServer(t, testutil.Env("RAW_URL", "https://raw.test"))
	s.Auth.Add("ann", auth.Principal{UserID: 1})
	for _, p := range []string{"/notifications", "/notifications/panel", "/notifications/stream"} {
		req := s.Request(t, "GET", p, nil)
		req.Host = "raw.test"
		authtest.Session(req, "ann")
		res, _ := s.Do(t, req) //nolint:bodyclose // Do closes it
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != s.URL+p || res.Header.Get("Content-Type") == "text/event-stream" {
			t.Errorf("GET %s on raw: %d %q", p, res.StatusCode, res.Header.Get("Location"))
		}
	}
	for _, p := range []string{"/notifications/read-all", "/notifications/1/read"} {
		req := s.Request(t, "POST", p, nil)
		req.Host = "raw.test"
		authtest.Session(req, "ann")
		if res, _ := s.Do(t, req); res.StatusCode != http.StatusNotFound { //nolint:bodyclose // Do closes it
			t.Errorf("POST %s on raw: %d", p, res.StatusCode)
		}
	}
}

// Read rows go after notify.read_retention_hours; unread rows and recent
// reads stay, and 0 keeps read rows for the whole retention period.
func TestInboxReadRetention(t *testing.T) {
	e := newEnv(t)
	svc := e.s.App.Notify
	now := time.Now()
	if err := svc.AddInbox(e.ctx, entry(1, "old read", "10", now.Add(-48*time.Hour)), entry(1, "recent read", "10", now.Add(-48*time.Hour)),
		entry(1, "old unread", "10", now.Add(-72*time.Hour)), entry(2, "bob's old read", "10", now.Add(-48*time.Hour))); err != nil {
		t.Fatal(err)
	}
	readAt := func(title string, ago time.Duration) {
		e.exec(`UPDATE inbox SET read_at = ? WHERE title = ?`, now.Add(-ago).UnixMilli(), title)
	}
	readAt("old read", 25*time.Hour)
	readAt("bob's old read", 30*time.Hour)
	readAt("recent read", 23*time.Hour)
	titles := func() string {
		var out []string
		for _, r := range append(e.inbox(1), e.inbox(2)...) {
			out = append(out, r.Title)
		}
		return strings.Join(out, ",")
	}

	// The default: a day.
	if err := svc.SweepInbox(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got != "recent read,old unread" {
		t.Fatalf("after the default sweep: %s", got)
	}
	// 0 keeps read rows as long as the rest.
	if err := app.NotifyReadRetentionHours.Set(e.ctx, e.s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	readAt("recent read", 1000*time.Hour)
	if err := svc.SweepInbox(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got != "recent read,old unread" {
		t.Fatalf("with 0 hours: %s", got)
	}
	// The setting decides.
	if err := app.NotifyReadRetentionHours.Set(e.ctx, e.s.App.Settings, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.SweepInbox(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := titles(); got != "old unread" {
		t.Fatalf("with 1 hour: %s", got)
	}
	if err := app.NotifyReadRetentionHours.Set(e.ctx, e.s.App.Settings, -1); err == nil {
		t.Fatal("a negative retention was accepted")
	}

	// The page says how long read rows stay, only when they go sooner.
	if _, body := e.do("GET", "/notifications", nil, as("ann")); !strings.Contains(body, "Read notifications are removed after 1 hour.") {
		t.Fatal("page lacks the read retention note")
	}
	if err := app.NotifyReadRetentionHours.Set(e.ctx, e.s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	if _, body := e.do("GET", "/notifications", nil, as("ann")); strings.Contains(body, "Read notifications are removed") {
		t.Fatal("note shown with 0 hours")
	}
}

// Delete and Clear read touch only the caller's rows, answer like the read
// routes and reach the caller's other tabs.
func TestInboxDeleteAndClearRead(t *testing.T) {
	e := newEnv(t)
	svc := e.s.App.Notify
	now := time.Now()
	if err := svc.AddInbox(e.ctx, entry(1, "a", "10", now.Add(-3*time.Minute)), entry(1, "b", "10", now.Add(-2*time.Minute)),
		entry(1, "c", "10", now.Add(-time.Minute)), entry(2, "bob's", "10", now)); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, r := range append(e.inbox(1), e.inbox(2)...) {
		ids[r.Title] = r.ID
	}
	path := func(title string) string { return "/notifications/" + strconv.FormatInt(ids[title], 10) + "/delete" }
	e.exec(`UPDATE inbox SET read_at = ? WHERE title IN ('b', 'bob''s')`, now.UnixMilli())

	// The rows carry a named delete button, a plain form.
	_, body := e.do("GET", "/notifications", nil, as("ann"))
	for _, s := range []string{`<form class="app-notif__x" method="post" action="` + path("a") + `">`, `aria-label="Delete notification: a"`,
		`action="/notifications/clear-read"`, `data-clearread`} {
		if !strings.Contains(body, s) {
			t.Errorf("page lacks %s", s)
		}
	}
	if _, body := e.do("GET", "/notifications/panel", nil, as("ann")); !strings.Contains(body, `action="`+path("c")+`"`) {
		t.Error("panel rows lack the delete button")
	}

	s, code := e.openStream(as("ann"))
	if code != 200 {
		t.Fatalf("stream: %d", code)
	}
	defer s.close()
	if d := s.event("count"); d != `{"unread":2}` {
		t.Fatalf("count %s", d)
	}

	// Bob can't delete Ann's rows; an unknown or malformed id is the same 404.
	for _, p := range []string{path("a"), "/notifications/999999/delete", "/notifications/abc/delete", "/notifications/-1/delete"} {
		if res, _ := e.do("POST", p, nil, as("bob"), wantJSON()); res.StatusCode != http.StatusNotFound {
			t.Fatalf("bob deletes %s: %d", p, res.StatusCode)
		}
	}
	if res, _ := e.do("POST", "/notifications/clear-read", nil, as("bob"), wantJSON()); res.StatusCode != 200 {
		t.Fatalf("bob clears: %d", res.StatusCode)
	}
	if n := len(e.inbox(1)); n != 3 {
		t.Fatalf("bob's requests left ann %d rows", n)
	}
	if n := len(e.inbox(2)); n != 0 {
		t.Fatalf("bob's clear left him %d rows", n)
	}

	// Ann deletes an unread row: the count drops, here and in her stream.
	res, body := e.do("POST", path("a"), nil, as("ann"), wantJSON())
	if res.StatusCode != 200 || strings.TrimSpace(body) != `{"unread":1}` {
		t.Fatalf("delete: %d %s", res.StatusCode, body)
	}
	if d := s.event("remove"); d != fmt.Sprintf(`{"unread":1,"id":%d}`, ids["a"]) {
		t.Fatalf("remove event %s", d)
	}
	if res, _ := e.do("POST", path("a"), nil, as("ann"), wantJSON()); res.StatusCode != http.StatusNotFound {
		t.Fatalf("delete twice: %d", res.StatusCode)
	}
	// Clear read leaves the unread row; a form post goes back to the list.
	res, _ = e.do("POST", "/notifications/clear-read", nil, as("ann"), form())
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/notifications" {
		t.Fatalf("clear read: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if d := s.event("remove"); d != `{"unread":1,"read":true}` {
		t.Fatalf("clear event %s", d)
	}
	if rows := e.inbox(1); len(rows) != 1 || rows[0].Title != "c" {
		t.Fatalf("after clear read: %+v", rows)
	}
	res, _ = e.do("POST", path("c"), nil, as("ann"), form())
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/notifications" || len(e.inbox(1)) != 0 {
		t.Fatalf("form delete: %d %q", res.StatusCode, res.Header.Get("Location"))
	}

	// Browser sessions only, and same-origin POSTs only.
	if err := svc.AddInbox(e.ctx, entry(1, "d", "10", now)); err != nil {
		t.Fatal(err)
	}
	d := "/notifications/" + strconv.FormatInt(e.inbox(1)[0].ID, 10) + "/delete"
	for _, p := range []string{d, "/notifications/clear-read"} {
		req := e.s.Request(t, "POST", p, nil)
		authtest.Bearer(req, "ann-key")
		if res, _ := e.send(req); res.StatusCode != http.StatusForbidden {
			t.Errorf("%s with an API key: %d", p, res.StatusCode)
		}
		req = e.s.Request(t, "POST", p, nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "https://evil.example")
		authtest.Session(req, "ann")
		if res, _ := e.send(req); res.StatusCode != http.StatusForbidden {
			t.Errorf("cross-site %s: %d", p, res.StatusCode)
		}
	}
	if len(e.inbox(1)) != 1 {
		t.Fatal("a refused request deleted the row")
	}
}

// An admin lapse is reported once per SSO sign-in: the same one again or
// an older one (another session) is not news; a newer one is.
func TestInboxAdminLapsedOncePerSignIn(t *testing.T) {
	e := newEnv(t)
	svc := e.s.App.Notify
	at := time.Now().Add(-25 * time.Hour).Truncate(time.Millisecond)
	svc.AdminLapsed(e.ctx, 1, at)
	svc.AdminLapsed(e.ctx, 1, at)
	svc.AdminLapsed(e.ctx, 1, at.Add(-time.Hour))
	rows := e.inbox(1)
	if len(rows) != 1 {
		t.Fatalf("%d rows: %+v", len(rows), rows)
	}
	if r := rows[0]; r.Kind != notify.InboxAdminLapsed || r.Status != "warn" || r.Title != "Admin access paused" || r.SubjectType != "account" || r.Subject != "1" ||
		r.Link != "/auth/oidc/start?next=%2Fadmin" || !strings.Contains(r.Body, "more than 24 hours old") {
		t.Fatalf("row %+v", r)
	}
	// Deleting the row does not make the same lapse news again.
	e.exec(`DELETE FROM inbox`)
	svc.AdminLapsed(e.ctx, 1, at)
	if n := len(e.inbox(1)); n != 0 {
		t.Fatalf("%d rows after deleting it", n)
	}
	svc.AdminLapsed(e.ctx, 1, at.Add(time.Hour))
	if n := len(e.inbox(1)); n != 1 {
		t.Fatalf("%d rows after a newer sign-in lapsed", n)
	}
	// The bell turned off for it: no row.
	e.exec(`INSERT OR IGNORE INTO notify_prefs (user_id, updated_at) VALUES (1, 1)`)
	e.exec(`INSERT INTO notify_event_prefs (user_id, event, channel, enabled) VALUES (1, ?, 'inbox', 0)`, notify.EventAdminLapsed)
	svc.AdminLapsed(e.ctx, 1, at.Add(2*time.Hour))
	if n := len(e.inbox(1)); n != 1 {
		t.Fatalf("%d rows with the bell off", n)
	}
}
