package admin_test

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/admin"
	"github.com/Battleroid/snap-share/internal/auth/oidctest"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// A key no feature defines, to exercise the JSON fallback editor and the
// label and group derived from the name.
var testList = settings.Define("zz.allowed_ports", []int{80}, nil)

func (e *env) stored(key string) string {
	e.t.Helper()
	var v string
	if err := e.s.App.DB.R.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func TestSettingsPageListsEveryKey(t *testing.T) {
	e := newEnv(t)
	res, body := e.get("/admin/settings", as("root"))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("page: %d", res.StatusCode)
	}
	for _, d := range settings.Definitions() {
		if !strings.Contains(body, `name="keys" value="`+d.Name()+`"`) {
			t.Errorf("%s has no control", d.Name())
		}
	}
	for _, want := range []string{
		`data-setting="auth.password_login"`, // bools are Switches
		`<h2>Sign-in</h2>`, `<h2>Zz</h2>`, "Allowed ports · json",
		`value="1 GB"`,    // uploads.max_bytes in human units
		`value="256 MiB"`, // secrets.max_bytes
		`value="30d"`,     // secrets.max_expiry, stored in seconds
		"default 1 GiB",   // storage.min_free_bytes default (the test set it to 0)
		"stays on while no SSO is configured",
		"Email is off.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

// Every registered key, whichever package defines it, reads back what the
// editor shows for it, so saving an untouched form changes nothing.
func TestEveryKeyRoundTrips(t *testing.T) {
	e := newEnv(t) // the test binary links the whole app, so every package's keys are registered
	for _, d := range settings.Definitions() {
		if kind, ok, msg := admin.RoundTrip(d); !ok {
			t.Errorf("%s (%s, default %s): %q", d.Name(), kind, d.DefaultJSON(), msg)
		}
	}
	form := url.Values{}
	_, body := e.get("/admin/settings", as("root"))
	for _, d := range settings.Definitions() {
		form.Add("keys", d.Name())
		if v, ok := fieldValue(body, d.Name()); ok {
			form.Set(d.Name(), v)
		}
	}
	if res, body := e.post("/admin/settings", form, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("saving the page as shown: %d %s", res.StatusCode, body)
	}
	if n := e.audits("settings.change"); n != 0 {
		t.Errorf("saving the page as shown changed %d settings", n)
	}
}

var (
	inputRE    = regexp.MustCompile(`<input name="([^"]+)" id="set-[^"]*" value="([^"]*)"`)
	textareaRE = regexp.MustCompile(`(?s)<textarea name="([^"]+)" id="set-[^"]*"[^>]*>(.*?)</textarea>`)
	selectRE   = regexp.MustCompile(`(?s)<select name="([^"]+)" id="set-[^"]*">.*?<option value="([^"]*)" selected>`)
	switchRE   = regexp.MustCompile(`name="([^"]+)" id="set-[^"]*" data-setting="[^"]*" aria-describedby="[^"]*" checked>`)
)

// fieldValue finds the value the settings page shows for key, as a browser
// would submit it.
func fieldValue(page, key string) (string, bool) {
	for _, re := range []*regexp.Regexp{inputRE, textareaRE, selectRE} {
		for _, m := range re.FindAllStringSubmatch(page, -1) {
			if m[1] == key {
				return html.UnescapeString(m[2]), true
			}
		}
	}
	for _, m := range switchRE.FindAllStringSubmatch(page, -1) {
		if m[1] == key {
			return "on", true
		}
	}
	return "", false
}

// One case per editor kind: a value that is read and stored, and values
// that are refused by the parser or by the key's validator.
func TestSettingsEditorKinds(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		key, value, stored string
		bad                map[string]string // value -> part of the message
	}{
		{"secrets.anonymous", "on", "true", nil},
		{"secrets.max_views", "5", "5", map[string]string{"abc": "Enter a number.", "0": "Must be between 1 and 10000."}},
		{"uploads.max_bytes", "2 GiB", "2147483648", map[string]string{"lots": "Use a size like", "0": "Must be between 1 B and 1 PiB."}},
		{"storage.min_free_bytes", "500 MB", "500000000", map[string]string{"-5 MB": "Use a size like"}},
		{"secrets.max_expiry", "7d", "604800", map[string]string{"soon": "Use a duration", "1m": "Must be between 5m and 3650d."}},
		{"items.max_expiry_days", "30", "30", map[string]string{"1.5x": "Enter a number."}},
		{"auth.signup_mode", "closed", `"closed"`, map[string]string{"nope": "Pick one of closed, invite, open."}},
		{"site.name", "Drop box", `"Drop box"`, map[string]string{"": "Must be 1-64 characters."}},
		{"uploads.blocked_extensions", "exe\n\n bat \n", `["exe","bat"]`, map[string]string{strings.Repeat("x", 200): "Entry"}},
		{"zz.allowed_ports", "[80, 443]", "[80,443]", map[string]string{"nope": "Enter valid JSON.", `["a"]`: "not the right kind of value"}},
	}
	for _, c := range cases {
		before := e.stored(c.key)
		for bad, msg := range c.bad {
			res, body := e.post("/admin/settings", url.Values{"keys": {c.key}, c.key: {bad}}, as("root"))
			if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, msg) || !strings.Contains(body, "Nothing was saved.") {
				t.Errorf("%s=%q: %d, want a message with %q", c.key, bad, res.StatusCode, msg)
			}
			if e.stored(c.key) != before {
				t.Errorf("%s=%q stored", c.key, bad)
			}
		}
		res, _ := e.post("/admin/settings", url.Values{"keys": {c.key}, c.key: {c.value}, "group": {"group-x"}}, as("root"))
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/settings?done=settings#group-x" {
			t.Errorf("%s=%q: %d %q", c.key, c.value, res.StatusCode, res.Header.Get("Location"))
		}
		if got := e.stored(c.key); got != c.stored {
			t.Errorf("%s stored %s, want %s", c.key, got, c.stored)
		}
	}
	if got := testList.Get(e.s.App.Settings); len(got) != 2 || got[1] != 443 {
		t.Errorf("zz.allowed_ports = %v", got)
	}
	if n := e.audits("settings.change"); n != len(cases) {
		t.Errorf("settings.change rows: %d", n)
	}
	// A Switch left off on the form reads as false.
	if res, _ := e.post("/admin/settings", url.Values{"keys": {"secrets.anonymous"}}, as("root")); res.StatusCode != http.StatusSeeOther || e.stored("secrets.anonymous") != "false" {
		t.Errorf("switch off: %d %s", res.StatusCode, e.stored("secrets.anonymous"))
	}
	// Saving what is already there changes nothing.
	before := e.audits("settings.change")
	e.post("/admin/settings", url.Values{"keys": {"secrets.max_views"}, "secrets.max_views": {"5"}}, as("root"))
	if e.audits("settings.change") != before {
		t.Error("unchanged value audited")
	}
	if res, _ := e.post("/admin/settings", url.Values{"keys": {"no.such_key"}, "no.such_key": {"1"}}, as("root")); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("unknown key: %d", res.StatusCode)
	}
}

