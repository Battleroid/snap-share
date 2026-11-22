package settings

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Battleroid/snap-share/internal/db"
)

var (
	testMode  = Define("test.mode", "invite", OneOf("closed", "invite", "open"))
	testLimit = Define("test.limit", int64(10), Range[int64](1, 100))
)

func TestGetSetReload(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if testMode.Get(s) != "invite" || testLimit.Get(s) != 10 {
		t.Fatal("defaults")
	}
	if err := testMode.Set(ctx, s, "nope"); err == nil {
		t.Fatal("invalid value accepted")
	}
	if err := testMode.Set(ctx, s, "open"); err != nil {
		t.Fatal(err)
	}
	if err := testLimit.SetJSON(ctx, s, []byte("42")); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if testMode.Get(s2) != "open" || testLimit.Get(s2) != 42 {
		t.Fatalf("persisted: %q %d", testMode.Get(s2), testLimit.Get(s2))
	}
	// A corrupt stored value falls back to the default.
	if _, err := d.W.Exec(`UPDATE settings SET value = '"zzz"' WHERE key = 'test.mode'`); err != nil {
		t.Fatal(err)
	}
	if err := s2.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if testMode.Get(s2) != "invite" {
		t.Fatal("invalid stored value should yield default")
	}
	if _, ok := Lookup("test.limit"); !ok {
		t.Fatal("lookup")
	}
}

func TestDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	Define("test.mode", "x", nil)
}
