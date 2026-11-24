old.Detail["time"] != "2020-01-02 03:04:05 UTC" || old.Detail["via"] != "cli" || old.Detail["json"] != `{"via":"cli"}` {package admin_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
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
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "/invite#t=") || !strings.Contains(body, "Invite created") {
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
	for _, want := range []string{"x@example.com", "0 / 5", ">root<", "Revoke this invite?", ">link<", "Copy link"} {
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
	if _, page := e.get("/admin/invites?show=all&done=revoked", as("root")); !strings.Contains(page, "Invite revoked.") || !strings.Contains(page, ">revoked<") {
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
	if _, page := e.get("/admin/invites", as("root")); !strings.Contains(page, ">sent<") {
		t.Error("sent invite not shown as sent")
	}

	// A failed send is recorded and the link is offered instead.
	e.s.Mail.FailWith(errors.New("smtp: 451 try later"))
	res, body = e.post("/admin/invites", url.Values{"uses": {"1"}, "expires": {"1d"}, "email": {"late@example.com"}}, as("root"))
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "Email failed — copy the link instead.") || !strings.Contains(body, "/invite#t=") {
		t.Fatalf("failed send: %d", res.StatusCode)
	}
	list, _ := e.s.App.Accounts.Store().ListInvites(e.ctx, 5)
	if list[0].MailError == "" || list[0].MailSentAt != nil || list[1].MailSentAt == nil {
		t.Errorf("mail result not recorded: %+v", list)
	}
	if _, page := e.get("/admin/invites", as("root")); !strings.Contains(page, "send failed") || !strings.Contains(page, `title="smtp: 451 try later"`) {
		t.Error("failed invite not shown as send failed")
	}
}

func TestInviteLabelAndStatus(t *testing.T) {
	e := newEnv(t)
	if res, body := e.post("/admin/invites", url.Values{"uses": {"1"}, "expires": {"7d"}, "label": {strings.Repeat("x", 81)}}, as("root")); res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "at most 80 characters") {
		t.Errorf("long label: %d", res.StatusCode)
	}
	if res, _ := e.post("/admin/invites", url.Values{"uses": {"3"}, "expires": {"7d"}, "label": {"for the band"}}, as("root")); res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", res.StatusCode)
	}
	list, _ := e.s.App.Accounts.Store().ListInvites(e.ctx, 5)
	if len(list) != 1 || list[0].Label != "for the band" {
		t.Fatalf("stored %+v", list)
	}
	id := itoa(list[0].ID)
	if _, err := e.s.App.DB.W.Exec(`UPDATE invites SET uses = 1 WHERE id = ?`, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, page := e.get("/admin/invites", as("root")); !strings.Contains(page, "for the band") || !strings.Contains(page, ">partly used<") || !strings.Contains(page, `aria-label="Actions for for the band"`) {
		t.Error("label or partly used not shown")
	}
	// An invite from before links were kept says so and offers no Copy link.
	if _, err := e.s.App.DB.W.Exec(`UPDATE invites SET token_ct = NULL WHERE id = ?`, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, page := e.get("/admin/invites", as("root")); !strings.Contains(page, "link not stored") || strings.Contains(page, "/admin/invites/"+id+"/link") {
		t.Error("missing link not shown")
	}
	if res, m := e.json("POST", "/admin/invites/"+id+"/link", nil, as("root")); res.StatusCode != http.StatusNotFound || !strings.Contains(m["error"].(string), "not stored") {
		t.Errorf("no stored link: %d %v", res.StatusCode, m)
	}
}

// The link comes back only from the link endpoint, to an admin's browser
// session, while the invite is active, and every time it is audited.
func TestInviteLinkAgain(t *testing.T) {
	e := newEnv(t)
	res, body := e.post("/admin/invites", url.Values{"uses": {"1"}, "expires": {"7d"}}, as("root"))
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", res.StatusCode)
	}
	created := regexp.MustCompile(`https://[^"]+/invite#t=[A-Za-z0-9_-]{43}`).FindString(body)
	list, _ := e.s.App.Accounts.Store().ListInvites(e.ctx, 5)
	id := itoa(list[0].ID)
	path := "/admin/invites/" + id + "/link"

	_, page := e.get("/admin/invites", as("root"))
	if strings.Contains(page, "/invite#t=") || !strings.Contains(page, `action="`+path+`"`) {
		t.Error("the list carries the link, or no Copy link")
	}
	res, m := e.json("POST", path, nil, as("root"))
	if res.StatusCode != http.StatusOK || m["url"] != created || created == "" {
		t.Fatalf("link: %d %v (created %q)", res.StatusCode, m, created)
	}
	if res, page := e.post(path, nil, as("root")); res.StatusCode != http.StatusOK || !strings.Contains(page, created) {
		t.Errorf("link without scripts: %d", res.StatusCode)
	}
	if e.audits("invite.link") != 2 {
		t.Errorf("link audited %d times", e.audits("invite.link"))
	}

	for name, o := range map[string]opt{"user": as("ann"), "admin key": bearer("root-key"), "key without admin": bearer("root-nokey")} {
		if res, body := e.do("POST", path, nil, o, header("Accept", "application/json")); res.StatusCode != http.StatusForbidden || strings.Contains(body, "/invite#t=") {
			t.Errorf("%s: %d %.80s", name, res.StatusCode, body)
		}
	}
	if res, body := e.do("GET", path, nil, as("root")); res.StatusCode == http.StatusOK || strings.Contains(body, "/invite#t=") { // POST only: it writes an audit row
		t.Errorf("GET: %d", res.StatusCode)
	}
	if res, _ := e.json("POST", "/admin/invites/999/link", nil, as("root")); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown: %d", res.StatusCode)
	}
	if res, _ := e.post("/admin/invites/"+id+"/revoke", url.Values{"back": {"/admin/invites?show=all"}}, as("root")); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/invites?done=revoked&show=all" {
		t.Errorf("revoke: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, m := e.json("POST", path, nil, as("root")); res.StatusCode != http.StatusNotFound || m["url"] != nil {
		t.Errorf("revoked: %d %v", res.StatusCode, m)
	}
	if e.audits("invite.link") != 2 {
		t.Error("a refused link was audited")
	}
}

