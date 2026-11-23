// Package app wires configuration, storage and HTTP handlers into a server.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/albums"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/dashboard"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/mediajobs"
	"github.com/Battleroid/snap-share/internal/metrics"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/obs"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/statsui"
	"github.com/Battleroid/snap-share/internal/web"
)

// App is a configured server.
type App struct {
	Cfg      *config.Config
	Log      *slog.Logger
	DB       *db.DB
	Settings *settings.Store
	Jobs     *jobs.Queue
	Mail     mail.Sender
	Auth     auth.Authenticator
	IP       clientip.Resolver
	Assets   *web.Assets
	Render   *web.Renderer
	Errors   *web.ErrorPages
	Router   *web.Router
	Metrics  *prometheus.Registry

	// observability
	Obs         *metrics.Metrics  // event counters (nil-safe)
	Instance    *metrics.Instance // gauges read from the database on scrape
	AuditStream *obs.Stream       // nil: SNAP_AUDIT_LOG=off

	// accounts
	Accounts   *accounts.Service
	oidcClient *http.Client

	// items
	Files    *local.Store     // blob store for items (cfg.FilesDir)
	Items    *items.Service   // uploads, share pages, slug dispatcher
	Home     http.HandlerFunc // signed-in home page: the composer (items)
	itemsNow func() time.Time

	// secrets
	Secrets      *secrets.Service
	Stats        *stats.Writer // visitor events (secrets now; items via media+stats)
	secretsBlob  blob.Store
	secretsStore *local.Store // set when the app created the secrets store itself

	// keys
	Keys *keys.Service // API keys (bearer verifier), integrations, /dl/

	// media+stats
	Media       *mediajobs.Service // thumbnails, posters, teasers (OnCreate hook and media.process jobs)
	mediaDeps   func(*mediajobs.Deps)
	mediaWorker *workerRunner // nil without SNAP_MEDIA_WORKER

	// dashboard
	Albums    *albums.Service    // album API and album share pages
	Dashboard *dashboard.Service // dashboard, bulk actions, tags

	// statsui+notify
	Notify  *notify.Service  // notification settings, open hooks, delivery jobs
	StatsUI *statsui.Service // item stats pages, secret timeline, stats API

	handler http.Handler
}

// Options overrides dependencies (tests).
type Options struct {
	Auth auth.Authenticator
	Mail mail.Sender

	// accounts: HTTP client for the OIDC provider (tests pass the stub's).
	OIDCClient *http.Client

	// secrets: overrides the blob store rooted at cfg.SecretsDir (tests).
	SecretsBlob blob.Store

	// items: the clock of the items service and its rate limits (tests;
	// nil: time.Now).
	ItemsNow func() time.Time

	// media+stats: adjusts the media service's dependencies before it is
	// built (tests: a fake Runner or a separate Local).
	MediaDeps func(*mediajobs.Deps)

	// observability: where committed audit entries are streamed (SNAP_AUDIT_LOG;
	// nil: nowhere). See obs.Setup.
	AuditSink obs.AuditSink
}

// SiteName is the name shown in the header and page titles.
var SiteName = settings.Define("site.name", "snap", func(s string) error {
	if s == "" || len(s) > 64 {
		return errors.New("must be 1-64 characters")
	}
	return nil
})

// New opens storage, applies migrations and builds the HTTP handler.
func New(ctx context.Context, cfg *config.Config, log *slog.Logger, opts Options) (*App, error) {
	for _, dir := range []string{cfg.DataDir, cfg.FilesDir, cfg.SecretsDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	d, err := db.Open(ctx, filepath.Join(cfg.DataDir, "snap.db"))
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Log: log, DB: d, IP: clientip.Resolver{Trusted: cfg.TrustedProxies}}
	if err := a.init(ctx, opts); err != nil {
		_ = d.Close()
		return nil, err
	}
	return a, nil
}

func (a *App) init(ctx context.Context, opts Options) error {
	applied, err := a.DB.Migrate(ctx)
	if err != nil {
		return err
	}
	for _, m := range applied {
		a.Log.Info("migration applied", "name", m)
	}
	if a.Settings, err = settings.Open(ctx, a.DB); err != nil {
		return err
	}
	a.Metrics = prometheus.NewRegistry()
	a.Metrics.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	a.Obs = metrics.New(a.Metrics, a.Settings)
	a.Jobs = jobs.New(a.DB, a.Log)
	a.Jobs.Observe(a.Obs)

	a.Mail = opts.Mail
	if a.Mail == nil {
		a.Mail = mail.Log{L: a.Log}
		// accounts
		if a.Cfg.SMTP.Enabled() {
			a.Mail = mail.NewSMTP(a.Cfg.SMTP)
		}
	}
	// accounts: without an override, registerRoutes installs the accounts
	// Authenticator (sessions; API keys through its BearerVerifier).
	a.Auth = opts.Auth
	a.oidcClient = opts.OIDCClient
	// Settings changed by the admin CLI (a second process) reach the server here.
	a.Jobs.Every("settings.reload", time.Minute, a.Settings.Reload)

	a.itemsNow = opts.ItemsNow // items
	// secrets
	a.secretsBlob = opts.SecretsBlob
	a.mediaDeps = opts.MediaDeps // media+stats
	// items: events of items deleted before the flush are dropped.
	a.Stats = stats.NewWriter(a.DB.W, stats.WriterOptions{Logger: a.Log, ItemExists: items.ExistsTx})
	// observability: the stats writer's own counters (written, dropped, ...)
	registerStatsWriter(a.Metrics, a.Stats)

	if a.Assets, err = web.NewAssets(); err != nil {
		return err
	}
	if a.Render, err = web.NewRenderer(a.Assets, a.site, a.Log); err != nil {
		return err
	}
	a.Errors = &web.ErrorPages{R: a.Render}
	a.Router = web.NewRouter(web.NewCSP(a.Cfg.Origin(), a.Cfg.RawOrigin()), a.Cfg.AdminCIDRs, a.Errors)

	if err := registerRoutes(a); err != nil {
		return err
	}
	if a.Auth == nil { // accounts: registerRoutes did not install one
		a.Auth = auth.Anonymous{}
	}

	// observability: instance gauges and the audit stream
	a.Instance = metrics.NewInstance(metrics.InstanceOptions{
		DB: a.DB, Settings: a.Settings, Log: a.Log, DataDir: a.Cfg.DataDir, FilesDir: a.Cfg.FilesDir,
		SecretsDir: a.Cfg.SecretsDir, JobKinds: a.Jobs.Kinds,
	})
	a.Metrics.MustRegister(a.Instance)
	if opts.AuditSink != nil {
		if a.AuditStream, err = obs.NewStream(ctx, a.DB, opts.AuditSink, a.Log); err != nil {
			return err
		}
	}
	httpMetrics := web.NewMetrics(a.Metrics)
	httpMetrics.Principal = a.Obs.Request

	cop := http.NewCrossOriginProtection()
	a.handler = web.Chain(a.Router.Handler(),
		a.IP.Middleware,
		web.Logging(a.Log, httpMetrics),
		web.Recover(a.Log, a.Errors),
		web.SecurityHeaders(a.Cfg.Secure()),
		web.RawHost(a.Cfg.RawURL, a.Cfg.Origin()), // items: the raw origin serves raw content only, anonymously
		web.Authenticate(a.Auth, cop, a.Errors),
		a.keyRateLimit, // keys: per-key request budget (429)
	)
	return nil
}

