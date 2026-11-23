package app

// media+stats: the media runner and jobs (thumbnails, posters, hover
// teasers, JPEG location stripping).

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediajobs"
	"github.com/Battleroid/snap-share/internal/mediaworker"
	"github.com/Battleroid/snap-share/internal/metrics"
)

// mediaParallel bounds concurrent ffmpeg children and media job workers.
const mediaParallel = 2

// onItemCreated is the items OnCreate hook; the media service is built after
// the items service it needs.
func (a *App) onItemCreated(ctx context.Context, it *items.Item) {
	a.Obs.ItemCreated(string(it.Kind), it.Size)
	if a.Media != nil {
		a.Media.OnCreate(ctx, it)
	}
}

// registerMedia starts the media service on the files store. Without ffmpeg
// on the host, images still get thumbnails and the ffmpeg steps are skipped.
// With SNAP_MEDIA_WORKER the job's steps go to the worker and snap never
// starts ffmpeg itself.
func registerMedia(a *App) error {
	d := mediajobs.Deps{
		Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Blobs: a.Files, Items: a.Items,
		WorkDir: filepath.Join(a.Cfg.DataDir, "media-work"), Concurrency: mediaParallel, Metrics: a.Obs,
	}
	switch {
	case a.Cfg.MediaWorker != "":
		if err := useMediaWorker(a, &d); err != nil {
			return fmt.Errorf("media: %w", err)
		}
	default:
		// Set Runner only when one exists: a nil *media.Runner in the
		// interface would not be a nil Runner. Local stays unset, so uploads
		// use the same Runner (and its ffmpeg fallback for images over the
		// memory budget).
		if runner, err := media.NewRunner(media.Config{MaxParallel: mediaParallel}); err != nil {
			a.Log.Warn("ffmpeg not found: no video posters, teasers or AVIF/HEIC thumbnails", "err", err)
		} else {
			d.Runner = runner
		}
	}
	if a.mediaDeps != nil {
		a.mediaDeps(&d)
	}
	var err error
	a.Media, err = mediajobs.New(d)
	if err != nil {
		return fmt.Errorf("media: %w", err)
	}
	a.Items.SetMediaJobs(a.Media.JobStates) // the owner's "processing" markers
	return nil
}

// useMediaWorker makes the media worker the job's Runner and a Go-only
// image runner the upload-time one: images over its memory budget are
// then queued, so their thumbnails arrive after the upload. The worker is
// checked now and every 30 s; jobs wait while it is unhealthy.
func useMediaWorker(a *App, d *mediajobs.Deps) error {
	c, err := mediaworker.NewClient(mediaworker.ClientConfig{URL: a.Cfg.MediaWorker, Token: a.Cfg.MediaWorkerToken})
	if err != nil {
		return err
	}
	w := newWorkerRunner(c, metrics.NewMediaWorker(a.Metrics), a.Log)
	if err := w.check(context.Background()); err != nil {
		a.Log.Error("media worker not healthy; media jobs wait until it is", "err", err)
	}
	a.Jobs.Every("media.worker.health", mediaWorkerHealthEvery, w.check)
	a.mediaWorker = w
	d.Runner = w
	d.Local = media.NewImageRunner(media.Config{MaxParallel: mediaParallel})
	return nil
}