func TestInviteRemoveAndFilter(t *testing.T) {
	e := newEnv(t)
	for _, l := range []string{"keep", "drop"} {
		if res, _ := e.post("/admin/invites", url.Values{"uses": {"1"}, "expires": {"7d"}, "label": {l}}, as("root")); res.StatusCode != http.StatusCreated {
			t.Fatal(res.StatusCode)
		}
	}
	list, _ := e.s.App.Accounts.Store().ListInvites(e.ctx, 5)
	drop, keep := itoa(list[0].ID), itoa(list[1].ID)
	if res, _ := e.post("/admin/invites/"+keep+"/remove", nil, as("root")); res.StatusCode != http.StatusConflict {
		t.Errorf("removed an active invite: %d", res.StatusCode)
	}
	if _, err := e.s.App.DB.W.Exec(`UPDATE invites SET expires_at = 0 WHERE id = ?`, list[0].ID); err != nil {
		t.Fatal(err)
	}
	_, page := e.get("/admin/invites", as("root"))
	if !strings.Contains(page, ">keep<") || strings.Contains(page, ">drop<") || !strings.Contains(page, `value="active" checked`) {
		t.Error("the default list is not active only")
	}
	_, page = e.get("/admin/invites?show=all", as("root"))
	if !strings.Contains(page, ">drop<") || !strings.Contains(page, ">expired<") || !strings.Contains(page, "Remove this invite?") || !strings.Contains(page, `value="all" checked`) {
		t.Error("all does not show the expired invite with Remove")
	}
	if res, _ := e.post("/admin/invites/"+drop+"/remove", nil, as("ann")); res.StatusCode != http.StatusForbidden {
		t.Errorf("user removed: %d", res.StatusCode)
	}
	if res, _ := e.post("/admin/invites/"+drop+"/remove", url.Values{"back": {"/admin/invites?show=all"}}, as("root")); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/invites?done=removed&show=all" {
		t.Errorf("remove: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := e.post("/admin/invites/"+drop+"/remove", nil, as("root")); res.StatusCode != http.StatusNotFound {
		t.Errorf("remove twice: %d", res.StatusCode)
	}
	if left, _ := e.s.App.Accounts.Store().ListInvites(e.ctx, 5); len(left) != 1 || e.audits("invite.remove") != 1 {
		t.Errorf("left %d, audited %d", len(left), e.audits("invite.remove"))
	}
	// Only finished invites left: the table stays, and the active/all toggle is the way to them.
	if _, err := e.s.App.DB.W.Exec(`UPDATE invites SET revoked_at = 1`); err != nil {
		t.Fatal(err)
	}
	if _, page := e.get("/admin/invites", as("root")); !strings.Contains(page, "No active invites") || !strings.Contains(page, `name="show" value="all"`) {
		t.Error("no way to the finished invites")
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
	if _, body = e.get("/admin/content?kind=link", as("root")); !strings.Contains(body, "Nothing matches") {
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
	if _, page := e.get("/admin/secrets", as("root")); !strings.Contains(page, `data-confirm-name="confirm" data-confirm-value="1" disabled data-confirm-enable`) || !strings.Contains(page, `<button class="pb-btn" type="submit" hidden disabled`) {
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

	// A target that tries to end the JSON block.
	const evil = `</script><b>x</b>`
	if _, err := e.s.App.DB.W.Exec(`INSERT INTO audit_log (at, action, target) VALUES (?, 'evil.thing', ?)`,
		time.Date(2019, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), evil); err != nil {
		t.Fatal(err)
	}

	_, body := e.get("/admin/audit", as("root"))
	tbl := auditBlock(t, body)
	for _, c := range tbl.Columns {
		if c.Sort == nil || *c.Sort {
			t.Errorf("column %s sorts one page; the server orders the log", c.Key)
		}
	}
	got := map[string]auditEntry{}
	ip := false
	for _, r := range tbl.Rows {
		got[r.Action] = r
		ip = ip || r.IP == "127.0.0.1"
	}
	for _, want := range []string{"user.status", "settings.change", "old.thing"} {
		if _, ok := got[want]; !ok {
			t.Errorf("audit table lacks %q", want)
		}
	}
	if !ip {
		t.Error("no row carries the actor's IP")
	}
	old := got["old.thing"]
	if old.At != "2020-01-02 03:04" || old.Actor != "cli" || old.Target != "x" ||
		old.Detail["time"] != "2020-01-02 03:04:05 UTC" || old.Detail["via"] != "cli" {
		t.Errorf("old.thing row: %+v", old)
	}
	if !strings.Contains(body, `<option value="user.status">`) {
		t.Error("action filter lacks its options")
	}
	if strings.Contains(body, evil) || got["evil.thing"].Target != evil {
		t.Errorf("target escaped badly: %q", got["evil.thing"].Target)
	}

	actions := func(path string) []string {
		var out []string
		_, body := e.get(path, as("root"))
		for _, r := range auditBlock(t, body).Rows {
			out = append(out, r.Action)
		}
		return out
	}
	if got := actions("/admin/audit?action=user.status"); len(got) == 0 || slices.ContainsFunc(got, func(a string) bool { return a != "user.status" }) {
		t.Errorf("action filter: %v", got)
	}
	if got := actions("/admin/audit?actor=root"); !slices.Contains(got, "settings.change") || slices.Contains(got, "old.thing") {
		t.Errorf("actor filter: %v", got)
	}
	if got := actions("/admin/audit?from=2020-01-01&to=2020-01-02"); !slices.Equal(got, []string{"old.thing"}) {
		t.Errorf("date filter: %v", got)
	}
	if _, body = e.get("/admin/audit?from=2020-01-03&to=2020-12-31", as("root")); !strings.Contains(body, "No entries match") || strings.Contains(body, "data-dt") {
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
	if !strings.Contains(body, "1–50 of 60") || !strings.Contains(body, "action=bulk.entry&amp;page=2") || len(auditBlock(t, body).Rows) != 50 {
		t.Error("first page")
	}
	if _, body = e.get("/admin/audit?action=bulk.entry&page=2", as("root")); !strings.Contains(body, "51–60 of 60") || len(auditBlock(t, body).Rows) != 10 {
		t.Error("second page")
	}
}

type auditEntry struct {
	At, Actor, IP, Action, Target, Details string
	Detail                                 map[string]string
}

// auditBlock reads the audit page's TableTools JSON block.
func auditBlock(t *testing.T, body string) (tbl struct {
	Columns []struct {
		Key  string
		Sort *bool
	}
	Rows []auditEntry
}) {
	t.Helper()
	m := regexp.MustCompile(`(?s)data-dt data-name="audit".*?<script type="application/json">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("audit page has no TableTools block")
	}
	if err := json.Unmarshal([]byte(m[1]), &tbl); err != nil {
		t.Fatalf("audit block: %v", err)
	}
	return tbl
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
