package keys_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/web"
)

// Public API routes check their own credentials in the handler. Every other
// /api/ route must declare a scope, so a key without it is refused by the router.
var publicAPI = map[string]string{
	"POST /api/v1/delete/lookup": "deletion token in the body; rate limited per address",
	"POST /api/v1/delete":        "deletion token in the body; rate limited per address",
}

// Keys management is for browser sessions only; the handler refuses keys.
var sessionAPI = map[string]bool{
	"GET /api/v1/keys":         true,
	"POST /api/v1/keys":        true,
	"DELETE /api/v1/keys/{id}": true,
}

var wildcard = regexp.MustCompile(`\{[^}]+\}`)

// TestAPIRoutesEnforceScopes walks every registered /api/ route: a key that
// lacks the route's scope gets 403 from the router, one that holds only that
// scope gets past it.
func TestAPIRoutesEnforceScopes(t *testing.T) {
	e := newEnv(t)
	root := e.user("root", auth.RoleAdmin)
	without := map[auth.Scope]string{}
	only := map[auth.Scope]string{}
	for _, sc := range auth.AllScopes {
		var rest []auth.Scope
		for _, o := range auth.AllScopes {
			if o != sc {
				rest = append(rest, o)
			}
		}
		without[sc] = e.mint(root, keys.NewKey{Name: "without " + string(sc), Scopes: rest})
		only[sc] = e.mint(root, keys.NewKey{Name: "only " + string(sc), Scopes: []auth.Scope{sc}})
	}
	everything := e.mint(root, keys.NewKey{Name: "all", Scopes: auth.AllScopes})

	checked := 0
	for _, rt := range e.s.App.Router.Routes() {
		method, path, ok := strings.Cut(rt.Pattern, " ")
		if !ok || !strings.HasPrefix(path, "/api/") {
			continue
		}
		url := wildcard.ReplaceAllString(path, "1")
		switch rt.Access {
		case web.AccessScope, web.AccessAdmin:
			checked++
			res, body := e.do(method, url, nil, bearer(without[rt.Scope]))
			if res.StatusCode != http.StatusForbidden {
				t.Errorf("%s without %s: %d %s", rt.Pattern, rt.Scope, res.StatusCode, body)
			} else if method != http.MethodHead && !strings.Contains(body, string(rt.Scope)) && !strings.Contains(body, "Admins only") {
				t.Errorf("%s without %s: 403 for another reason: %s", rt.Pattern, rt.Scope, body)
			}
			res, body = e.do(method, url, nil, bearer(only[rt.Scope]))
			refused := res.StatusCode == http.StatusUnauthorized ||
				(res.StatusCode == http.StatusForbidden && (method == http.MethodHead || strings.Contains(body, "permission") || strings.Contains(body, "Admins only")))
			if refused {
				t.Errorf("%s with only %s: %d %s", rt.Pattern, rt.Scope, res.StatusCode, body)
			}
		case web.AccessUser:
			checked++
			if !sessionAPI[rt.Pattern] {
				t.Errorf("%s: signed-in API route without a scope; give it web.Scope or list it here", rt.Pattern)
				continue
			}
			if res, body := e.do(method, url, nil, bearer(everything)); res.StatusCode != http.StatusForbidden {
				t.Errorf("%s with a key: %d %s (keys must not manage keys)", rt.Pattern, res.StatusCode, body)
			}
		case web.AccessPublic:
			if _, ok := publicAPI[rt.Pattern]; !ok {
				t.Errorf("%s: public API route; check credentials in the handler and document it in publicAPI", rt.Pattern)
			}
		}
	}
	if checked < 15 {
		t.Fatalf("only %d API routes checked; did the routes move?", checked)
	}
}

