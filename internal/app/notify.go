package app

// stats-ui+notify track: open notifications (the in-app inbox, email, ntfy)
// and the stats pages and API. Notifications are built before the secrets
// service, which takes them as its Notifier; the items counter flush reports
// opens through onItemsOpened.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/statsui"
)

func registerNotify(a *App) error {
	n, err := notify.New(notify.Deps{
		Cfg: a.Cfg, Log: a.Log, DB: a.DB, Settings: a.Settings, Jobs: a.Jobs, Mail: a.Mail, Items: a.Items,
		Render: a.Render, Errors: a.Errors, SiteName: func() string { return SiteName.Get(a.Settings) },
		// Inbox rows hold visitor IPs: kept as long as the raw stats events.
		RetentionDays: func() int { return StatsRetentionDays.Get(a.Settings) },
	})
	if err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	n.Register(a.Router)
	a.Render.SetBell(n.Unread) // the header bell's count on every page
	a.Notify = n
	return nil
}

func registerStatsUI(a *App) error {
	d := statsui.Deps{DB: a.DB, Render: a.Render, Errors: a.Errors, Log: a.Log, Items: a.Items, AdminCIDRs: a.Cfg.AdminCIDRs}
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
