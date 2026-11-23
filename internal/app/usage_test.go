package app_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// Settings › Profile shows its owner's Usage: storage against the quota,
// item and secret counts, the max file size and items by type.
func TestProfileUsage(t *testing.T) {
	s := testutil.NewServer(t)
	if err := local.MinFreeBytes.Set(context.Background(), s.App.Settings, 0); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.App.DB.W.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users (id, username, email, email_verified_at, quota_bytes, max_upload_bytes, created_at, updated_at) VALUES (1, 'ann', 'ann@example.test', 1, 10000000, 2000000, 1, 1)`)
	exec(`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at) VALUES (2, 'bob', 'bob@example.test', 1, 1, 1)`)
	s.Auth.Add("ann", auth.Principal{UserID: 1, Username: "ann", EmailVerified: true, SessionID: "s-ann"})
	s.Auth.Add("bob", auth.Principal{UserID: 2, Username: "bob", EmailVerified: true, SessionID: "s-bob"})

	do := func(method, path, as string, body string) (int, string) {
		t.Helper()
		req := s.Request(t, method, path, strings.NewReader(body))
		authtest.Session(req, as)
		res, page := s.Do(t, req) //nolint:bodyclose // Do reads and closes the body
		return res.StatusCode, page
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		if code, body := do("PUT", "/api/v1/upload/"+url.PathEscape(name), "ann", "hello "+name); code != 201 {
			t.Fatalf("upload %s: %d %s", name, code, body)
		}
	}

	code, page := do("GET", "/settings/profile", "ann", "")
	if code != 200 {
		t.Fatalf("profile: %d %s", code, page)
	}
	for _, want := range []string{
		`<section id="usage">`, `href="#usage"`,
		`<span class="pb-tile__label">storage used</span>`, "of 10 MB",
		`<span class="pb-tile__label">items</span><span class="pb-tile__value">2</span>`, "&#43;2 · 30d",
		`<span class="pb-tile__label">secrets · active</span><span class="pb-tile__value">0</span>`,
		`<span class="pb-tile__label">views · 30d</span><span class="pb-tile__value">0</span>`,
		"Max file size", `<span class="pb-mono">2 MB</span>`, "Items by type", `aria-label="2 text"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("ann's Usage lacks %q", want)
		}
	}

	// Bob sees his own numbers: nothing stored, no quota.
	_, page = do("GET", "/settings/profile", "bob", "")
	if !strings.Contains(page, `<section id="usage">`) ||
		!strings.Contains(page, `<span class="pb-tile__label">items</span><span class="pb-tile__value">0</span>`) ||
		strings.Contains(page, "of 10 MB") || strings.Contains(page, `aria-label="2 text"`) {
		t.Error("bob's Usage shows someone else's numbers")
	}
}