func TestKeysAPI(t *testing.T) {
	e := newEnv(t)
	ann := e.user("ann", auth.RoleUser)
	bob := e.user("bob", auth.RoleUser)
	sess := withCookie(e.session(ann))

	res, m := e.doJSON("POST", "/api/v1/keys", map[string]any{
		"name": "ci", "scopes": []string{"items:write", "account:read"}, "expires": "30d",
		"allowed_cidrs": []string{"127.0.0.1", "::1"}, "constraints": map[string]any{"visibility": "private", "max_upload_bytes": 5000},
	}, sess)
	if res.StatusCode != http.StatusCreated || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d %v", res.StatusCode, m)
	}
	token, _ := m["token"].(string)
	key, _ := m["key"].(map[string]any)
	if _, _, ok := keys.ParseToken(token); !ok || key["status"] != "active" || key["expires_at"] == nil ||
		!strings.HasPrefix(token, key["prefix"].(string)+"_") {
		t.Fatalf("created %v", m)
	}
	id := int64(key["id"].(float64))

	// The token works, with the key's scopes and limits.
	res, me := e.doJSON("GET", "/api/v1/me", nil, bearer(token))
	if res.StatusCode != 200 || me["username"] != "ann" {
		t.Fatalf("me: %d %v", res.StatusCode, me)
	}
	k := me["key"].(map[string]any)
	if fmt.Sprint(k["scopes"]) != "[items:write account:read]" || me["quota"].(map[string]any)["max_upload_bytes"].(float64) != 5000 {
		t.Errorf("me key %v quota %v", k, me["quota"])
	}

	// Listing never shows a secret.
	res, body := e.do("GET", "/api/v1/keys", nil, sess)
	if res.StatusCode != 200 || strings.Contains(body, token[18:]) || !strings.Contains(body, `"name":"ci"`) {
		t.Fatalf("list: %d %s", res.StatusCode, body)
	}

	// Validation errors name the field.
	res, m = e.doJSON("POST", "/api/v1/keys", map[string]any{"name": "", "scopes": []string{"admin"}}, sess)
	fields, _ := m["fields"].(map[string]any)
	if res.StatusCode != 400 || fields["name"] == nil || fields["scopes"] == nil {
		t.Errorf("invalid create: %d %v", res.StatusCode, m)
	}

	// Keys never manage keys, whatever their scopes.
	all := e.mint(ann, keys.NewKey{Scopes: auth.AllScopes[:8]})
	for _, c := range []struct{ method, path string }{{"GET", "/api/v1/keys"}, {"POST", "/api/v1/keys"}, {"DELETE", fmt.Sprintf("/api/v1/keys/%d", id)}} {
		res, m := e.doJSON(c.method, c.path, map[string]any{"name": "x", "scopes": []string{"items:read"}}, bearer(all))
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with a key: %d %v", c.method, c.path, res.StatusCode, m)
		}
	}

	// Cross-site cookie requests are refused before the handler (CSRF).
	res, m = e.doJSON("POST", "/api/v1/keys", map[string]any{"name": "evil", "scopes": []string{"items:read"}}, sess, crossSite())
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site create: %d %v", res.StatusCode, m)
	}
	res, _ = e.doJSON("DELETE", fmt.Sprintf("/api/v1/keys/%d", id), nil, sess, crossSite())
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site revoke: %d", res.StatusCode)
	}
	if list, _ := e.s.App.Keys.Store().List(e.ctx, ann.ID); len(list) != 2 || list[1].RevokedAt != nil {
		t.Fatalf("cross-site requests changed keys: %d", len(list))
	}

	// Another user cannot revoke it; the owner can, and it stops working at once.
	if res, _ := e.doJSON("DELETE", fmt.Sprintf("/api/v1/keys/%d", id), nil, withCookie(e.session(bob))); res.StatusCode != 404 {
		t.Errorf("bob revoking ann's key: %d", res.StatusCode)
	}
	if res, _ := e.doJSON("DELETE", fmt.Sprintf("/api/v1/keys/%d", id), nil, sess); res.StatusCode != 204 {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if res, m := e.doJSON("GET", "/api/v1/me", nil, bearer(token)); res.StatusCode != 401 || m["error"] != "Invalid credentials." {
		t.Errorf("revoked key: %d %v", res.StatusCode, m)
	}
}

