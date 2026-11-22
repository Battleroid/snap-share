package admin_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func TestInvites(t *testing.T) {
	e := newEnv(t)
	res, body := e.post("/admin/invites", url.Values{"uses": {"0"}, "expires": {"7d"}}, as("root"))
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "from 1 to 1000") || !strings.Contains(body, `value="0"`) {
		t.Errorf("bad uses: %d", res.StatusCode)
	}
	if res, _ := e.post("/admin/invites", url.Values{"uses": {"1"}, "expires": {"forever"}}, as("root")); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("bad expiry: %d", res.StatusCode)
	}
	res, body = e.post("/admin/invites", url.Values{"uses": {"5"}, "expires": {"30d"}, "role": {"admin"}, "email": {"x@example.com"}}, as("root"))
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "/invite#t=") || !strings.Contains(body, "Copy the link now") {
		t.Fatalf("create: %d", res.StatusCode)
	}
	if strings.Contains(body, "and sent to") || len(e.s.Mail.Sent()) != 0 {
		t.Error("invite emailed without SMTP")
	}
	list, _ := e.s.App.Accounts.Store().ListInvites(e.ctx, 5)
	if len(list) != 1 || list[0].MaxUses != 5 || list[0].Role != auth.RoleAdmin || list[0].CreatedByName != "root" {
		t.Fatalf("stored %+v", list)
	}
	_, page := e.get("/admin/invites", as("root"))
	for _, want := range []string{"x@example.com", "0 / 5", ">root<", "Revoke this invite?"} {
		if !strings.Contains(page, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	if strings.Contains(page, "/invite#t=") {
		t.Error("the link is shown again")
	}
	if res, _ := e.post("/admin/invites/"+itoa(list[0].ID)+"/revoke", nil, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Errorf("revoke: %d", res.StatusCode)
	}
	if _, page := e.get("/admin/invites?done=revoked", as("root")); !strings.Contains(page, "Invite revoked.") || !strings.Contains(page, ">revoked<") {
		t.Error("revoked invite not shown as revoked")
	}
	if res, _ := e.post("/admin/invites/999/revoke", nil, as("root")); res.StatusCode != http.StatusNotFound {
		t.Errorf("revoke unknown: %d", res.StatusCode)
	}
	if e.audits("invite.create") != 1 || e.audits("invite.revoke") != 1 {
		t.Error("invites not audited")
	}

	if err := accounts.SignupMode.Set(e.ctx, e.s.App.Settings, accounts.SignupClosed); err != nil {
		t.Fatal(err)
	}
	if _, page := e.get("/admin/invites", as("root")); !strings.Contains(page, "invite links do not work") {
		t.Error("no closed sign-up notice")
	}
}

func TestInviteEmailed(t *testing.T) {
	e := newEnv(t, testutil.WithSMTP())
	res, body := e.post("/admin/invites", url.Values{"uses": {"1"}, "expires": {"1d"}, "email": {"new@example.com"}}, as("root"))
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "Invite created and sent to new@example.com") {
		t.Fatalf("create: %d", res.StatusCode)
	}
	if sent := e.s.Mail.Sent(); len(sent) != 1 || sent[0].To[0] != "new@example.com" {
		t.Errorf("mail %+v", sent)
	}
}

func TestContentModeration(t *testing.T) {
	e := newEnv(t)
	bob := e.user("bob", auth.RoleUser)
	annItem := e.paste("ann", "ann's notes")
	e.paste("bob", "bob's notes")
	keys := e.itemBlobs(annItem)

	res, body := e.get("/admin/content", as("root"))
	if res.StatusCode != http.StatusOK || strings.Count(body, `href="/admin/content?owner=`) != 2 {
		t.Fatalf("content: %d", res.StatusCode)
	}
	if _, body = e.get("/admin/content?owner=bob", as("root")); !strings.Contains(body, "owner=bob") || strings.Contains(body, "owner=ann") {
		t.Error("owner filter")
	}
	if _, body = e.get("/admin/content?kind=link", as("root")); !strings.Contains(body, "Nothing matches.") {
		t.Error("kind filter")
	}
	var annSlug string
	_ = e.s.App.DB.R.QueryRow(`SELECT slug FROM items WHERE id = ?`, annItem).Scan(&annSlug)
	if _, body = e.get("/admin/content?q="+annSlug, as("root")); !strings.Contains(body, "owner=ann") || strings.Contains(body, "owner=bob") {
		t.Error("text filter")
	}

	res, _ = e.post("/admin/content/"+itoa(annItem)+"/delete", url.Values{"back": {"/admin/content?owner=ann"}}, as("root"))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/content?done=item_deleted&owner=ann" {
		t.Fatalf("delete: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if e.count(`SELECT count(*) FROM items WHERE id = ?`, annItem) != 0 {
		t.Error("item still there")
	}
	for _, k := range keys {
		if blobExists(t, e.s.App.Cfg.FilesDir, k) {
			t.Error("blob left")
		}
	}
	var target, details string
	_ = e.s.App.DB.R.QueryRow(`SELECT target, details FROM audit_log WHERE action = 'moderation.delete'`).Scan(&target, &details)
	if target != "item:"+itoa(annItem) || !strings.Contains(details, annSlug) {
		t.Errorf("audit %s %s", target, details)
	}
	if row, _ := e.s.App.Accounts.Store().UserRow(e.ctx, e.ann.ID); row.UsedBytes != 0 {
		t.Errorf("ann's usage not settled: %d", row.UsedBytes)
	}
	if e.count(`SELECT count(*) FROM items WHERE owner_id = ?`, bob.ID) != 1 {
		t.Error("bob's item went too")
	}
	if res, _ := e.post("/admin/content/"+itoa(annItem)+"/delete", nil, as("root")); res.StatusCode != http.StatusNotFound {
		t.Errorf("delete again: %d", res.StatusCode)
	}
}

