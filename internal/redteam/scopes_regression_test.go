package redteam_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

// TestWriteOnlyKeyReadsAndUnprotectsPrivateItems (regression, H2): the
// ShareX key (items:write only) can create items but not list, read,
// unprotect or delete the owner's other items. PATCH /api/v1/items/{id}
// answers 404 for every item the key did not create, so it cannot
// enumerate ids, flip visibility, clear a password or set an expiry.
func TestWriteOnlyKeyReadsAndUnprotectsPrivateItems(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", auth.RoleUser)
	c := e.session(alice)
	secret := "TOPSECRET-" + fmt.Sprint(time.Now().UnixNano())
	priv := e.paste(c, map[string]any{"content": secret, "visibility": "private", "password": "correct horse battery"})
	id := int64(priv["id"].(float64))

	tok := e.mint(alice, keys.NewKey{Scopes: []auth.Scope{auth.ScopeItemsWrite}})
	if res, _ := e.doJSON("GET", "/api/v1/items", nil, bearer(tok)); res.StatusCode != http.StatusForbidden {
		t.Fatalf("list with a write-only key: %d, want 403", res.StatusCode)
	}

	// No enumeration: every id answers 404, the owner's included.
	for i := 1; i <= 20; i++ {
		res, m := e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%d", i), map[string]any{}, bearer(tok))
		if res.StatusCode != http.StatusNotFound || m["slug"] != nil {
			t.Fatalf("empty PATCH of item %d with a write-only key: %d %v", i, res.StatusCode, m)
		}
	}
	// No unprotecting, no expiry (a delete in disguise), no rename to a known slug.
	for _, body := range []map[string]any{
		{"visibility": "unlisted", "password": nil},
		{"expires": time.Now().Add(1500 * time.Millisecond).UTC().Format(time.RFC3339Nano)},
		{"slug": "known-name"},
	} {
		if res, m := e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%d", id), body, bearer(tok)); res.StatusCode != http.StatusNotFound {
			t.Fatalf("PATCH %v with a write-only key: %d %v", body, res.StatusCode, m)
		}
	}
	time.Sleep(1600 * time.Millisecond)
	if err := e.s.App.Items.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	it, err := e.s.App.Items.Get(e.ctx, id)
	if err != nil {
		t.Fatalf("item gone: %v", err)
	}
	if it.Visibility != "private" || it.PasswordHash == "" || it.ExpiresAt != nil {
		t.Fatalf("item changed: visibility %s, password %v, expires %v", it.Visibility, it.PasswordHash != "", it.ExpiresAt)
	}
	if res, body := e.do("GET", "/"+it.Slug+"/raw", nil, anonymous()); res.StatusCode == http.StatusOK || strings.Contains(body, secret) {
		t.Fatalf("anonymous read: %d", res.StatusCode)
	}

	// What the key created itself it may still edit.
	res, own := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "from sharex"}, bearer(tok))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create with the key: %d %v", res.StatusCode, own)
	}
	if res, m := e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%v", own["id"]), map[string]any{"title": "renamed"}, bearer(tok)); res.StatusCode != http.StatusOK || m["title"] != "renamed" {
		t.Fatalf("PATCH of the key's own item: %d %v", res.StatusCode, m)
	}
	// Another write-only key of the same owner may not.
	other := e.mint(alice, keys.NewKey{Name: "other", Scopes: []auth.Scope{auth.ScopeItemsWrite}})
	if res, _ := e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%v", own["id"]), map[string]any{"title": "x"}, bearer(other)); res.StatusCode != http.StatusNotFound {
		t.Fatalf("PATCH of another key's item: %d", res.StatusCode)
	}
	// A key with items:read edits any of the owner's items, as before.
	rw := e.mint(alice, keys.NewKey{Name: "rw", Scopes: []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite}})
	if res, _ := e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%d", id), map[string]any{"title": "ok"}, bearer(rw)); res.StatusCode != http.StatusOK {
		t.Fatalf("PATCH with items:read: %d", res.StatusCode)
	}
}

// TestWriteOnlyAlbumKeyCannotCollectMembers (regression, H2 for albums): a
// key with albums:write but no read scopes cannot put the owner's items in
// an album (which would list their links on the album page) or read
// members back from write responses, and edits only albums it created.
func TestWriteOnlyAlbumKeyCannotCollectMembers(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", auth.RoleUser)
	c := e.session(alice)
	it := e.paste(c, map[string]any{"content": "unlisted notes"})
	tok := e.mint(alice, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAlbumsWrite}})

	if res, m := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "loot", "items": []any{it["id"]}}, bearer(tok)); res.StatusCode != http.StatusForbidden {
		t.Fatalf("album with members from an albums:write key: %d %v", res.StatusCode, m)
	}
	res, al := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "empty"}, bearer(tok))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("empty album: %d %v", res.StatusCode, al)
	}
	if res, _ := e.doJSON("POST", fmt.Sprintf("/api/v1/albums/%v/items", al["id"]), map[string]any{"add": []any{it["id"]}}, bearer(tok)); res.StatusCode != http.StatusForbidden {
		t.Fatalf("adding members with an albums:write key: %d", res.StatusCode)
	}
	// alice's own album, made in the browser, is not the key's to change.
	res, mine := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "mine", "items": []any{it["id"]}}, withCookie(c))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("browser album: %d %v", res.StatusCode, mine)
	}
	if res, m := e.doJSON("PATCH", fmt.Sprintf("/api/v1/albums/%v", mine["id"]), map[string]any{}, bearer(tok)); res.StatusCode != http.StatusNotFound || m["items"] != nil {
		t.Fatalf("PATCH of the browser's album with an albums:write key: %d %v", res.StatusCode, m)
	}
	if res, _ := e.doJSON("POST", fmt.Sprintf("/api/v1/albums/%v/items", mine["id"]), map[string]any{"order": []any{}}, bearer(tok)); res.StatusCode != http.StatusNotFound {
		t.Fatalf("reordering the browser's album with an albums:write key: %d", res.StatusCode)
	}
}
