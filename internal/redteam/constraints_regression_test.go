package redteam_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

// TestKeyExpiryCapBypassViaExpiryActionPrivate (regression, M7): a key whose
// expiry cap is 1 hour cannot create items with expiry_action=private (the
// sweep would clear their expiry), cannot switch an item to it later, and a
// change of visibility re-applies the cap to an item that has no expiry.
func TestKeyExpiryCapBypassViaExpiryActionPrivate(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", auth.RoleUser)
	tok := e.mint(alice, keys.NewKey{
		Scopes:      []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite},
		Constraints: keys.Constraints{MaxExpirySeconds: 3600, Visibility: "unlisted"},
	})
	exp := time.Now().Add(1500 * time.Millisecond).UTC().Format(time.RFC3339Nano)
	if res, m := e.doJSON("POST", "/api/v1/pastes", map[string]any{
		"content": "lives forever", "expires": exp, "expiry_action": "private",
	}, bearer(tok)); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with expiry_action=private under a capped key: %d %v", res.StatusCode, m)
	}
	res, m := e.doJSON("POST", "/api/v1/pastes", map[string]any{"content": "capped", "expires": exp}, bearer(tok))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", res.StatusCode, m)
	}
	id := int64(m["id"].(float64))
	if res, _ := e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%d", id), map[string]any{"expiry_action": "private"}, bearer(tok)); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH expiry_action=private under a capped key: %d", res.StatusCode)
	}
	// Bulk is held to the same rule.
	if res, _ := e.doJSON("POST", "/api/v1/items/bulk", map[string]any{"ids": []int64{id}, "action": "set_expiry",
		"params": map[string]any{"expires": "1h", "expiry_action": "private"}}, bearer(tok)); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bulk set_expiry private under a capped key: %d", res.StatusCode)
	}

	// An item already in the bypass state (made before the fix, or by the
	// owner's browser): private with no expiry. The capped key making it
	// unlisted again gives it an expiry within the cap.
	for _, via := range []string{"patch", "bulk"} {
		if _, err := e.s.App.DB.W.Exec(`UPDATE items SET visibility = 'private', expires_at = NULL, expiry_action = 'private' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if via == "patch" {
			res, m = e.doJSON("PATCH", fmt.Sprintf("/api/v1/items/%d", id), map[string]any{"visibility": "unlisted"}, bearer(tok))
		} else {
			res, m = e.doJSON("POST", "/api/v1/items/bulk", map[string]any{"ids": []int64{id}, "action": "set_visibility",
				"params": map[string]any{"visibility": "unlisted"}}, bearer(tok))
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s visibility: %d %v", via, res.StatusCode, m)
		}
		it, err := e.s.App.Items.Get(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if it.Visibility != "unlisted" || it.ExpiresAt == nil || it.ExpiresAt.After(time.Now().Add(time.Hour+time.Minute)) || it.ExpiryAction != "delete" {
			t.Fatalf("%s: visibility %s, expires %v, action %s; want unlisted within 1h, delete", via, it.Visibility, it.ExpiresAt, it.ExpiryAction)
		}
	}
}