func TestBurnSecretByLink(t *testing.T) {
	logs := newSyncBuffer()
	e := newEnv(t, testutil.LogTo(logs))
	link, id := e.secret("ann", "the eagle lands at noon")
	key := link[strings.Index(link, "#")+1:]

	_, page := e.get("/admin/secrets", as("root"))
	if !strings.Contains(page, id) || !strings.Contains(page, ">ann<") || !strings.Contains(page, "server-encrypted") || !strings.Contains(page, "0 used · 1 left") {
		t.Error("secret not listed with its metadata")
	}

	// Without the dialog's confirm=1 (Enter in the field), nothing burns.
	if res, body := e.post("/admin/secrets/burn", url.Values{"ref": {link}}, as("root")); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "confirm in the dialog") {
		t.Fatalf("unconfirmed burn: %d", res.StatusCode)
	}
	if _, page := e.get("/admin/secrets", as("root")); !strings.Contains(page, `name="confirm" value="1"`) || !strings.Contains(page, `<button type="submit" hidden disabled`) {
		t.Error("burn form lacks the confirm button or the disabled default button")
	}
	res, _ := e.post("/admin/secrets/burn", url.Values{"confirm": {"1"}, "ref": {"  " + link + "  "}, "back": {"/admin/secrets"}}, as("root"))
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/secrets?done=burned" {
		t.Fatalf("burn: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	var state string
	_ = e.s.App.DB.R.QueryRow(`SELECT state FROM secrets WHERE id = ?`, id).Scan(&state)
	if state != "revoked" || len(e.secretBlobs(id)) != 0 {
		t.Errorf("state %s, ledger %v", state, e.secretBlobs(id))
	}
	var target, details string
	_ = e.s.App.DB.R.QueryRow(`SELECT target, details FROM audit_log WHERE action = 'moderation.burn'`).Scan(&target, &details)
	if target != "secret:"+id {
		t.Errorf("audit target %q", target)
	}
	if strings.Contains(details, key) || strings.Contains(e.dump(), key) {
		t.Error("the key reached the database")
	}
	if strings.Contains(logs.String(), key) {
		t.Error("the key reached the log")
	}
	if !strings.Contains(logs.String(), "/admin/secrets/burn") {
		t.Fatal("no request log captured")
	}

	// Again: already gone.
	if res, _ := e.post("/admin/secrets/burn", url.Values{"confirm": {"1"}, "ref": {link}}, as("root")); res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "done=burned") && !strings.Contains(res.Header.Get("Location"), "done=gone") {
		t.Errorf("burn again: %d %q", res.StatusCode, res.Header.Get("Location"))
	}

	// A link whose key was stripped by the browser, and a bare id.
	link2, id2 := e.secret("ann", "second")
	if res, _ := e.post("/admin/secrets/burn", url.Values{"confirm": {"1"}, "ref": {strings.Split(link2, "#")[0]}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Errorf("link without key: %d", res.StatusCode)
	}
	_, id3 := e.secret("ann", "third")
	if res, _ := e.post("/admin/secrets/burn", url.Values{"confirm": {"1"}, "ref": {id3}}, as("root")); res.StatusCode != http.StatusSeeOther {
		t.Errorf("bare id: %d", res.StatusCode)
	}
	for _, sid := range []string{id2, id3} {
		_ = e.s.App.DB.R.QueryRow(`SELECT state FROM secrets WHERE id = ?`, sid).Scan(&state)
		if state != "revoked" {
			t.Errorf("%s: %s", sid, state)
		}
	}

	res, body := e.post("/admin/secrets/burn", url.Values{"confirm": {"1"}, "ref": {"not a link#" + key}}, as("root"))
	if res.StatusCode != http.StatusUnprocessableEntity || strings.Contains(body, key) {
		t.Errorf("garbage: %d (key echoed: %v)", res.StatusCode, strings.Contains(body, key))
	}
	if res, _ := e.post("/admin/secrets/burn", url.Values{"confirm": {"1"}, "ref": {"AAAAAAAAAAAAAAAAAAAAAA"}}, as("root")); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown id: %d", res.StatusCode)
	}
	if e.audits("moderation.burn") < 3 {
		t.Errorf("burns audited: %d", e.audits("moderation.burn"))
	}

	// The list hides burned secrets by default and shows them on request.
	if _, page := e.get("/admin/secrets", as("root")); strings.Contains(page, id) {
		t.Error("burned secret in the active list")
	}
	if _, page := e.get("/admin/secrets?show=all", as("root")); !strings.Contains(page, id) || !strings.Contains(page, ">revoked<") {
		t.Error("burned secret missing from the full list")
	}
}