func (a *App) site(r *http.Request) web.Site {
	s := web.Site{
		Name:        SiteName.Get(a.Settings),
		Host:        a.Cfg.PublicURL.Host,
		OIDCEnabled: a.Cfg.OIDC.Enabled(),
		OIDCName:    a.Cfg.OIDC.Name,
		Values: map[string]any{
			"MailEnabled": a.Cfg.SMTP.Enabled(),
		},
	}
	// accounts: SignupOpen, MailEnabled, PasswordLoginOff, VerifyEmail
	if a.Accounts != nil {
		maps.Copy(s.Values, a.Accounts.SiteValues(r))
	}
	return s
}

// MetricsHandler serves GET /metrics for the metrics listener only, with the
// SNAP_METRICS_TOKEN and SNAP_METRICS_CIDRS checks. The public handler never
// serves it.
func (a *App) MetricsHandler() http.Handler {
	return metrics.Handler(a.Metrics, metrics.Access{Token: a.Cfg.MetricsToken, CIDRs: a.Cfg.MetricsCIDRs})
}

// Handler is the public HTTP handler.
func (a *App) Handler() http.Handler { return a.handler }

// Close releases storage.
func (a *App) Close() error {
	// items: write batched view counters before the database closes.
	if a.Items != nil {
		_ = a.Items.FlushCounters(context.Background())
	}
	// keys: write batched key last-use.
	if a.Keys != nil {
		_ = a.Keys.Flush(context.Background())
	}
	// statsui+notify: end live inbox streams; let secret
	// notifications finish queueing.
	if a.Notify != nil {
		a.Notify.CloseStreams()
		a.Notify.Close()
	}
	if a.Files != nil {
		_ = a.Files.Close()
	}
	if a.secretsStore != nil {
		_ = a.secretsStore.Close()
	}
	// secrets: flush buffered events before the database closes.
	if a.Stats != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = a.Stats.Close(ctx)
		cancel()
	}
	return a.DB.Close()
}

// Run serves until ctx is cancelled, then shuts down gracefully.
func (a *App) Run(ctx context.Context) error {
	// No WriteTimeout: downloads and the notification stream (which ends
	// itself after a few minutes) are long responses. IdleTimeout only
	// applies between requests.
	srv := &http.Server{
		Addr:              a.Cfg.Listen,
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       0, // none server-wide (uploads take long); the router gives other bodies web.BodyReadTimeout
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(a.Log.Handler(), slog.LevelWarn),
	}
	if a.Notify != nil {
		srv.RegisterOnShutdown(a.Notify.CloseStreams) // Shutdown waits for handlers; streams end at once
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", a.Cfg.Listen)
	if err != nil {
		return err
	}
	var msrv *http.Server
	var mln net.Listener
	if a.Cfg.MetricsListen != "" {
		msrv = &http.Server{Addr: a.Cfg.MetricsListen, Handler: a.MetricsHandler(), ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
		if mln, err = lc.Listen(ctx, "tcp", a.Cfg.MetricsListen); err != nil {
			_ = ln.Close()
			return err
		}
	}

	jobsCtx, stopJobs := context.WithCancel(context.WithoutCancel(ctx))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); a.Jobs.Run(jobsCtx) }()
	if a.AuditStream != nil {
		wg.Add(1)
		go func() { defer wg.Done(); a.AuditStream.Run(jobsCtx, time.Second) }()
	}

	errc := make(chan error, 2)
	go func() { errc <- srv.Serve(ln) }()
	if msrv != nil {
		go func() { errc <- msrv.Serve(mln) }()
	}
	a.Log.Info("listening", "addr", a.Cfg.Listen, "metrics", a.Cfg.MetricsListen, "public_url", a.Cfg.PublicURL.String())

	select {
	case <-ctx.Done():
	case err = <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	if msrv != nil {
		_ = msrv.Shutdown(shutdown)
	}
	stopJobs()
	wg.Wait()
	return err
}
