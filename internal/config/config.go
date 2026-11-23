// Package config loads process configuration from the environment.
//
// Every variable is SNAP_<NAME>. Secrets can instead be read from a file named
// by SNAP_<NAME>_FILE. Runtime-editable options live in the settings package.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the process configuration. It is read once at startup.
type Config struct {
	PublicURL *url.URL // required, e.g. https://share.example.com
	RawURL    *url.URL // optional separate origin for user content

	Listen        string // default :8080
	MetricsListen string // default :9090, empty disables

	DataDir    string // default /data (database lives here)
	FilesDir   string // default <DataDir>/files
	SecretsDir string // default <DataDir>/secrets
	CLIDir     string // default /usr/share/snap/cli; sp builds served at /dl/

	SecretKey         []byte // >= 32 bytes; signs cookies, state and URLs
	SecretKeyPrevious []byte // optional, accepted for verification during rotation

	TrustedProxies  []netip.Prefix
	RateLimitExempt []netip.Prefix
	AdminCIDRs      []netip.Prefix // empty = no network restriction on /admin

	OIDC OIDC
	SMTP SMTP

	LogLevel  slog.Level
	LogFormat string // json (default) or text
	LogFile   string // empty: stdout; otherwise an append-only file, reopened on SIGHUP
	AuditLog  string // off (default), stdout, or a file path (docs/observability.md)

	MetricsToken string         // optional bearer token for the metrics listener
	MetricsCIDRs []netip.Prefix // empty: any address may scrape
}

// OIDC configures single sign-on. Disabled when Issuer is empty.
type OIDC struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	Name          string // button label, e.g. "Gitea"
	AutoProvision bool
	AllowedGroups []string
	AdminGroups   []string
	AdminSubs     []string
	LinkByEmail   bool
}

// Enabled reports whether SSO is configured.
func (o OIDC) Enabled() bool { return o.Issuer != "" }

// SMTP configures outgoing mail. Disabled when Host is empty.
type SMTP struct {
	Host     string
	Port     int
	TLS      string // implicit | starttls | none
	User     string
	Password string
	From     string
}

// Enabled reports whether outgoing mail is configured.
func (s SMTP) Enabled() bool { return s.Host != "" }

