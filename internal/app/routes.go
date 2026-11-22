package app

import (
	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/admin"
)

// registerRoutes wires every feature package. Only the integrator edits this
// file: feature tracks expose New(Deps) and Register(*web.Router), and the
// wiring is added here at merge time.
func registerRoutes(a *App) error {
	registerCore(a)

	// accounts track
	if err := registerAccounts(a); err != nil {
		return err
	}
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
