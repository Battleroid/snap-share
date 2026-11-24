package admin_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/testutil"
)

var resetLink = regexp.MustCompile(`https://[^\s"]+/reset#t=[A-Za-z0-9_-]{43}`)

func TestUsersList(t *testing.T) {
	e := newEnv(t)
	e.paste("ann", "hello there")
	if _, err := e.s.App.Accounts.Store().CreateUser(e.ctx, accounts.NewUser{Username: "zed"}, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	res, body := e.get("/admin/users", as("root"))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list: %d", res.StatusCode)
	}
	for _, want := range []string{`data-user="ann"`, `data-user="root"`, `data-user="zed"`, "ann@example.com", "verified", `<span class="pb-person__meta">you</span>`, "/admin/users/" + itoa(e.ann.ID) + "#delete-user"} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	// Reset password and Disable ask first (the confirm partial), and the menu forms keep the way back.
	for _, want := range []string{`data-confirm="Reset password for ann?"`, `data-confirm="Disable ann?"`, `name="status" value="disabled"`} {
		if !strings.Contains(body, want) {
			t.Errorf("row menu lacks %q", want)
		}
	}
	// Delete is type-to-confirm: the list links to the user page, which holds the dialog.
	_, body = e.get("/admin/users/"+itoa(e.ann.ID), as("root"))
	for _, want := range []string{"Delete ann?", "with 1 item and 0 secrets", `data-confirm-text="ann"`} {
		if !strings.Contains(body, want) {
			t.Errorf("user page lacks %q", want)
		}
	}
	_, body = e.get("/admin/users?q=AN", as("root"))
	if !strings.Contains(body, `data-user="ann"`) || strings.Contains(body, `data-user="zed"`) {
		t.Error("search by username")
	}
	_, body = e.get("/admin/users?q=root%40example", as("root"))
	if !strings.Contains(body, `data-user="root"`) || strings.Contains(body, `data-user="ann"`) {
		t.Error("search by email")
	}
	// LIKE wildcards are matched literally.
	if _, body = e.get("/admin/users?q=%25", as("root")); !strings.Contains(body, "No users match") {
		t.Error("% matched everything")
	}
}

func TestCreateUserWithoutMail(t *testing.T) {
	e := newEnv(t)
	res, body := e.post("/admin/users", url.Values{"username": {"bob"}, "email": {"bob@example.com"}, "role": {"user"}}, as("root"))
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "Created bob.") || !strings.Contains(body, "Copy the link now") {
		t.Fatalf("create: %d", res.StatusCode)
	}
	link := resetLink.FindString(body)
	if link == "" {
		t.Fatal("no set-password link shown")
	}
	u, err := e.s.App.Accounts.Store().UserByUsername(e.ctx, "bob")
	if err != nil || u.Role != auth.RoleUser || u.Email != "bob@example.com" || !u.EmailVerified() || u.HasPassword() {
		t.Fatalf("stored %+v %v", u, err)
	}
	token := strings.SplitN(link, "#t=", 2)[1]
	if _, err := e.s.App.Accounts.Store().PeekToken(e.ctx, token, accounts.TokenReset); err != nil {
		t.Errorf("link token: %v", err)
	}
	// Forms on the page answered by the POST return to the user's page.
	if !strings.Contains(body, `name="back" value="/admin/users/`+itoa(u.ID)+`"`) {
		t.Error("forms return to the POST URL")
	}
	if e.audits("user.create") != 3 || e.audits("user.reset_link") != 1 { // root and ann, then bob
		t.Errorf("audits: create %d reset %d", e.audits("user.create"), e.audits("user.reset_link"))
	}
	// The link is not shown again.
	if _, page := e.get("/admin/users/"+itoa(u.ID), as("root")); strings.Contains(page, token) {
		t.Error("link shown again")
	}

	for name, form := range map[string]url.Values{
		"bad username": {"username": {"-x"}},
		"taken":        {"username": {"BOB"}},
		"bad email":    {"username": {"carl"}, "email": {"nope"}},
		"email in use": {"username": {"carl"}, "email": {"ann@example.com"}},
		"bad role":     {"username": {"carl"}, "role": {"owner"}},
	} {
		if res, _ := e.post("/admin/users", form, as("root")); res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, res.StatusCode)
		}
	}

	// An admin, without an email address.
	res, body = e.post("/admin/users", url.Values{"username": {"ops"}, "role": {"admin"}}, as("root"))
	if res.StatusCode != http.StatusCreated || resetLink.FindString(body) == "" {
		t.Fatalf("create admin: %d", res.StatusCode)
	}
	if u, _ := e.s.App.Accounts.Store().UserByUsername(e.ctx, "ops"); u.Role != auth.RoleAdmin {
		t.Error("not an admin")
	}
}

