package dashboard_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/dashboard"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/testutil"
)

func sendBody(ids []int64, to ...string) map[string]any {
	return map[string]any{"ids": ids, "action": "send_email", "params": map[string]any{"to": to}}
}

func (e *env) sends(user int64) (rows, recipients int) {
	e.t.Helper()
	if err := e.s.App.DB.R.QueryRow(`SELECT count(*), coalesce(sum(json_extract(details, '$.recipients')), 0) FROM audit_log
		WHERE action = 'items.send_link' AND actor_user_id = ?`, user).Scan(&rows, &recipients); err != nil {
		e.t.Fatal(err)
	}
	return rows, recipients
}

func TestSendLink(t *testing.T) {
	e := newEnv(t, testutil.WithSMTP())
	e.user("ann", 1, true)
	e.user("bob", 2, false)
	e.user("cy", 3, true)
	e.exec(`UPDATE users SET email = NULL, email_verified_at = NULL WHERE id = 3`) // SSO account without an address
	photo := e.put("ann", "photo.png", pngData)
	notes := e.put("ann", "notes.txt", "n")

	res, m := e.bulk("ann", sendBody(ids(photo), "friend@example.com", "other@example.org", "FRIEND@example.com"))
	if res.StatusCode != 200 || num(m, "sent") != 2 || num(m, "recipients") != 2 {
		t.Fatalf("send: %d %v", res.StatusCode, m)
	}
	sent := e.s.Mail.Sent()
	if len(sent) != 2 {
		t.Fatalf("%d messages", len(sent))
	}
	link := e.s.URL + "/" + str(photo, "slug")
	for i, msg := range sent {
		if len(msg.To) != 1 || msg.ReplyTo != "ann@example.test" || msg.Subject != "ann shared a link with you on snap" {
			t.Errorf("message %d: %+v", i, msg)
		}
		if !strings.HasPrefix(msg.Text, "ann shared a link with you on snap:\n\nimage: "+link+"\n") || !strings.Contains(msg.HTML, link) {
			t.Errorf("message %d body: %q", i, msg.Text)
		}
		// Nothing the sender typed: not the file name.
		if strings.Contains(strings.ReplaceAll(msg.Subject+msg.Text+msg.HTML, link, ""), "photo.png") {
			t.Errorf("message %d carries the item's name", i)
		}
	}
	if sent[0].To[0] != "friend@example.com" || sent[1].To[0] != "other@example.org" {
		t.Errorf("recipients: %v %v", sent[0].To, sent[1].To)
	}
	if rows, n := e.sends(1); rows != 1 || n != 2 {
		t.Errorf("audit: %d rows, %d recipients", rows, n)
	}

	// Several items: one message listing each link.
	res, _ = e.bulk("ann", sendBody(ids(photo, notes), "friend@example.com"))
	msg := e.s.Mail.Sent()[2]
	if res.StatusCode != 200 || msg.Subject != "ann shared 2 links with you on snap" || !strings.Contains(msg.Text, "text: "+e.s.URL+"/"+str(notes, "slug")) {
		t.Errorf("two items: %d %+v", res.StatusCode, msg)
	}

	// No free text, ever.
	for _, extra := range []string{"message", "subject", "body", "note", "reply_to", "from"} {
		body := sendBody(ids(photo), "friend@example.com")
		body["params"].(map[string]any)[extra] = "Click here to win"
		if res, _ := e.bulk("ann", body); res.StatusCode != 400 {
			t.Errorf("%s accepted: %d", extra, res.StatusCode)
		}
	}
	// Recipient checks.
	many := make([]string, 11)
	for i := range many {
		many[i] = fmt.Sprintf("r%d@example.com", i)
	}
	for _, to := range [][]string{nil, {""}, many, {"Mallory <m@example.com>"}, {"not-an-address"}, {"a@example.com\r\nBcc: x@example.com"}} {
		if res, _ := e.bulk("ann", sendBody(ids(photo), to...)); res.StatusCode != 400 {
			t.Errorf("to %q: %d", to, res.StatusCode)
		}
	}
	// At most 20 links per email.
	var lots []int64
	for i := range 21 {
		lots = append(lots, id(e.put("ann", fmt.Sprintf("f%d.txt", i), "x")))
	}
	if res, _ := e.bulk("ann", sendBody(lots, "friend@example.com")); res.StatusCode != 400 {
		t.Errorf("21 links: %d", res.StatusCode)
	}

	// The sender needs a verified address (checked before the items).
	if res, _ := e.bulk("bob", sendBody([]int64{999}, "friend@example.com")); res.StatusCode != 403 {
		t.Errorf("unverified: %d", res.StatusCode)
	}
	if res, _ := e.bulk("cy", sendBody([]int64{999}, "friend@example.com")); res.StatusCode != 403 {
		t.Errorf("no address: %d", res.StatusCode)
	}

	// Daily cap per user (one per recipient), counted from the audit log.
	ctx := e.ctx
	if err := dashboard.SendLinksPerDay.Set(ctx, e.s.App.Settings, 5); err != nil {
		t.Fatal(err)
	}
	// ann has sent 3 so far.
	if res, m := e.bulk("ann", sendBody(ids(photo), "a@example.com", "b@example.com", "c@example.com")); res.StatusCode != 429 || !strings.Contains(str(m, "error"), "2 more emails") {
		t.Errorf("over cap: %d %v", res.StatusCode, m)
	}
	if res, _ := e.bulk("ann", sendBody(ids(photo), "a@example.com", "b@example.com")); res.StatusCode != 200 {
		t.Errorf("up to cap: %d", res.StatusCode)
	}
	if res, m := e.bulk("ann", sendBody(ids(photo), "a@example.com")); res.StatusCode != 429 || !strings.Contains(str(m, "error"), "daily limit") {
		t.Errorf("at cap: %d %v", res.StatusCode, m)
	}
	if len(e.s.Mail.Sent()) != 5 {
		t.Errorf("%d messages", len(e.s.Mail.Sent()))
	}

	// Site-wide cap.
	if err := dashboard.SendLinksPerDay.Set(ctx, e.s.App.Settings, 100); err != nil {
		t.Fatal(err)
	}
	if err := dashboard.SendLinksSitePerDay.Set(ctx, e.s.App.Settings, 6); err != nil {
		t.Fatal(err)
	}
	if res, m := e.bulk("ann", sendBody(ids(photo), "a@example.com", "b@example.com")); res.StatusCode != 429 || !strings.Contains(str(m, "error"), "This server") {
		t.Errorf("site cap: %d %v", res.StatusCode, m)
	}
	if err := dashboard.SendLinksSitePerDay.Set(ctx, e.s.App.Settings, 200); err != nil {
		t.Fatal(err)
	}

	// The admin toggle.
	if err := dashboard.SendLinks.Set(ctx, e.s.App.Settings, false); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.bulk("ann", sendBody(ids(photo), "a@example.com")); res.StatusCode != 403 {
		t.Errorf("turned off: %d", res.StatusCode)
	}
	if _, page := e.do("GET", "/dashboard", nil, as("ann")); strings.Contains(page, "dash-email") {
		t.Error("Send link offered while off")
	}
	if err := dashboard.SendLinks.Set(ctx, e.s.App.Settings, true); err != nil {
		t.Fatal(err)
	}
	if _, page := e.do("GET", "/dashboard", nil, as("ann")); !strings.Contains(page, `id="dash-email"`) {
		t.Error("Send link not offered")
	}

	// A sender that is not configured gives a clear error.
	e.s.App.Dashboard.SetMail(mail.Disabled{})
	if res, m := e.bulk("ann", sendBody(ids(photo), "a@example.com")); res.StatusCode != 503 || !strings.Contains(str(m, "error"), "not set up") {
		t.Errorf("mail disabled: %d %v", res.StatusCode, m)
	}
	if rows, _ := e.sends(1); rows != 4 {
		t.Errorf("audit rows: %d", rows)
	}
}

func TestSendLinkWithoutSMTP(t *testing.T) {
	e := newEnv(t)
	e.user("ann", 1, true)
	it := e.put("ann", "photo.png", pngData)
	if res, m := e.bulk("ann", sendBody(ids(it), "a@example.com")); res.StatusCode != 503 {
		t.Errorf("no smtp: %d %v", res.StatusCode, m)
	}
	if len(e.s.Mail.Sent()) != 0 {
		t.Error("sent without smtp")
	}
	if _, page := e.do("GET", "/dashboard", nil, as("ann")); strings.Contains(page, `id="dash-email"`) {
		t.Error("Send link offered without smtp")
	}
}
