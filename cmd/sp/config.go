package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// clientConfig is stored at $XDG_CONFIG_HOME/snap/config.json (mode 0600).
// SNAP_URL and SNAP_KEY override it, for scripts and CI.
type clientConfig struct { //nolint:unused // used by the sp commands
	URL string `json:"url"`
	Key string `json:"key"`
}

//nolint:unused // used by the sp commands
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snap", "config.json"), nil
}

//nolint:unused // used by the sp commands
func loadConfig() (clientConfig, error) {
	var c clientConfig
	p, err := configPath()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p) // #nosec G304 -- user's own config path
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return c, err
	default:
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %w", p, err)
		}
	}
	if v := os.Getenv("SNAP_URL"); v != "" {
		c.URL = v
	}
	if v := os.Getenv("SNAP_KEY"); v != "" {
		c.Key = v
	}
	if c.URL == "" || c.Key == "" {
		return c, errors.New("not logged in: run sp login <url>, or set SNAP_URL and SNAP_KEY")
	}
	return c, nil
}

//nolint:unused // used by the login command
func saveConfig(c clientConfig) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}
