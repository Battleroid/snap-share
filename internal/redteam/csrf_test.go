package redteam_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
)

// TestCrossSiteStateChangesRefused (holds): every non-GET route, sent
// cross-site with an admin's session cookie (as a top-level form post or a
// no-cors fetch would be), gets the CSRF 403 before its handler runs.
func TestCrossSiteStateChangesRefused(t *testing.T) {
	e := newEnv(t)
	admin := e.user("root", auth.RoleAdmin)
	c := e.session(admin)
	param := regexp.MustCompile(`\{[^}]+\}`)
	checked := 0
	for _, rt := range e.s.App.Router.Routes() {
		method, path, ok := strings.Cut(rt.Pattern, " ")
		if !ok || method == "GET" || method == "HEAD" {
			continue
		}
		path = strings.TrimSuffix(param.ReplaceAllString(path, "1"), "{$}")
		for _, site := range []string{"cross-site", "same-site"} {
			res, body := e.do(method, path, strings.NewReader("a=b"), withCookie(c),
				header("Content-Type", "application/x-www-form-urlencoded"),
				header("Sec-Fetch-Site", site), header("Origin", "https://evil.example"))
			if res.StatusCode != http.StatusForbidden || !strings.Contains(body, "Cross-origin") {
				t.Errorf("%s %s (%s): %d %.80q", method, path, site, res.StatusCode, body)
			}
		}
		// Old browser: no Sec-Fetch-Site, foreign Origin.
		res, _ := e.do(method, path, strings.NewReader("a=b"), withCookie(c),
			header("Content-Type", "application/x-www-form-urlencoded"),
			func(r *http.Request) { r.Header.Del("Sec-Fetch-Site"); r.Header.Set("Origin", "https://evil.example") })
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s (Origin only): %d", method, path, res.StatusCode)
		}
		checked++
	}
	t.Logf("%d state-changing routes refuse cross-site requests", checked)
}
