package dashboard_test

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func ids(list ...map[string]any) []int64 {
	out := make([]int64, len(list))
	for i, m := range list {
		out[i] = id(m)
	}
	return out
}

func TestBulkActions(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	a := e.put("ann", "a.txt", "alpha")
	b := e.put("ann", "b.txt", "bravo")
	c := e.put("ann", "c.txt", "charlie")
	sel := ids(a, b)

	check := func(name string, body map[string]any, wantCount int) map[string]any {
		t.Helper()
		res, m := e.bulk("ann", body)
		if res.StatusCode != 200 || num(m, "count") != wantCount {
			t.Fatalf("%s: %d %v", name, res.StatusCode, m)
		}
		return m
	}

	// Expiry: set and clear; the untouched item keeps its own.
	m := check("set_expiry", map[string]any{"ids": sel, "action": "set_expiry", "params": map[string]any{"expires": "7d", "expiry_action": "private"}}, 2)
	for _, x := range []map[string]any{a, b} {
		it := e.item("ann", id(x))
		exp, err := time.Parse(time.RFC3339, str(it, "expires_at"))
		if err != nil || time.Until(exp) < 6*24*time.Hour || time.Until(exp) > 8*24*time.Hour || str(it, "expiry_action") != "private" {
			t.Errorf("expiry %v: %v", it["expires_at"], it["expiry_action"])
		}
	}
	if m["expires_at"] == nil || e.item("ann", id(c))["expires_at"] != nil {
		t.Errorf("expiry response %v / c changed", m)
	}
	check("never", map[string]any{"ids": sel, "action": "set_expiry", "params": map[string]any{"expires": "never"}}, 2)
	if e.item("ann", id(a))["expires_at"] != nil {
		t.Error("never")
	}
	if res, _ := e.bulk("ann", map[string]any{"ids": sel, "action": "set_expiry", "params": map[string]any{"expires": "soonish"}}); res.StatusCode != 400 {
		t.Errorf("bad expiry: %d", res.StatusCode)
	}

	// Visibility.
	check("set_visibility", map[string]any{"ids": sel, "action": "set_visibility", "params": map[string]any{"visibility": "private"}}, 2)
	if str(e.item("ann", id(a)), "visibility") != "private" || str(e.item("ann", id(c)), "visibility") != "unlisted" {
		t.Error("visibility")
	}
	if res, _ := e.do("GET", "/"+str(a, "slug"), nil); res.StatusCode != 404 {
		t.Errorf("private for others: %d", res.StatusCode)
	}
	if res, _ := e.bulk("ann", map[string]any{"ids": sel, "action": "set_visibility", "params": map[string]any{"visibility": "public"}}); res.StatusCode != 400 {
		t.Errorf("bad visibility: %d", res.StatusCode)
	}
	check("unlisted", map[string]any{"ids": sel, "action": "set_visibility", "params": map[string]any{"visibility": "unlisted"}}, 2)

	// Password: set (the share page asks for it), then clear.
	check("set_password", map[string]any{"ids": sel, "action": "set_password", "params": map[string]any{"password": "hunter2-long"}}, 2)
	if it := e.item("ann", id(b)); it["password"] != true {
		t.Error("password not set")
	}
	if _, page := e.do("GET", "/"+str(b, "slug"), nil); !strings.Contains(page, `name="password"`) {
		t.Error("no unlock page")
	}
	if res, _ := e.bulk("ann", map[string]any{"ids": sel, "action": "set_password", "params": map[string]any{"password": ""}}); res.StatusCode != 400 {
		t.Errorf("empty password: %d", res.StatusCode)
	}
	check("clear_password", map[string]any{"ids": sel, "action": "clear_password"}, 2)
	if it := e.item("ann", id(b)); it["password"] != false {
		t.Error("password not cleared")
	}

	// Tags: add, then remove one.
	check("add_tags", map[string]any{"ids": sel, "action": "add_tags", "params": map[string]any{"tags": []string{"Work", "q3"}}}, 2)
	if got := e.names("ann", "tag=work"); len(got) != 2 {
		t.Errorf("tagged: %v", got)
	}
	check("remove_tags", map[string]any{"ids": ids(a), "action": "remove_tags", "params": map[string]any{"tags": []string{"WORK"}}}, 1)
	if got := e.names("ann", "tag=work"); !reflect.DeepEqual(got, []string{"b.txt"}) {
		t.Errorf("after remove: %v", got)
	}
	if got := e.names("ann", "q=work"); !reflect.DeepEqual(got, []string{"b.txt"}) {
		t.Errorf("search after remove: %v", got)
	}

	// Albums: new from the selection, then add more (an album in the selection is skipped).
	m = check("new_album", map[string]any{"ids": sel, "action": "new_album", "params": map[string]any{"title": "Trip"}}, 2)
	album := m["album"].(map[string]any)
	if str(album, "url") == "" || str(album, "name") != "Trip" {
		t.Errorf("new album: %v", m)
	}
	albumID := id(album)
	m = check("add_to_album", map[string]any{"ids": []int64{id(c), id(a), albumID}, "action": "add_to_album", "params": map[string]any{"album_id": albumID}}, 1)
	if num(m, "skipped") != 2 {
		t.Errorf("skipped: %v", m)
	}
	_, al := e.doJSON("GET", fmt.Sprintf("/api/v1/albums/%d", albumID), nil, as("ann"))
	var order []string
	for _, x := range al["items"].([]any) {
		order = append(order, x.(map[string]any)["name"].(string))
	}
	if !reflect.DeepEqual(order, []string{"a.txt", "b.txt", "c.txt"}) {
		t.Errorf("album order: %v", order)
	}
	if res, _ := e.bulk("ann", map[string]any{"ids": sel, "action": "add_to_album", "params": map[string]any{"album_id": id(c)}}); res.StatusCode != 404 {
		t.Errorf("add to a non-album: %d", res.StatusCode)
	}

	// Parameters are strict.
	for _, body := range []map[string]any{
		{"ids": sel, "action": "set_visibility", "params": map[string]any{"visibility": "private", "extra": 1}},
		{"ids": sel, "action": "clear_password", "params": map[string]any{"password": "x"}},
		{"ids": sel, "action": "explode"},
		{"ids": []int64{}, "action": "delete"},
		{"action": "delete"},
	} {
		if res, _ := e.bulk("ann", body); res.StatusCode != 400 {
			t.Errorf("%v: %d", body, res.StatusCode)
		}
	}
	if res, _ := e.bulk("ann", map[string]any{"ids": make([]int64, 1001), "action": "delete"}); res.StatusCode != 400 {
		t.Errorf("too many ids: %d", res.StatusCode)
	}

	// Delete: items, files and usage go; the album loses them.
	used := func() (n int64) {
		_ = e.s.App.DB.R.QueryRow(`SELECT used_bytes FROM users WHERE id = 1`).Scan(&n)
		return n
	}
	before := used()
	var charged int64 // content and cached renders
	_ = e.s.App.DB.R.QueryRow(`SELECT sum(size + render_size) FROM items WHERE id IN (?, ?)`, id(a), id(b)).Scan(&charged)
	if charged < int64(len("alpha")+len("bravo")) {
		t.Fatalf("charged %d", charged)
	}
	blob := e.blobPath(id(a))
	check("delete", map[string]any{"ids": sel, "action": "delete"}, 2)
	for _, x := range []map[string]any{a, b} {
		if res, _ := e.doJSON("GET", fmt.Sprintf("/api/v1/items/%d", id(x)), nil, as("ann")); res.StatusCode != 404 {
			t.Errorf("deleted item: %d", res.StatusCode)
		}
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Errorf("blob kept: %v", err)
	}
	if used() != before-charged {
		t.Errorf("usage %d -> %d", before, used())
	}
	_, al = e.doJSON("GET", fmt.Sprintf("/api/v1/albums/%d", albumID), nil, as("ann"))
	if num(al, "count") != 1 {
		t.Errorf("album after delete: %v", al)
	}
}

