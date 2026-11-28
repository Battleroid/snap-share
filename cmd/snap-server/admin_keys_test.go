package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/keys"
)

// create-key prints a working key once, through the same rules as the
// browser, records it as made from the CLI, and refuses the admin scope,
// unknown users, disabled users and bad input.
func TestAdminCLICreateKey(t *testing.T) {
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
		err := runAdmin(ctx, cfg, log, args, strings.NewReader(""), &out)
		return out.String(), err
	}
	for _, u := range []string{"ann", "dee"} {
		if _, err := run("create-user", "--username", u); err != nil {
			t.Fatal(err)
		}
	}
	token := regexp.MustCompile(`(?m)^snap_[A-Za-z0-9_]+$`)

	out, err := run("create-key", "--username", "ann", "--name", "claude code")
	if err != nil || !strings.Contains(out, `Created key "claude code" for ann: items:read items:write items:delete, never expires.`) {
		t.Fatalf("create-key: %q %v", out, err)
	}
	full := token.FindString(out)
	if len(full) != keys.TokenLen {
		t.Fatalf("no key printed: %q", out)
	}
	out, err = run("create-key", "--username", "ann", "--name", "upload", "--scopes", "items:write", "--expires", "30d", "--networks", "192.0.2.0/24, 2001:db8::1")
	if err != nil || !strings.Contains(out, ": items:write, expires "+time.Now().Add(30*24*time.Hour).UTC().Format(time.DateOnly)+".") {
		t.Fatalf("create-key with options: %q %v", out, err)
	}
	limited := token.FindString(out)

	for args, want := range map[string]string{
		"--username nobody --name x":                        "no user named",
		"--username ann":                                    "usage:",
		"--name x":                                          "usage:",
		"--username ann --name x --scopes admin":            "admin scope",
		"--username ann --name x --scopes items:read,admin": "admin scope",
		"--username ann --name x --scopes everything":       "create-key:",
		"--username ann --name x --expires soon":            "Expiry must be",
		"--username ann --name x --networks not-an-ip":      "not an address",
		"--username ann --name x extra":                     "usage:",
	} {
		if _, err := run(append([]string{"create-key"}, strings.Fields(args)...)...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("create-key %s: %v, want %q", args, err, want)
		}
	}

	d, err := db.Open(ctx, filepath.Join(dir, "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	store := keys.NewStore(d, nil)
	lookup := func(tok string) *keys.Key {
		t.Helper()
		pub, _, ok := keys.ParseToken(tok)
		if !ok {
			t.Fatalf("printed key %q does not parse", tok)
		}
		k, err := store.ByPublicID(ctx, pub)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	k := lookup(full)
	if k.Name != "claude code" || k.ExpiresAt != nil || !slices.Equal(k.Scopes, keys.DefaultScopes) || k.RevokedAt != nil {
		t.Errorf("full key: %+v", k)
	}
	if k = lookup(limited); !slices.Equal(k.Scopes, []auth.Scope{auth.ScopeItemsWrite}) || k.ExpiresAt == nil || len(k.CIDRs) != 2 {
		t.Errorf("limited key: %+v", k)
	}
	var audited, made int
	_ = d.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'key.create' AND details LIKE '%"via":"cli"%' AND details LIKE '%"name":"claude code"%'`).Scan(&audited)
	_ = d.R.QueryRow(`SELECT count(*) FROM api_keys`).Scan(&made)
	if audited != 1 || made != 2 {
		t.Errorf("audited %d, keys %d (refused requests must make none)", audited, made)
	}
	if _, err := d.W.Exec(`UPDATE users SET status = 'disabled' WHERE username = 'dee'`); err != nil {
		t.Fatal(err)
	}
	if _, err := run("create-key", "--username", "dee", "--name", "x"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("disabled user: %v", err)
	}
}
