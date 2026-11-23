package redteam_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func (e *env) used(id int64) int64 {
	e.t.Helper()
	var used int64
	if err := e.s.App.DB.R.QueryRowContext(e.ctx, `SELECT used_bytes FROM users WHERE id = ?`, id).Scan(&used); err != nil {
		e.t.Fatal(err)
	}
	return used
}

// TestRenderBlobNotCountedAgainstQuota (regression, L15): the cached render
// of a paste is charged to its owner with the source, refunded on delete,
// and counted by the hourly reconcile; a render that does not fit the
// quota is dropped instead of stored uncharged.
func TestRenderBlobNotCountedAgainstQuota(t *testing.T) {
	e := newEnv(t)
	u := e.user("alice", auth.RoleUser)
	c := e.session(u)
	src := strings.Repeat("a+b;", (96<<10)/4)
	res, m := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": src, "lang": "go"}, withCookie(c))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("paste: %d %v", res.StatusCode, m)
	}
	id := int64(m["id"].(float64))
	it, err := e.s.App.Items.Get(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	f, err := e.s.App.Files.Open(e.ctx, it.RenderKey)
	if err != nil {
		t.Fatal(err)
	}
	renderSize := f.Size()
	_ = f.Close()
	if it.RenderSize != renderSize {
		t.Fatalf("render_size %d, blob %d", it.RenderSize, renderSize)
	}
	if used := e.used(u.ID); used != int64(len(src))+renderSize {
		t.Fatalf("used_bytes %d, want source %d + render %d", used, len(src), renderSize)
	}
	if _, err := e.s.App.DB.W.Exec(`UPDATE users SET used_bytes = 0`); err != nil {
		t.Fatal(err)
	}
	if err := e.s.App.Items.Reconcile(e.ctx); err != nil {
		t.Fatal(err)
	}
	if used := e.used(u.ID); used != int64(len(src))+renderSize {
		t.Fatalf("after reconcile: used_bytes %d, want %d", used, int64(len(src))+renderSize)
	}
	if res, _ := e.do("DELETE", fmt.Sprintf("/api/v1/items/%d", id), nil, withCookie(c)); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if used := e.used(u.ID); used != 0 {
		t.Fatalf("after delete: used_bytes %d", used)
	}

	// With a quota that fits the source but not its render, the paste is
	// stored without one (shown as plain text) and nothing goes uncharged.
	if _, err := e.s.App.DB.W.Exec(`UPDATE users SET quota_bytes = ? WHERE id = ?`, len(src)+1000, u.ID); err != nil {
		t.Fatal(err)
	}
	res, m = e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": src, "lang": "go"}, withCookie(c))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("paste under quota: %d %v", res.StatusCode, m)
	}
	it, err = e.s.App.Items.Get(e.ctx, int64(m["id"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	if it.RenderKey != "" || e.used(u.ID) != int64(len(src)) {
		t.Fatalf("render kept over quota: key %q, used %d", it.RenderKey, e.used(u.ID))
	}
	if res, body := e.do("GET", "/"+it.Slug, nil, anonymous()); res.StatusCode != http.StatusOK || !strings.Contains(body, "a+b;") {
		t.Fatalf("share page without a render: %d", res.StatusCode)
	}
}

// TestLinksAndCreationRate (regression, L16): short links count
// items.LinkChargeBytes toward the quota, and one account creates at most
// items.CreatesBurst items in a burst (sessions and keys together).
func TestLinksAndCreationRate(t *testing.T) {
	// The items clock stands still, so the burst does not refill while a
	// slow runner works through it.
	now := time.Now()
	e := newEnv(t, testutil.AppOptions(func(o *app.Options) { o.ItemsNow = func() time.Time { return now } }))
	u := e.user("alice", auth.RoleUser)
	c := e.session(u)
	if _, err := e.s.App.DB.W.Exec(`UPDATE users SET quota_bytes = ? WHERE id = ?`, 3*items.LinkChargeBytes, u.ID); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if res, m := e.doJSON("POST", "/api/v1/links", map[string]any{"url": fmt.Sprintf("https://example.net/%d", i)}, withCookie(c)); res.StatusCode != http.StatusCreated {
			t.Fatalf("link %d: %d %v", i, res.StatusCode, m)
		}
	}
	if used := e.used(u.ID); used != 3*items.LinkChargeBytes {
		t.Fatalf("used_bytes %d after 3 links", used)
	}
	if res, _ := e.doJSON("POST", "/api/v1/links", map[string]any{"url": "https://example.net/4"}, withCookie(c)); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("link over quota: %d", res.StatusCode)
	}

	if _, err := e.s.App.DB.W.Exec(`UPDATE users SET quota_bytes = NULL WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	limited := 0
	for i := range items.CreatesBurst + 5 {
		res, _ := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": fmt.Sprint("p", i)}, withCookie(c))
		switch res.StatusCode {
		case http.StatusCreated:
		case http.StatusTooManyRequests:
			if res.Header.Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
			limited++
		default:
			t.Fatalf("paste %d: %d", i, res.StatusCode)
		}
	}
	// 4 creations above (one refused for quota) spent from the burst too.
	if limited != 4+5 {
		t.Fatalf("%d of %d creations limited, want 9", limited, items.CreatesBurst+5)
	}
}
