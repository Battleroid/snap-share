package secrets_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
)

type inboxRow struct{ Title, Body, Link, Status, Subject string }

func (e *env) inbox() []inboxRow {
	e.t.Helper()
	e.s.App.Notify.Close() // secret notifications are stored in the background
	rows, err := e.s.App.DB.R.Query(`SELECT title, body, link, status, subject_id FROM inbox ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []inboxRow
	for rows.Next() {
		var r inboxRow
		if err := rows.Scan(&r.Title, &r.Body, &r.Link, &r.Status, &r.Subject); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// Every reveal of an owned secret reaches the owner's inbox, with or
// without notify; the owner's own reveal and anonymous secrets do not. A
// burn after wrong passphrases does, and a purged secret takes its rows.
func TestInboxGetsEveryReveal(t *testing.T) {
	e := newEnv(t)
	sec := e.createText("ann", "hello", 3, nil)
	e.exec(`UPDATE secrets SET note = 'prod db' WHERE id = ?`, sec.ids)
	e.open(sec, nil)
	rows := e.inbox()
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	if r := rows[0]; r.Title != `Secret "prod db" revealed` || r.Body != "Safari · macOS · 127.0.0.1 · 2 of 3 views left" ||
		r.Link != "/dashboard/secrets/"+sec.ids+"/timeline" || r.Status != "info" || r.Subject != sec.ids {
		t.Fatalf("row %+v", r)
	}

	e.open(sec, nil, as("ann")) // the owner's own reveal
	if rows := e.inbox(); len(rows) != 1 {
		t.Fatalf("the owner's reveal was recorded: %+v", rows)
	}
	e.exec(`UPDATE secrets SET notify = 1 WHERE id = ?`, sec.ids)
	e.open(sec, nil, as("bob"))
	if rows := e.inbox(); len(rows) != 2 || !strings.HasSuffix(rows[1].Body, "0 of 3 views left") {
		t.Fatalf("the last reveal: %+v", rows)
	}

	if err := secrets.Anonymous.Set(context.Background(), e.s.App.Settings, true); err != nil {
		t.Fatal(err)
	}
	anon := e.createText("", "nobody's", 1, nil)
	e.open(anon, nil)
	if rows := e.inbox(); len(rows) != 2 {
		t.Fatalf("an anonymous secret was recorded: %+v", rows)
	}

	locked := e.createText("ann", "x", 1, []byte("right"))
	bad := e.keys(locked, []byte("wrong"))
	for range 10 {
		st, _, b := e.claimWith(locked, bad.Auth)
		mustStatus(t, "wrong", st, http.StatusForbidden, b)
	}
	// The first wrong passphrase is listed (the rest are coalesced, and
	// the burn ends the window), then the burn.
	rows = e.inbox()
	if len(rows) != 4 || rows[2].Title != "Text secret: wrong passphrase" || rows[2].Status != "warn" ||
		rows[3].Title != "Text secret burned after wrong passphrases" || rows[3].Status != "down" ||
		rows[3].Body != "last attempt Safari · macOS · 127.0.0.1" {
		t.Fatalf("burn: %+v", rows)
	}

	// Purged tombstones take their rows with them (and the WAL is truncated
	// after, as for their events).
	if err := e.svc.SweepAt(context.Background(), time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if rows := e.inbox(); len(rows) != 0 {
		t.Fatalf("rows outlived their secrets: %+v", rows)
	}
}

// A secret that expires without a single reveal is reported once; one that
// was opened (views left) is not. An admin's removal is reported; the
// owner's own revoke is not. The fallback MailNotifier mails neither.
func TestInboxExpiredUnopenedAndRemoved(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	unopened := e.createText("ann", "a", 2, nil)
	opened := e.createText("ann", "b", 2, nil)
	e.exec(`UPDATE secrets SET note = 'never' WHERE id = ?`, unopened.ids)
	e.open(opened, nil) // one of two views: still ready, but opened
	before := len(e.inbox())

	soon := time.Now().Add(time.Minute)
	e.exec(`UPDATE secrets SET expires_at = ? WHERE id IN (?, ?)`, soon.UnixMilli(), unopened.ids, opened.ids)
	later := soon.Add(time.Minute)
	if err := e.svc.SweepAt(ctx, later); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SweepAt(ctx, later); err != nil { // nothing left to expire
		t.Fatal(err)
	}
	rows := e.inbox()
	if len(rows) != before+1 || rows[before].Title != `Secret "never" expired unopened` || rows[before].Subject != unopened.ids {
		t.Fatalf("expired: %+v", rows)
	}

	mine := e.createText("ann", "c", 1, nil)
	removed := e.createText("ann", "d", 1, nil)
	if _, ok, err := e.svc.Revoke(ctx, mine.ids, 1); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, err := e.svc.Revoke(ctx, removed.ids, 0); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, err := e.svc.Revoke(ctx, removed.ids, 0); err != nil || !ok { // already revoked: not again
		t.Fatal(ok, err)
	}
	rows = e.inbox()
	if len(rows) != before+2 || rows[before+1].Title != "Text secret removed by an admin" || rows[before+1].Status != "warn" ||
		rows[before+1].Subject != removed.ids {
		t.Fatalf("removed: %+v", rows)
	}

	fake := &mail.Fake{}
	m := secrets.MailNotifier{DB: e.s.App.DB, Mail: fake, BaseURL: e.s.URL}
	for _, k := range []stats.Kind{secrets.NotifyExpired, secrets.NotifyRemoved, stats.KindFailed} {
		m.Notify(ctx, secrets.Notification{OwnerID: 1, Kind: k, Requested: true, At: time.Now()})
	}
	m.Notify(ctx, secrets.Notification{OwnerID: 1, Kind: stats.KindBurned, At: time.Now()})
	eventually(t, "the burn mail", func() bool { return len(fake.Sent()) == 1 })
	if sent := fake.Sent(); !strings.Contains(sent[0].Subject, "destroyed") {
		t.Fatalf("mailed %q", sent[0].Subject)
	}
}
