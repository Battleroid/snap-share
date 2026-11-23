package statsui

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/stats"
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

func TestNewChart(t *testing.T) {
	day := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	daily := []stats.Day{
		{Day: day, Views: 0, Uniques: 0},
		{Day: day.AddDate(0, 0, 1), Views: 2, Uniques: 1},
		// A day that arrives in another zone still lands on its own UTC noon.
		{Day: day.AddDate(0, 0, 2).In(time.FixedZone("x", 13*3600)), Views: 1, Uniques: 1},
	}
	c := newChart(daily, "views", true)
	if c.Label != "views" || c.Data.XFormat != "date" || c.Data.Unit != "/day" || len(c.Data.Series) != 2 {
		t.Fatalf("chart %+v", c)
	}
	noon := func(d int) int64 { return time.Date(2026, 11, 20+d, 12, 0, 0, 0, time.UTC).UnixMilli() }
	views, visitors := c.Data.Series[0], c.Data.Series[1]
	if views.Name != "Views" || visitors.Name != "Visitors" || views.C == visitors.C {
		t.Fatalf("series %+v %+v", views, visitors)
	}
	if want := [][2]int64{{noon(0), 0}, {noon(1), 2}, {noon(2), 1}}; !reflect.DeepEqual(views.Data, want) {
		t.Fatalf("views %v, want %v", views.Data, want)
	}
	if want := [][2]int64{{noon(0), 0}, {noon(1), 1}, {noon(2), 1}}; !reflect.DeepEqual(visitors.Data, want) {
		t.Fatalf("visitors %v, want %v", visitors.Data, want)
	}
	// The axis tops at 4 for a peak of 2, so its four steps are whole numbers (the kit alone would use 0.5).
	if c.Data.Max != 4 {
		t.Fatalf("max %d", c.Data.Max)
	}
	// Your stats has no uniques to show (they cannot be summed over items).
	if c := newChart(daily, "", false); len(c.Data.Series) != 1 || c.Data.Series[0].Name != "Views" {
		t.Fatalf("views only: %+v", c.Data.Series)
	}
	// No days still marshals to the arrays the kit expects.
	b, err := json.Marshal(newChart(nil, "", true).Data)
	if err != nil || !strings.Contains(string(b), `"data":[]`) || strings.Contains(string(b), "null") {
		t.Fatalf("empty %s %v", b, err)
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
