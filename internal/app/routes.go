package app

import (
	"crypto/hkdf"
	"crypto/sha256"

	"github.com/Battleroid/snap-share/internal/secrets"
)

// registerRoutes wires every feature package. Only the integrator edits this
// file: feature tracks expose New(Deps) and Register(*web.Router), and the
// wiring is added here at merge time.
func registerRoutes(a *App) error {
	registerCore(a)

	// secrets track
	if a.secretsBlob != nil {
		visitor, err := hkdf.Key(sha256.New, a.Cfg.SecretKey, nil, "snap stats visitor key", 32)
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
			Notifier:        secrets.MailNotifier{DB: a.DB, Mail: a.Mail, BaseURL: a.Cfg.PublicURL.String(), Log: a.Log},
			RateLimitExempt: a.Cfg.RateLimitExempt,
		})
		a.Secrets.Register(a.Router)
		a.Secrets.RegisterJobs(a.Jobs)
	}
	return nil
}
