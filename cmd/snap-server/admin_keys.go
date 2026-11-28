package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

const createKeyUsage = "usage: snap-server admin create-key --username U --name N [--scopes S,S] [--expires never|7d|30d|90d|1y|YYYY-MM-DD] [--networks CIDR,CIDR]"

// createKey makes an API key for a user, as Settings › API keys would, and
// prints it once. It goes through keys.Validate, so the same scope, role,
// lifetime, network and key-count rules apply. The admin scope is refused:
// in the browser it needs the user's password and second factor.
func (e *adminEnv) createKey(args []string) error {
	fs := flag.NewFlagSet("create-key", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("username", "", "the key's owner")
	name := fs.String("name", "", "what the key is for")
	scopes := fs.String("scopes", "", "comma-separated scopes (default: items:read,items:write,items:delete)")
	expires := fs.String("expires", "never", "never, 7d, 30d, 90d, 1y or a date")
	networks := fs.String("networks", "", "comma-separated addresses or CIDRs the key works from")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("create-key: %w", err)
	}
	if *username == "" || *name == "" || fs.NArg() > 0 {
		return errors.New(createKeyUsage)
	}
	u, err := e.user(*username)
	if err != nil {
		return err
	}
	if !u.Active() {
		return fmt.Errorf("%s is disabled; enable the account first", u.Username)
	}
	in := keys.Input{Name: *name, Expires: *expires}
	if *networks != "" {
		in.AllowedCIDRs = []string{*networks}
	}
	if *scopes == "" {
		for _, s := range keys.DefaultScopes {
			in.Scopes = append(in.Scopes, string(s))
		}
	} else {
		for _, s := range strings.Split(*scopes, ",") {
			if s = strings.TrimSpace(s); s != "" {
				in.Scopes = append(in.Scopes, s)
			}
		}
	}
	if slices.Contains(in.Scopes, string(auth.ScopeAdmin)) {
		return errors.New("create-key: the admin scope needs the user's password and second factor; create that key in Settings, API keys")
	}
	nk, ferrs := keys.Validate(in, u.Role, time.Now())
	if ferrs != nil {
		return fmt.Errorf("create-key: %s", ferrs.Error())
	}
	token, k, err := keys.NewStore(e.db, nil).Create(e.ctx, u.ID, nk, accounts.CLIActor())
	if errors.Is(err, keys.ErrTooManyKeys) {
		return fmt.Errorf("%s has %d active keys; revoke one first", u.Username, keys.MaxActiveKeys)
	}
	if err != nil {
		return err
	}
	names := make([]string, len(k.Scopes))
	for i, s := range k.Scopes {
		names[i] = string(s)
	}
	until := "never expires"
	if k.ExpiresAt != nil {
		until = "expires " + k.ExpiresAt.UTC().Format(time.DateOnly)
	}
	e.printf("Created key %q for %s: %s, %s.\nIt is shown once:\n%s\n", k.Name, u.Username, strings.Join(names, " "), until, token)
	return nil
}
