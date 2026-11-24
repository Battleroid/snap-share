package main

import (
	"bufio"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/settings"
)

const adminUsage = `usage: snap-server admin <command>

commands:
  create-user --username U [--email E] [--admin] [--password-stdin]
                             without a password, prints a link to set one
  set-role U admin|user
  reset-password U           prints a one-time link to set a new password
  reset-2fa --username U     removes U's passkeys, authenticator app and
                             recovery codes and signs U out everywhere
  invite [--label L] [--email E] [--uses N] [--expires 7d] [--admin]
                             prints an invite link
  invite list [--all]        active invites (--all: also used, expired and
                             revoked ones); never prints links
  invite revoke ID           stops an invite from working
  settings list
  settings get KEY
  settings set KEY JSON      e.g. settings set auth.signup_mode '"open"'

The command opens the database directly; it is safe while the server runs.
`

// adminCmd runs `snap-server admin ...` against the configured database.
func adminCmd(args []string) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return runAdmin(context.Background(), cfg, log, args, os.Stdin, os.Stdout)
}

type adminEnv struct {
	cfg  *config.Config
	db   *db.DB
	set  *settings.Store
	acc  *accounts.Service
	in   io.Reader
	out  io.Writer
	ctx  context.Context
	user func(name string) (*accounts.User, error)
}

func runAdmin(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprint(out, adminUsage)
		return nil
	}
	d, err := db.Open(ctx, filepath.Join(cfg.DataDir, "snap.db"))
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	if _, err := d.Migrate(ctx); err != nil {
		return err
	}
	st, err := settings.Open(ctx, d)
	if err != nil {
		return err
	}
	var sender mail.Sender
	if cfg.SMTP.Enabled() {
		sender = mail.NewSMTP(cfg.SMTP) // reset-2fa emails the user
	}
	acc, err := accounts.New(accounts.Deps{Cfg: cfg, Log: log, DB: d, Settings: st, Mail: sender,
		SiteName: func() string { return app.SiteName.Get(st) }})
	if err != nil {
		return err
	}
	e := &adminEnv{cfg: cfg, db: d, set: st, acc: acc, in: in, out: out, ctx: ctx}
	e.user = func(name string) (*accounts.User, error) {
		u, err := acc.Store().UserByUsername(ctx, name)
		if errors.Is(err, accounts.ErrNotFound) {
			return nil, fmt.Errorf("no user named %q", name)
		}
		return u, err
	}
	switch args[0] {
	case "create-user":
		return e.createUser(args[1:])
	case "set-role":
		return e.setRole(args[1:])
	case "reset-password":
		return e.resetPassword(args[1:])
	case "reset-2fa":
		return e.resetMFA(args[1:])
	case "invite":
		return e.invite(args[1:])
	case "settings":
		return e.settings(args[1:])
	}
	return fmt.Errorf("unknown admin command %q (see snap-server admin help)", args[0])
}

func (e *adminEnv) printf(format string, a ...any) { _, _ = fmt.Fprintf(e.out, format, a...) }

func (e *adminEnv) createUser(args []string) error {
	fs := flag.NewFlagSet("create-user", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("username", "", "username")
	email := fs.String("email", "", "email address (treated as verified)")
	isAdmin := fs.Bool("admin", false, "make the user an admin")
	fromStdin := fs.Bool("password-stdin", false, "read the password from standard input")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("create-user: %w", err)
	}
	if *username == "" || fs.NArg() > 0 {
		return errors.New("usage: snap-server admin create-user --username U [--email E] [--admin] [--password-stdin]")
	}
	nu := accounts.NewUser{Username: *username, Role: auth.RoleUser}
	if *isAdmin {
		nu.Role = auth.RoleAdmin
	}
	if *email != "" {
		addr, err := accounts.NormalizeEmail(*email)
		if err != nil {
			return err
		}
		nu.Email, nu.EmailVerified = addr, true
	}
	if *fromStdin {
		pw, err := readPassword(e.in)
		if err != nil {
			return err
		}
		if nu.PasswordHash, err = auth.HashPassword(e.ctx, pw); err != nil {
			return err
		}
	}
	u, err := e.acc.Store().CreateUser(e.ctx, nu, accounts.CLIActor())
	if err != nil {
		return err
	}
	e.printf("Created %s (%s).\n", u.Username, u.Role)
	if !u.HasPassword() {
		link, err := e.acc.ResetLink(e.ctx, u.ID, accounts.AdminResetTTL)
		if err != nil {
			return err
		}
		e.printf("Set a password within 24 hours, once:\n%s\n", link)
	}
	return nil
}