// A form is saved whole or not at all.
func TestSettingsFormAllOrNothing(t *testing.T) {
	e := newEnv(t)
	form := url.Values{
		"keys":              {"secrets.max_views", "secrets.max_failures", "secrets.anonymous"},
		"secrets.max_views": {"7"}, "secrets.max_failures": {"0"}, "secrets.anonymous": {"on"},
	}
	res, body := e.post("/admin/settings", form, as("root"))
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, `value="0"`) {
		t.Fatalf("mixed form: %d", res.StatusCode)
	}
	for _, k := range []string{"secrets.max_views", "secrets.max_failures", "secrets.anonymous"} {
		if e.stored(k) != "" {
			t.Errorf("%s saved from a refused form", k)
		}
	}
	if e.audits("settings.change") != 0 {
		t.Error("refused form audited")
	}
}

func TestPasswordLoginGuard(t *testing.T) {
	e := newEnv(t)
	if res, body := e.post("/admin/settings", url.Values{"keys": {"auth.password_login"}}, as("root")); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "nobody could sign in") {
		t.Errorf("form without SSO: %d", res.StatusCode)
	}
	res, body := e.post("/admin/settings/apply", url.Values{"key": {"auth.password_login"}, "value": {"false"}}, as("root"))
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "nobody could sign in") {
		t.Errorf("switch without SSO: %d %s", res.StatusCode, body)
	}
	if !accounts.PasswordLogin.Get(e.s.App.Settings) {
		t.Fatal("password sign-in turned off without SSO")
	}
	if res, body := e.post("/admin/settings", url.Values{"keys": {"auth.signup_mode"}, "auth.signup_mode": {"open"}}, as("root")); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "needs SMTP") {
		t.Errorf("open sign-up without SMTP: %d", res.StatusCode)
	}

	p := oidctest.New(t)
	sso := newEnv(t, testutil.WithSMTP(), testutil.OIDCClient(p.Client()), testutil.Env("OIDC_ISSUER", p.URL),
		testutil.Env("OIDC_CLIENT_ID", p.ClientID), testutil.Env("OIDC_CLIENT_SECRET", p.ClientSecret))
	res, body = sso.post("/admin/settings/apply", url.Values{"key": {"auth.password_login"}, "value": {"false"}}, as("root"))
	if res.StatusCode != http.StatusOK || accounts.PasswordLogin.Get(sso.s.App.Settings) {
		t.Errorf("switch with SSO: %d %s", res.StatusCode, body)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(body), &got)
	if got["on"] != false || got["key"] != "auth.password_login" {
		t.Errorf("apply answer %v", got)
	}
	if res, _ := sso.post("/admin/settings", url.Values{"keys": {"auth.signup_mode"}, "auth.signup_mode": {"open"}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Errorf("open sign-up with SMTP: %d", res.StatusCode)
	}
}

