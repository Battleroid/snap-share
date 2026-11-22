package statsui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Chart is a server-rendered line chart in the Patchbay Chart markup:
// pb-grid lines, pb-axis labels, a pb-line pb-s1 path with a pb-area under
// it and an end dot. Everything is geometry in SVG attributes, so the page
// needs no script and no style attributes.
type Chart struct {
	ViewBox   string
	GridX1    int // grid lines run from GridX1 to GridX2
	GridX2    int
	AxisX     int    // right edge of the value labels
	LabelY    int    // baseline of the date labels
	Label     string // accessible description
	Grid      []chartTick
	XLabels   []chartLabel
	Line      string // path d
	Area      string
	EndX      string
	EndY      string
	HasPoints bool
}

type chartTick struct {
	Y     string
	Label string
}

type chartLabel struct {
	X      string
	Label  string
	Anchor string // text-anchor
}

// Chart geometry (viewBox units).
const (
	chartW, chartH     = 960, 220
	chartL, chartR     = 36, 952
	chartT, chartB     = 10, 190
	chartLabelBaseline = 212
)

func f1(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// niceMax rounds up to a 1, 2, 2.5 or 5 step so four grid intervals read well.
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

// lineChart draws one value per day.
func lineChart(days []time.Time, values []int64, label string) Chart {
	c := Chart{
		ViewBox: fmt.Sprintf("0 0 %d %d", chartW, chartH), Label: label,
		GridX1: chartL, GridX2: chartR, AxisX: chartL - 8, LabelY: chartLabelBaseline,
	}
	var peak int64
	for _, v := range values {
		peak = max(peak, v)
	}
	top, step := niceMax(peak)
	y := func(v int64) float64 { return chartB - float64(v)/float64(top)*(chartB-chartT) }
	for v := int64(0); v <= top; v += step {
		c.Grid = append(c.Grid, chartTick{Y: f1(y(v)), Label: strconv.FormatInt(v, 10)})
	}
	n := len(values)
	if n == 0 {
		return c
	}
	x := func(i int) float64 {
		if n == 1 {
			return chartL
		}
		return chartL + float64(i)*float64(chartR-chartL)/float64(n-1)
	}
	var b strings.Builder
	for i, v := range values {
		if i == 0 {
			b.WriteString("M")
		} else {
			b.WriteString(" L")
		}
		b.WriteString(f1(x(i)) + " " + f1(y(v)))
	}
	c.Line = b.String()
	c.Area = c.Line + " L" + f1(x(n-1)) + " " + f1(chartB) + " L" + f1(x(0)) + " " + f1(chartB) + " Z"
	c.EndX, c.EndY = f1(x(n-1)), f1(y(values[n-1]))
	c.HasPoints = true

	// First, middle and last day.
	idx := []int{0}
	if n > 2 {
		idx = append(idx, (n-1)/2)
	}
	if n > 1 {
		idx = append(idx, n-1)
	}
	for k, i := range idx {
		anchor := "middle"
		switch {
		case k == 0:
			anchor = "start"
		case i == n-1:
			anchor = "end"
		}
		c.XLabels = append(c.XLabels, chartLabel{X: f1(x(i)), Label: days[i].UTC().Format("Jan 2"), Anchor: anchor})
	}
	return c
}

// Spark is a StatTile background sparkline (viewBox 0 0 200 48, drawn
// with preserveAspectRatio="none").
type Spark struct {
	Line, Area string
}

func sparkline(values []int64) Spark {
	const w, h, pad = 200.0, 48.0, 6.0
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
	return Spark{Line: line, Area: line + " L200 48 L0 48 Z"}
}
