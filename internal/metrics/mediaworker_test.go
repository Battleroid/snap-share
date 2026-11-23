package metrics_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Battleroid/snap-share/internal/metrics"
)

// The media worker series have bounded labels: ops and results outside
// their sets are other, and only the last reported version and protocol,
// cleaned, are exported.
func TestMediaWorkerLabelsBounded(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMediaWorker(reg)
	for _, op := range append(metrics.MediaWorkerOps, "../../etc/passwd", "probe?key=abc") {
		for _, res := range append(metrics.MediaWorkerResults, "weird", "") {
			m.Request(op, res)
		}
	}
	for i, v := range []string{"v1.2.3", "v1.2.4", strings.Repeat("9", 100), "v1 <script>", "bad\nversion"} {
		m.Info(v, i*500)
	}
	m.Up(true)
	var nilM *metrics.MediaWorker
	nilM.Request("probe", "ok") // nil counts nothing
	nilM.Up(true)
	nilM.Info("v", 1)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	ops := append(metrics.MediaWorkerOps, metrics.Other)
	results := append(metrics.MediaWorkerResults, metrics.Other)
	for _, mf := range mfs {
		switch mf.GetName() {
		case "snap_media_worker_requests_total":
			if n := len(mf.GetMetric()); n != len(ops)*len(results) {
				t.Errorf("%d request series, want %d", n, len(ops)*len(results))
			}
			for _, s := range mf.GetMetric() {
				for _, l := range s.GetLabel() {
					set := ops
					if l.GetName() == "result" {
						set = results
					}
					if !slices.Contains(set, l.GetValue()) {
						t.Errorf("%s=%q outside its set", l.GetName(), l.GetValue())
					}
				}
			}
		case "snap_media_worker_info":
			if n := len(mf.GetMetric()); n != 1 {
				t.Fatalf("%d info series, want only the latest", n)
			}
			for _, l := range mf.GetMetric()[0].GetLabel() {
				if l.GetValue() != metrics.Other {
					t.Errorf("info %s=%q, want other", l.GetName(), l.GetValue())
				}
			}
		case "snap_media_worker_up":
			if v := mf.GetMetric()[0].GetGauge().GetValue(); v != 1 {
				t.Errorf("up %v", v)
			}
		}
	}
	m.Info("v1.2.3", 1)
	mfs, _ = reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() == "snap_media_worker_info" {
			got := map[string]string{}
			for _, l := range mf.GetMetric()[0].GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			if len(mf.GetMetric()) != 1 || got["version"] != "v1.2.3" || got["protocol"] != "1" {
				t.Errorf("info %v", got)
			}
		}
	}
}
