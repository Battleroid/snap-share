package dashboard_test

import (
	"strings"
	"testing"
)

// mediaEnv has ann (1) and bob (2), and ann's video: the upload queues its
// media job (the queue is not running in tests, so it stays as set here).
func mediaEnv(t *testing.T) (e *env, vid int64, slug string) {
	t.Helper()
	e = newEnv(t)
	e.user("ann", 1, true)
	e.user("bob", 2, true)
	if !e.s.App.Media.FFmpeg() {
		t.Fatal("ffmpeg is not available; run the tests with `make test-go`")
	}
	j := e.put("ann", "clip.mp4", mp4Data)
	return e, id(j), str(j, "slug")
}

// setJob puts the item's media job into state.
func (e *env) setJob(vid int64, state string) {
	e.t.Helper()
	e.exec(`UPDATE jobs SET state = ? WHERE kind = 'media.process' AND json_extract(payload, '$.id') = ?`, state, vid)
}

func (e *env) fragment(user string, path string) (int, string) {
	e.t.Helper()
	res, body := e.do("GET", path, nil, as(user), header("HX-Request", "true"))
	return res.StatusCode, body
}

func TestMediaStateOnCreate(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	j := e.put("ann", "clip.mp4", mp4Data)
	if got := str(j, "media_state"); got != "pending" {
		t.Errorf("creation response media_state = %q", got)
	}
	for name, m := range map[string]map[string]any{
		"paste": e.paste("ann", "n.txt", "hi"), "png": e.put("ann", "a.png", pngData), "flac": e.put("ann", "a.flac", flacData),
	} {
		if got := str(m, "media_state"); got != "" {
			t.Errorf("%s: media_state = %q, want none", name, got)
		}
	}
}

func TestMediaFragmentFollowsTheJob(t *testing.T) {
	e, vid, _ := mediaEnv(t)
	url := "/dashboard/items/" + itoa(vid) + "/media?as="

	// Queued and running: 204, so htmx swaps nothing and the shimmer keeps going.
	for _, state := range []string{"queued", "running"} {
		e.setJob(vid, state)
		for _, as := range []string{"lead", "tile", "note"} {
			if code, body := e.fragment("ann", url+as); code != 204 || body != "" {
				t.Errorf("%s as=%s: %d %q", state, as, code, body)
			}
		}
	}

	// Failed: the icon and "no preview", with nothing left to poll.
	e.setJob(vid, "failed")
	for _, as := range []string{"lead", "tile", "note"} {
		code, body := e.fragment("ann", url+as)
		if code != 200 || !strings.Contains(body, "no preview") || strings.Contains(body, "hx-") || strings.Contains(body, "processing") {
			t.Errorf("failed as=%s: %d %q", as, code, body)
		}
	}

	// Done without a poster: the type icon, and the note goes away.
	e.setJob(vid, "done")
	code, body := e.fragment("ann", url+"lead")
	if code != 200 || !strings.Contains(body, "pb-item__lead") || strings.Contains(body, "hx-") || strings.Contains(body, "no preview") {
		t.Errorf("done lead: %d %q", code, body)
	}
	if code, body := e.fragment("ann", url+"note"); code != 200 || strings.TrimSpace(body) != "" {
		t.Errorf("done note: %d %q", code, body)
	}

	// A poster makes it ready whatever the job says: a failed teaser step
	// leaves a failed job next to a good poster.
	e.setJob(vid, "failed")
	e.exec(`UPDATE items SET thumb_key = ? WHERE id = ?`, strings.Repeat("a", 32), vid)
	code, body = e.fragment("ann", url+"lead")
	if code != 200 || !strings.Contains(body, "<img") || strings.Contains(body, "no preview") {
		t.Errorf("ready lead: %d %q", code, body)
	}
	if code, body = e.fragment("ann", url+"tile"); code != 200 || !strings.HasPrefix(body, "<img") {
		t.Errorf("ready tile: %d %q", code, body)
	}
}

// A newer job wins over an older failed one (a re-run after a failure).
func TestMediaStateNewestJobWins(t *testing.T) {
	e, vid, _ := mediaEnv(t)
	e.setJob(vid, "failed")
	e.exec(`INSERT INTO jobs (kind, payload, state, run_after, created_at, updated_at) VALUES ('media.process', ?, 'done', 1, 1, 1)`, `{"id":`+itoa(vid)+`}`)
	code, body := e.fragment("ann", "/dashboard/items/"+itoa(vid)+"/media?as=lead")
	if code != 200 || strings.Contains(body, "no preview") {
		t.Errorf("done after failed: %d %q", code, body)
	}
}