func TestBearerErrors(t *testing.T) {
	e := newEnv(t)
	ann := e.user("ann", auth.RoleUser)
	tok := e.mint(ann, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAccountRead}})
	outside := e.mint(ann, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAccountRead}, CIDRs: mustPrefixes("192.0.2.0/24")})
	inside := e.mint(ann, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAccountRead}, CIDRs: mustPrefixes("127.0.0.0/8", "::1/128")})
	revoked := e.mint(ann, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAccountRead}})
	pub, secret, _ := keys.ParseToken(tok)
	_, otherSecret, _ := keys.ParseToken(outside)
	if err := e.s.App.Keys.Store().Revoke(e.ctx, ann.ID, keyID(t, e, ann.ID, revoked), accountsActor()); err != nil {
		t.Fatal(err)
	}

	// Unknown, wrong, revoked and malformed keys look the same from outside.
	bodies := map[string]bool{}
	for name, bad := range map[string]string{
		"wrong secret": keys.WithChecksum("snap_" + pub + "_" + otherSecret),
		"checksum":     flipAt(tok, 67),
		"unknown":      keys.WithChecksum("snap_ZZZZZZZZZZZZ_" + secret),
		"revoked":      revoked,
		"garbage":      "nope",
	} {
		res, body := e.do("GET", "/api/v1/me", nil, bearer(bad))
		if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s: %d %s", name, res.StatusCode, body)
		}
		bodies[body] = true
	}
	if len(bodies) != 1 {
		t.Errorf("error bodies differ: %v", bodies)
	}

	if res, body := e.do("GET", "/api/v1/me", nil, bearer(outside)); res.StatusCode != 401 || !strings.Contains(body, "not allowed from this address") {
		t.Errorf("outside network: %d %s", res.StatusCode, body)
	}
	if res, body := e.do("GET", "/api/v1/me", nil, bearer(inside)); res.StatusCode != 200 {
		t.Errorf("inside network: %d %s", res.StatusCode, body)
	}

	// A bearer header never falls back to the session cookie.
	sess := withCookie(e.session(ann))
	if res, _ := e.do("GET", "/api/v1/me", nil, sess, bearer("nope")); res.StatusCode != 401 {
		t.Errorf("bad bearer with a good cookie: %d", res.StatusCode)
	}

	// Disabling the owner ends the key; demoting an admin ends its admin scope.
	if err := e.s.App.Accounts.Store().SetStatus(e.ctx, ann.ID, "disabled", accountsActor()); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.do("GET", "/api/v1/me", nil, bearer(tok)); res.StatusCode != 401 {
		t.Errorf("disabled owner: %d", res.StatusCode)
	}
	root := e.user("root", auth.RoleAdmin)
	adm := e.mint(root, keys.NewKey{Scopes: []auth.Scope{auth.ScopeAdmin, auth.ScopeAccountRead}})
	if res, _ := e.do("GET", "/_kit", nil, bearer(adm)); res.StatusCode != 200 {
		t.Fatalf("admin key on an admin page: %d", res.StatusCode)
	}
	if err := e.s.App.Accounts.Store().SetRole(e.ctx, root.ID, auth.RoleUser, accountsActor()); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.do("GET", "/_kit", nil, bearer(adm)); res.StatusCode != 403 {
		t.Errorf("demoted admin's key on an admin page: %d", res.StatusCode)
	}
	res, me := e.doJSON("GET", "/api/v1/me", nil, bearer(adm))
	if res.StatusCode != 200 || slices.Contains(toStrings(me["key"].(map[string]any)["scopes"]), "admin") {
		t.Errorf("demoted scopes: %v", me["key"])
	}
}

func TestMeWithSession(t *testing.T) {
	e := newEnv(t)
	ann := e.user("ann", auth.RoleUser)
	res, me := e.doJSON("GET", "/api/v1/me", nil, withCookie(e.session(ann)))
	if res.StatusCode != 200 || me["username"] != "ann" || me["role"] != "user" || me["email_verified"] != true || me["key"] != nil {
		t.Fatalf("%d %v", res.StatusCode, me)
	}
	q := me["quota"].(map[string]any)
	if q["max_upload_bytes"].(float64) != 1e9 || q["used_bytes"].(float64) != 0 {
		t.Errorf("quota %v", q)
	}
	// account:read is required for keys.
	tok := e.mint(ann, keys.NewKey{Scopes: []auth.Scope{auth.ScopeItemsRead}})
	if res, body := e.do("GET", "/api/v1/me", nil, bearer(tok)); res.StatusCode != 403 || !strings.Contains(body, "account:read") {
		t.Errorf("without account:read: %d %s", res.StatusCode, body)
	}
}

func mustPrefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(list))
	for i, s := range list {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

func accountsActor() accounts.AuditEntry { return accounts.AuditEntry{} }

// keyID finds the id of u's key tok.
func keyID(t *testing.T, e *env, uid int64, tok string) int64 {
	t.Helper()
	pub, _, _ := keys.ParseToken(tok)
	k, err := e.s.App.Keys.Store().ByPublicID(e.ctx, pub)
	if err != nil || k.UserID != uid {
		t.Fatalf("key %s: %v", pub, err)
	}
	return k.ID
}

func flipAt(s string, i int) string {
	c := byte('A')
	if s[i] == 'A' {
		c = 'B'
	}
	return s[:i] + string(c) + s[i+1:]
}

func toStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, x := range l {
		out = append(out, fmt.Sprint(x))
	}
	return out
}
