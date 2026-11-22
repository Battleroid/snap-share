package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/admin"
	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/dashboard"
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

	// accounts track
	if err := registerAccounts(a); err != nil {
		return err
	}

	// dashboard track
	if err := registerDashboard(a); err != nil {
		return err
	}
	return nil
}

// dashboard track: albums (API and the album share page, which the items
// dispatcher hands album slugs to) and the dashboard (filters, bulk actions,
// zip downloads, "Send link", tags). Needs the items and accounts services.
func registerDashboard(a *App) error {
	al, err := albums.New(albums.Deps{
		Log: a.Log, DB: a.DB, Items: a.Items, Render: a.Render, Errors: a.Errors, Assets: a.Assets,
	})
	if err != nil {
		return err
	}
	al.Register(a.Router)
	a.Items.SetAlbumPage(al.Page)
	a.Albums = al
	dash, err := dashboard.New(dashboard.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Items: a.Items, Albums: al, Blobs: a.Files,
		Users: a.Accounts.Store(), Mail: a.Mail, Render: a.Render, Errors: a.Errors,
		SiteName: func() string { return SiteName.Get(a.Settings) },
	})
	if err != nil {
		return err
	}
	dash.Register(a.Router)
	a.Dashboard = dash
	return nil
}

// accounts track: accounts (sign-in, sessions, sign-up, SSO, account settings)
// and the bare admin settings page.
func registerAccounts(a *App) error {
	acc, err := accounts.New(accounts.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Mail: a.Mail,
		Render: a.Render, Errors: a.Errors, SiteName: func() string { return SiteName.Get(a.Settings) },
		OIDCClient: a.oidcClient,
	})
	if err != nil {
		return err
	}
	acc.Register(a.Router)
	a.Accounts = acc
	if a.Auth == nil {
		a.Auth = acc
	}
	admin.New(admin.Deps{
		Log: a.Log, Settings: a.Settings, Render: a.Render, Errors: a.Errors, Accounts: acc, SiteName: SiteName,
	}).Register(a.Router)
	return nil
}

// items track: the files blob store, its temp reaper, and the items service
// (uploads, share pages, slug dispatcher; the composer is the signed-in home).
// itemPasswords adapts the accounts track's Argon2id hasher (same PHC format,
// shared hashing semaphore) to items.PasswordHasher.
type itemPasswords struct{}

func (itemPasswords) Hash(pw string) (string, error) {
	return auth.HashPassword(context.Background(), pw)
}

func (itemPasswords) Verify(hash, pw string) (bool, error) {
	ok, _, err := auth.VerifyPassword(context.Background(), hash, pw)
	return ok, err
}

func registerItems(a *App) error {
	files, err := local.New(a.Cfg.FilesDir, local.Options{MinFree: func() int64 { return local.MinFreeBytes.Get(a.Settings) }})
	if err != nil {
		return err
	}
	a.Files = files
	a.Jobs.Every("blob.files.reap", time.Hour, files.Reap)
	a.Items, err = items.New(items.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Blobs: files,
		Render: a.Render, Errors: a.Errors, Passwords: itemPasswords{},
	})
	if err != nil {
		return fmt.Errorf("items: %w", err)
	}
	a.Items.Register(a.Router)
	a.Home = a.Items.Home
	return nil
}