func TestMediaFragmentIsOwnerOnly(t *testing.T) {
	e, vid, _ := mediaEnv(t)
	missing := "/dashboard/items/999999/media?as=lead"
	_, want := e.fragment("bob", missing)
	for _, state := range []string{"queued", "failed", "done"} {
		e.setJob(vid, state)
		// Another user, a missing item, a bad variant and a bad id all
		// answer the same 404: no state, and no way to tell who owns what.
		for _, as := range []string{"lead", "tile", "note"} {
			p := "/dashboard/items/" + itoa(vid) + "/media?as=" + as
			if code, body := e.fragment("bob", p); code != 404 || body != want {
				t.Errorf("bob %s (%s): %d %q", p, state, code, body)
			}
		}
		if code, body := e.fragment("ann", "/dashboard/items/"+itoa(vid)+"/media?as=bogus"); code != 404 || body != want {
			t.Errorf("bad variant (%s): %d", state, code)
		}
		if code, _ := e.fragment("ann", "/dashboard/items/x/media?as=lead"); code != 404 {
			t.Errorf("bad id (%s): %d", state, code)
		}
		if code, _ := e.fragment("ann", missing); code != 404 {
			t.Errorf("missing (%s): %d", state, code)
		}
	}

	// Not signed in, and an API key (even the owner's), get nothing.
	p := "/dashboard/items/" + itoa(vid) + "/media?as=lead"
	if res, _ := e.do("GET", p, nil); res.StatusCode == 200 || res.StatusCode == 204 {
		t.Errorf("anonymous: %d", res.StatusCode)
	}
	if res, body := e.do("GET", p, nil, asKey("ann-key")); res.StatusCode == 200 || res.StatusCode == 204 || strings.Contains(body, "processing") {
		t.Errorf("api key: %d", res.StatusCode)
	}
}

func TestMediaMarkerOnOwnerPages(t *testing.T) {
	e, vid, slug := mediaEnv(t)
	hx := `hx-get="/dashboard/items/` + itoa(vid) + `/media?as=`

	// Dashboard, table and grid; the composer's Recent list.
	_, table := e.do("GET", "/dashboard", nil, as("ann"))
	if !containsAll(table, hx+`lead"`, `hx-trigger="every 5s"`, "processing") {
		t.Errorf("table row lacks the marker")
	}
	_, grid := e.do("GET", "/dashboard?view=grid", nil, as("ann"))
	if !containsAll(grid, hx+`tile"`, `hx-trigger="every 5s"`, "processing") {
		t.Errorf("grid tile lacks the marker")
	}
	_, home := e.do("GET", "/", nil, as("ann"))
	if !containsAll(home, hx+`lead"`, "processing") {
		t.Errorf("Recent row lacks the marker")
	}

	// The owner's share page and album page.
	_, share := e.do("GET", "/"+slug, nil, as("ann"))
	if !containsAll(share, hx+`note"`, "processing") {
		t.Errorf("share page lacks the marker")
	}
	alb := e.album("ann", "reel", vid)
	_, album := e.do("GET", "/"+str(alb, "slug"), nil, as("ann"))
	if !containsAll(album, hx+`tile"`, "processing") {
		t.Errorf("album tile lacks the marker")
	}

	// Visitors, other users and the owner's API key see today's pages.
	for name, opts := range map[string][]reqOpt{"visitor": nil, "bob": {as("bob")}, "key": {asKey("ann-key")}} {
		for _, p := range []string{"/" + slug, "/" + str(alb, "slug")} {
			_, body := e.do("GET", p, nil, opts...)
			if containsAny(body, "processing", "no preview", "/media?as=") {
				t.Errorf("%s sees a marker on %s", name, p)
			}
		}
	}
	_, bobs := e.do("GET", "/dashboard", nil, as("bob"))
	if containsAny(bobs, "processing", "/media?as=") {
		t.Error("bob's dashboard shows ann's state")
	}

	// Failed: "no preview" in place of the marker, on every page.
	e.setJob(vid, "failed")
	for name, p := range map[string]string{"table": "/dashboard", "grid": "/dashboard?view=grid", "home": "/", "share": "/" + slug, "album": "/" + str(alb, "slug")} {
		_, body := e.do("GET", p, nil, as("ann"))
		if !strings.Contains(body, "no preview") || strings.Contains(body, "/media?as=") {
			t.Errorf("%s after failure: want \"no preview\" and no polling", name)
		}
	}
}
