package statsui

import (
	"time"

	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/usageview"
)

// Chart is the data of the kit's time-series chart (figure.pb-ts[data-ts]):
// the stats.chart partial puts Data in the figure's JSON block and the kit
// draws it in the browser (crosshair tooltip per day, legend toggles, drag
// to zoom). Label is the figure's accessible description.
type Chart struct {
	Label string
	Data  TimeSeries
}

// TimeSeries is the JSON the kit's pbTimeSeries reads.
type TimeSeries struct {
	Series  []TSSeries `json:"series"`
	Unit    string     `json:"unit"`
	XFormat string     `json:"xFormat"`
	// Max is the top of the value axis, drawn in four steps. The kit would
	// pick steps like 0.3 for a peak of 1; counts need whole numbers.
	Max int64 `json:"max"`
}

// niceMax rounds a peak up to a 1, 2, 2.5 or 5 step (times a power of ten)
// so that four grid intervals are whole numbers and read well.
func niceMax(v int64) (top, step int64) {
	if v <= 4 {
		return 4, 1
	}
	raw := (v + 3) / 4
	mag := int64(1)
	for mag*10 <= raw {
		mag *= 10
	}
	for _, m := range []int64{10, 20, 25, 50, 100} { // tenths of mag
		if s := m * mag / 10; s >= raw {
			return 4 * s, s
		}
	}
	return 4 * raw, raw
}

// TSSeries is one line: a name, a colour token and [ms, value] points.
type TSSeries struct {
	Name string     `json:"name"`
	C    string     `json:"c"`
	Data [][2]int64 `json:"data"`
}

// dayUnit follows each value in the tooltip and legend ("12/day").
const dayUnit = "/day"

// newChart is views per day, and visitors (Uniques) when withVisitors: an
// item's days carry them, but stats.UserDaily leaves Uniques at zero because
// a person who opened two items is two uniques, so Your stats shows views
// only. A point sits at noon UTC, so the date the kit shows (xFormat "date",
// formatted in UTC) is the same in every time zone.
func newChart(daily []stats.Day, label string, withVisitors bool) Chart {
	views := TSSeries{Name: "Views", C: "var(--chart-1)", Data: make([][2]int64, len(daily))}
	visitors := TSSeries{Name: "Visitors", C: "var(--chart-2)", Data: make([][2]int64, len(daily))}
	for i, d := range daily {
		ms := midnight(d.Day).Add(12 * time.Hour).UnixMilli()
		views.Data[i] = [2]int64{ms, d.Views}
		visitors.Data[i] = [2]int64{ms, d.Uniques}
	}
	series := []TSSeries{views}
	if withVisitors {
		series = append(series, visitors)
	}
	var peak int64
	for _, s := range series {
		for _, p := range s.Data {
			peak = max(peak, p[1])
		}
	}
	top, _ := niceMax(peak)
	return Chart{Label: label, Data: TimeSeries{Series: series, Unit: dayUnit, XFormat: "date", Max: top}}
}

// Spark is a StatTile background sparkline (viewBox 0 0 200 48, drawn
// with preserveAspectRatio="none").
type Spark = usageview.Spark

func sparkline(values []int64) Spark { return usageview.NewSpark(values) }