func TestCreateUserWithMail(t *testing.T) {
	e := newEnv(t, testutil.WithSMTP())
	res, body := e.post("/admin/users", url.Values{"username": {"bob"}, "email": {"bob@example.com"}}, as("root"))
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "A link to set a password was sent to bob@example.com.") {
		t.Fatalf("create: %d", res.StatusCode)
	}
	if resetLink.FindString(body) != "" {
		t.Error("emailed link also shown")
	}
	sent := e.s.Mail.Sent()
	if len(sent) != 1 || sent[0].To[0] != "bob@example.com" || resetLink.FindString(sent[0].Text) == "" {
		t.Fatalf("mail: %+v", sent)
	}

	// Reset by email.
	u, _ := e.s.App.Accounts.Store().UserByUsername(e.ctx, "bob")
	res, _ = e.post("/admin/users/"+itoa(u.ID)+"/reset", url.Values{"back": {"/admin/users"}}, as("root"))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/users?done=reset_sent" {
		t.Fatalf("reset: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if sent := e.s.Mail.Sent(); len(sent) != 2 || !strings.Contains(sent[1].Subject, "Reset") {
		t.Errorf("reset mail: %+v", sent)
	}
}

func TestResetLinkShownWithoutMail(t *testing.T) {
	e := newEnv(t)
	res, body := e.post("/admin/users/"+itoa(e.ann.ID)+"/reset", nil, as("root"))
	if res.StatusCode != http.StatusOK || resetLink.FindString(body) == "" {
		t.Fatalf("reset: %d", res.StatusCode)
	}
	if err := accounts.PasswordLogin.Set(e.ctx, e.s.App.Settings, false); err != nil {
		t.Fatal(err)
	}
	if res, body := e.post("/admin/users/"+itoa(e.ann.ID)+"/reset", nil, as("root")); res.StatusCode != http.StatusConflict || !strings.Contains(body, "Password sign-in is off") {
		t.Errorf("reset with password sign-in off: %d", res.StatusCode)
	}
}

