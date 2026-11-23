package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
)

func TestAdminCLI(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		"SNAP_PUBLIC_URL": "https://snap.example",
		"SNAP_SECRET_KEY": strings.Repeat("s", 32),
		"SNAP_DATA_DIR":   dir,
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		err := runAdmin(context.Background(), cfg, log, args, strings.NewReader(stdin), &out)
		return out.String(), err
	}
	link := regexp.MustCompile(`https://snap\.example/(reset|invite)#t=[A-Za-z0-9_-]{43}`)

	out, err := run("a long admin password\n", "create-user", "--username", "root", "--email", "root@example.com", "--admin", "--password-stdin")
	if err != nil || !strings.Contains(out, "Created root (admin).") || link.MatchString(out) {
		t.Fatalf("create admin: %q %v", out, err)
	}
	if _, err := run("short\n", "create-user", "--username", "x", "--password-stdin"); err == nil {
		t.Error("short password accepted")
	}
	if _, err := run("", "create-user", "--username", "root"); err == nil {
		t.Error("duplicate username accepted")
	}
	out, err = run("", "create-user", "--username", "bob")
	if err != nil || !strings.Contains(out, "Created bob (user).") || !link.MatchString(out) {
		t.Fatalf("create without password: %q %v", out, err)
	}
	if out, err = run("", "set-role", "bob", "admin"); err != nil || out != "bob is now admin.\n" {
		t.Errorf("set-role: %q %v", out, err)
	}
	if _, err := run("", "set-role", "nobody", "admin"); err == nil || !strings.Contains(err.Error(), "no user named") {
		t.Errorf("unknown user: %v", err)
	}
	if _, err := run("", "set-role", "bob", "root"); err == nil {
		t.Error("bad role accepted")
	}
	if out, err = run("", "reset-password", "bob"); err != nil || !strings.Contains(out, "/reset#t=") {
		t.Errorf("reset-password: %q %v", out, err)
	}
	if out, err = run("", "invite", "--uses", "3", "--expires", "2d", "--email", "guest@example.com"); err != nil || !strings.Contains(out, "/invite#t=") || !strings.Contains(out, "3 uses") {
		t.Errorf("invite: %q %v", out, err)
	}
	if _, err := run("", "invite", "--expires", "soon"); err == nil {
		t.Error("bad expiry accepted")
	}

	if out, err = run("", "settings", "set", "auth.signup_mode", `"open"`); err != nil || !strings.Contains(out, "Saved") {
		t.Errorf("settings set: %q %v", out, err)
	}
	if out, _ = run("", "settings", "get", "auth.signup_mode"); out != "\"open\"\n" {
		t.Errorf("settings get: %q", out)
	}
	if out, _ = run("", "settings", "get", "auth.password_login"); out != "true\n" {
		t.Errorf("default: %q", out)
	}
	if out, _ = run("", "settings", "list"); !strings.Contains(out, `auth.signup_mode = "open"`) || !strings.Contains(out, "site.name = \"snap\" (default)") {
		t.Errorf("settings list: %q", out)
	}
	for _, bad := range [][]string{{"settings", "set", "auth.signup_mode", `"bogus"`}, {"settings", "set", "auth.signup_mode", "open"}, {"settings", "set", "no.such", "1"}, {"settings", "get"}} {
		if _, err := run("", bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}

	// Everything landed in the database the server uses, and was audited.
	d, err := db.Open(context.Background(), filepath.Join(dir, "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	st := accounts.NewStore(d)
	root, err := st.UserByUsername(context.Background(), "root")
	if err != nil || root.Role != auth.RoleAdmin || root.EmailVerifiedAt == nil {
		t.Fatalf("root %+v %v", root, err)
	}
	if ok, _, _ := auth.VerifyPassword(context.Background(), root.PasswordHash, "a long admin password"); !ok {
		t.Error("password from stdin not stored")
	}
	var n int
	_ = d.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action IN ('user.create', 'user.role_change', 'invite.create', 'settings.change', 'user.reset_link')`).Scan(&n)
	if n != 6 {
		t.Errorf("audit rows %d", n)
	}
	if out, _ := run("", "help"); !strings.Contains(out, "create-user") {
		t.Error("help")
	}
}

func TestAdminCLIResetMFA(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	env := map[string]string{
		"SNAP_PUBLIC_URL": "https://snap.example",
		"SNAP_SECRET_KEY": strings.Repeat("s", 32),
		"SNAP_DATA_DIR":   dir,
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runAdmin(ctx, cfg, log, args, strings.NewReader("a long user password\n"), &out)
		return out.String(), err
	}
	if _, err := run("create-user", "--username", "bob", "--email", "bob@example.com", "--password-stdin"); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(ctx, filepath.Join(dir, "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	st := accounts.NewStore(d)
	bob, err := st.UserByUsername(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.Exec(`INSERT INTO user_totp (user_id, secret, created_at) VALUES (?, x'00', 0)`, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReplaceRecoveryCodes(ctx, bob.ID, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	if err := d.Tx(ctx, func(tx *sql.Tx) error {
		_, err := accounts.NewSessions(d).Create(ctx, tx, bob.ID, "127.0.0.1", "test")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if out, err := run("reset-2fa", "--username", "bob"); err != nil || !strings.Contains(out, "Two-factor authentication reset for bob.") {
		t.Fatalf("reset-2fa: %q %v", out, err)
	}
	if f, err := st.Factors(ctx, bob.ID); err != nil || f.Any() || f.RecoveryLeft != 0 {
		t.Errorf("factors after reset-2fa: %+v %v", f, err)
	}
	var sessions, audited int
	_ = d.R.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, bob.ID).Scan(&sessions)
	_ = d.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'user.mfa_reset' AND details LIKE '%"via":"cli"%'`).Scan(&audited)
	if sessions != 0 || audited != 1 {
		t.Errorf("%d sessions left, %d audit rows", sessions, audited)
	}
	if _, err := run("reset-2fa"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("no username: %v", err)
	}
	if _, err := run("reset-2fa", "--username", "nobody"); err == nil || !strings.Contains(err.Error(), "no user named") {
		t.Errorf("unknown user: %v", err)
	}
	if out, _ := run("help"); !strings.Contains(out, "reset-2fa --username U") {
		t.Error("help lacks reset-2fa")
	}
}
