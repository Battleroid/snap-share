package keys

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
)

func TestValidate(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	ok := Input{Name: "  laptop" + string(rune(0x202e)) + " ", Scopes: []string{"items:write", "items:read", "items:write"}}
	nk, errs := Validate(ok, auth.RoleUser, now)
	if errs != nil {
		t.Fatal(errs)
	}
	if nk.Name != "laptop" || !slices.Equal(nk.Scopes, []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite}) || nk.ExpiresAt != nil {
		t.Fatalf("%+v", nk)
	}

	exp := func(spec string) *time.Time {
		nk, errs := Validate(Input{Name: "k", Scopes: []string{"items:read"}, Expires: spec}, auth.RoleUser, now)
		if errs != nil {
			t.Fatalf("%s: %v", spec, errs)
		}
		return nk.ExpiresAt
	}
	if got := exp("30d"); !got.Equal(now.Add(30 * 24 * time.Hour)) {
		t.Errorf("30d: %v", got)
	}
	if got := exp("1y"); !got.Equal(now.Add(365 * 24 * time.Hour)) {
		t.Errorf("1y: %v", got)
	}
	if got := exp("2027-01-31"); got.Format(time.RFC3339Nano) != "2027-01-31T23:59:59.999Z" {
		t.Errorf("date: %v", got)
	}
	if exp("never") != nil || exp("") != nil {
		t.Error("never has an expiry")
	}

	bad := []struct {
		field string
		in    Input
		role  auth.Role
	}{
		{"name", Input{Scopes: []string{"items:read"}}, auth.RoleUser},
		{"name", Input{Name: strings.Repeat("x", 65), Scopes: []string{"items:read"}}, auth.RoleUser},
		{"scopes", Input{Name: "k"}, auth.RoleUser},
		{"scopes", Input{Name: "k", Scopes: []string{"items:everything"}}, auth.RoleUser},
		{"scopes", Input{Name: "k", Scopes: []string{"admin"}}, auth.RoleUser}, // a user cannot grant admin
		{"expires", Input{Name: "k", Scopes: []string{"items:read"}, Expires: "2020-01-01"}, auth.RoleUser},
		{"expires", Input{Name: "k", Scopes: []string{"items:read"}, Expires: "soon"}, auth.RoleUser},
		{"allowed_cidrs", Input{Name: "k", Scopes: []string{"items:read"}, AllowedCIDRs: []string{"10.0.0.0/33"}}, auth.RoleUser},
		{"allowed_cidrs", Input{Name: "k", Scopes: []string{"items:read"}, AllowedCIDRs: []string{"example.com"}}, auth.RoleUser},
		{"visibility", Input{Name: "k", Scopes: []string{"items:read"}, Constraints: InputConstraints{Visibility: "public"}}, auth.RoleUser},
		{"max_expiry", Input{Name: "k", Scopes: []string{"items:read"}, Constraints: InputConstraints{MaxExpirySeconds: -1}}, auth.RoleUser},
		{"max_upload", Input{Name: "k", Scopes: []string{"items:read"}, Constraints: InputConstraints{MaxUploadBytes: -5}}, auth.RoleUser},
	}
	for _, c := range bad {
		if _, errs := Validate(c.in, c.role, now); errs[c.field] == "" {
			t.Errorf("%+v: no error for %s (%v)", c.in, c.field, errs)
		}
	}
	if _, errs := Validate(Input{Name: "k", Scopes: []string{"admin"}}, auth.RoleAdmin, now); errs != nil {
		t.Errorf("admin granting admin: %v", errs)
	}
}

func TestParseNetworks(t *testing.T) {
	got, msg := parseNetworks([]string{"192.168.1.7/24\n10.0.0.1, 2001:db8::1/48 ::ffff:172.16.0.0/108", "10.0.0.1"})
	if msg != "" {
		t.Fatal(msg)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("10.0.0.1/32"),
		netip.MustParsePrefix("2001:db8::/48"), netip.MustParsePrefix("172.16.0.0/12"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if got, msg := parseNetworks([]string{" \n , "}); msg != "" || got != nil {
		t.Errorf("blank input: %v %q", got, msg)
	}
	var many []string
	for i := range maxCIDRs + 1 {
		many = append(many, netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}).String())
	}
	if _, msg := parseNetworks(many); msg == "" {
		t.Error("too many networks accepted")
	}
	if _, msg := parseNetworks([]string{"fe80::1%eth0"}); msg == "" {
		t.Error("zoned address accepted")
	}
}