// blobPath is the file holding item id's content.
func (e *env) blobPath(itemID int64) string {
	e.t.Helper()
	var key string
	if err := e.s.App.DB.R.QueryRow(`SELECT blob_key FROM items WHERE id = ?`, itemID).Scan(&key); err != nil {
		e.t.Fatal(err)
	}
	p, err := e.s.App.Files.Path(key)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

// Every action refuses another user's ids, and a mix of own and foreign ids
// changes nothing at all.
func TestBulkOwnership(t *testing.T) {
	e := newEnv(t, testutil.WithSMTP())
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	annItem := e.put("ann", "ann.txt", "ann's")
	bobItem := e.put("bob", "bob.txt", "bob's")
	bobAlbum := e.album("bob", "Bob's")
	mixed := []int64{id(bobItem), id(annItem)}
	actions := []map[string]any{
		{"action": "set_expiry", "params": map[string]any{"expires": "1d"}},
		{"action": "set_visibility", "params": map[string]any{"visibility": "private"}},
		{"action": "set_password", "params": map[string]any{"password": "pw-123456"}},
		{"action": "clear_password"},
		{"action": "add_to_album", "params": map[string]any{"album_id": id(bobAlbum)}},
		{"action": "new_album", "params": map[string]any{"title": "x"}},
		{"action": "add_tags", "params": map[string]any{"tags": []string{"t"}}},
		{"action": "remove_tags", "params": map[string]any{"tags": []string{"t"}}},
		{"action": "delete"},
		{"action": "download_zip"},
		{"action": "send_email", "params": map[string]any{"to": []string{"x@example.com"}}},
	}
	snapshot := func() string {
		var s string
		_ = e.s.App.DB.R.QueryRow(`SELECT group_concat(id || ':' || visibility || ':' || coalesce(expires_at, '') || ':' || coalesce(password_hash, ''), ',') FROM items`).Scan(&s)
		var tags, albumItems int
		_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM item_tags`).Scan(&tags)
		_ = e.s.App.DB.R.QueryRow(`SELECT count(*) FROM album_items`).Scan(&albumItems)
		return fmt.Sprintf("%s|%d|%d", s, tags, albumItems)
	}
	before := snapshot()
	for _, a := range actions {
		for _, sel := range [][]int64{{id(annItem)}, mixed} {
			body := map[string]any{"ids": sel}
			for k, v := range a {
				body[k] = v
			}
			for _, who := range []string{"bob", "bob-key"} {
				if res, m := e.bulk(who, body); res.StatusCode != 404 {
					t.Errorf("%s %s %v: %d %v", who, a["action"], sel, res.StatusCode, m)
				}
			}
		}
	}
	if after := snapshot(); after != before {
		t.Errorf("state changed:\n%s\n%s", before, after)
	}
	if len(e.s.Mail.Sent()) != 0 {
		t.Error("mail sent")
	}
	// Bob cannot add his own items to Ann's album either.
	annAlbum := e.album("ann", "Ann's")
	if res, _ := e.bulk("bob", map[string]any{"ids": []int64{id(bobItem)}, "action": "add_to_album", "params": map[string]any{"album_id": id(annAlbum)}}); res.StatusCode != 404 {
		t.Errorf("bob into ann's album: %d", res.StatusCode)
	}
}

// Each action needs the scope of the matching single-item endpoint.
func TestBulkScopes(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.s.Auth.Add("reader", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 7, Scopes: []auth.Scope{auth.ScopeItemsRead}})
	e.s.Auth.Add("writer", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 8, Scopes: []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite}})
	it := e.put("ann", "a.txt", "alpha")
	sel := []int64{id(it)}
	cases := []struct {
		who, action string
		params      map[string]any
		want        int
	}{
		{"reader", "set_expiry", map[string]any{"expires": "1d"}, 403},
		{"reader", "add_tags", map[string]any{"tags": []string{"x"}}, 403},
		{"reader", "download_zip", nil, 200},
		{"writer", "set_expiry", map[string]any{"expires": "1d"}, 200},
		{"writer", "delete", nil, 403},
		{"writer", "new_album", map[string]any{"title": "x"}, 403},
		{"writer", "add_to_album", map[string]any{"album_id": 1}, 403},
		{"ann-key", "delete", nil, 200},
	}
	for _, c := range cases {
		body := map[string]any{"ids": sel, "action": c.action}
		if c.params != nil {
			body["params"] = c.params
		}
		res, m := e.doJSON("POST", "/api/v1/items/bulk", body, asKey(c.who))
		if res.StatusCode != c.want {
			t.Errorf("%s %s: %d %v", c.who, c.action, res.StatusCode, m)
		}
	}
	if res, _ := e.doJSON("POST", "/api/v1/items/bulk", map[string]any{"ids": sel, "action": "delete"}); res.StatusCode != 401 {
		t.Errorf("anonymous: %d", res.StatusCode)
	}
}

// API key constraints apply to bulk changes exactly as to PATCH /api/v1/items/{id}.
func TestBulkKeyConstraints(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.s.Auth.Add("forced", auth.Principal{UserID: 1, EmailVerified: true, KeyID: 11, Scopes: allScopes,
		Key: &auth.KeyConstraints{Visibility: "private", MaxExpiry: 24 * time.Hour}})
	it := e.put("ann", "a.txt", "alpha")
	sel := []int64{id(it)}
	post := func(body map[string]any) (*resp, map[string]any) {
		return e.doJSON("POST", "/api/v1/items/bulk", body, asKey("forced"))
	}
	if res, m := post(map[string]any{"ids": sel, "action": "set_visibility", "params": map[string]any{"visibility": "unlisted"}}); res.StatusCode != 403 {
		t.Errorf("unlisted with a private-only key: %d %v", res.StatusCode, m)
	}
	if res, _ := post(map[string]any{"ids": sel, "action": "set_visibility", "params": map[string]any{"visibility": "private"}}); res.StatusCode != 200 {
		t.Errorf("private: %d", res.StatusCode)
	}
	for _, spec := range []string{"never", "30d"} {
		res, m := post(map[string]any{"ids": sel, "action": "set_expiry", "params": map[string]any{"expires": spec}})
		exp, err := time.Parse(time.RFC3339, str(e.item("ann", id(it)), "expires_at"))
		if res.StatusCode != 200 || err != nil || time.Until(exp) > 24*time.Hour || time.Until(exp) < 23*time.Hour {
			t.Errorf("%s clamped: %d %v %v", spec, res.StatusCode, m, exp)
		}
	}
	res, m := post(map[string]any{"ids": sel, "action": "new_album", "params": map[string]any{"title": "Forced", "visibility": "unlisted"}})
	if res.StatusCode != 200 {
		t.Fatalf("new album: %d %v", res.StatusCode, m)
	}
	al := e.item("ann", id(m["album"].(map[string]any)))
	if str(al, "visibility") != "private" || al["expires_at"] == nil {
		t.Errorf("album from a constrained key: %v %v", al["visibility"], al["expires_at"])
	}
}