// Load reads the configuration using getenv (os.Getenv in production).
func Load(getenv func(string) string) (*Config, error) {
	l := loader{getenv: getenv}
	c := &Config{
		Listen:        l.str("LISTEN", ":8080"),
		MetricsListen: l.metricsListen(),
		DataDir:       l.str("DATA_DIR", "/data"),
	}
	c.PublicURL = l.url("PUBLIC_URL", true)
	c.RawURL = l.url("RAW_URL", false)
	c.FilesDir = l.str("FILES_DIR", filepath.Join(c.DataDir, "files"))
	c.SecretsDir = l.str("SECRETS_DIR", filepath.Join(c.DataDir, "secrets"))
	c.CLIDir = l.str("CLI_DIR", "/usr/share/snap/cli")

	c.SecretKey = []byte(l.secret("SECRET_KEY"))
	if len(c.SecretKey) < 32 {
		l.fail("SNAP_SECRET_KEY must be at least 32 bytes (e.g. openssl rand -base64 48)")
	}
	if prev := l.secret("SECRET_KEY_PREVIOUS"); prev != "" {
		if len(prev) < 32 {
			l.fail("SNAP_SECRET_KEY_PREVIOUS must be at least 32 bytes")
		}
		c.SecretKeyPrevious = []byte(prev)
	}

	c.TrustedProxies = l.prefixes("TRUSTED_PROXIES")
	c.RateLimitExempt = l.prefixes("RATELIMIT_EXEMPT")
	c.AdminCIDRs = l.prefixes("ADMIN_CIDRS")
	c.MetricsToken = l.secret("METRICS_TOKEN")
	c.MetricsCIDRs = l.prefixes("METRICS_CIDRS")
	if c.MetricsToken != "" && len(c.MetricsToken) < 16 {
		l.fail("SNAP_METRICS_TOKEN must be at least 16 characters")
	}

	c.OIDC = OIDC{
		Issuer:        strings.TrimRight(l.str("OIDC_ISSUER", ""), "/"),
		ClientID:      l.str("OIDC_CLIENT_ID", ""),
		ClientSecret:  l.secret("OIDC_CLIENT_SECRET"),
		Name:          l.str("OIDC_NAME", "SSO"),
		AutoProvision: l.boolean("OIDC_AUTO_PROVISION", true),
		AllowedGroups: l.list("OIDC_ALLOWED_GROUPS"),
		AdminGroups:   l.list("OIDC_ADMIN_GROUPS"),
		AdminSubs:     l.list("OIDC_ADMIN_SUBS"),
		LinkByEmail:   l.boolean("OIDC_LINK_BY_EMAIL", false),
	}
	if c.OIDC.Enabled() {
		if _, err := url.Parse(c.OIDC.Issuer); err != nil || !strings.HasPrefix(c.OIDC.Issuer, "https://") {
			l.fail("SNAP_OIDC_ISSUER must be an https URL")
		}
		if c.OIDC.ClientID == "" || c.OIDC.ClientSecret == "" {
			l.fail("SNAP_OIDC_CLIENT_ID and SNAP_OIDC_CLIENT_SECRET are required with SNAP_OIDC_ISSUER")
		}
	}

	c.SMTP = SMTP{
		Host:     l.str("SMTP_HOST", ""),
		Port:     l.integer("SMTP_PORT", 465),
		TLS:      l.str("SMTP_TLS", "implicit"),
		User:     l.str("SMTP_USER", ""),
		Password: l.secret("SMTP_PASSWORD"),
		From:     l.str("SMTP_FROM", ""),
	}
	if c.SMTP.Enabled() {
		switch c.SMTP.TLS {
		case "implicit", "starttls", "none":
		default:
			l.fail("SNAP_SMTP_TLS must be implicit, starttls or none")
		}
		if c.SMTP.From == "" {
			l.fail("SNAP_SMTP_FROM is required with SNAP_SMTP_HOST")
		}
	}

	switch strings.ToLower(l.str("LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info":
		c.LogLevel = slog.LevelInfo
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		l.fail("SNAP_LOG_LEVEL must be debug, info, warn or error")
	}
	c.LogFormat = strings.ToLower(l.str("LOG_FORMAT", "json"))
	if c.LogFormat != "json" && c.LogFormat != "text" {
		l.fail("SNAP_LOG_FORMAT must be json or text")
	}
	c.LogFile = l.str("LOG_FILE", "")
	switch v := l.str("AUDIT_LOG", "off"); strings.ToLower(v) {
	case "off", "none", "false", "0":
		c.AuditLog = "off"
	case "stdout":
		c.AuditLog = "stdout"
	default:
		c.AuditLog = v // a file path, checked when it is opened
	}

	if len(l.errs) > 0 {
		return nil, errors.Join(l.errs...)
	}
	return c, nil
}

// Secure reports whether the public origin is https (cookies get Secure, HSTS is sent).
func (c *Config) Secure() bool { return c.PublicURL.Scheme == "https" }

// Origin returns scheme://host[:port] of the public URL.
func (c *Config) Origin() string { return c.PublicURL.Scheme + "://" + c.PublicURL.Host }

// RawOrigin returns the raw content origin, or "" when user content is served from the public origin.
func (c *Config) RawOrigin() string {
	if c.RawURL == nil {
		return ""
	}
	return c.RawURL.Scheme + "://" + c.RawURL.Host
}

type loader struct {
	getenv func(string) string
	errs   []error
}

func (l *loader) fail(format string, a ...any) { l.errs = append(l.errs, fmt.Errorf(format, a...)) }

func (l *loader) raw(name string) string { return strings.TrimSpace(l.getenv("SNAP_" + name)) }

func (l *loader) str(name, def string) string {
	if v := l.raw(name); v != "" {
		return v
	}
	return def
}

// secret reads SNAP_<name>, or the contents of the file named by SNAP_<name>_FILE.
func (l *loader) secret(name string) string {
	v := l.getenv("SNAP_" + name)
	f := l.raw(name + "_FILE")
	switch {
	case v != "" && f != "":
		l.fail("set only one of SNAP_%s and SNAP_%s_FILE", name, name)
		return ""
	case f != "":
		b, err := os.ReadFile(f) // #nosec G304 -- operator-supplied path
		if err != nil {
			l.fail("SNAP_%s_FILE: %w", name, err)
			return ""
		}
		return strings.TrimRight(string(b), "\r\n")
	default:
		return v
	}
}

func (l *loader) url(name string, required bool) *url.URL {
	v := l.raw(name)
	if v == "" {
		if required {
			l.fail("SNAP_%s is required", name)
		}
		return nil
	}
	u, err := url.Parse(strings.TrimRight(v, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		l.fail("SNAP_%s must be an absolute http(s) URL without query or fragment", name)
		return nil
	}
	if u.Path != "" {
		l.fail("SNAP_%s must not have a path; serve snap from the root of its host", name)
		return nil
	}
	return u
}

// metricsListen is SNAP_METRICS_LISTEN: an address, "off" to disable, or the
// default :9090 when unset. (An empty value can't mean "off": the runtime image
// sets the variable, and an empty override is indistinguishable from unset.)
func (l *loader) metricsListen() string {
	switch v := strings.ToLower(l.raw("METRICS_LISTEN")); v {
	case "":
		return ":9090"
	case "off", "none", "false", "0":
		return ""
	default:
		return l.raw("METRICS_LISTEN")
	}
}

func (l *loader) boolean(name string, def bool) bool {
	v := l.raw(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.fail("SNAP_%s must be true or false", name)
	}
	return b
}

func (l *loader) integer(name string, def int) int {
	v := l.raw(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.fail("SNAP_%s must be an integer", name)
	}
	return n
}

func (l *loader) list(name string) []string {
	var out []string
	for _, f := range strings.Split(l.raw(name), ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func (l *loader) prefixes(name string) []netip.Prefix {
	var out []netip.Prefix
	for _, f := range l.list(name) {
		if !strings.Contains(f, "/") {
			a, err := netip.ParseAddr(f)
			if err != nil {
				l.fail("SNAP_%s: %q is not an address or CIDR", name, f)
				continue
			}
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			l.fail("SNAP_%s: %q is not an address or CIDR", name, f)
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}
