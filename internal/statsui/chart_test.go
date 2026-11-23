package statsui

import (
	"strings"
	"testing"
	"time"
)

func TestNiceMax(t *testing.T) {
	for _, tc := range []struct{ in, top, step int64 }{
		{0, 4, 1}, {3, 4, 1}, {4, 4, 1}, {5, 8, 2}, {13, 20, 5}, {40, 40, 10}, {41, 80, 20}, {90, 100, 25}, {101, 200, 50}, {999, 1000, 250},
	} {
		if top, step := niceMax(tc.in); top != tc.top || step != tc.step {
			t.Errorf("niceMax(%d) = %d, %d; want %d, %d", tc.in, top, step, tc.top, tc.step)
		}
	}
}

func TestLineChart(t *testing.T) {
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	days := []time.Time{day, day.AddDate(0, 0, 1), day.AddDate(0, 0, 2)}
	c := lineChart(days, []int64{0, 2, 1}, "views")
	if !c.HasPoints || len(c.Grid) != 5 || c.Grid[0].Label != "0" || c.Grid[4].Label != "4" {
		t.Fatalf("grid %+v", c.Grid)
	}
	if !strings.HasPrefix(c.Line, "M36.0 190.0 L494.0 100.0 L952.0 145.0") || !strings.HasSuffix(c.Area, " L952.0 190.0 L36.0 190.0 Z") {
		t.Fatalf("line %q area %q", c.Line, c.Area)
	}
	if len(c.XLabels) != 3 || c.XLabels[0].Label != "Nov 20" || c.XLabels[0].Anchor != "start" || c.XLabels[2].Anchor != "end" || c.XLabels[1].Anchor != "middle" {
		t.Fatalf("labels %+v", c.XLabels)
	}
	if c.EndX != "952.0" || c.EndY != "145.0" {
		t.Fatalf("end %s %s", c.EndX, c.EndY)
	}
	if n := c.Narrow; n == nil || n.ViewBox != "0 0 360 220" || n.GridX2 != 352 || n.EndX != "352.0" || len(n.XLabels) != 3 {
		t.Fatalf("narrow %+v", n)
	}
	if empty := lineChart(nil, nil, ""); empty.HasPoints || empty.Line != "" {
		t.Fatalf("empty %+v", empty)
	}
	if s := sparkline([]int64{1, 1}); s.Line != "M0.0 42.0 L200.0 42.0" {
		t.Fatalf("flat spark %q", s.Line)
	}
}

func TestCSVCell(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@A1": "'@A1", "\tx": "'\tx", "\rx": "'\rx",
		"": "", "Firefox": "Firefox", "203.0.113.4": "203.0.113.4", "a=b": "a=b",
	} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q) = %q, want %q", in, got, want)
		}
	}
}
