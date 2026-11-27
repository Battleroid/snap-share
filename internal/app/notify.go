package app

// statsui+notify: open notifications (the in-app inbox, email, ntfy)
// and the stats pages and API. Notifications are built before the secrets
// service, which takes them as its Notifier; items reports opens
// (onItemsOpened), expiries (onItemExpired) and uploads (notifyQuota).

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/dashboard"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/statsui"
)

func registerNotify(a *App) error {
	n, err := notify.New(notify.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Mail: a.Mail, Items: a.Items, Metrics: a.Obs,
		Render: a.Render, Errors: a.Errors, SiteName: func() string { return SiteName.Get(a.Settings) },
		// Inbox rows hold visitor IPs: kept as long as the raw stats events.
		RetentionDays: func() int { return StatsRetentionDays.Get(a.Settings) },
		// Read rows go sooner (0: with the rest).
		ReadRetentionHours: func() int { return NotifyReadRetentionHours.Get(a.Settings) },
	})
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	n.Register(a.Router)
	a.Render.SetBell(n.Unread) // the header bell's count on every page
	a.Notify = n
	if a.Accounts != nil {
		a.Accounts.SetAdminLapsed(n.AdminLapsed) // tells an SSO admin once that admin rights paused
	}
	return nil
}

func registerStatsUI(a *App) error {
	d := statsui.Deps{DB: a.DB, Render: a.Render, Errors: a.Errors, Log: a.Log, Items: a.Items, AdminCIDRs: a.Cfg.AdminCIDRs,
		// The stats pages' retention note.
		RetentionDays: func() int { return StatsRetentionDays.Get(a.Settings) },
		// The item page's details. Albums is registered after statsui, so
		// these read a.Albums when a page is rendered.
		Host: a.Cfg.PublicURL.Host,
		Tags: func(ctx context.Context, id int64) ([]string, error) {
			m, err := dashboard.ItemTags(ctx, a.DB.R, []int64{id})
			return m[id], err
		},
		Albums: func(ctx context.Context, owner, id int64) ([]*items.Item, error) {
			return albums.Containing(ctx, a.DB.R, owner, id)
		},
		Card: func(it *items.Item) albums.Card { return a.Albums.CardFor(it, true) },
	}
	if a.Notify != nil {
		d.Notify = a.Notify
	}
	if a.Secrets != nil {
		d.Secrets = a.Secrets
		if a.Notify != nil {
			// "Notify me" on the secret composer only when a channel is on.
			a.Secrets.SetNotifyEnabled(a.Notify.Enabled)
		}
	}
	s, err := statsui.New(d)
	if err != nil {
		return fmt.Errorf("statsui: %w", err)
	}
	s.Register(a.Router)
	a.StatsUI = s
	return nil
}

// onItemsOpened is items.Deps.OnOpened.
func (a *App) onItemsOpened(ctx context.Context, opened []items.Opened) {
	if a.Notify != nil {
		a.Notify.ItemsOpened(ctx, opened)
	}
}

// onItemExpired is items.Deps.OnExpired.
func (a *App) onItemExpired(ctx context.Context, it *items.Item, deleted bool) {
	if a.Notify != nil {
		a.Notify.ItemExpired(ctx, it, deleted)
	}
}

// notifyQuota runs after an item is created (onItemCreated): storage
// passing 80% or 95% is notified once per crossing.
func (a *App) notifyQuota(ctx context.Context, userID int64) {
	if a.Notify != nil {
		a.Notify.QuotaChanged(ctx, userID)
	}
}

// onSecretDeleted is secrets.Deps.OnDelete: inbox rows about a secret go in
// the transaction that deletes it.
func (a *App) onSecretDeleted(ctx context.Context, tx *sql.Tx, id string) error {
	if a.Notify == nil {
		return nil
	}
	return a.Notify.DeleteSecretTx(ctx, tx, id)
}

// secretNotifier is the secrets service's Notifier: the notify dispatcher
// (the inbox, and email and ntfy per the owner's settings).
func (a *App) secretNotifier() secrets.Notifier {
	if a.Notify != nil {
		return a.Notify
	}
	return secrets.MailNotifier{DB: a.DB, Mail: a.Mail, BaseURL: a.Cfg.PublicURL.String(), Log: a.Log}
}

// NotifyReadRetentionHours is how long a read in-app notification is kept;
// 0 keeps it as long as the rest (stats.retention_days).
var NotifyReadRetentionHours = settings.Define("notify.read_retention_hours", 24, settings.Range(0, 24*3650))
