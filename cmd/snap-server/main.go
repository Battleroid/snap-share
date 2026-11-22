// Command snap-server runs the snap web server and its maintenance commands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/version"
)

const usage = `usage: snap-server [command]

commands:
  serve        run the server (default)
  migrate      apply database migrations and exit
  admin ...    manage users, invites and settings (snap-server admin help)
  backup FILE  write a consistent copy of the database to FILE
  healthcheck  exit 0 if the local server answers /healthz
  version      print the version
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "migrate":
		err = migrate()
	case "admin":
		err = adminCmd(args)
	case "backup":
		err = backup(args)
	case "healthcheck":
		err = healthcheck()
	case "version":
		fmt.Println(version.Version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "snap-server:", err)
		os.Exit(1)
	}
}

func logger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func load() (*config.Config, *slog.Logger, error) {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	return cfg, logger(cfg.LogLevel), nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, log, err := load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.New(ctx, cfg, log, app.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	log.Info("starting", "version", version.Version)
	return a.Run(ctx)
}

func migrate() error {
	cfg, log, err := load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg.DataDir+"/snap.db")
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	applied, err := d.Migrate(ctx)
	for _, m := range applied {
		log.Info("migration applied", "name", m)
	}
	return err
}

func backup(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: snap-server backup FILE")
	}
	cfg, _, err := load()
	if err != nil {
		return err
	}
	if _, err := os.Stat(args[0]); err == nil { // #nosec G703 -- operator-supplied path
		return fmt.Errorf("%s already exists", args[0])
	}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg.DataDir+"/snap.db")
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Backup(ctx, args[0]) // #nosec G703 -- operator-supplied path
}

// healthcheck needs only SNAP_LISTEN, so it works without secrets in the environment.
func healthcheck() error {
	addr := os.Getenv("SNAP_LISTEN")
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil) // #nosec G704 -- loopback only
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req) // #nosec G704 -- loopback only
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", res.Status)
	}
	return nil
}