func TestApplySetting(t *testing.T) {
	e := newEnv(t)
	res, body := e.post("/admin/settings/apply", url.Values{"key": {"secrets.anonymous"}, "value": {"true"}}, as("root"))
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") || !strings.Contains(body, `"on":true`) {
		t.Fatalf("apply: %d %s", res.StatusCode, body)
	}
	if e.stored("secrets.anonymous") != "true" || e.audits("settings.change") != 1 {
		t.Error("not stored or not audited")
	}
	var details string
	_ = e.s.App.DB.R.QueryRow(`SELECT details FROM audit_log WHERE action = 'settings.change'`).Scan(&details)
	if details != `{"from":false,"to":true}` {
		t.Errorf("audit details %s", details)
	}
	if res, _ := e.post("/admin/settings/apply", url.Values{"key": {"nope"}, "value": {"1"}}, as("root")); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown key: %d", res.StatusCode)
	}
	if res, body := e.post("/admin/settings/apply", url.Values{"key": {"secrets.max_views"}, "value": {"0"}}, as("root")); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, `"error"`) {
		t.Errorf("invalid: %d %s", res.StatusCode, body)
	}
}

func TestTestEmail(t *testing.T) {
	e := newEnv(t, testutil.WithSMTP())
	_, body := e.get("/admin/settings", as("root"))
	if !strings.Contains(body, "Send test email") || !strings.Contains(body, `value="root@example.com"`) {
		t.Fatal("no test email form")
	}
	if res, body := e.post("/admin/settings/test-email", url.Values{"to": {"ops@example.com"}}, as("root")); res.StatusCode != http.StatusOK || !strings.Contains(body, "Test email sent to ops@example.com.") {
		t.Fatalf("send: %d", res.StatusCode)
	}
	sent := e.s.Mail.Sent()
	if len(sent) != 1 || sent[0].To[0] != "ops@example.com" || !strings.Contains(sent[0].Subject, "Test email") {
		t.Fatalf("sent %+v", sent)
	}
	if res, _ := e.post("/admin/settings/test-email", url.Values{"to": {"nope"}}, as("root")); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("bad address: %d", res.StatusCode)
	}

	off := newEnv(t)
	if res, body := off.post("/admin/settings/test-email", url.Values{"to": {"ops@example.com"}}, as("root")); res.StatusCode != http.StatusBadGateway || !strings.Contains(body, "not configured") {
		t.Errorf("send without SMTP: %d", res.StatusCode)
	}
}
