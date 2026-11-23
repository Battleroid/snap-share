package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/admin"
	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/dashboard"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/secrets"
)

// registerRoutes wires every feature package. Feature tracks expose New(Deps)
// and Register(*web.Router); each track's wiring lives in its own block here.
func registerRoutes(a *App) error {
	// items track (before core: the composer replaces core's signed-in home)
	if err := registerItems(a); err != nil {
		return err
	}
	registerCore(a)

	// accounts track
	if err := registerAccounts(a); err != nil {
		return err
	}

	// stats-ui+notify track: notifications before secrets (its Notifier)
	if err := registerNotify(a); err != nil {
		return err
	}

	// secrets track
	if err := registerSecrets(a); err != nil {
		return err
	}

	// keys track
	if err := registerKeys(a); err != nil {
		return err
	}

	// dashboard track
	if err := registerDashboard(a); err != nil {
		return err
	}
	// stats-ui+notify track
	if err := registerStatsUI(a); err != nil {
		return err
	}
	// admin track (needs items, secrets and keys)
	registerAdmin(a)
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
	a.Items.SetAlbumMembers(func(ctx context.Context, id int64) ([]*items.Item, error) { return albums.Members(ctx, a.DB.R, id) })
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

// admin track: the admin area (overview, users, invites, moderation,
// settings, audit) and /api/v1/admin.
func registerAdmin(a *App) {
	admin.New(admin.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Render: a.Render, Errors: a.Errors,
		Accounts: a.Accounts, Items: a.Items, Secrets: a.Secrets,
	}).Register(a.Router)
}

// keys track: API keys (the accounts Authenticator's bearer verifier), the key
// and integration settings pages, /api/v1/keys, /api/v1/me and /dl/.
func registerKeys(a *App) error {
	k, err := keys.New(keys.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Owners: a.Accounts.Store(),
		Owner:  a.Accounts, // track T: admin-scope keys need confirm it's you with 2FA
		Render: a.Render, Errors: a.Errors, SiteName: func() string { return SiteName.Get(a.Settings) },
	})
	if err != nil {
		return fmt.Errorf("keys: %w", err)
	}
	k.Register(a.Router)
	a.Accounts.SetBearerVerifier(k)
	a.Accounts.SetKeyRevoker(k.Store()) // keys end on reset, sign-out everywhere, disable
	a.Keys = k
	return nil
}

// keys track: the per-key rate limit runs after authentication.
func (a *App) keyRateLimit(next http.Handler) http.Handler {
	if a.Keys == nil {
		return next
	}
	return a.Keys.RateLimit(next)
}

// accounts track: accounts (sign-in, sessions, sign-up, SSO, account settings).
func registerAccounts(a *App) error {
	acc, err := accounts.New(accounts.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Mail: a.Mail,
		Render: a.Render, Errors: a.Errors, SiteName: func() string { return SiteName.Get(a.Settings) },
		OIDCClient: a.oidcClient,
		Usage:      a.profileUsage, // Settings › Profile › Usage (usage.go)
	})
	if err != nil {
		return err
	}
	acc.Register(a.Router)
	a.Accounts = acc
	if a.Auth == nil {
		a.Auth = acc
	}
	return nil
}

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

// items track: the files blob store, its temp reaper, and the items service
// (uploads, share pages, slug dispatcher; the composer is the signed-in home).
func registerItems(a *App) error {
	files, err := local.New(a.Cfg.FilesDir, local.Options{MinFree: func() int64 { return local.MinFreeBytes.Get(a.Settings) }})
	if err != nil {
		return err
	}
	a.Files = files
	a.Jobs.Every("blob.files.reap", time.Hour, files.Reap)
	statsKey, err := visitorSecret(a.Cfg.SecretKey) // media+stats track
	if err != nil {
		return err
	}
	a.Items, err = items.New(items.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Blobs: files,
		Render: a.Render, Errors: a.Errors, Passwords: itemPasswords{},
		OnCreate: a.onItemCreated, Stats: a.Stats, StatsKey: statsKey, // media+stats track
		OnOpened: a.onItemsOpened, // stats-ui+notify track
		SiteName: func() string { return SiteName.Get(a.Settings) },
	})
	if err != nil {
		return fmt.Errorf("items: %w", err)
	}
	a.Items.Register(a.Router)
	a.Home = a.Items.Home
	a.Render.SetQuota(a.Items.Quota) // the storage ring in the app shell's header
	// media+stats track: stats retention; thumbnails, posters, teasers
	a.Jobs.Every("stats.purge", 24*time.Hour, a.PurgeStats)
	return registerMedia(a)
}

// secrets track: one-time secrets on their own blob store under SecretsDir
// (a separate dataset in production, without snapshots).
func registerSecrets(a *App) error {
	if a.secretsBlob == nil {
		store, err := local.New(a.Cfg.SecretsDir, local.Options{MinFree: func() int64 { return local.MinFreeBytes.Get(a.Settings) }})
		if err != nil {
			return fmt.Errorf("secrets store: %w", err)
		}
		a.Jobs.Every("blob.secrets.reap", time.Hour, store.Reap)
		a.secretsStore = store
		a.secretsBlob = store
	}
	visitor, err := visitorSecret(a.Cfg.SecretKey)
	if err != nil {
		return err
	}
	a.Secrets = secrets.New(secrets.Deps{
		DB:              a.DB,
		Blobs:           a.secretsBlob,
		Settings:        a.Settings,
		Render:          a.Render,
		Errors:          a.Errors,
		Log:             a.Log,
		PublicURL:       a.Cfg.PublicURL.String(),
		Stats:           a.Stats,
		VisitorSecret:   visitor,
		Notifier:        a.secretNotifier(), // stats-ui+notify track
		OnDelete:        a.onSecretDeleted,  // stats-ui+notify track: the inbox forgets a purged secret
		RateLimitExempt: a.Cfg.RateLimitExempt,
		DefaultQuota:    func() int64 { return items.DefaultQuotaBytes.Get(a.Settings) },
	})
	a.Secrets.Register(a.Router)
	a.Secrets.RegisterJobs(a.Jobs)
	// The composer's Secret tab (items reads this hook at request time).
	a.Items.SetSecretComposer(func(r *http.Request) any { return a.Secrets.ComposerFor(r.Context(), auth.FromContext(r.Context())) })
	return nil
}
