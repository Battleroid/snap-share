package app

import (
	"fmt"
	"time"

	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/items"
)

// registerRoutes wires every feature package. Only the integrator edits this
// file: feature tracks expose New(Deps) and Register(*web.Router), and the
// wiring is added here at merge time.
func registerRoutes(a *App) error {
	// items track
	if err := registerItems(a); err != nil {
		return err
	}
	registerCore(a)
	return nil
}

// items track: the files blob store, its temp reaper, and the items service
// (uploads, share pages, slug dispatcher; the composer is the signed-in home).
func registerItems(a *App) error {
	files, err := local.New(a.Cfg.FilesDir, local.Options{MinFree: func() int64 { return local.MinFreeBytes.Get(a.Settings) }})
	if err != nil {
		return err
	}
	a.Files = files
	a.Jobs.Every("blob.files.reap", time.Hour, files.Reap)
	a.Items, err = items.New(items.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Blobs: files,
		Render: a.Render, Errors: a.Errors,
	})
	if err != nil {
		return fmt.Errorf("items: %w", err)
	}
	a.Items.Register(a.Router)
	a.Home = a.Items.Home
	return nil
}
