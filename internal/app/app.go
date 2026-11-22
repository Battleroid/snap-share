// Package app wires configuration, storage and HTTP handlers into a server.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/settings"
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

	handler http.Handler
}

// Options overrides dependencies (tests, and tracks not yet merged).
type Options struct {
	Auth auth.Authenticator
	Mail mail.Sender
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
	a.Jobs = jobs.New(a.DB, a.Log)

	a.Mail = opts.Mail
	if a.Mail == nil {
		a.Mail = mail.Log{L: a.Log}
	}
	a.Auth = opts.Auth
	if a.Auth == nil {
		a.Auth = auth.Anonymous{}
	}

	a.Metrics = prometheus.NewRegistry()
	a.Metrics.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

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

	cop := http.NewCrossOriginProtection()
	a.handler = web.Chain(a.Router.Handler(),
		a.IP.Middleware,
		web.Logging(a.Log, web.NewMetrics(a.Metrics)),
		web.Recover(a.Log, a.Errors),
		web.SecurityHeaders(a.Cfg.Secure()),
		web.Authenticate(a.Auth, cop, a.Errors),
	)
	return nil
}

func (a *App) site(r *http.Request) web.Site {
	return web.Site{
		Name:        SiteName.Get(a.Settings),
		OIDCEnabled: a.Cfg.OIDC.Enabled(),
		OIDCName:    a.Cfg.OIDC.Name,
		Values: map[string]any{
			"MailEnabled": a.Cfg.SMTP.Enabled(),
		},
	}
}

// Handler is the public HTTP handler.
func (a *App) Handler() http.Handler { return a.handler }

// Close releases storage.
func (a *App) Close() error { return a.DB.Close() }

// Run serves until ctx is cancelled, then shuts down gracefully.
func (a *App) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              a.Cfg.Listen,
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(a.Log.Handler(), slog.LevelWarn),
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", a.Cfg.Listen)
	if err != nil {
		return err
	}
	var metrics *http.Server
	var mln net.Listener
	if a.Cfg.MetricsListen != "" {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", promhttp.HandlerFor(a.Metrics, promhttp.HandlerOpts{}))
		metrics = &http.Server{Addr: a.Cfg.MetricsListen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		if mln, err = lc.Listen(ctx, "tcp", a.Cfg.MetricsListen); err != nil {
			_ = ln.Close()
			return err
		}
	}

	jobsCtx, stopJobs := context.WithCancel(context.WithoutCancel(ctx))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); a.Jobs.Run(jobsCtx) }()

	errc := make(chan error, 2)
	go func() { errc <- srv.Serve(ln) }()
	if metrics != nil {
		go func() { errc <- metrics.Serve(mln) }()
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
	if metrics != nil {
		_ = metrics.Shutdown(shutdown)
	}
	stopJobs()
	wg.Wait()
	return err
}
