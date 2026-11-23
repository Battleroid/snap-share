package redteam_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

// Design review (re-copying invite links): keeping the raw token sealed so
// admins can copy the link again must not turn it into something a user,
// an API key (even with the admin scope) or a used-up invite hands out.
// Keys never get invite links (admin/keyguard.go), and the sealed copy is
// gone once the invite stops working.
func TestInviteLinkOnlyToAdminSessions(t *testing.T) {
	e := newEnv(t)
	root := e.user("root", auth.RoleAdmin)
	ann := e.user("ann", auth.RoleUser)
	raw, inv, err := e.s.App.Accounts.Store().CreateInvite(e.ctx, accounts.NewInvite{CreatedBy: root.ID, MaxUses: 1, TTL: time.Hour}, accounts.AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/invites/" + strconv.FormatInt(inv.ID, 10) + "/link"
	adminKey := e.mint(root, keys.NewKey{Scopes: auth.AllScopes})
	annKey := e.mint(ann, keys.NewKey{Scopes: []auth.Scope{auth.ScopeItemsRead}})

	for name, as := range map[string]opt{
		"user session": withCookie(e.session(ann)),
		"user key":     bearer(annKey),
		"admin key":    bearer(adminKey),
		"anonymous":    anonymous(),
		"cross-site": func(r *http.Request) {
			r.AddCookie(e.session(root))
			r.Header.Set("Origin", "https://evil.example")
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		},
	} {
		res, body := e.do("POST", path, nil, as, header("Accept", "application/json"))
		if res.StatusCode < 400 || strings.Contains(body, raw) {
			t.Errorf("%s: %d, link in reply: %v", name, res.StatusCode, strings.Contains(body, raw))
		}
	}
	rootC := withCookie(e.session(root))
	if _, page := e.do("GET", "/admin/invites", nil, rootC); strings.Contains(page, raw) {
		t.Error("the invites list carries the link")
	}
	res, m := e.doJSON("POST", path, nil, rootC)
	if res.StatusCode != http.StatusOK || !strings.HasSuffix(m["url"].(string), "#t="+raw) {
		t.Fatalf("admin session: %d", res.StatusCode)
	}

	// Use the invite up; its sealed token goes with the last use.
	res, _ = e.do("POST", "/invite", strings.NewReader(url.Values{"token": {raw}}.Encode()), anonymous(),
		header("Content-Type", "application/x-www-form-urlencoded"), header("Accept", "application/json"))
	var cookie *http.Cookie
	for _, c := range (&http.Response{Header: res.Header}).Cookies() {
		if strings.HasSuffix(c.Name, "snap_invite") {
			cookie = c
		}
	}
	if res.StatusCode != http.StatusOK || cookie == nil {
		t.Fatalf("accept invite: %d", res.StatusCode)
	}
	form := url.Values{"username": {"newbie"}, "email": {"newbie@example.test"}, "password": {"a long enough password"}}
	if res, _ := e.do("POST", "/register", strings.NewReader(form.Encode()), anonymous(), withCookie(cookie),
		header("Content-Type", "application/x-www-form-urlencoded")); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("register: %d", res.StatusCode)
	}
	var left int
	if err := e.s.App.DB.R.QueryRow(`SELECT count(*) FROM invites WHERE id = ? AND token_ct IS NOT NULL`, inv.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("used-up invite still holds its sealed token (%d, %v)", left, err)
	}
	if res, m := e.doJSON("POST", path, nil, rootC); res.StatusCode != http.StatusNotFound || m["url"] != nil {
		t.Errorf("used-up invite: %d %v", res.StatusCode, m)
	}
}
