package admin_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestUsersAPI(t *testing.T) {
	e := newEnv(t)
	e.paste("ann", "hello")
	res, out := e.json("GET", "/api/v1/admin/users?q=ann", nil, bearer("root-key"))
	users, _ := out["users"].([]any)
	if res.StatusCode != http.StatusOK || out["total"] != float64(1) || len(users) != 1 {
		t.Fatalf("list: %d %v", res.StatusCode, out)
	}
	ann := users[0].(map[string]any)
	if ann["username"] != "ann" || ann["items"] != float64(1) || ann["quota_bytes"] != nil || ann["email_verified"] != true {
		t.Errorf("ann: %v", ann)
	}
	if res, _ := e.json("GET", "/api/v1/admin/users?limit=0", nil, bearer("root-key")); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad limit: %d", res.StatusCode)
	}

	// A key never gets a set-password link (no mail here, so it can't be
	// sent either), and can't give the admin role; a browser session can.
	res, out = e.json("POST", "/api/v1/admin/users", map[string]any{"username": "carl", "email": "carl@example.com", "role": "user", "quota_bytes": 5000}, bearer("root-key"))
	if res.StatusCode != http.StatusForbidden || out["link"] != nil {
		t.Fatalf("create with a key: %d %v", res.StatusCode, out)
	}
	if res, _ := e.json("POST", "/api/v1/admin/users", map[string]any{"username": "carl2", "role": "admin"}, bearer("root-key")); res.StatusCode != http.StatusForbidden {
		t.Fatalf("create an admin with a key: %d", res.StatusCode)
	}
	res, out = e.json("POST", "/api/v1/admin/users", map[string]any{"username": "carl", "email": "carl@example.com", "role": "user", "quota_bytes": 5000}, as("root"))
	if res.StatusCode != http.StatusCreated || out["emailed"] != false || !strings.Contains(out["link"].(string), "/reset#t=") {
		t.Fatalf("create: %d %v", res.StatusCode, out)
	}
	carl := out["user"].(map[string]any)
	if carl["quota_bytes"] != float64(5000) {
		t.Errorf("created with %v", carl)
	}
	id := itoa(int64(carl["id"].(float64)))
	if res, out := e.json("POST", "/api/v1/admin/users", map[string]any{"username": "carl"}, as("root")); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(out["error"].(string), "taken") {
		t.Errorf("duplicate: %d %v", res.StatusCode, out)
	}
	if res, _ := e.json("POST", "/api/v1/admin/users", map[string]any{"username": "dave", "nope": 1}, bearer("root-key")); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field: %d", res.StatusCode)
	}

	if res, _ := e.json("PATCH", "/api/v1/admin/users/"+id, map[string]any{"role": "admin"}, bearer("root-key")); res.StatusCode != http.StatusForbidden {
		t.Fatalf("promote with a key: %d", res.StatusCode)
	}
	res, out = e.json("PATCH", "/api/v1/admin/users/"+id, map[string]any{"role": "admin", "max_upload_bytes": 2048, "quota_bytes": nil}, as("root"))
	if res.StatusCode != http.StatusOK || out["role"] != "admin" || out["max_upload_bytes"] != float64(2048) || out["quota_bytes"] != nil {
		t.Fatalf("patch: %d %v", res.StatusCode, out)
	}
	res, out = e.json("PATCH", "/api/v1/admin/users/"+id, map[string]any{"status": "disabled"}, bearer("root-key"))
	if res.StatusCode != http.StatusOK || out["status"] != "disabled" || out["max_upload_bytes"] != float64(2048) {
		t.Fatalf("patch status: %d %v", res.StatusCode, out)
	}
	for _, bad := range []map[string]any{{"role": "owner"}, {"status": "gone"}, {"quota_bytes": "lots"}, {"max_upload_bytes": 0}, {"username": "x"}} {
		if res, _ := e.json("PATCH", "/api/v1/admin/users/"+id, bad, bearer("root-key")); res.StatusCode != http.StatusUnprocessableEntity && res.StatusCode != http.StatusBadRequest {
			t.Errorf("patch %v: %d", bad, res.StatusCode)
		}
	}
	if res, _ := e.json("GET", "/api/v1/admin/users/999", nil, bearer("root-key")); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown: %d", res.StatusCode)
	}

	if res, out := e.json("POST", "/api/v1/admin/users/"+id+"/reset", nil, bearer("root-key")); res.StatusCode != http.StatusForbidden || out["link"] != nil {
		t.Errorf("reset with a key: %d %v", res.StatusCode, out)
	}
	res, out = e.json("POST", "/api/v1/admin/users/"+id+"/reset", nil, as("root"))
	if res.StatusCode != http.StatusOK || !strings.Contains(out["link"].(string), "/reset#t=") {
		t.Errorf("reset: %d %v", res.StatusCode, out)
	}

	res, out = e.json("DELETE", "/api/v1/admin/users/"+itoa(e.ann.ID), nil, bearer("root-key"))
	if res.StatusCode != http.StatusOK || out["deleted"] != true || out["items"] != float64(1) {
		t.Fatalf("delete: %d %v", res.StatusCode, out)
	}
	if _, err := e.s.App.Accounts.Store().User(e.ctx, e.ann.ID); err == nil {
		t.Error("ann still there")
	}

	// Changes made with a key are audited with the key.
	var key int64
	_ = e.s.App.DB.R.QueryRow(`SELECT coalesce(actor_key_id, 0) FROM audit_log WHERE action = 'user.delete'`).Scan(&key)
	if key != 7 {
		t.Errorf("audited key %d", key)
	}
}

