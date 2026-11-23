// Package metrics defines the Prometheus metrics served on the metrics
// listener (SNAP_METRICS_LISTEN): event counters incremented where things
// happen (Metrics), gauges read from the database on scrape (Instance),
// and the opt-in per-user, per-key and referrer series behind the
// metrics.* settings.
//
// Every label value comes from a fixed set in this file or from code
// (route patterns, job kinds, limiter names); anything else is counted as
// "other". No metric carries an item, and user and key ids appear only
// when an admin turns the matching setting on.
package metrics

import (
	"runtime"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/version"
)

// Opt-in series with a label per user, per key or per referrer host. They
// are off by default; see docs/observability.md for their cost.
var (
	PerUser   = settings.Define("metrics.per_user", false, nil)
	PerKey    = settings.Define("metrics.per_key", false, nil)
	Referrers = settings.Define("metrics.referrers", false, nil)
)

// Label value sets. Values outside them are counted as "other".
var (
	AuthMethods  = []string{"password", "totp", "recovery", "passkey", "sso", "register", "reset", "key"}
	ItemKinds    = []string{"file", "paste", "link", "album", "gist"}
	SecretKinds  = []string{"text", "file"}
	Channels     = []string{"email", "ntfy"}
	MediaSteps   = []string{"probe", "poster", "teaser", "thumbnail"}
	StatsKinds   = []string{"preview", "view", "embed", "download", "click", "play", "link_opened", "revealed", "failed", "burned"}
	UserStatuses = []string{"active", "disabled"}
	UserRoles    = []string{"user", "admin"}
	JobStates    = []string{"queued", "running", "done", "failed"}
	SecretStates = []string{"uploading", "ready", "burned", "expired", "revoked"}
	jobResults   = []string{"ok", "retry", "failed"}
)

// Other is the label value for anything outside a set.
const Other = "other"

func oneOf(v string, set []string) string {
	if slices.Contains(set, v) {
		return v
	}
	return Other
}

// Metrics are the event metrics. A nil *Metrics counts nothing, so
// packages and tests can leave it unset.
type Metrics struct {
	settings *settings.Store

	authAttempts *prometheus.CounterVec
	rateLimited  *prometheus.CounterVec
	uploads      *prometheus.CounterVec
	uploadBytes  *prometheus.CounterVec
	jobRuns      *prometheus.CounterVec
	jobDuration  *prometheus.HistogramVec
	taskRuns     *prometheus.CounterVec
	taskDuration *prometheus.HistogramVec
	mediaSteps   *prometheus.HistogramVec
	notify       *prometheus.CounterVec
	statsEvents  *prometheus.CounterVec

	principals *principals
}

var durationBuckets = []float64{.01, .05, .25, 1, 5, 15, 60, 300}

// New registers the event metrics on reg. st holds the metrics.* settings.
func New(reg prometheus.Registerer, st *settings.Store) *Metrics {
	m := &Metrics{
		settings: st,
		authAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_auth_attempts_total", Help: "Sign-in and API key authentication attempts by method and result.",
		}, []string{"method", "result"}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_ratelimit_hits_total", Help: "Requests refused by a rate limiter, by limiter.",
		}, []string{"limiter"}),
		uploads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_uploads_total", Help: "Items and secrets created, by target and kind.",
		}, []string{"target", "kind"}),
		uploadBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_upload_bytes_total", Help: "Bytes stored by new items and secrets, by target and kind.",
		}, []string{"target", "kind"}),
		jobRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_job_runs_total", Help: "Queued job attempts by kind and result (ok, retry, failed).",
		}, []string{"kind", "result"}),
		jobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "snap_job_duration_seconds", Help: "Queued job attempt duration by kind.", Buckets: durationBuckets,
		}, []string{"kind"}),
		taskRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_task_runs_total", Help: "Periodic task runs by task and result.",
		}, []string{"task", "result"}),
		taskDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "snap_task_duration_seconds", Help: "Periodic task duration by task.", Buckets: durationBuckets,
		}, []string{"task"}),
		mediaSteps: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "snap_media_step_duration_seconds", Help: "ffmpeg step duration by step and result.", Buckets: durationBuckets,
		}, []string{"step", "result"}),
		notify: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_notify_deliveries_total", Help: "Notification deliveries by channel and result.",
		}, []string{"channel", "result"}),
		statsEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "snap_stats_events_total", Help: "Visitor events recorded, by kind.",
		}, []string{"kind"}),
	}
	m.principals = newPrincipals(st)
	build := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "snap_build_info", Help: "Always 1; the labels carry the build.",
		ConstLabels: prometheus.Labels{"version": version.Version, "goversion": runtime.Version()},
	})
	build.Set(1)
	reg.MustRegister(m.authAttempts, m.rateLimited, m.uploads, m.uploadBytes, m.jobRuns, m.jobDuration,
		m.taskRuns, m.taskDuration, m.mediaSteps, m.notify, m.statsEvents, build, m.principals)
	return m
}

