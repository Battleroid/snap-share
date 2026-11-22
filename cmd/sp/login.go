package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func init() {
	register(command{name: "login", summary: "save the server URL and an API key", run: runLogin})
}

// runLogin: sp login [url]. It asks for the URL (unless given) and a key,
// checks them with GET /api/v1/me, and saves the config with mode 0600.
// Keys are created in the browser at <url>/settings/keys.
func runLogin(ctx context.Context, args []string) error {
	e := envFrom(ctx)
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() {
		fprintf(e.stderr, "%s\n", "usage: sp login [url]\n\nReads the API key from the prompt, or from stdin when it is not a terminal.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return errors.New("too many arguments")
	}
	in := bufio.NewReader(e.stdin)
	interactive := isTerminal(e.stdin)
	raw := fs.Arg(0)
	if raw == "" {
		var err error
		if raw, err = prompt(e, in, interactive, "Server URL: "); err != nil {
			return err
		}
	}
	base, err := normalizeBase(raw)
	if err != nil {
		return err
	}
	if strings.HasPrefix(base, "http://") {
		fprintf(e.stderr, "%s\n", "warning: http sends your key unencrypted")
	}
	key, err := prompt(e, in, interactive, "API key (create one at "+base+"/settings/keys): ")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(key, "snap_") {
		return errors.New("that is not a snap API key (they start with snap_)")
	}
	c := &client{base: base, key: key, hc: e.http, env: e}
	var who me
	err = c.call(ctx, http.MethodGet, "/api/v1/me", nil, &who)
	var ae *apiError
	switch {
	case err == nil:
	case errors.As(err, &ae) && ae.status == http.StatusForbidden && strings.Contains(ae.msg, "account:read"):
		// A valid key without account:read: fine for uploads, but whoami won't work.
		who.Username = ""
	default:
		return fmt.Errorf("checking the key: %w", err)
	}
	if err := saveConfig(clientConfig{URL: base, Key: key}); err != nil {
		return fmt.Errorf("saving the config: %w", err)
	}
	path, _ := configPath()
	if who.Username != "" {
		fprintf(e.stderr, "Logged in to %s as %s. Saved %s\n", base, who.Username, path)
	} else {
		fprintf(e.stderr, "Logged in to %s (the key has no account:read, so sp whoami is unavailable). Saved %s\n", base, path)
	}
	return nil
}

// prompt reads one line; the prompt is shown only on a terminal.
func prompt(e *env, in *bufio.Reader, interactive bool, label string) (string, error) {
	if interactive {
		fprintf(e.stderr, "%s", label)
	}
	line, err := in.ReadString('\n')
	switch {
	case err == nil, errors.Is(err, io.EOF) && line != "": // a last line without a newline is fine
	case errors.Is(err, io.EOF):
		return "", errors.New("no input")
	default:
		return "", err
	}
	return strings.TrimSpace(line), nil
}
