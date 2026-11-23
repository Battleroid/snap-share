package app

// observability: event counters that need the app's wiring (visitor events,
// the stats writer's own counters).

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Battleroid/snap-share/internal/metrics"
	"github.com/Battleroid/snap-share/internal/stats"
)

// countingRecorder counts each visitor event by kind before the stats
// writer queues it.
type countingRecorder struct {
	next stats.Recorder
	m    *metrics.Metrics
}

func (c countingRecorder) Record(ctx context.Context, e stats.Event) {
	c.m.StatsEvent(string(e.Kind))
	c.next.Record(ctx, e)
}

// statsRecorder is the recorder feature packages get: a.Stats, counted.
func (a *App) statsRecorder() stats.Recorder {
	return countingRecorder{next: a.Stats, m: a.Obs}
}

// registerStatsWriter exports the writer's counters as
// snap_stats_writer_events_total{outcome}.
func registerStatsWriter(reg prometheus.Registerer, w *stats.Writer) {
	d := prometheus.NewDesc("snap_stats_writer_events_total",
		"Visitor events by what the stats writer did with them: written, dropped (buffer full), invalid, failed (a flush failed) or orphaned (item deleted first).",
		[]string{"outcome"}, nil)
	reg.MustRegister(collectorFunc{d, func(ch chan<- prometheus.Metric) {
		s := w.Stats()
		for outcome, n := range map[string]uint64{"written": s.Written, "dropped": s.Dropped, "invalid": s.Invalid, "failed": s.Failed, "orphaned": s.Orphaned} {
			ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, float64(n), outcome)
		}
	}})
}

type collectorFunc struct {
	d *prometheus.Desc
	f func(chan<- prometheus.Metric)
}

func (c collectorFunc) Describe(ch chan<- *prometheus.Desc) { ch <- c.d }
func (c collectorFunc) Collect(ch chan<- prometheus.Metric) { c.f(ch) }
