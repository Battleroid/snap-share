package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/settings"
)

func store(t *testing.T) (*db.DB, *settings.Store) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	return d, st
}

func TestHandlerAccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	New(reg, nil)
	h := Handler(reg, Access{Token: "s3cret-token-0123456789", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}})
	cases := []struct {
		name, path, remote, auth, xff string
		want                          int
	}{
		{"allowed", "/metrics", "10.1.2.3:5000", "Bearer s3cret-token-0123456789", "", 200},
		{"v4-mapped peer", "/metrics", "[::ffff:10.1.2.3]:5000", "Bearer s3cret-token-0123456789", "", 200},
		{"scheme is case-insensitive", "/metrics", "10.1.2.3:5000", "bearer s3cret-token-0123456789", "", 200},
		{"no token", "/metrics", "10.1.2.3:5000", "", "", 401},
		{"wrong token", "/metrics", "10.1.2.3:5000", "Bearer s3cret-token-012345678", "", 401},
		{"token prefix", "/metrics", "10.1.2.3:5000", "Bearer s3cret", "", 401},
		{"basic", "/metrics", "10.1.2.3:5000", "Basic s3cret-token-0123456789", "", 401},
		{"outside the allowlist", "/metrics", "192.0.2.1:5000", "Bearer s3cret-token-0123456789", "", 403},
		{"forwarded-for is ignored", "/metrics", "192.0.2.1:5000", "Bearer s3cret-token-0123456789", "10.1.2.3", 403},
		{"other paths", "/", "10.1.2.3:5000", "Bearer s3cret-token-0123456789", "", 404},
		{"other paths 2", "/metrics/x", "10.1.2.3:5000", "Bearer s3cret-token-0123456789", "", 404},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", c.path, nil)
		req.RemoteAddr = c.remote
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.want)
		}
		if c.want == 200 && !strings.Contains(rec.Body.String(), "snap_build_info") {
			t.Errorf("%s: body %q", c.name, rec.Body.String())
		}
		if c.want == 401 && rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: no WWW-Authenticate", c.name)
		}
	}
	// Without a token or allowlist, anyone on the listener may scrape.
	open := Handler(reg, Access{})
	rec := httptest.NewRecorder()
	open.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("open handler: %d", rec.Code)
	}
}

func gather(t *testing.T, reg *prometheus.Registry, name string) map[string]float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			var l []string
			for _, lp := range m.GetLabel() {
				l = append(l, lp.GetName()+"="+lp.GetValue())
			}
			v := m.GetCounter().GetValue() + m.GetGauge().GetValue()
			out[strings.Join(l, ",")] = v
		}
	}
	return out
}

func TestPerPrincipalSeriesFollowTheSettingAndAreCapped(t *testing.T) {
	ctx := context.Background()
	_, st := store(t)
	reg := prometheus.NewRegistry()
	m := New(reg, st)
	m.principals.max = 3

	m.Request(1, 0)
	if got := gather(t, reg, "snap_user_requests_total"); len(got) != 0 {
		t.Fatalf("series while off: %v", got)
	}
	if err := PerUser.Set(ctx, st, true); err != nil {
		t.Fatal(err)
	}
	for id := int64(1); id <= 10; id++ {
		m.Request(id, 100+id)
	}
	m.Request(1, 0)
	got := gather(t, reg, "snap_user_requests_total")
	if len(got) != 4 || got["user_id=1"] != 2 || got["user_id=other"] != 7 {
		t.Fatalf("per user: %v", got)
	}
	if keys := gather(t, reg, "snap_key_requests_total"); len(keys) != 0 {
		t.Fatalf("per key while off: %v", keys)
	}
	// Off again: the series go at once and counting stops.
	if err := PerUser.Set(ctx, st, false); err != nil {
		t.Fatal(err)
	}
	if got := gather(t, reg, "snap_user_requests_total"); len(got) != 0 {
		t.Fatalf("series after turning off: %v", got)
	}
	if err := PerKey.Set(ctx, st, true); err != nil {
		t.Fatal(err)
	}
	m.Request(1, 7)
	if keys := gather(t, reg, "snap_key_requests_total"); len(keys) != 1 || keys["key_id=7"] != 1 {
		t.Fatalf("per key: %v", keys)
	}
}

func TestLabelValuesAreBounded(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, nil)
	m.AuthAttempt("made-up", true)
	m.ItemCreated("../etc", 10)
	m.NotifyDelivery("carrier-pigeon", false)
	m.StatsEvent("x\"y")
	m.MediaStep("rm -rf", 0, nil)
	for name, want := range map[string]string{
		"snap_auth_attempts_total":     "method=other,result=ok",
		"snap_uploads_total":           "kind=other,target=item",
		"snap_notify_deliveries_total": "channel=other,result=failed",
		"snap_stats_events_total":      "kind=other",
	} {
		if got := gather(t, reg, name); len(got) != 1 || got[want] != 1 {
			t.Errorf("%s: %v", name, got)
		}
	}
	var nilM *Metrics // nil-safe
	nilM.AuthAttempt("password", true)
	nilM.Request(1, 1)
}

func TestInstanceReferrersCapped(t *testing.T) {
	ctx := context.Background()
	d, st := store(t)
	if err := Referrers.Set(ctx, st, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.Exec(`INSERT INTO users (username, created_at, updated_at) VALUES ('u', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.Exec(`INSERT INTO items (owner_id, kind, slug, created_at, updated_at) VALUES (1, 'paste', 'abc', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	now := db.Millis(time.Now())
	for i := range TopReferrers + 15 {
		host := "site" + strconv.Itoa(i) + ".example"
		if i == 3 {
			host = "evil\"} 1\nsnap_x{" // not a hostname: counted as other
		}
		for range i + 1 {
			if _, err := d.W.Exec(`INSERT INTO item_events (item_id, at, kind, referrer_host) VALUES (1, ?, 'view', ?)`, now, host); err != nil {
				t.Fatal(err)
			}
		}
	}
	reg := prometheus.NewRegistry()
	inst := NewInstance(InstanceOptions{DB: d, Settings: st, DataDir: filepath.Dir("."), JobKinds: func() []string { return []string{"media.process"} }})
	reg.MustRegister(inst)
	got := gather(t, reg, "snap_referrer_events_24h")
	if len(got) != TopReferrers+1 || got["host=other"] == 0 {
		t.Fatalf("%d series: %v", len(got), got)
	}
	for k := range got {
		if strings.ContainsAny(k, "\"{}\n") {
			t.Errorf("unsafe host label %q", k)
		}
	}
	if jobs := gather(t, reg, "snap_jobs"); len(jobs) != 2*len(JobStates) {
		t.Errorf("jobs: %v", jobs)
	}
	// Turned off: gone at the next scrape, even from the cache.
	if err := Referrers.Set(ctx, st, false); err != nil {
		t.Fatal(err)
	}
	if got := gather(t, reg, "snap_referrer_events_24h"); len(got) != 0 {
		t.Fatalf("after turning off: %v", got)
	}
}
