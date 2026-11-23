package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Battleroid/snap-share/internal/blob/local"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/media"
	"github.com/Battleroid/snap-share/internal/mediaworker"
	"github.com/Battleroid/snap-share/internal/obs"
	"github.com/Battleroid/snap-share/internal/version"
)

// mediaWorker runs `snap-server media-worker [healthcheck]`: the ffmpeg
// steps of media processing, served to snap over a Unix socket or http
// (docs/media.md#media-worker).
func mediaWorker(args []string) error {
	switch {
	case len(args) == 1 && args[0] == "healthcheck":
		return mediaWorkerHealthcheck()
	case len(args) > 0:
		return errors.New("usage: snap-server media-worker [healthcheck]")
	}
	cfg, err := config.LoadWorker(os.Getenv)
	if err != nil {
		return err
	}
	log := slog.New(obs.NewHandler(cfg.LogFormat, cfg.LogLevel, os.Stdout))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	files, err := local.OpenReadOnly(cfg.FilesDir)
	if err != nil {
		return err
	}
	defer func() { _ = files.Close() }()
	srv, err := mediaworker.NewServer(mediaworker.ServerConfig{
		Files: files, Scratch: filepath.Join(os.TempDir(), "snap-media"), Token: cfg.Token, Log: log,
	})
	if err != nil {
		return err
	}
	// Listen first, so snap sees "starting" rather than an absent worker
	// while ffmpeg is checked.
	ln, err := mediaworker.Listen(cfg.Network, cfg.Address)
	if err != nil {
		return err
	}
	hs := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       time.Minute,
		// No WriteTimeout: a request may run for mediaworker.DefaultRequestTimeout.
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	go func() { served <- hs.Serve(ln) }()

	// The container's cgroup and ulimits bound ffmpeg, so no rlimit helper.
	runner, err := media.NewRunner(media.Config{Direct: true, MaxParallel: cfg.Parallel})
	if err != nil {
		_ = hs.Close()
		return err
	}
	ffmpeg := mediaworker.FFmpegVersion(ctx, "")
	srv.Start(runner, ffmpeg)
	log.Info("media worker ready", "version", version.Version, "protocol", mediaworker.Protocol,
		"ffmpeg", ffmpeg, "network", cfg.Network, "parallel", cfg.Parallel)

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(sctx)
}

// mediaWorkerHealthcheck exits 0 if the worker answers /v1/health with this
// binary's protocol. It is the worker container's healthcheck.
func mediaWorkerHealthcheck() error {
	cfg, err := config.LoadWorker(os.Getenv)
	if err != nil {
		return err
	}
	url := "unix://" + cfg.Address
	if cfg.Network == "tcp" {
		host, port, err := net.SplitHostPort(cfg.Address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			host = "127.0.0.1"
		}
		url = "http://" + net.JoinHostPort(host, port)
	}
	c, err := mediaworker.NewClient(mediaworker.ClientConfig{URL: url, Token: cfg.Token})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h, err := c.Health(ctx)
	if err != nil {
		return err
	}
	if h.Protocol != mediaworker.Protocol {
		return fmt.Errorf("media worker speaks protocol %d, want %d", h.Protocol, mediaworker.Protocol)
	}
	return nil
}
