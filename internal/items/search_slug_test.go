package items_test

import (
	"net/url"
	"strings"
	"testing"
)

// TestSearchFindsALinkName: a query that is a whole link name finds that
// item, ignoring case, for its owner only; part of a name finds nothing, as
// slugs are not in the text index.
func TestSearchFindsALinkName(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	mine := str(e.put("ann", "notes.txt", "nothing to see", ""), "slug")
	theirs := str(e.put("bob", "notes.txt", "nothing to see", ""), "slug")
	search := func(q string) int64 {
		t.Helper()
		_, list := e.doJSON("GET", "/api/v1/items?q="+url.QueryEscape(q), nil, as("ann"))
		return num(list, "total")
	}
	for q, want := range map[string]int64{
		mine: 1, strings.ToUpper(mine): 1, " " + mine + " ": 1, mine[:5]: 0, theirs: 0,
		"notes": 1, "notes " + mine: 0,
	} {
		if n := search(q); n != want {
			t.Errorf("search %q: %d, want %d", q, n, want)
		}
	}
}