func readPassword(in io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(bufio.NewReader(in), auth.MaxPasswordLen+2))
	if err != nil {
		return "", err
	}
	pw := strings.TrimRight(string(b), "\r\n")
	if err := auth.CheckPassword(pw); err != nil {
		return "", err
	}
	return pw, nil
}

func (e *adminEnv) setRole(args []string) error {
	if len(args) != 2 || (args[1] != string(auth.RoleAdmin) && args[1] != string(auth.RoleUser)) {
		return errors.New("usage: snap-server admin set-role USERNAME admin|user")
	}
	u, err := e.user(args[0])
	if err != nil {
		return err
	}
	if err := e.acc.Store().SetRole(e.ctx, u.ID, auth.Role(args[1]), accounts.CLIActor()); err != nil {
		return err
	}
	e.printf("%s is now %s.\n", u.Username, args[1])
	return nil
}

func (e *adminEnv) resetPassword(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: snap-server admin reset-password USERNAME")
	}
	u, err := e.user(args[0])
	if err != nil {
		return err
	}
	link, err := e.acc.ResetLink(e.ctx, u.ID, accounts.AdminResetTTL)
	if err != nil {
		return err
	}
	if err := e.acc.Store().Audit(e.ctx, accounts.AuditEntry{Action: "user.reset_link", Target: fmt.Sprintf("user:%d", u.ID), Details: map[string]any{"via": "cli"}}); err != nil {
		return err
	}
	e.printf("Reset link for %s (works once, for 24 hours):\n%s\n", u.Username, link)
	return nil
}

// resetMFA removes a user's second factors for an owner who lost them.
func (e *adminEnv) resetMFA(args []string) error {
	fs := flag.NewFlagSet("reset-2fa", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("username", "", "username")
	if err := fs.Parse(args); err != nil || *username == "" || fs.NArg() > 0 {
		return errors.New("usage: snap-server admin reset-2fa --username U")
	}
	u, err := e.user(*username)
	if err != nil {
		return err
	}
	mailed, err := e.acc.ResetMFA(e.ctx, u, accounts.CLIActor())
	if err != nil {
		return err
	}
	e.printf("Two-factor authentication reset for %s. They are signed out everywhere.\n", u.Username)
	if mailed {
		e.printf("Emailed %s.\n", u.Email)
	}
	return nil
}

func (e *adminEnv) invite(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return e.inviteList(args[1:])
		case "revoke":
			return e.inviteRevoke(args[1:])
		}
	}
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	label := fs.String("label", "", "a note on who the invite is for")
	email := fs.String("email", "", "address the invite is for (prefilled at sign-up)")
	uses := fs.Int("uses", 1, "how many accounts the link can create")
	expires := fs.String("expires", "7d", "lifetime, e.g. 12h, 7d, 30d")
	isAdmin := fs.Bool("admin", false, "accounts created with it are admins")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New("usage: snap-server admin invite [--label L] [--email E] [--uses N] [--expires 7d] [--admin] | list [--all] | revoke ID")
	}
	ttl, err := parseLifetime(*expires)
	if err != nil {
		return err
	}
	ni := accounts.NewInvite{Label: *label, Email: *email, MaxUses: *uses, TTL: ttl, Role: auth.RoleUser}
	if *isAdmin {
		ni.Role = auth.RoleAdmin
	}
	raw, inv, err := e.acc.Store().CreateInvite(e.ctx, ni, accounts.CLIActor())
	if err != nil {
		return err
	}
	if accounts.SignupMode.Get(e.set) == accounts.SignupClosed {
		e.printf("Note: sign-up is closed, so the link works only after: snap-server admin settings set auth.signup_mode '\"invite\"'\n")
	}
	e.printf("Invite (%d use%s, expires %s):\n%s\n", inv.MaxUses, plural(inv.MaxUses), inv.ExpiresAt.Format("2006-01-02 15:04 MST"), e.acc.Link("/invite", raw))
	return nil
}

