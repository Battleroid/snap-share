package dashboard_test

import (
	"strings"
	"testing"
)

// Add-to mode (?add_to=<album id>): the bulk bar's primary action reads
// "Add to <album>", the mode survives the filter form, the pager and the
// pushed address, and only the signed-in user's own album counts.
func TestDashboardAddToMode(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	png := e.put("ann", "sunset.png", pngData)
	trip := e.album("ann", "Trip")
	other := e.album("bob", "Bobs")
	tripID, otherID := itoa(id(trip)), itoa(id(other))

	_, page := e.do("GET", "/dashboard?add_to="+tripID, nil, as("ann"))
	if !containsAll(page, `data-add-to="`+tripID+`"`, `data-add-to-name="Trip"`, "Add to Trip", `name="add_to" value="`+tripID+`"`, "data-bulk-matching") {
		t.Errorf("add-to page without the primary action or the hidden field")
	}
	// The mode is in the pushed address of a swap (the pager takes the same value).
	res, frag := e.do("GET", "/dashboard?add_to="+tripID+"&q=sunset", nil, as("ann"), header("HX-Request", "true"), header("HX-Target", "dash-results"))
	if got := res.Header.Get("HX-Push-Url"); got != "/dashboard?add_to="+tripID+"&q=sunset" {
		t.Errorf("HX-Push-Url %q", got)
	}
	if !strings.Contains(frag, "Add to Trip") || !strings.Contains(frag, "sunset.png") {
		t.Error("fragment lost the mode")
	}

	// Another user's album, an item that is not an album, and junk: the plain list.
	for _, q := range []string{otherID, itoa(id(png)), "999999", "abc", "-3"} {
		_, p := e.do("GET", "/dashboard?add_to="+q, nil, as("ann"))
		if strings.Contains(p, "data-add-to=") || strings.Contains(p, `name="add_to"`) || strings.Contains(p, "Bobs") {
			t.Errorf("add_to=%s was honoured", q)
		}
	}

	// Without the mode there is no primary action; the bar still has the kit's parts.
	_, plain := e.do("GET", "/dashboard", nil, as("ann"))
	if strings.Contains(plain, "data-add-to=") || !containsAll(plain, `data-bulk-total="`, "data-bulk-clear", `id="bulk-menu"`) {
		t.Error("plain dashboard bar")
	}

	// The Albums view offers New album, in the head and when there are none.
	_, albums := e.do("GET", "/dashboard?type=album", nil, as("ann"))
	if !containsAll(albums, `data-head-action="albums"`, `data-dialog-open="album-new"`, `id="album-new"`) {
		t.Error("albums view without New album")
	}
	e.user("cy", 3, true)
	_, empty := e.do("GET", "/dashboard?type=album", nil, as("cy"))
	if !containsAll(empty, "No albums yet", `data-dialog-open="album-new"`) || strings.Contains(empty, "Go to Items") {
		t.Error("empty albums state does not lead to New album")
	}
}

// A browser session can make an empty album: the New album dialog's request.
func TestNewAlbumFromSession(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	res, got := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "Fresh"}, as("ann"))
	if res.StatusCode != 201 || str(got, "page_url") == "" || num(got, "count") != 0 {
		t.Fatalf("empty album: %d %v", res.StatusCode, got)
	}
	e.user("un", 4, false)
	if res, _ := e.doJSON("POST", "/api/v1/albums", map[string]any{"title": "x"}, as("un")); res.StatusCode != 403 {
		t.Errorf("unverified: %d", res.StatusCode)
	}
}