func TestRoleAndStatus(t *testing.T) {
	e := newEnv(t)
	id := itoa(e.ann.ID)
	if res, _ := e.post("/admin/users/"+id+"/role", url.Values{"role": {"admin"}, "back": {"/admin/users/" + id}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("promote: %d", res.StatusCode)
	}
	if u, _ := e.s.App.Accounts.Store().User(e.ctx, e.ann.ID); u.Role != auth.RoleAdmin {
		t.Error("not promoted")
	}
	if res, _ := e.post("/admin/users/"+id+"/role", url.Values{"role": {"owner"}}, as("root")); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad role: %d", res.StatusCode)
	}
	// Disable signs out everywhere.
	if _, err := e.s.App.DB.W.Exec(`INSERT INTO sessions (id, user_id, created_at, last_seen_at, expires_at) VALUES (x'01', ?, 1, 1, 9999999999999)`, e.ann.ID); err != nil {
		t.Fatal(err)
	}
	res, _ := e.post("/admin/users/"+id+"/status", url.Values{"status": {"disabled"}, "back": {"https://evil.example/x"}}, as("root"))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/users/"+id+"?done=disabled" {
		t.Fatalf("disable: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if u, _ := e.s.App.Accounts.Store().User(e.ctx, e.ann.ID); u.Active() {
		t.Error("still active")
	}
	if n := e.count(`SELECT count(*) FROM sessions WHERE user_id = ?`, e.ann.ID); n != 0 {
		t.Errorf("%d sessions left", n)
	}
	if _, body := e.get("/admin/users/"+id+"?done=disabled", as("root")); !strings.Contains(body, "User disabled.") || !strings.Contains(body, ">Enable<") {
		t.Error("user page after disabling")
	}
	if res, _ := e.post("/admin/users/"+id+"/status", url.Values{"status": {"active"}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Errorf("enable: %d", res.StatusCode)
	}
	if e.audits("user.status") != 2 || e.audits("user.role_change") != 1 {
		t.Errorf("audits: status %d role %d", e.audits("user.status"), e.audits("user.role_change"))
	}
}

// The last active admin cannot be demoted, disabled or deleted, by anyone.
func TestLastAdmin(t *testing.T) {
	e := newEnv(t)
	root := itoa(e.root.ID)
	for path, form := range map[string]url.Values{
		"/admin/users/" + root + "/role":   {"role": {"user"}},
		"/admin/users/" + root + "/status": {"status": {"disabled"}},
		"/admin/users/" + root + "/delete": nil,
	} {
		if res, body := e.post(path, form, as("root")); res.StatusCode != http.StatusConflict || !strings.Contains(body, "last active admin") {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
	for _, c := range []struct {
		method string
		body   any
	}{{"PATCH", map[string]any{"role": "user"}}, {"PATCH", map[string]any{"status": "disabled"}}, {"DELETE", nil}} {
		if res, out := e.json(c.method, "/api/v1/admin/users/"+root, c.body, bearer("root-key")); res.StatusCode != http.StatusConflict {
			t.Errorf("API %s %v: %d %v", c.method, c.body, res.StatusCode, out)
		}
	}
	if u, err := e.s.App.Accounts.Store().User(e.ctx, e.root.ID); err != nil || u.Role != auth.RoleAdmin || !u.Active() {
		t.Fatalf("root changed: %+v %v", u, err)
	}

	// A disabled admin does not count.
	bob := e.user("bob", auth.RoleAdmin)
	if err := e.s.App.Accounts.Store().SetStatus(e.ctx, bob.ID, accounts.StatusDisabled, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.post("/admin/users/"+root+"/role", url.Values{"role": {"user"}}, as("root")); res.StatusCode != http.StatusConflict {
		t.Errorf("demote with only a disabled second admin: %d", res.StatusCode)
	}
	// With a second active admin, root may step down; bob is then the last one.
	if err := e.s.App.Accounts.Store().SetStatus(e.ctx, bob.ID, accounts.StatusActive, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.post("/admin/users/"+root+"/role", url.Values{"role": {"user"}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("demote with another admin: %d", res.StatusCode)
	}
	if res, _ := e.post("/admin/users/"+itoa(bob.ID)+"/delete", nil, as("bob")); res.StatusCode != http.StatusConflict {
		t.Errorf("delete the new last admin: %d", res.StatusCode)
	}
}

func TestLimitsApplyToUploads(t *testing.T) {
	e := newEnv(t)
	id := itoa(e.ann.ID)
	res, _ := e.post("/admin/users/"+id+"/limits", url.Values{"quota": {"1 KB"}, "max_upload": {""}}, as("root"))
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("set quota: %d", res.StatusCode)
	}
	row, _ := e.s.App.Accounts.Store().UserRow(e.ctx, e.ann.ID)
	if row.QuotaBytes == nil || *row.QuotaBytes != 1000 || row.MaxUploadBytes != nil {
		t.Fatalf("stored %+v", row)
	}
	if _, body := e.get("/admin/users/"+id, as("root")); !strings.Contains(body, `value="1 KB"`) {
		t.Error("quota not shown in human units")
	}
	big := strings.Repeat("x", 1500)
	if res, body := e.do("POST", "/api/v1/pastes", strings.NewReader(big), as("ann"), header("Content-Type", "text/plain")); res.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(body, "quota") {
		t.Errorf("over quota: %d %s", res.StatusCode, body)
	}
	e.paste("ann", strings.Repeat("y", 900))

	// A per-user file size limit below the site's.
	if res, _ := e.post("/admin/users/"+id+"/limits", url.Values{"quota": {""}, "max_upload": {"0.5 KiB"}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Fatal("set max upload")
	}
	if res, body := e.do("POST", "/api/v1/pastes", strings.NewReader(strings.Repeat("z", 600)), as("ann"), header("Content-Type", "text/plain")); res.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(body, "512 B") {
		t.Errorf("over max upload: %d %s", res.StatusCode, body)
	}
	e.paste("ann", strings.Repeat("z", 500))

	// Back to the site defaults (no quota by default).
	if res, _ := e.post("/admin/users/"+id+"/limits", url.Values{"quota": {""}, "max_upload": {""}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Fatal("clear limits")
	}
	e.paste("ann", big)
	if err := items.DefaultQuotaBytes.Set(e.ctx, e.s.App.Settings, 100); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.do("POST", "/api/v1/pastes", strings.NewReader("more than nothing"), as("ann"), header("Content-Type", "text/plain")); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("site default quota: %d", res.StatusCode)
	}
	// 0 means no limit, whatever the site default.
	if res, _ := e.post("/admin/users/"+id+"/limits", url.Values{"quota": {"0"}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Fatal("unlimited")
	}
	e.paste("ann", "more than nothing")

	for _, form := range []url.Values{{"quota": {"lots"}}, {"quota": {"-1 GB"}}, {"max_upload": {"0"}}, {"max_upload": {"2 parsecs"}}} {
		if res, _ := e.post("/admin/users/"+id+"/limits", form, as("root")); res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", form, res.StatusCode)
		}
	}
	if e.audits("user.limits") != 4 {
		t.Errorf("user.limits audits: %d", e.audits("user.limits"))
	}
}

func TestDeleteUser(t *testing.T) {
	e := newEnv(t)
	itemID := e.paste("ann", "# notes\n\nsome text")
	keys := e.itemBlobs(itemID)
	_, secretID := e.secret("ann", "a one-time secret")
	skeys := e.secretBlobs(secretID)
	if len(keys) == 0 || len(skeys) == 0 {
		t.Fatalf("no blobs: %v %v", keys, skeys)
	}
	for _, k := range keys {
		if !blobExists(t, e.s.App.Cfg.FilesDir, k) {
			t.Fatal("item blob missing before delete")
		}
	}
	for _, k := range skeys {
		if !blobExists(t, e.s.App.Cfg.SecretsDir, k) {
			t.Fatal("secret blob missing before delete")
		}
	}
	_, page := e.get("/admin/users/"+itoa(e.ann.ID), as("root"))
	if !strings.Contains(page, "is deleted with 1 item and 1 secret") {
		t.Error("confirm dialog does not name the counts")
	}

	res, _ := e.post("/admin/users/"+itoa(e.ann.ID)+"/delete", nil, as("root"))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/users?done=deleted" {
		t.Fatalf("delete: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if _, err := e.s.App.Accounts.Store().User(e.ctx, e.ann.ID); err == nil {
		t.Error("user still there")
	}
	if n := e.count(`SELECT count(*) FROM items WHERE owner_id = ?`, e.ann.ID); n != 0 {
		t.Errorf("%d items left", n)
	}
	if n := e.count(`SELECT count(*) FROM secrets WHERE id = ?`, secretID) + e.count(`SELECT count(*) FROM secret_blobs WHERE secret_id = ?`, secretID); n != 0 {
		t.Errorf("%d secret rows left", n)
	}
	for _, k := range keys {
		if blobExists(t, e.s.App.Cfg.FilesDir, k) {
			t.Error("item blob left on disk")
		}
	}
	for _, k := range skeys {
		if blobExists(t, e.s.App.Cfg.SecretsDir, k) {
			t.Error("secret blob left on disk")
		}
	}
	if e.audits("user.delete") != 1 {
		t.Error("deletion not audited")
	}
	if res, _ := e.post("/admin/users/"+itoa(e.ann.ID)+"/delete", nil, as("root")); res.StatusCode != http.StatusNotFound {
		t.Errorf("delete again: %d", res.StatusCode)
	}
}

// Secrets whose owner row disappears without the admin path (e.g. a crash
// between steps) still lose their files: the sweeper follows the ledger.
func TestSweeperCleansSecretsOfDeletedUsers(t *testing.T) {
	e := newEnv(t)
	_, id := e.secret("ann", "orphaned soon")
	keys := e.secretBlobs(id)
	if err := e.s.App.Accounts.Store().DeleteUser(e.ctx, e.ann.ID, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM secrets WHERE id = ?`, id) != 0 || len(e.secretBlobs(id)) == 0 {
		t.Fatal("expected the row gone and the ledger kept")
	}
	if err := e.s.App.Secrets.Sweep(e.ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.secretBlobs(id)) != 0 {
		t.Error("ledger rows left after the sweep")
	}
	for _, k := range keys {
		if blobExists(t, e.s.App.Cfg.SecretsDir, k) {
			t.Error("file left after the sweep")
		}
	}
}
