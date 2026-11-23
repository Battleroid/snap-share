// Package usageview turns the user-level numbers of internal/stats into the
// view data for the usage partials in templates/_partials/stats.html:
// stats.tile, stats.storage and stats.bykind. "Your stats" (statsui) and
// Settings > Profile "Usage" build their panels from the same constructors,
// so the two pages can't drift apart.
package usageview

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/web"
)

// Spark is a path pair for a sparkline: Line is the stroke, Area closes it
// along the bottom edge for the fill. Both are empty for an empty series.
type Spark struct {
	Line, Area string
}

func f1(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// NewSpark draws values as a StatTile background sparkline (viewBox
// "0 0 200 48", preserveAspectRatio none). A flat series is a flat line.
func NewSpark(values []int64) Spark { return drawSpark(values, 200, 48, 6) }

// NewMiniSpark draws values for the kit's inline sparkline (svg.pb-spark,
// viewBox "0 0 96 28").
func NewMiniSpark(values []int64) Spark { return drawSpark(values, 96, 28, 3) }

func drawSpark(values []int64, w, h, pad float64) Spark {
	n := len(values)
	if n == 0 {
		return Spark{}
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		lo, hi = min(lo, v), max(hi, v)
	}
	span := float64(hi - lo)
	if span == 0 {
		span = 1
	}
	var b strings.Builder
	for i, v := range values {
		px := 0.0
		if n > 1 {
			px = float64(i) * w / float64(n-1)
		}
		py := pad + (h-2*pad)*(1-float64(v-lo)/span)
		if i == 0 {
			b.WriteString("M")
		} else {
			b.WriteString(" L")
		}
		b.WriteString(f1(px) + " " + f1(py))
	}
	line := b.String()
	return Spark{Line: line, Area: fmt.Sprintf("%s L%g %g L0 %g Z", line, w, h, h)}
}

// Tile is one StatTile (partial stats.tile): a label, a value with an
// optional unit and delta line, and a background sparkline.
//
//	Label  "views · 30d"
//	Value  "462" or "1.2"
//	Small  the unit shown small after the value ("GB"), or ""
//	Delta  a muted line under the value ("+380 MB · 30d"), or ""
type Tile struct {
	Label string
	Value string
	Small string
	Delta string
	Spark Spark
}

// Count formats n as a tile value.
func Count(n int64) string { return strconv.FormatInt(n, 10) }

// Segment is one part of the storage bar.
type Segment struct {
	Key   string // "k0"…: ties the bar segment to its legend row
	Label string // "images"
	Size  string // "0.42 GB"
	W     string // width in percent of the bar, "4.2"
	Color string // a kit color token: "var(--chart-1)"
}

// StorageView is the data of partial stats.storage: a joined StorageBar
// split by kind.
//
// With a quota the bar is measured against it and Free is set; without one
// it is measured against what is used, so the segments fill it.
type StorageView struct {
	Used     string // "1.2 GB"
	Quota    string // "10 GB", "" without a quota
	Limited  bool   // a quota is set
	Pct      string // "12%" of the quota, "" without one
	Aria     string // the bar's accessible description
	Segments []Segment
	Free     string // "8.8 GB" left under a quota, "" without one
}

// kinds in bar order, with the kit chart colors they draw in.
var storageKinds = [...]struct{ label, color string }{
	{"images", "var(--chart-1)"}, {"video", "var(--pop-mint)"}, {"audio", "var(--pop-yellow)"}, {"other", "var(--chart-3)"},
}

// NewStorage builds the storage bar for used bytes against quota (0: no
// quota), split as k (stats.UserStorageKinds; used is items.Service.Usage).
// Segments with no bytes are left out.
func NewStorage(used, quota int64, k stats.StorageKinds) StorageView {
	v := StorageView{Used: web.HumanBytes(used)}
	denom := used
	if quota > 0 {
		v.Limited, v.Quota = true, web.HumanBytes(quota)
		v.Pct = strconv.FormatInt(min(used*100/quota, 999), 10) + "%"
		v.Free = web.HumanBytes(max(quota-used, 0))
		denom = max(quota, used)
	}
	bytes := [...]int64{k.Images, k.Video, k.Audio, k.Other}
	var parts []string
	for i, n := range bytes {
		if n <= 0 || denom <= 0 {
			continue
		}
		s := Segment{
			Key: "k" + strconv.Itoa(i), Label: storageKinds[i].label, Size: web.HumanBytes(n),
			W: f1(float64(n) * 100 / float64(denom)), Color: storageKinds[i].color,
		}
		v.Segments = append(v.Segments, s)
		parts = append(parts, s.Label+" "+s.Size)
	}
	v.Aria = v.Used + " used"
	if v.Limited {
		v.Aria = v.Used + " of " + v.Quota + " used"
	}
	if len(parts) > 0 {
		v.Aria += ": " + strings.Join(parts, ", ")
	}
	return v
}

// KindRow is one row of partial stats.bykind.
type KindRow struct {
	Label string // "image"
	N     int64
	Pct   int // the meter's fill: N against the largest row, 1-100
}

// NewKindRows turns stats.UserItemCounts into rows for the "Items by type"
// list. Order is kept (the query sorts by count); types with no items are
// not in its output.
func NewKindRows(counts []stats.Count) []KindRow {
	var top int64
	for _, c := range counts {
		top = max(top, c.N)
	}
	rows := make([]KindRow, 0, len(counts))
	for _, c := range counts {
		if c.N <= 0 {
			continue
		}
		rows = append(rows, KindRow{Label: c.Key, N: c.N, Pct: max(int((c.N*100+top/2)/top), 1)})
	}
	return rows
}