func result(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

// AuthAttempt counts one sign-in or API key authentication.
func (m *Metrics) AuthAttempt(method string, ok bool) {
	if m == nil {
		return
	}
	m.authAttempts.WithLabelValues(oneOf(method, AuthMethods), result(ok)).Inc()
}

// RateLimited counts a refusal by the named limiter (ratelimit.Counter).
func (m *Metrics) RateLimited(limiter string) {
	if m == nil {
		return
	}
	m.rateLimited.WithLabelValues(limiter).Inc()
}

// ItemCreated counts a new item of kind and its stored size.
func (m *Metrics) ItemCreated(kind string, size int64) {
	m.upload("item", oneOf(kind, ItemKinds), size)
}

// SecretCreated counts a new secret of kind and its ciphertext size.
func (m *Metrics) SecretCreated(kind string, size int64) {
	m.upload("secret", oneOf(kind, SecretKinds), size)
}

func (m *Metrics) upload(target, kind string, size int64) {
	if m == nil {
		return
	}
	m.uploads.WithLabelValues(target, kind).Inc()
	if size > 0 {
		m.uploadBytes.WithLabelValues(target, kind).Add(float64(size))
	}
}

// JobRun records one queued job attempt (jobs.Observer). result is ok,
// retry or failed.
func (m *Metrics) JobRun(kind, res string, d time.Duration) {
	if m == nil {
		return
	}
	m.jobRuns.WithLabelValues(kind, oneOf(res, jobResults)).Inc()
	m.jobDuration.WithLabelValues(kind).Observe(d.Seconds())
}

// TaskRun records one periodic task run (jobs.Observer).
func (m *Metrics) TaskRun(task string, ok bool, d time.Duration) {
	if m == nil {
		return
	}
	m.taskRuns.WithLabelValues(task, result(ok)).Inc()
	m.taskDuration.WithLabelValues(task).Observe(d.Seconds())
}

// MediaStep records one ffmpeg step.
func (m *Metrics) MediaStep(step string, d time.Duration, err error) {
	if m == nil {
		return
	}
	m.mediaSteps.WithLabelValues(oneOf(step, MediaSteps), result(err == nil)).Observe(d.Seconds())
}

// NotifyDelivery counts one delivery attempt on channel.
func (m *Metrics) NotifyDelivery(channel string, ok bool) {
	if m == nil {
		return
	}
	m.notify.WithLabelValues(oneOf(channel, Channels), result(ok)).Inc()
}

// StatsEvent counts one recorded visitor event.
func (m *Metrics) StatsEvent(kind string) {
	if m == nil {
		return
	}
	m.statsEvents.WithLabelValues(oneOf(kind, StatsKinds)).Inc()
}

// Request counts a request by a user or an API key for the opt-in
// per-user and per-key series (web.Metrics.Principal).
func (m *Metrics) Request(userID, keyID int64) {
	if m == nil {
		return
	}
	m.principals.request(userID, keyID)
}