func TestBurnAPI(t *testing.T) {
	logs := newSyncBuffer()
	e := newEnv(t, testutil.LogTo(logs))
	link, id := e.secret("ann", "api burn")
	res, out := e.json("POST", "/api/v1/admin/secrets/burn", map[string]any{"link": link}, bearer("root-key"))
	if res.StatusCode != http.StatusOK || out["state"] != "revoked" || out["id"] != id {
		t.Fatalf("burn by link: %d %v", res.StatusCode, out)
	}
	if strings.Contains(logs.String(), link[strings.Index(link, "#")+1:]) {
		t.Error("key logged")
	}
	_, id2 := e.secret("ann", "api burn 2")
	if res, out := e.json("POST", "/api/v1/admin/secrets/burn", map[string]any{"id": id2}, bearer("root-key")); res.StatusCode != http.StatusOK || out["state"] != "revoked" {
		t.Errorf("burn by id: %d %v", res.StatusCode, out)
	}
	if res, _ := e.json("POST", "/api/v1/admin/secrets/burn", map[string]any{"id": "AAAAAAAAAAAAAAAAAAAAAA"}, bearer("root-key")); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown: %d", res.StatusCode)
	}
	if res, _ := e.json("POST", "/api/v1/admin/secrets/burn", map[string]any{"link": "https://x/nope"}, bearer("root-key")); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("garbage: %d", res.StatusCode)
	}
	if res, _ := e.json("POST", "/api/v1/admin/secrets/burn", map[string]any{"id": id2}, bearer("root-nokey")); res.StatusCode != http.StatusForbidden {
		t.Errorf("without admin scope: %d", res.StatusCode)
	}
}

func TestAuditAPI(t *testing.T) {
	e := newEnv(t)
	if err := e.s.App.Accounts.Store().SetRole(e.ctx, e.ann.ID, "admin", accounts.Actor(e.s.Request(t, "GET", "/", nil))); err != nil {
		t.Fatal(err)
	}
	res, out := e.json("GET", "/api/v1/admin/audit?action=user.", nil, bearer("root-key"))
	entries, _ := out["entries"].([]any)
	if res.StatusCode != http.StatusOK || len(entries) != 3 || out["total"] != float64(3) { // two user.create, one user.role_change
		t.Fatalf("prefix: %d %v", res.StatusCode, out)
	}
	first := entries[0].(map[string]any)
	if first["action"] != "user.role_change" || first["details"].(map[string]any)["to"] != "admin" {
		t.Errorf("newest first, with details: %v", first)
	}
	if res, out := e.json("GET", "/api/v1/admin/audit?action=user.create&limit=1", nil, bearer("root-key")); res.StatusCode != http.StatusOK || len(out["entries"].([]any)) != 1 || out["total"] != float64(2) {
		t.Errorf("limit: %v", out)
	}
	if res, _ := e.json("GET", "/api/v1/admin/audit?to=soon", nil, bearer("root-key")); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad date: %d", res.StatusCode)
	}
}

func TestOverview(t *testing.T) {
	e := newEnv(t)
	e.paste("ann", "one")
	e.paste("ann", "two")
	e.secret("ann", "s")
	if err := e.s.App.Accounts.Store().SetStatus(e.ctx, e.ann.ID, accounts.StatusDisabled, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	res, body := e.get("/admin", as("root"))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("overview: %d", res.StatusCode)
	}
	for _, want := range []string{
		`users · active</span><span class="pb-tile__value">1</span><span class="pb-tile__delta">1 disabled`,
		`items</span><span class="pb-tile__value">2</span>`,
		`active secrets</span><span class="pb-tile__value">1</span>`,
		`uploads · 30d</span><span class="pb-tile__value">2</span><span class="pb-tile__delta">2 today`,
		`<svg class="pb-tile__bg" viewBox="0 0 200 48"`,
		"free on disk",
		"user.status",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
}
