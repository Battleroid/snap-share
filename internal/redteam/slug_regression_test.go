package redteam_test

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
)

// TestRandomSlugsMatchCaseInsensitively (regression, L17): lookups stay
// case-insensitive for every scheme, so random slugs are drawn from the 36
// symbols that lookups tell apart (lowercase letters and digits), at least
// 10 of them: 36^10 is about 2^51.7.
func TestRandomSlugsMatchCaseInsensitively(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", auth.RoleUser)
	c := e.session(alice)
	m := e.paste(c, map[string]any{"content": "unlisted content"})
	slug := m["slug"].(string)
	if !regexp.MustCompile(`^[0-9a-z]{10,}$`).MatchString(slug) {
		t.Fatalf("random slug %q: want 10 or more lowercase letters and digits", slug)
	}
	res, body := e.do("GET", "/"+strings.ToUpper(slug), nil, anonymous())
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "unlisted content") {
		t.Fatalf("GET /%s: %d, want the case-insensitive lookup to stay", strings.ToUpper(slug), res.StatusCode)
	}
	// Shorter random names are refused rather than silently weaker.
	if res, m := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "x", "naming": "random", "length": 8}, withCookie(c)); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("length 8: %d %v", res.StatusCode, m)
	}
}

// TestDeletedSlugReclaimedByAnotherUser (regression, L1): a deleted item's
// slug stays with its owner for the cooldown, so links already shared do
// not lead to another account's content; renaming into it is refused too.
func TestDeletedSlugReclaimedByAnotherUser(t *testing.T) {
	e := newEnv(t)
	alice, mallory := e.user("alice", auth.RoleUser), e.user("mallory", auth.RoleUser)
	ca, cm := e.session(alice), e.session(mallory)
	m := e.paste(ca, map[string]any{"content": "Q3 numbers: see attachment", "slug": "q3-report"})
	if res, _ := e.do("DELETE", fmt.Sprintf("/api/v1/items/%d", int64(m["id"].(float64))), nil, withCookie(ca)); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res, out := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "Updated link: https://phish.example/login", "slug": "q3-report"}, withCookie(cm)); res.StatusCode != http.StatusConflict {
		t.Fatalf("mallory takes /q3-report: %d %v", res.StatusCode, out)
	}
	if res, body := e.do("GET", "/q3-report", nil, anonymous()); res.StatusCode != http.StatusNotFound || strings.Contains(body, "phish.example") {
		t.Fatalf("old link: %d", res.StatusCode)
	}

	m2 := e.paste(ca, map[string]any{"content": "v2", "slug": "handbook"})
	if res, _ := e.do("DELETE", fmt.Sprintf("/api/v1/items/%d", int64(m2["id"].(float64))), nil, withCookie(ca)); res.StatusCode != http.StatusNoContent {
		t.Fatal("delete")
	}
	m3 := e.paste(cm, map[string]any{"content": "evil"})
	if res, out := e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%d", int64(m3["id"].(float64))), map[string]any{"slug": "handbook"}, withCookie(cm)); res.StatusCode != http.StatusConflict {
		t.Fatalf("rename into a held slug: %d %v", res.StatusCode, out)
	}
	// The previous owner takes it back.
	if res, out := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "v3", "slug": "handbook"}, withCookie(ca)); res.StatusCode != http.StatusCreated {
		t.Fatalf("alice takes back /handbook: %d %v", res.StatusCode, out)
	}
}
