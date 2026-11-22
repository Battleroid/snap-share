package albums_test

import (
	"strconv"
	"strings"
	"testing"
)

// A gist can be an album member: an icon tile on the album page, its files
// in the album's API JSON.
func TestGistMember(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1)
	code, g := e.json("POST", "/api/v1/gists", map[string]any{"title": "Snippets", "files": []map[string]any{
		{"name": "a.go", "content": "package a\n"}, {"name": "b.sh", "content": "echo b\n"},
	}}, as("ann"))
	if code != 201 {
		t.Fatalf("gist: %d %v", code, g)
	}
	code, al := e.json("POST", "/api/v1/albums", map[string]any{"title": "Mixed", "items": []int64{id(g)}}, as("ann"))
	if code != 201 {
		t.Fatalf("album: %d %v", code, al)
	}
	_, page := e.do("GET", "/"+str(al, "slug"), nil)
	if !strings.Contains(page, "pb-icon--terminal") || !strings.Contains(page, "Snippets") || !strings.Contains(page, "gist · ") {
		t.Errorf("album page lacks the gist tile:\n%s", page)
	}
	_, got := e.json("GET", "/api/v1/albums/"+strconv.FormatInt(id(al), 10), nil, as("ann"))
	members, _ := got["items"].([]any)
	if len(members) != 1 {
		t.Fatalf("members: %v", got)
	}
	if fl, _ := members[0].(map[string]any)["files"].([]any); len(fl) != 2 {
		t.Errorf("member files: %v", members[0])
	}
}
