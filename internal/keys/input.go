package keys

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Battleroid/snap-share/internal/auth"
)

// Input is a key request, from the create form or POST /api/v1/keys.
type Input struct {
	Name         string           `json:"name"`
	Scopes       []string         `json:"scopes"`
	Expires      string           `json:"expires"` // never | 7d | 30d | 90d | 1y | YYYY-MM-DD | RFC 3339
	AllowedCIDRs []string         `json:"allowed_cidrs"`
	Constraints  InputConstraints `json:"constraints"`
}

// InputConstraints are the requested restrictions.
type InputConstraints struct {
	Visibility       string `json:"visibility"`
	MaxExpirySeconds int64  `json:"max_expiry_seconds"`
	MaxUploadBytes   int64  `json:"max_upload_bytes"`
}

// choice is one option of a form select.
type choice struct {
	Value, Label string
	Secs         int64 // for expiry caps
}

// Key lifetimes offered in the form.
var keyExpiries = []choice{
	{"never", "never", 0}, {"7d", "7 days", 0}, {"30d", "30 days", 0}, {"90d", "90 days", 0}, {"1y", "1 year", 0}, {"date", "on a date", 0},
}

var keyDurations = map[string]time.Duration{
	"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour, "1y": 365 * 24 * time.Hour,
}

// Expiry caps offered for what a key creates.
var maxExpiries = []choice{
	{"", "no cap", 0}, {"1h", "1 hour", 3600}, {"1d", "1 day", 86400}, {"7d", "7 days", 7 * 86400},
	{"30d", "30 days", 30 * 86400}, {"90d", "90 days", 90 * 86400}, {"1y", "1 year", 365 * 86400},
}

// Visibilities a key can force ("" forces nothing).
var forcedVisibilities = []choice{{"", "any", 0}, {"unlisted", "unlisted", 0}, {"private", "private", 0}}

// Limits on what a request may ask for.
const (
	maxNameLen   = 64
	maxCIDRs     = 32
	maxExpiryCap = 100 * 365 * 24 * 3600 // seconds
)

// DefaultScopes are preselected in the create form.
var DefaultScopes = []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite, auth.ScopeItemsDelete}

// FieldErrors maps an input field to a sentence describing what is wrong.
type FieldErrors map[string]string

func (e FieldErrors) Error() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	msgs := make([]string, len(keys))
	for i, k := range keys {
		msgs[i] = e[k]
	}
	return strings.Join(msgs, " ")
}

func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	return s
}

// parseNetworks reads addresses and CIDRs separated by newlines, commas or spaces.
func parseNetworks(list []string) ([]netip.Prefix, string) {
	var out []netip.Prefix
	for _, item := range list {
		for _, f := range strings.FieldsFunc(item, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
			var p netip.Prefix
			if strings.Contains(f, "/") {
				var err error
				if p, err = netip.ParsePrefix(f); err != nil {
					return nil, fmt.Sprintf("%q is not an address or CIDR.", f)
				}
			} else {
				a, err := netip.ParseAddr(f)
				if err != nil || a.Zone() != "" {
					return nil, fmt.Sprintf("%q is not an address or CIDR.", f)
				}
				p = netip.PrefixFrom(a, a.BitLen())
			}
			// Client addresses are compared unmapped, so store IPv4-mapped networks as IPv4.
			if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
			}
			p = p.Masked()
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	if len(out) > maxCIDRs {
		return nil, fmt.Sprintf("Use at most %d networks.", maxCIDRs)
	}
	return out, ""
}

// parseKeyExpiry resolves a key lifetime at now (nil = never).
func parseKeyExpiry(spec string, now time.Time) (*time.Time, string) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "never" {
		return nil, ""
	}
	var t time.Time
	if d, ok := keyDurations[spec]; ok {
		t = now.Add(d)
	} else if day, err := time.Parse(time.DateOnly, spec); err == nil {
		t = day.Add(24*time.Hour - time.Millisecond) // through the end of that day (UTC)
	} else if t, err = time.Parse(time.RFC3339, spec); err != nil {
		return nil, "Expiry must be never, 7d, 30d, 90d, 1y or a date."
	}
	if !t.After(now) {
		return nil, "That date is in the past."
	}
	t = t.UTC().Truncate(time.Millisecond)
	return &t, ""
}

// Validate checks in for an owner with role and returns the key to create,
// or the problems field by field.
func Validate(in Input, role auth.Role, now time.Time) (NewKey, FieldErrors) {
	errs := FieldErrors{}
	nk := NewKey{Name: cleanName(in.Name)}
	switch n := len([]rune(nk.Name)); {
	case n == 0:
		errs["name"] = "Give the key a name."
	case n > maxNameLen:
		errs["name"] = fmt.Sprintf("Use at most %d characters.", maxNameLen)
	}
	for _, name := range in.Scopes {
		sc := auth.Scope(strings.TrimSpace(name))
		switch {
		case !sc.Valid():
			errs["scopes"] = fmt.Sprintf("%q is not a scope.", name)
		case !auth.RoleAllows(role, sc):
			errs["scopes"] = fmt.Sprintf("Your account cannot grant %s.", sc)
		case !slices.Contains(nk.Scopes, sc):
			nk.Scopes = append(nk.Scopes, sc)
		}
	}
	if len(nk.Scopes) == 0 && errs["scopes"] == "" {
		errs["scopes"] = "Pick at least one scope."
	}
	// Keep display order regardless of request order.
	slices.SortFunc(nk.Scopes, func(a, b auth.Scope) int {
		return slices.Index(auth.AllScopes, a) - slices.Index(auth.AllScopes, b)
	})
	var msg string
	if nk.ExpiresAt, msg = parseKeyExpiry(in.Expires, now); msg != "" {
		errs["expires"] = msg
	}
	if nk.CIDRs, msg = parseNetworks(in.AllowedCIDRs); msg != "" {
		errs["allowed_cidrs"] = msg
	}
	c := in.Constraints
	switch c.Visibility {
	case "", "unlisted", "private":
		nk.Constraints.Visibility = c.Visibility
	default:
		errs["visibility"] = "Visibility must be unlisted or private."
	}
	if c.MaxExpirySeconds < 0 || c.MaxExpirySeconds > maxExpiryCap {
		errs["max_expiry"] = "That expiry cap is out of range."
	} else {
		nk.Constraints.MaxExpirySeconds = c.MaxExpirySeconds
	}
	if c.MaxUploadBytes < 0 {
		errs["max_upload"] = "The upload limit must not be negative."
	} else {
		nk.Constraints.MaxUploadBytes = c.MaxUploadBytes
	}
	if len(errs) > 0 {
		return NewKey{}, errs
	}
	return nk, nil
}
