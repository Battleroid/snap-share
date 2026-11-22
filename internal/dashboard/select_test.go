package dashboard_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// "Select all N matching": the server re-runs the filter, bounded by
// created_before, and refuses when the count is not what the client saw.
func TestSelectAllMatching(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	for i := range 5 {
		e.put("ann", fmt.Sprintf("pic-%d.png", i), pngData)
	}
	e.put("ann", "notes.txt", "text")
	e.put("bob", "bob.png", pngData)
	bound := time.Now().UTC().Format(time.RFC3339Nano)
	time.Sleep(5 * time.Millisecond)
	late := e.put("ann", "late.png", pngData) // after the bound: never swept in

	all := func(filter string, expected int) map[string]any {
		return map[string]any{"filter": filter, "expected": expected, "created_before": bound,
			"action": "set_visibility", "params": map[string]any{"visibility": "private"}}
	}
	privates := func(user string) int { return len(e.names(user, "visibility=private")) }

	// A count that no longer matches changes nothing.
	if res, m := e.bulk("ann", all("type=image", 6)); res.StatusCode != 409 || !strings.Contains(str(m, "error"), "5 items match now, not 6") {
		t.Errorf("mismatch: %d %v", res.StatusCode, m)
	}
	if privates("ann") != 0 {
		t.Error("partial effect on mismatch")
	}
	res, m := e.bulk("ann", all("type=image&view=grid&page=2", 5))
	if res.StatusCode != 200 || num(m, "count") != 5 {
		t.Fatalf("select all: %d %v", res.StatusCode, m)
	}
	if privates("ann") != 5 || str(e.item("ann", id(late)), "visibility") != "unlisted" {
		t.Errorf("privates %d; late %s", privates("ann"), str(e.item("ann", id(late)), "visibility"))
	}
	// Another user's filter only ever covers their own items.
	if res, _ := e.bulk("bob", all("type=image", 5)); res.StatusCode != 409 {
		t.Errorf("bob: %d", res.StatusCode)
	}
	if res, _ := e.bulk("bob", all("type=image", 1)); res.StatusCode != 200 || privates("bob") != 1 || privates("ann") != 5 {
		t.Errorf("bob own: %d", res.StatusCode)
	}
	// Malformed selections.
	for _, body := range []map[string]any{
		{"filter": "type=image", "expected": 5, "action": "delete"},                                             // no bound
		{"filter": "type=image", "created_before": bound, "action": "delete"},                                   // no count
		{"filter": "type=image", "expected": 5, "created_before": "soon", "action": "delete"},                   // bad bound
		{"filter": "type=exe", "expected": 5, "created_before": bound, "action": "delete"},                      // bad filter
		{"filter": "type=image", "expected": 5, "created_before": bound, "ids": []int64{1}, "action": "delete"}, // both
		{"ids": []int64{1}, "expected": 1, "action": "delete"},
	} {
		if res, m := e.bulk("ann", body); res.StatusCode != 400 {
			t.Errorf("%v: %d %v", body, res.StatusCode, m)
		}
	}
	if res, _ := e.bulk("ann", map[string]any{"filter": "type=video", "expected": 0, "created_before": bound, "action": "delete"}); res.StatusCode != 400 {
		t.Errorf("empty match: %d", res.StatusCode)
	}
	// Deleting everything matching.
	if res, m := e.bulk("ann", map[string]any{"filter": "", "expected": 6, "created_before": bound, "action": "delete"}); res.StatusCode != 200 || num(m, "count") != 6 {
		t.Errorf("delete all: %d %v", res.StatusCode, m)
	}
	if got := e.names("ann", ""); len(got) != 1 || got[0] != "late.png" {
		t.Errorf("left: %v", got)
	}
}
