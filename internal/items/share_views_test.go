package items_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Stats are shown only to the owner (docs/stats.md): a visitor's share page
// carries no view count; the owner's has it, linked to the item's stats.
func TestShareViewsOwnerOnly(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	file := e.put("ann", "photo.png", string(pixelPNG), "")
	gist := e.gist("ann", gf{"files": []gf{{"name": "a.go", "content": "package a\n"}, {"name": "b.txt", "content": "b\n"}}})
	for _, it := range []map[string]any{file, gist} {
		page := "/" + str(it, "slug")
		link := fmt.Sprintf(`href="/dashboard/items/%d">`, num(it, "id"))
		for who, o := range map[string]reqOpt{"anonymous": func(*http.Request) {}, "another user": as("bob")} {
			res, body := e.do("GET", page, nil, o)
			if res.StatusCode != 200 || !strings.Contains(body, "<dt>uploaded</dt>") {
				t.Fatalf("%s %s: %d, no details", who, page, res.StatusCode)
			}
			if strings.Contains(body, "<dt>views</dt>") || strings.Contains(body, "/stats") {
				t.Errorf("%s %s: the page shows a view count or a stats link", who, page)
			}
		}
		_, body := e.do("GET", page, nil, as("ann"))
		if !strings.Contains(body, "<dt>views</dt>") || !strings.Contains(body, link+"0</a>") {
			t.Errorf("owner %s: no view count linked to the stats", page)
		}
	}
}
