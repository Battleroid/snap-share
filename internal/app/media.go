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
func registerMedia(a *App) error {
	runner, err := media.NewRunner(media.Config{MaxParallel: mediaParallel})
	if err != nil {
		a.Log.Warn("ffmpeg not found: no video posters, teasers or AVIF/HEIC thumbnails", "err", err)
		runner = nil
	}
	a.Media, err = mediajobs.New(mediajobs.Deps{
		Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Blobs: a.Files, Items: a.Items,
		Runner: runner, WorkDir: filepath.Join(a.Cfg.DataDir, "media-work"), Concurrency: mediaParallel, Metrics: a.Obs,
	})
	if err != nil {
		return fmt.Errorf("media: %w", err)
	}
	return nil
}
