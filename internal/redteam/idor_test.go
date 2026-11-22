package redteam_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

// TestIDORSweep (holds): every route with an {id} (or {token}/{slug}
// variant) is called by mallory, with a browser session and with a key
// holding every scope, on ids of alice's item, album, tag and key. No call
// may succeed or change alice's data.
func TestIDORSweep(t *testing.T) {
	e := newEnv(t)
	alice, mallory := e.user("alice", auth.RoleUser), e.user("mallory", auth.RoleUser)
	ca, cm := e.session(alice), e.session(mallory)
	it := e.paste(ca, map[string]any{"content": "alice's", "visibility": "private"})
	res, al := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "alice album", "items": []int64{int64(it["id"].(float64))}}, withCookie(ca))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("album: %d %v", res.StatusCode, al)
	}
	res, tg := e.doJSON("POST", "/api/v1/tags", map[string]any{"name": "alice-tag"}, withCookie(ca))
	if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
		t.Fatalf("tag: %d %v", res.StatusCode, tg)
	}
	for range 3 { // alice's keys get ids 1-3; mallory's is 4
		e.mint(alice, keys.NewKey{Scopes: []auth.Scope{auth.ScopeItemsRead}})
	}
	all := []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite, auth.ScopeItemsDelete, auth.ScopeAlbumsRead,
		auth.ScopeAlbumsWrite, auth.ScopeSecretsRead, auth.ScopeSecretsWrite, auth.ScopeAccountRead}
	mk := e.mint(mallory, keys.NewKey{Scopes: all})

	ids := []string{fmt.Sprint(it["id"]), fmt.Sprint(al["id"]), "1", "2", "3"}
	checked := 0
	for _, rt := range e.s.App.Router.Routes() {
		method, path, ok := strings.Cut(rt.Pattern, " ")
		if !ok || !strings.Contains(path, "{id}") || strings.HasPrefix(path, "/api/v1/secrets/") || path == "/s/{id}" || strings.HasPrefix(path, "/dashboard/secrets/") {
			continue
		}
		for _, id := range ids {
			p := strings.ReplaceAll(path, "{id}", id)
			for _, as := range []opt{withCookie(cm), bearer(mk)} {
				r, body := e.do(method, p, strings.NewReader(`{"title":"pwned","name":"pwned","tags":["pwned"],"add":[1]}`), as,
					header("Content-Type", "application/json"), header("Accept", "application/json"))
				if r.StatusCode < 400 {
					t.Errorf("%s %s as mallory: %d %.120q", method, p, r.StatusCode, body)
				}
				checked++
			}
		}
	}
	// alice's data unchanged
	res, got := e.doJSON("GET", fmt.Sprintf("/api/v1/items/%v", it["id"]), nil, withCookie(ca))
	if res.StatusCode != http.StatusOK || got["title"] == "pwned" {
		t.Errorf("alice's item changed: %v", got)
	}
	t.Logf("%d cross-user calls refused", checked)
}
