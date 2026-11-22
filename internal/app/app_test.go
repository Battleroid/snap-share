package app_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/auth/authtest"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestCorePages(t *testing.T) {
	s := testutil.NewServer(t)
	s.Auth.Add("ann", auth.Principal{UserID: 1})

	res, body := s.Do(t, s.Request(t, "GET", "/", nil))
	if res.StatusCode != 200 || !strings.Contains(body, "Sign in") {
		t.Fatalf("anonymous home: %d", res.StatusCode)
	}
	if res.Header.Get("Content-Security-Policy") == "" {
		t.Error("page without CSP")
	}

	// Every asset referenced by the page is served with the right type and long cache.
	for _, m := range regexp.MustCompile(`(?:href|src)="(/static/[^"]+)"`).FindAllStringSubmatch(body, -1) {
		u := strings.ReplaceAll(m[1], "&amp;", "&")
		r, _ := s.Do(t, s.Request(t, "GET", u, nil))
		if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
			t.Errorf("%s: %d %q", u, r.StatusCode, r.Header.Get("Cache-Control"))
		}
		if strings.Contains(u, ".js") && !strings.HasPrefix(r.Header.Get("Content-Type"), "text/javascript") {
			t.Errorf("%s: content type %q", u, r.Header.Get("Content-Type"))
		}
	}

	// ServeMux cleans dot segments with a redirect, so traversal never reaches the asset handler.
	for path, want := range map[string]int{"/healthz": 200, "/robots.txt": 200, "/static/../app.go": 307, "/static/vendor/VENDOR.md": 404, "/missing": 404} {
		if r, _ := s.Do(t, s.Request(t, "GET", path, nil)); r.StatusCode != want {
			t.Errorf("%s: %d want %d", path, r.StatusCode, want)
		}
	}

	// The component reference is admin only.
	if r, _ := s.Do(t, s.Request(t, "GET", "/_kit", nil)); r.StatusCode != 303 {
		t.Errorf("anonymous kit: %d", r.StatusCode)
	}
	userKit := s.Request(t, "GET", "/_kit", nil)
	authtest.Session(userKit, "ann")
	if r, _ := s.Do(t, userKit); r.StatusCode != 403 {
		t.Errorf("user kit: %d", r.StatusCode)
	}
	s.Auth.Add("root", auth.Principal{UserID: 2, Role: auth.RoleAdmin})
	adminKit := s.Request(t, "GET", "/_kit", nil)
	authtest.Session(adminKit, "root")
	if r, b := s.Do(t, adminKit); r.StatusCode != 200 || !strings.Contains(b, "pb-drop") {
		t.Errorf("admin kit: %d", r.StatusCode)
	}

	home := s.Request(t, "GET", "/", nil)
	authtest.Session(home, "ann")
	if r, b := s.Do(t, home); r.StatusCode != 200 || !strings.Contains(b, `aria-current="page"`) {
		t.Errorf("signed-in home: %d", r.StatusCode)
	}
}