// inviteList prints invites, newest first, without their links.
func (e *adminEnv) inviteList(args []string) error {
	fs := flag.NewFlagSet("invite list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	all := fs.Bool("all", false, "also used, expired and revoked invites")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return errors.New("usage: snap-server admin invite list [--all]")
	}
	st := e.acc.Store()
	list, err := st.ListActiveInvites(e.ctx, 1000)
	if *all {
		list, err = st.ListInvites(e.ctx, 1000)
	}
	if err != nil {
		return err
	}
	if len(list) == 0 {
		e.printf("No invites.\n")
		return nil
	}
	now := time.Now()
	tw := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tLABEL\tEMAIL\tROLE\tUSES\tEXPIRES\tBY")
	for _, inv := range list {
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d/%d\t%s\t%s\n", inv.ID, inv.Status(now), dashed(inv.Label), dashed(inv.Email), inv.Role,
			inv.Uses, inv.MaxUses, inv.ExpiresAt.UTC().Format("2006-01-02 15:04"), dashed(cmp.Or(inv.CreatedByName, "cli")))
	}
	return tw.Flush()
}

// inviteRevoke stops one invite from working.
func (e *adminEnv) inviteRevoke(args []string) error {
	var id int64
	var err error
	if len(args) == 1 {
		id, err = strconv.ParseInt(args[0], 10, 64)
	}
	if len(args) != 1 || err != nil || id < 1 {
		return errors.New("usage: snap-server admin invite revoke ID (see invite list)")
	}
	err = e.acc.Store().RevokeInvite(e.ctx, id, accounts.CLIActor())
	if errors.Is(err, accounts.ErrNotFound) {
		return fmt.Errorf("no invite %d that is not already revoked", id)
	}
	if err != nil {
		return err
	}
	e.printf("Invite %d revoked.\n", id)
	return nil
}

func dashed(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// parseLifetime accepts Go durations plus a day suffix ("7d").
func parseLifetime(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 1 {
			return 0, fmt.Errorf("bad lifetime %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("bad lifetime %q", s)
	}
	return d, nil
}

func (e *adminEnv) storedSettings() (map[string]string, error) {
	rows, err := e.db.R.QueryContext(e.ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (e *adminEnv) settings(args []string) error {
	usage := errors.New("usage: snap-server admin settings list | get KEY | set KEY JSON")
	if len(args) == 0 {
		return usage
	}
	stored, err := e.storedSettings()
	if err != nil {
		return err
	}
	switch {
	case args[0] == "list" && len(args) == 1:
		for _, d := range settings.Definitions() {
			v, ok := stored[d.Name()]
			if !ok {
				e.printf("%s = %s (default)\n", d.Name(), d.DefaultJSON())
				continue
			}
			e.printf("%s = %s\n", d.Name(), v)
		}
		return nil
	case args[0] == "get" && len(args) == 2:
		d, ok := settings.Lookup(args[1])
		if !ok {
			return fmt.Errorf("unknown setting %q (see settings list)", args[1])
		}
		if v, ok := stored[d.Name()]; ok {
			e.printf("%s\n", v)
		} else {
			e.printf("%s\n", d.DefaultJSON())
		}
		return nil
	case args[0] == "set" && len(args) == 3:
		d, ok := settings.Lookup(args[1])
		if !ok {
			return fmt.Errorf("unknown setting %q (see settings list)", args[1])
		}
		raw := []byte(args[2])
		if !json.Valid(raw) {
			return fmt.Errorf("%s: value must be JSON, e.g. '\"text\"', true or 42", args[1])
		}
		if err := d.SetJSON(e.ctx, e.set, raw); err != nil {
			return err
		}
		err := e.db.Tx(e.ctx, func(tx *sql.Tx) error {
			return accounts.AuditTx(e.ctx, tx, accounts.AuditEntry{Action: "settings.change", Target: d.Name(),
				Details: map[string]any{"from": json.RawMessage(orDefault(stored, d)), "to": json.RawMessage(raw), "via": "cli"}})
		})
		if err != nil {
			return err
		}
		e.printf("Saved %s. A running server picks it up within a minute.\n", d.Name())
		return nil
	}
	return usage
}

func orDefault(stored map[string]string, d settings.Definition) string {
	if v, ok := stored[d.Name()]; ok {
		return v
	}
	return d.DefaultJSON()
}
