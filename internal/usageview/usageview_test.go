package usageview

import (
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/stats"
)

func TestNewSpark(t *testing.T) {
	if s := NewSpark(nil); s.Line != "" || s.Area != "" {
		t.Errorf("empty: %+v", s)
	}
	if s := NewSpark([]int64{1, 1}); s.Line != "M0.0 42.0 L200.0 42.0" {
		t.Errorf("flat: %q", s.Line)
	}
	s := NewSpark([]int64{0, 10})
	if s.Line != "M0.0 42.0 L200.0 6.0" || s.Area != "M0.0 42.0 L200.0 6.0 L200 48 L0 48 Z" {
		t.Errorf("rising: %+v", s)
	}
	if m := NewMiniSpark([]int64{0, 5}); m.Line != "M0.0 25.0 L96.0 3.0" || !strings.HasSuffix(m.Area, " L96 28 L0 28 Z") {
		t.Errorf("mini: %+v", m)
	}
}

func TestNewStorage(t *testing.T) {
	k := stats.StorageKinds{Images: 400_000_000, Video: 500_000_000, Audio: 60_000_000, Other: 240_000_000}
	for _, tc := range []struct {
		name        string
		used, quota int64
		kinds       stats.StorageKinds
		limited     bool
		pct, free   string
		widths      []string
	}{
		{"under a quota", 1_200_000_000, 10_000_000_000, k, true, "12%", "8.8 GB", []string{"4.0", "5.0", "0.6", "2.4"}},
		{"no quota fills the bar", 1_200_000_000, 0, k, false, "", "", []string{"33.3", "41.7", "5.0", "20.0"}},
		{"over the quota", 2_000_000_000, 1_000_000_000, stats.StorageKinds{Images: 1_000_000_000, Other: 1_000_000_000}, true, "200%", "0 B", []string{"50.0", "50.0"}},
		{"nothing stored", 0, 0, stats.StorageKinds{}, false, "", "", nil},
		{"nothing stored under a quota", 0, 5_000_000_000, stats.StorageKinds{}, true, "0%", "5 GB", nil},
	} {
		v := NewStorage(tc.used, tc.quota, tc.kinds)
		var w []string
		for _, s := range v.Segments {
			w = append(w, s.W)
		}
		if v.Limited != tc.limited || v.Pct != tc.pct || v.Free != tc.free || strings.Join(w, ",") != strings.Join(tc.widths, ",") {
			t.Errorf("%s: %+v", tc.name, v)
		}
	}
	v := NewStorage(1_200_000_000, 10_000_000_000, k)
	if v.Aria != "1.2 GB of 10 GB used: images 400 MB, video 500 MB, audio 60 MB, other 240 MB" || v.Segments[0].Key != "k0" || v.Segments[3].Color != "var(--chart-3)" {
		t.Errorf("aria/segments: %q %+v", v.Aria, v.Segments)
	}
	// Zero kinds are left out but keep their key, so colors stay with their kind.
	v = NewStorage(100, 0, stats.StorageKinds{Images: 60, Other: 40})
	if len(v.Segments) != 2 || v.Segments[1].Key != "k3" || v.Segments[1].Label != "other" {
		t.Errorf("kinds with none: %+v", v.Segments)
	}
}

func TestNewKindRows(t *testing.T) {
	rows := NewKindRows([]stats.Count{{Key: "image", N: 38}, {Key: "paste", N: 31}, {Key: "audio", N: 3}, {Key: "album", N: 0}})
	if len(rows) != 3 || rows[0].Pct != 100 || rows[1].Pct != 82 || rows[2].Pct != 8 || rows[2].Label != "audio" {
		t.Errorf("rows %+v", rows)
	}
	if rows := NewKindRows(nil); len(rows) != 0 {
		t.Errorf("none: %+v", rows)
	}
	if rows := NewKindRows([]stats.Count{{Key: "link", N: 1}, {Key: "image", N: 1000}}); rows[0].Pct != 1 || rows[1].Pct != 100 {
		t.Errorf("a small count still shows: %+v", rows)
	}
}
