package metrics

import (
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// Label value sets of the media worker series.
var (
	MediaWorkerOps     = []string{"probe", "thumbnail", "poster", "teaser", "health"}
	MediaWorkerResults = []string{"ok", "unsupported", "too_large", "timeout", "error", "unavailable"}
)

// maxVersionLen cuts the worker's version label (the worker reports it).
const maxVersionLen = 40

// MediaWorker are the series of snap's calls to a media worker
// (SNAP_MEDIA_WORKER). They exist only when a worker is configured, so a
// single-container install exports no "worker down". A nil *MediaWorker
// counts nothing.
type MediaWorker struct {
	requests *prometheus.CounterVec
	up       prometheus.Gauge
	info     *prometheus.GaugeVec

	mu   sync.Mutex
	last [2]string // the info labels set now
}

// NewMediaWorker registers the media worker series on reg.
func NewMediaWorker(reg prometheus.Registerer) *MediaWorker {
	m := &MediaWorker{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_media_worker_requests_total",
			Help: "Calls to the media worker by operation and result (unavailable: unreachable, starting or unhealthy; the job waits).",
		}, []string{"op", "result"}),
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "snap_media_worker_up", Help: "1 when the last health check of the media worker passed (reachable, token accepted, same protocol).",
		}),
		info: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "snap_media_worker_info", Help: "Always 1; the labels carry the version and protocol the media worker last reported.",
		}, []string{"version", "protocol"}),
	}
	reg.MustRegister(m.requests, m.up, m.info)
	return m
}

// Request counts one call (op and result outside their sets are other).
func (m *MediaWorker) Request(op, result string) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(oneOf(op, MediaWorkerOps), oneOf(result, MediaWorkerResults)).Inc()
}

// Up sets snap_media_worker_up.
func (m *MediaWorker) Up(up bool) {
	if m == nil {
		return
	}
	if up {
		m.up.Set(1)
	} else {
		m.up.Set(0)
	}
}

// Info records what the worker's health answer reported. Only the latest
// pair is exported; values that are not a plain version or a small
// protocol number are other.
func (m *MediaWorker) Info(version string, protocol int) {
	if m == nil {
		return
	}
	labels := [2]string{versionLabel(version), Other}
	if protocol >= 0 && protocol < 1000 {
		labels[1] = strconv.Itoa(protocol)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if labels == m.last {
		return
	}
	m.info.Reset()
	m.info.WithLabelValues(labels[0], labels[1]).Set(1)
	m.last = labels
}

func versionLabel(v string) string {
	if v == "" || len(v) > maxVersionLen {
		return Other
	}
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '-', r == '+', r == '_':
		default:
			return Other
		}
	}
	return v
}
