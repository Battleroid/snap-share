package app

// media+stats: calling a media worker (SNAP_MEDIA_WORKER) instead of
// running ffmpeg in process.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediajobs"
	"github.com/Battleroid/snap-share/internal/mediaworker"
	"github.com/Battleroid/snap-share/internal/metrics"
)

const (
	// mediaWorkerHealthEvery is the media.worker.health task's interval.
	mediaWorkerHealthEvery = 30 * time.Second
	// mediaWorkerRecheck is how soon a job may check an unhealthy worker
	// again itself, so jobs resume soon after the worker comes up.
	mediaWorkerRecheck   = 5 * time.Second
	mediaWorkerHealthMax = 3 * time.Second
)

// workerRunner is the media.process Runner in worker mode: the
// mediaworker.Client, gated on the worker's health, counted, and with
// job-<id>-<attempt> request ids.
//
// While the worker is unhealthy (unreachable, starting, refusing the token
// or speaking another protocol) every call defers the job without
// spending an attempt. It starts unhealthy until a check passes, so a
// wrong token never reaches a job. A call that fails is never turned into
// a deferral afterwards: an input that crashes the worker must spend its
// attempts.
type workerRunner struct {
	c   *mediaworker.Client
	m   *metrics.MediaWorker
	log *slog.Logger
	now func() time.Time

	healthy atomic.Bool
	mu      sync.Mutex // one check at a time
	checked time.Time  // last check, under mu
}

var _ mediajobs.Runner = (*workerRunner)(nil)

func newWorkerRunner(c *mediaworker.Client, m *metrics.MediaWorker, log *slog.Logger) *workerRunner {
	w := &workerRunner{c: c, m: m, log: log, now: time.Now}
	m.Up(false)
	return w
}

// check asks the worker for /v1/health and records the answer. It is the
// media.worker.health task; an error is an unhealthy worker.
func (w *workerRunner) check(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.checkLocked(ctx)
}

func (w *workerRunner) checkLocked(ctx context.Context) error {
	w.checked = w.now()
	ctx, cancel := context.WithTimeout(ctx, mediaWorkerHealthMax)
	defer cancel()
	h, err := w.c.Health(ctx)
	w.m.Request("health", workerResult(err))
	if err == nil {
		w.m.Info(h.Version, h.Protocol)
		if h.Protocol != mediaworker.Protocol {
			err = fmt.Errorf("media worker speaks protocol %d, snap speaks %d: run the same image for both", h.Protocol, mediaworker.Protocol)
		}
	}
	ok := err == nil
	w.m.Up(ok)
	if was := w.healthy.Swap(ok); ok && !was {
		w.log.Info("media worker up", "version", h.Version, "protocol", h.Protocol, "ffmpeg", h.FFmpeg)
	}
	return err
}

// ready returns nil when the worker is healthy, else a deferral. An
// unhealthy worker is checked again first if the last check is a few
// seconds old.
func (w *workerRunner) ready(ctx context.Context) error {
	if w.healthy.Load() {
		return nil
	}
	w.mu.Lock()
	if !w.healthy.Load() && w.now().Sub(w.checked) >= mediaWorkerRecheck {
		_ = w.checkLocked(ctx)
	}
	w.mu.Unlock()
	if w.healthy.Load() {
		return nil
	}
	return jobs.Defer(errors.New("media worker is not healthy"), mediaworker.DefaultDefer)
}

// call runs one worker operation.
func (w *workerRunner) call(ctx context.Context, op string, fn func(context.Context) error) error {
	if err := w.ready(ctx); err != nil {
		w.m.Request(op, "unavailable")
		return err
	}
	if r, ok := jobs.Current(ctx); ok {
		ctx = mediaworker.WithRequestID(ctx, fmt.Sprintf("job-%d-%d", r.ID, r.Attempt))
	}
	err := fn(ctx)
	w.m.Request(op, workerResult(err))
	return err
}

// workerResult is the result label of a call's error.
func workerResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case jobs.IsDeferred(err):
		return "unavailable"
	case errors.Is(err, media.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, media.ErrTooLarge):
		return "too_large"
	case errors.Is(err, media.ErrTimeout):
		return "timeout"
	}
	return "error"
}

func (w *workerRunner) Probe(ctx context.Context, path string, t media.Type) (media.Info, error) {
	var info media.Info
	err := w.call(ctx, mediaworker.OpProbe, func(ctx context.Context) error {
		var err error
		info, err = w.c.Probe(ctx, path, t)
		return err
	})
	return info, err
}

func (w *workerRunner) Thumbnail(ctx context.Context, src string, t media.Type, out io.Writer) (media.Thumb, error) {
	var th media.Thumb
	err := w.call(ctx, mediaworker.OpThumbnail, func(ctx context.Context) error {
		var err error
		th, err = w.c.Thumbnail(ctx, src, t, out)
		return err
	})
	return th, err
}

func (w *workerRunner) Poster(ctx context.Context, src string, t media.Type, dst string) error {
	return w.call(ctx, mediaworker.OpPoster, func(ctx context.Context) error { return w.c.Poster(ctx, src, t, dst) })
}

func (w *workerRunner) Teaser(ctx context.Context, src string, t media.Type, info media.Info, dst string) error {
	return w.call(ctx, mediaworker.OpTeaser, func(ctx context.Context) error { return w.c.Teaser(ctx, src, t, info, dst) })
}

// HasFFmpeg is true: a configured worker counts, up or not.
func (w *workerRunner) HasFFmpeg() bool { return true }

// CheckMediaWorker runs the media worker health check now (the
// media.worker.health task). It fails without a configured worker.
func (a *App) CheckMediaWorker(ctx context.Context) error {
	if a.mediaWorker == nil {
		return errors.New("no media worker configured")
	}
	return a.mediaWorker.check(ctx)
}
