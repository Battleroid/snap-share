package dashboard_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func itemTagsPath(id int64) string { return fmt.Sprintf("/api/v1/items/%d/tags", id) }

func tagList(m map[string]any) []string {
	var out []string
	for _, x := range m["tags"].([]any) {
		switch v := x.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			out = append(out, fmt.Sprintf("%s:%v", v["name"], v["count"]))
		}
	}
	return out
}

func TestTags(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	it := e.put("ann", "photo.png", pngData)
	other := e.put("ann", "other.png", pngData)

	res, m := e.doJSON("POST", "/api/v1/tags", map[string]any{"name": "  Travel "}, as("ann"))
	if res.StatusCode != 201 || str(m, "name") != "Travel" {
		t.Fatalf("create: %d %v", res.StatusCode, m)
	}
	travel := id(m)
	if res, _ := e.doJSON("POST", "/api/v1/tags", map[string]any{"name": "travel"}, as("ann")); res.StatusCode != 409 {
		t.Errorf("duplicate (case): %d", res.StatusCode)
	}
	for _, bad := range []string{"", strings.Repeat("x", 41), "a,b", "\x00"} {
		if res, _ := e.doJSON("POST", "/api/v1/tags", map[string]any{"name": bad}, as("ann")); res.StatusCode != 400 {
			t.Errorf("name %q: %d", bad, res.StatusCode)
		}
	}
	// Names are per owner.
	if res, _ := e.doJSON("POST", "/api/v1/tags", map[string]any{"name": "Travel"}, as("bob")); res.StatusCode != 201 {
		t.Errorf("bob's own travel: %d", res.StatusCode)
	}

	// Set an item's tags: existing names are reused whatever their case.
	res, m = e.doJSON("PUT", itemTagsPath(id(it)), map[string]any{"tags": []string{"travel", "Beach", "beach"}}, as("ann"))
	if res.StatusCode != 200 || !reflect.DeepEqual(tagList(m), []string{"Beach", "Travel"}) {
		t.Errorf("set: %d %v", res.StatusCode, m)
	}
	_, m = e.doJSON("GET", itemTagsPath(id(it)), nil, as("ann"))
	if !reflect.DeepEqual(tagList(m), []string{"Beach", "Travel"}) {
		t.Errorf("get: %v", m)
	}
	_, m = e.doJSON("GET", "/api/v1/tags", nil, as("ann"))
	if !reflect.DeepEqual(tagList(m), []string{"Beach:1", "Travel:1"}) {
		t.Errorf("list: %v", m)
	}

	// Search finds items by tag, and follows renames and deletes.
	if got := e.names("ann", "q=beach"); !reflect.DeepEqual(got, []string{"photo.png"}) {
		t.Errorf("search tag: %v", got)
	}
	var beach int64
	for _, x := range m["tags"].([]any) {
		if x.(map[string]any)["name"] == "Beach" {
			beach = id(x.(map[string]any))
		}
	}
	res, m = e.doJSON("PATCH", fmt.Sprintf("/api/v1/tags/%d", beach), map[string]any{"name": "Seaside"}, as("ann"))
	if res.StatusCode != 200 || str(m, "name") != "Seaside" || num(m, "count") != 1 {
		t.Errorf("rename: %d %v", res.StatusCode, m)
	}
	if got := e.names("ann", "q=beach"); len(got) != 0 {
		t.Errorf("old name still found: %v", got)
	}
	if got := e.names("ann", "q=seaside"); !reflect.DeepEqual(got, []string{"photo.png"}) {
		t.Errorf("new name: %v", got)
	}
	if got := e.names("ann", "tag=seaside"); !reflect.DeepEqual(got, []string{"photo.png"}) {
		t.Errorf("filter renamed: %v", got)
	}
	if res, _ := e.doJSON("PATCH", fmt.Sprintf("/api/v1/tags/%d", beach), map[string]any{"name": "TRAVEL"}, as("ann")); res.StatusCode != 409 {
		t.Errorf("rename onto existing: %d", res.StatusCode)
	}
	if res, _ := e.doJSON("DELETE", fmt.Sprintf("/api/v1/tags/%d", beach), nil, as("ann")); res.StatusCode != 204 {
		t.Errorf("delete: %d", res.StatusCode)
	}
	if got := e.names("ann", "q=seaside"); len(got) != 0 {
		t.Errorf("deleted tag still found: %v", got)
	}
	_, m = e.doJSON("GET", itemTagsPath(id(it)), nil, as("ann"))
	if !reflect.DeepEqual(tagList(m), []string{"Travel"}) {
		t.Errorf("after delete: %v", m)
	}
	// The other indexed fields survive tag changes.
	if got := e.names("ann", "q=photo.png"); !reflect.DeepEqual(got, []string{"photo.png"}) {
		t.Errorf("filename search: %v", got)
	}
	// Clearing.
	res, m = e.doJSON("PUT", itemTagsPath(id(other)), map[string]any{"tags": []string{}}, as("ann"))
	if res.StatusCode != 200 || len(m["tags"].([]any)) != 0 {
		t.Errorf("clear: %d %v", res.StatusCode, m)
	}

	// Limits.
	var many []string
	for i := range 21 {
		many = append(many, fmt.Sprintf("t%d", i))
	}
	if res, _ := e.doJSON("PUT", itemTagsPath(id(it)), map[string]any{"tags": many}, as("ann")); res.StatusCode != 400 {
		t.Errorf("21 tags: %d", res.StatusCode)
	}
	if res, _ := e.doJSON("PUT", itemTagsPath(id(it)), map[string]any{"tags": []string{strings.Repeat("y", 41)}}, as("ann")); res.StatusCode != 400 {
		t.Errorf("long tag: %d", res.StatusCode)
	}

	// Other users: 404 everywhere, nothing changes.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", itemTagsPath(id(it)), nil},
		{"PUT", itemTagsPath(id(it)), map[string]any{"tags": []string{"pwned"}}},
		{"PATCH", fmt.Sprintf("/api/v1/tags/%d", travel), map[string]any{"name": "pwned"}},
		{"DELETE", fmt.Sprintf("/api/v1/tags/%d", travel), nil},
	} {
		for _, who := range []string{"bob", "bob-key"} {
			if res, _ := e.doJSON(c.method, c.path, c.body, as(who)); res.StatusCode != 404 {
				t.Errorf("%s %s %s: %d", who, c.method, c.path, res.StatusCode)
			}
		}
	}
	_, m = e.doJSON("GET", "/api/v1/tags", nil, as("bob"))
	if !reflect.DeepEqual(tagList(m), []string{"Travel:0"}) {
		t.Errorf("bob's tags: %v", m)
	}
	_, m = e.doJSON("GET", itemTagsPath(id(it)), nil, as("ann"))
	if !reflect.DeepEqual(tagList(m), []string{"Travel"}) {
		t.Errorf("ann's tags changed: %v", m)
	}
	// Bob's tag filter never shows Ann's items.
	if got := e.names("bob", "tag=travel"); len(got) != 0 {
		t.Errorf("bob filter: %v", got)
	}
}
