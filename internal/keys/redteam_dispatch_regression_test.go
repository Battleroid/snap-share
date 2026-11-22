package keys_test

// Regression (H1): a real API key holding only account:read is a visitor on
// the public slug dispatcher and the home page, as every items:* route
// already treats it: it cannot list the owner's recent items from GET / nor
// read private or password-protected items.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

func TestRedteamAccountReadKeyReadsPrivateItems(t *testing.T) {
	e := newEnv(t)
	ann := e.user("ann", auth.RoleUser)
	sess := e.session(ann)
	tok := e.mint(ann, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAccountRead}})

	upload := func(name, body string, hdr map[string]string) string {
		req := e.s.Request(t, "PUT", "/api/v1/upload/"+name+"?visibility=private", strings.NewReader(body))
		req.AddCookie(sess)
		req.Header.Set("Accept", "text/plain")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s: %d %s", name, res.StatusCode, b)
		}
		u := strings.TrimSpace(string(b))
		return u[strings.LastIndexByte(u, '/')+1:]
	}
	get := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", e.s.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		res, err := e.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	priv := upload("diary.txt", "private diary", nil)
	locked := upload("taxes.txt", "private and password protected", map[string]string{"X-Snap-Password": "correct horse"})

	if code, _ := get("/api/v1/items"); code != http.StatusForbidden {
		t.Fatalf("GET /api/v1/items with an account:read key: %d, want 403", code)
	}
	code, home := get("/")
	if code != http.StatusOK || strings.Contains(home, priv) || strings.Contains(home, locked) {
		t.Fatalf("GET / with an account:read key: %d, private slugs listed: %v", code, strings.Contains(home, priv))
	}
	for _, slug := range []string{priv, locked} {
		for _, m := range []string{"", "/raw", ".txt", "/download"} {
			if code, body := get("/" + slug + m); code != http.StatusNotFound || strings.Contains(body, "private") && m != "" {
				t.Errorf("%s%s with an account:read key: %d", slug, m, code)
			}
		}
	}
	// /api/v1/me, the key's own scope, still works.
	if code, _ := get("/api/v1/me"); code != http.StatusOK {
		t.Fatalf("GET /api/v1/me: %d", code)
	}
}
