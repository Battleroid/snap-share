package metrics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/metrics"
	"github.com/Battleroid/snap-share/internal/obs"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// /metrics is served only by the metrics listener: the public handler and
// the raw origin answer it like any unknown path, and no slug can take it.
func TestMetricsNeverOnThePublicListener(t *testing.T) {
	s := testutil.NewServer(t, testutil.Env("RAW_URL", "https://raw.test"))
	s.Auth.Add("root", auth.Principal{UserID: 1, Role: auth.RoleAdmin})
	if err := local.MinFreeBytes.Set(context.Background(), s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/metrics", "/metrics/", "/Metrics", "/metrics.txt", "/metrics/raw"} {
		for _, host := range []string{"", "raw.test"} {
			for _, signedIn := range []bool{false, true} {
				req := s.Request(t, "GET", path, nil)
				if host != "" {
					req.Host = host
				}
				if signedIn {
					authtest.Session(req, "root")
				}
				code, body := call(t, s, req)
				if code == http.StatusOK || strings.Contains(body, "snap_build_info") || strings.Contains(body, "go_goroutines") {
					t.Errorf("%s (host %q, signed in %v): %d, metrics served", path, host, signedIn, code)
				}
			}
		}
	}
	// "metrics" is reserved: no item can claim the path.
	if _, err := s.App.Accounts.Store().CreateUser(context.Background(), accounts.NewUser{Username: "root", Role: auth.RoleAdmin}, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"metrics", "METRICS"} {
		req := s.Request(t, "POST", "/api/v1/pastes", strings.NewReader(`{"content":"x","slug":"`+slug+`"}`))
		req.Header.Set("Content-Type", "application/json")
		authtest.Session(req, "root")
		if code, body := call(t, s, req); code < 400 {
			t.Errorf("slug %q accepted: %d %s", slug, code, body)
		}
	}
	// The metrics handler itself answers.
	rec := httptest.NewRecorder()
	s.App.MetricsHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "snap_build_info") {
		t.Fatalf("metrics handler: %d", rec.Code)
	}
}

func TestMetricsTokenFromConfig(t *testing.T) {
	s := testutil.NewServer(t, testutil.Env("METRICS_TOKEN", "scrape-token-0123456789"), testutil.Env("METRICS_CIDRS", "192.0.2.0/24"))
	for _, c := range []struct {
		remote, token string
		want          int
	}{
		{"192.0.2.9:4000", "scrape-token-0123456789", 200},
		{"192.0.2.9:4000", "", 401},
		{"198.51.100.1:4000", "scrape-token-0123456789", 403},
	} {
		req := httptest.NewRequest("GET", "/metrics", nil)
		req.RemoteAddr = c.remote
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		rec := httptest.NewRecorder()
		s.App.MetricsHandler().ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s token=%v: %d want %d", c.remote, c.token != "", rec.Code, c.want)
		}
	}
}

type series struct {
	name   string
	labels map[string]string
}

func gatherAll(t *testing.T, a *app.App) []series {
	t.Helper()
	a.Instance.Refresh()
	mfs, err := a.Metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var out []series
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			l := map[string]string{}
			for _, lp := range m.GetLabel() {
				l[lp.GetName()] = lp.GetValue()
			}
			out = append(out, series{mf.GetName(), l})
		}
	}
	return out
}

func countByName(ss []series) map[string]int {
	out := map[string]int{}
	for _, s := range ss {
		out[s.name]++
	}
	return out
}

