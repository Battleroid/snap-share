package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func base() map[string]string {
	return map[string]string{
		"SNAP_PUBLIC_URL": "https://share.example.com",
		"SNAP_SECRET_KEY": strings.Repeat("k", 32),
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8080" || c.MetricsListen != ":9090" || c.DataDir != "/data" {
		t.Errorf("defaults: %+v", c)
	}
	if c.FilesDir != "/data/files" || c.SecretsDir != "/data/secrets" {
		t.Errorf("dirs: %q %q", c.FilesDir, c.SecretsDir)
	}
	if !c.Secure() || c.Origin() != "https://share.example.com" || c.RawOrigin() != "" {
		t.Errorf("origin: %v %q %q", c.Secure(), c.Origin(), c.RawOrigin())
	}
	if c.OIDC.Enabled() || c.SMTP.Enabled() {
		t.Error("oidc/smtp should be disabled")
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"missing url":  {"SNAP_SECRET_KEY": strings.Repeat("k", 32)},
		"short key":    {"SNAP_PUBLIC_URL": "https://a.example", "SNAP_SECRET_KEY": "short"},
		"url path":     {"SNAP_PUBLIC_URL": "https://a.example/snap", "SNAP_SECRET_KEY": strings.Repeat("k", 32)},
		"bad scheme":   {"SNAP_PUBLIC_URL": "ftp://a.example", "SNAP_SECRET_KEY": strings.Repeat("k", 32)},
		"oidc no id":   merge(base(), map[string]string{"SNAP_OIDC_ISSUER": "https://git.example"}),
		"oidc http":    merge(base(), map[string]string{"SNAP_OIDC_ISSUER": "http://git.example", "SNAP_OIDC_CLIENT_ID": "x", "SNAP_OIDC_CLIENT_SECRET": "y"}),
		"smtp no from": merge(base(), map[string]string{"SNAP_SMTP_HOST": "smtp.example"}),
		"bad cidr":     merge(base(), map[string]string{"SNAP_TRUSTED_PROXIES": "nope"}),
		"both secret":  merge(base(), map[string]string{"SNAP_SMTP_PASSWORD": "a", "SNAP_SMTP_PASSWORD_FILE": "/x"}),
		"bad level":    merge(base(), map[string]string{"SNAP_LOG_LEVEL": "loud"}),
	}
	for name, m := range cases {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestSecretFileAndLists(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "key")
	if err := os.WriteFile(f, []byte(strings.Repeat("s", 40)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := base()
	delete(m, "SNAP_SECRET_KEY")
	m["SNAP_SECRET_KEY_FILE"] = f
	m["SNAP_TRUSTED_PROXIES"] = "172.20.0.0/14, 10.0.0.1"
	m["SNAP_OIDC_ADMIN_GROUPS"] = "ops, ops:admins ,"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if string(c.SecretKey) != strings.Repeat("s", 40) {
		t.Errorf("secret file not trimmed: %q", c.SecretKey)
	}
	if len(c.TrustedProxies) != 2 || c.TrustedProxies[1].Bits() != 32 {
		t.Errorf("prefixes: %v", c.TrustedProxies)
	}
	if len(c.OIDC.AdminGroups) != 2 || c.OIDC.AdminGroups[1] != "ops:admins" {
		t.Errorf("list: %v", c.OIDC.AdminGroups)
	}
}

func merge(a, b map[string]string) map[string]string {
	for k, v := range b {
		a[k] = v
	}
	return a
}
