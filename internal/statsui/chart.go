package statsui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/usageview"
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
	// Narrow is the same chart drawn for a phone: a narrower viewBox, so
	// its labels keep their size when it is scaled to the screen.
	Narrow *Chart
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

// Chart geometry (viewBox units): chartW wide, or chartNarrowW on a phone.
const (
	chartW, chartH     = 960, 220
	chartNarrowW       = 360
	chartL             = 36
	chartPadR          = 8
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

// lineChart draws one value per day, wide and narrow.
func lineChart(days []time.Time, values []int64, label string) Chart {
	c := drawLine(days, values, label, chartW)
	n := drawLine(days, values, label, chartNarrowW)
	c.Narrow = &n
	return c
}

// drawLine draws the chart w viewBox units wide.
func drawLine(days []time.Time, values []int64, label string, w int) Chart {
	chartR := w - chartPadR
	c := Chart{
		ViewBox: fmt.Sprintf("0 0 %d %d", w, chartH), Label: label,
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
type Spark = usageview.Spark

func sparkline(values []int64) Spark { return usageview.NewSpark(values) }