// workload creates users with API keys, pastes, views from many referrer
// hosts and bad slugs, all with names unique to round.
func workload(t *testing.T, s *testutil.Server, round, users int) (dynamic []string) {
	t.Helper()
	ctx := context.Background()
	for i := range users {
		name := fmt.Sprintf("u%dn%d", round, i)
		u, err := s.App.Accounts.Store().CreateUser(ctx, accounts.NewUser{Username: name, Role: auth.RoleUser}, accounts.AuditEntry{})
		if err != nil {
			t.Fatal(err)
		}
		keyID := int64(1000*round + i + 1)
		s.Auth.Add(name, auth.Principal{UserID: u.ID, EmailVerified: true})
		s.Auth.Add(name+"-key", auth.Principal{UserID: u.ID, EmailVerified: true, KeyID: keyID, Scopes: []auth.Scope{auth.ScopeItemsWrite, auth.ScopeItemsRead}})
		slug := fmt.Sprintf("s%dslug%d", round, i)
		host := fmt.Sprintf("r%dhost%d.example", round, i)
		dynamic = append(dynamic, name, slug, host, fmt.Sprint(u.ID), fmt.Sprint(keyID))

		req := s.Request(t, "POST", "/api/v1/pastes", strings.NewReader(`{"content":"hello","slug":"`+slug+`"}`))
		req.Header.Set("Content-Type", "application/json")
		authtest.Bearer(req, name+"-key")
		if code, body := call(t, s, req); code != http.StatusCreated {
			t.Fatalf("paste: %d %s", code, body)
		}
		view := s.Request(t, "GET", "/"+slug, nil)
		view.Header.Set("Referer", "https://"+host+"/page?q=1")
		view.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0")
		send(t, s, view)
		me := s.Request(t, "GET", "/api/v1/items", nil)
		authtest.Session(me, name)
		send(t, s, me)
		send(t, s, s.Request(t, "GET", "/no-such-"+slug, nil))
	}
	if err := s.App.Items.FlushCounters(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.App.Stats.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	return dynamic
}

// The cardinality guard: with the metrics.* settings off, no series is per
// user, key, item or referrer, so more of them add no series; with each
// setting on its series appear, stay capped, and go when it is off again.
func TestMetricsCardinality(t *testing.T) {
	ctx := context.Background()
	// In worker mode, so the media worker series are exported too (no
	// worker listens: its health check fails).
	s := testutil.NewServer(t, testutil.Env("MEDIA_WORKER", "unix:///nonexistent/snap-media/w.sock"),
		testutil.Env("MEDIA_WORKER_TOKEN", "metrics-test-media-worker-token-0123"))
	if err := local.MinFreeBytes.Set(ctx, s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	optIn := []string{"snap_user_requests_total", "snap_key_requests_total", "snap_user_storage_bytes", "snap_user_items", "snap_referrer_events_24h"}
	forbidden := []string{"user", "user_id", "username", "key", "key_id", "item", "item_id", "slug", "host", "referrer", "email", "ip", "path"}

	// Families the test server never fires (no job runner, ffmpeg or
	// deliveries) get a sample each, so the label checks cover them.
	o := s.App.Obs
	o.JobRun("media.process", "ok", time.Second)
	o.TaskRun("stats.purge", true, time.Second)
	o.RateLimited("login_ip")
	o.AuthAttempt("password", false)
	o.NotifyDelivery("email", true)
	o.MediaStep("probe", time.Second, nil)
	o.SecretCreated("text", 100)
	_ = s.App.CheckMediaWorker(ctx)
	dyn := workload(t, s, 1, 3)
	first := countByName(gatherAll(t, s.App))
	dyn = append(dyn, workload(t, s, 2, 9)...)
	all := gatherAll(t, s.App)
	second := countByName(all)

	for name, n := range second {
		if strings.HasPrefix(name, "go_") || strings.HasPrefix(name, "process_") {
			continue
		}
		if n != first[name] {
			t.Errorf("%s: %d series after 3 users, %d after 12", name, first[name], n)
		}
	}
	for _, sr := range all {
		if slices.Contains(optIn, sr.name) {
			t.Errorf("%s exported with its setting off", sr.name)
		}
		for l, v := range sr.labels {
			if slices.Contains(forbidden, l) {
				t.Errorf("%s has label %s", sr.name, l)
			}
			for _, d := range dyn {
				if len(d) > 2 && strings.Contains(v, d) {
					t.Errorf("%s{%s=%q} carries %q", sr.name, l, v, d)
				}
			}
		}
		if route, ok := sr.labels["route"]; ok && !knownRoute(s.App, route) {
			t.Errorf("%s route label %q is not a pattern", sr.name, route)
		}
	}

	// Each setting turns its series on.
	for _, set := range []func(bool){
		func(on bool) { must(t, metrics.PerUser.Set(ctx, s.App.Settings, on)) },
		func(on bool) { must(t, metrics.PerKey.Set(ctx, s.App.Settings, on)) },
		func(on bool) { must(t, metrics.Referrers.Set(ctx, s.App.Settings, on)) },
	} {
		set(true)
	}
	workload(t, s, 3, 2)
	on := countByName(gatherAll(t, s.App))
	for _, name := range optIn {
		if on[name] == 0 {
			t.Errorf("%s missing with its setting on", name)
		}
	}
	if on["snap_user_storage_bytes"] > metrics.MaxSeries || on["snap_user_requests_total"] > metrics.MaxSeries+1 ||
		on["snap_key_requests_total"] > metrics.MaxSeries+1 || on["snap_referrer_events_24h"] > metrics.TopReferrers+1 {
		t.Errorf("opt-in series over their caps: %v", on)
	}
	must(t, metrics.PerUser.Set(ctx, s.App.Settings, false))
	must(t, metrics.PerKey.Set(ctx, s.App.Settings, false))
	must(t, metrics.Referrers.Set(ctx, s.App.Settings, false))
	off := countByName(gatherAll(t, s.App))
	for _, name := range optIn {
		if off[name] != 0 {
			t.Errorf("%s still exported after its setting went off", name)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func knownRoute(a *app.App, route string) bool {
	if route == "unmatched" {
		return true
	}
	for _, r := range a.Router.Routes() {
		if r.Pattern == route {
			return true
		}
	}
	return false
}

func TestEventMetricsCount(t *testing.T) {
	s := testutil.NewServer(t)
	if err := local.MinFreeBytes.Set(context.Background(), s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	workload(t, s, 1, 2)
	ss := gatherAll(t, s.App)
	want := map[string]map[string]string{
		"snap_uploads_total":       {"target": "item", "kind": "paste"},
		"snap_upload_bytes_total":  {"target": "item", "kind": "paste"},
		"snap_stats_events_total":  {"kind": "view"},
		"snap_http_requests_total": {"route": "POST /api/v1/pastes", "code": "201"},
		"snap_users":               {"status": "active", "role": "user"},
		"snap_items":               {"kind": "paste"},
		"snap_build_info":          {},
		"snap_db_size_bytes":       {},
	}
	for name, labels := range want {
		found := false
		for _, sr := range ss {
			if sr.name != name {
				continue
			}
			match := true
			for k, v := range labels {
				if sr.labels[k] != v {
					match = false
				}
			}
			found = found || match
		}
		if !found {
			t.Errorf("no %s%v", name, labels)
		}
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// Audit entries reach the stream after they commit, one line each, with
// the table as the source of truth.
func TestAuditStreamMatchesTable(t *testing.T) {
	ctx := context.Background()
	out := &syncBuffer{}
	s := testutil.NewServer(t, testutil.RealAuth(), testutil.AppOptions(func(o *app.Options) { o.AuditSink = obs.JSONLines{W: out} }))
	if s.App.AuditStream == nil {
		t.Fatal("no stream with a sink")
	}
	store := s.App.Accounts.Store()
	u, err := store.CreateUser(ctx, accounts.NewUser{Username: "ann", Email: "ann@example.test", Role: auth.RoleUser}, accounts.AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.App.Keys.Store().Create(ctx, u.ID, keys.NewKey{Name: "ci", Scopes: []auth.Scope{auth.ScopeItemsRead}}, accounts.AuditEntry{ActorUserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	// A failed sign-in is audited too.
	form := strings.NewReader("login=ann&password=wrong-password-123")
	req := s.Request(t, "POST", "/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	send(t, s, req)
	if err := store.Audit(ctx, accounts.AuditEntry{Action: "moderation.burn", Target: "secret:AAAAAAAAAAAAAAAAAAAAAA"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.App.AuditStream.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.App.DB.R.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSpace(out.String())
	got := strings.Split(text, "\n")
	if rows < 4 || len(got) != rows {
		t.Fatalf("%d lines for %d rows:\n%s", len(got), rows, text)
	}
	actions := map[string]bool{}
	for _, l := range got {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		actions[m["action"].(string)] = true
	}
	for _, a := range []string{"user.create", "key.create", "auth.login_failed", "moderation.burn"} {
		if !actions[a] {
			t.Errorf("no %s line in %s", a, text)
		}
	}
	if strings.Contains(text, "AAAAAAAAAAAAAAAAAAAAAA") || strings.Contains(text, "ann@example.test") {
		t.Errorf("unredacted: %s", text)
	}
	if !regexp.MustCompile(`"auth.login_failed"`).MatchString(text) {
		t.Error("login failure missing")
	}
}

// call sends req with a fresh client and returns the status and body.
func call(t *testing.T, s *testutil.Server, req *http.Request) (int, string) {
	t.Helper()
	res, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}

// send makes a request whose answer doesn't matter.
func send(t *testing.T, s *testutil.Server, req *http.Request) {
	t.Helper()
	_, _ = call(t, s, req)
}