// The admin secrets list carries metadata only: never the owner's note.
func TestSecretsListHidesNotes(t *testing.T) {
	e := newEnv(t)
	_, id := e.secret("ann", "text")
	if _, err := e.s.App.DB.W.Exec(`UPDATE secrets SET note = 'wifi for the cabin' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, page := e.get("/admin/secrets?owner=ann", as("root")); !strings.Contains(page, id) || strings.Contains(page, "wifi for the cabin") {
		t.Error("note shown, or the secret missing")
	}
	if _, page := e.get("/admin/secrets?owner=anonymous", as("root")); strings.Contains(page, id) {
		t.Error("ann's secret listed as anonymous")
	}
}

func TestAuditViewer(t *testing.T) {
	e := newEnv(t)
	e.post("/admin/users/"+itoa(e.ann.ID)+"/status", url.Values{"status": {"disabled"}}, as("root"))
	e.post("/admin/settings/apply", url.Values{"key": {"secrets.anonymous"}, "value": {"true"}}, as("root"))
	if _, err := e.s.App.DB.W.Exec(`INSERT INTO audit_log (at, action, target, details) VALUES (?, 'old.thing', 'x', '{"via":"cli"}')`,
		time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	_, body := e.get("/admin/audit", as("root"))
	for _, want := range []string{"user.status", "settings.change", "old.thing", "2020-01-02 03:04:05", "127.0.0.1", ">cli<", `<option value="user.status">`} {
		if !strings.Contains(body, want) {
			t.Errorf("audit page lacks %q", want)
		}
	}
	_, body = e.get("/admin/audit?action=user.status", as("root"))
	if !strings.Contains(body, "user.status") || strings.Contains(body, "<td class=\"pb-mono\">settings.change") {
		t.Error("action filter")
	}
	_, body = e.get("/admin/audit?actor=root", as("root"))
	if !strings.Contains(body, "settings.change") || strings.Contains(body, "old.thing</td>") {
		t.Error("actor filter")
	}
	_, body = e.get("/admin/audit?from=2020-01-01&to=2020-01-02", as("root"))
	if !strings.Contains(body, "old.thing</td>") || strings.Contains(body, "settings.change</td>") {
		t.Error("date filter")
	}
	if _, body = e.get("/admin/audit?from=2020-01-03&to=2020-12-31", as("root")); !strings.Contains(body, "No entries match.") {
		t.Error("date filter excludes")
	}
	if res, body := e.get("/admin/audit?from=yesterday", as("root")); res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "2026-11-22") {
		t.Errorf("bad date: %d", res.StatusCode)
	}

	// Pages of 50, newest first.
	for i := range 60 {
		if _, err := e.s.App.DB.W.Exec(`INSERT INTO audit_log (at, action) VALUES (?, 'bulk.entry')`, int64(1000+i)); err != nil {
			t.Fatal(err)
		}
	}
	_, body = e.get("/admin/audit?action=bulk.entry", as("root"))
	if !strings.Contains(body, "1–50 of 60") || !strings.Contains(body, "action=bulk.entry&amp;page=2") {
		t.Error("first page")
	}
	if _, body = e.get("/admin/audit?action=bulk.entry&page=2", as("root")); !strings.Contains(body, "51–60 of 60") {
		t.Error("second page")
	}
}

// dump returns every text value in the tables that could hold a secret's key.
func (e *env) dump() string {
	e.t.Helper()
	var b strings.Builder
	for _, q := range []string{`SELECT coalesce(group_concat(details, ' '), '') FROM audit_log`, `SELECT coalesce(group_concat(value, ' '), '') FROM settings`} {
		var s string
		if err := e.s.App.DB.R.QueryRow(q).Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		b.WriteString(s)
	}
	return b.String()
}
