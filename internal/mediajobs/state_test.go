package mediajobs_test

import (
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/mediajobs"
)

// Item states derive from the thumbnail and the newest media job, in one
// lookup for a whole page. The queue is not running here, so the job rows stay
// as the test sets them.
func TestMediaStateDerivation(t *testing.T) {
	e := newEnv(t)
	vid := id(e.put("clip.mp4", clip(t)))
	paste := id(e.put("notes.txt", []byte("hello")))
	set := func(state string) {
		t.Helper()
		if _, err := e.s.App.DB.W.Exec(`UPDATE jobs SET state = ? WHERE kind = ?`, state, mediajobs.JobKind); err != nil {
			t.Fatal(err)
		}
	}
	state := func(ids ...int64) map[int64]items.MediaState {
		t.Helper()
		var list []*items.Item
		for _, i := range ids {
			list = append(list, e.item(i))
		}
		return e.s.App.Items.MediaStates(e.ctx, list)
	}

	for _, tc := range []struct {
		job  string
		want items.MediaState
	}{
		{"queued", items.MediaPending}, {"running", items.MediaPending}, // a retry in backoff is queued
		{"failed", items.MediaFailed}, {"done", items.MediaNone},
	} {
		set(tc.job)
		got := state(vid, paste)
		if got[vid] != tc.want || got[paste] != items.MediaNone {
			t.Errorf("job %s: video %q, text %q; want %q and none", tc.job, got[vid], got[paste], tc.want)
		}
	}

	// A thumbnail is ready, whatever the job says (a failed teaser after a
	// good poster leaves a failed job).
	set("failed")
	if _, err := e.s.App.DB.W.Exec(`UPDATE items SET thumb_key = ? WHERE id = ?`, strings.Repeat("b", 32), vid); err != nil {
		t.Fatal(err)
	}
	if got := state(vid)[vid]; got != items.MediaReady {
		t.Errorf("poster and a failed job: %q, want ready", got)
	}

	// Audio never gets a preview, so it never shows one being made.
	if _, err := e.s.App.DB.W.Exec(`UPDATE items SET thumb_key = NULL, mime = 'audio/mpeg' WHERE id = ?`, vid); err != nil {
		t.Fatal(err)
	}
	set("queued")
	if got := state(vid)[vid]; got != items.MediaNone {
		t.Errorf("audio with a queued job: %q, want none", got)
	}
}
