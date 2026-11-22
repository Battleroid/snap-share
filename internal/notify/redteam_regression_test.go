package notify_test

// Regression tests for the security review's notification findings.

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// L14: with no SMTP, a user whose email channel is still on gets nothing
// mailed and nothing logged about the message (the fallback sender used to
// log every subject: the visitor's address and the secret's note).
func TestNotificationSubjectsNotLoggedWithoutSMTP(t *testing.T) {
	s := testutil.NewServer(t) // no WithSMTP: SMTP is not configured
	if s.App.Cfg.SMTP.Enabled() {
		t.Fatal("SMTP unexpectedly enabled")
	}
	logs := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	if _, err := s.App.DB.W.Exec(`INSERT INTO users (id, username, email, email_verified_at, created_at, updated_at)
VALUES (1, 'ann', 'ann@example.test', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.App.DB.W.Exec(`INSERT INTO notify_prefs (user_id, email_enabled, default_event, updated_at) VALUES (1, 1, 'every', 1)`); err != nil {
		t.Fatal(err)
	}
	n, err := notify.New(notify.Deps{Cfg: s.App.Cfg, Log: log, DB: s.App.DB, Settings: s.App.Settings, Mail: mail.Log{L: log}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"user_id": 1, "channel": "email",
		"message": map[string]any{
			"kind":   "secret_revealed",
			"title":  `Secret "prod db root password" opened`,
			"detail": "Firefox · macOS · 203.0.113.77 · 14:03 UTC · 0 of 1 views left",
		},
	})
	_ = n.HandleSend(context.Background(), payload)
	if out := logs.String(); strings.Contains(out, "203.0.113.77") || strings.Contains(out, "prod db root password") {
		t.Fatalf("the message reached the log:\n%s", out)
	}
	st, err := n.Statuses(context.Background(), 1)
	if err != nil || !strings.Contains(st[notify.ChannelEmail].LastError, "not set up") {
		t.Fatalf("status %+v %v", st, err)
	}
	// mail.Log itself names nothing about a message.
	_ = mail.Log{L: log}.Send(context.Background(), mail.Message{To: []string{"x@example.test"}, Subject: "visitor 198.51.100.9"})
	if strings.Contains(logs.String(), "198.51.100.9") || strings.Contains(logs.String(), "x@example.test") {
		t.Fatal("mail.Log logged the subject or recipient")
	}
}

// Email jobs are not even queued without SMTP (their payloads hold the
// visitor's details); the status says why.
func TestNoEmailJobsWithoutSMTP(t *testing.T) {
	e := newEnv(t)
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true)
	off, err := notify.New(notify.Deps{
		Cfg: e.s.App.Cfg, DB: e.s.App.DB, Settings: e.s.App.Settings, Jobs: jobs.New(e.s.App.DB, slog.New(slog.DiscardHandler)),
		Mail: mail.Disabled{}, Items: e.s.App.Items,
		MailEnabled: func() bool { return false }, Now: e.clk.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	off.Notify(e.ctx, secrets.Notification{OwnerID: 1, Kind: stats.KindRevealed, At: e.clk.Now(), IP: "203.0.113.77", MaxViews: 2})
	off.Close()
	if n := len(e.jobs(notify.JobSend)); n != 0 {
		t.Fatalf("%d email jobs queued without SMTP", n)
	}
	st, _ := off.Statuses(e.ctx, 1)
	if !st[notify.ChannelEmail].Failing() {
		t.Fatalf("status %+v", st)
	}
}

// Item opens and secret reveals have separate hourly budgets: a busy link
// can't crowd out the news that a secret was opened.
func TestItemOpensDoNotCrowdOutReveals(t *testing.T) {
	e := newEnv(t)
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true)
	_ = e.s.App.Notify.SetDefault(e.ctx, 1, notify.ModeFirst)
	ms := e.clk.Now().UnixMilli()
	for range notify.MessagesPerHour + 5 {
		e.open(map[string]any{"item_id": e.link, "first": true, "count": 1, "from": ms, "to": ms})
	}
	if n := len(e.jobs(notify.JobSend)); n != notify.MessagesPerHour {
		t.Fatalf("%d open messages, want the cap %d", n, notify.MessagesPerHour)
	}
	e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, Kind: stats.KindRevealed, At: e.clk.Now(), MaxViews: 9})
	e.own.Close()
	reveals := 0
	for _, p := range e.jobs(notify.JobSend) {
		if strings.Contains(p, "secret_revealed") {
			reveals++
		}
	}
	if reveals != 1 {
		t.Fatalf("reveal dropped after %d item opens", notify.MessagesPerHour)
	}
}

// Finished notify jobs (visitor address, client, a secret's note) are
// deleted instead of waiting a week in the jobs table.
func TestFinishedNotifyJobsArePurged(t *testing.T) {
	e := newEnv(t)
	e.exec(`INSERT INTO jobs (kind, payload, state, run_after, created_at, updated_at) VALUES
  ('notify.send', '{"ip":"203.0.113.77"}', 'done', 1, 1, 1),
  ('notify.send', '{"ip":"203.0.113.78"}', 'failed', 1, 1, 1),
  ('notify.open', '{"item_id":1}', 'done', 1, 1, 1),
  ('notify.send', '{"ip":"203.0.113.79"}', 'queued', 1, 1, 1),
  ('other.kind', '{}', 'done', 1, 1, 1)`)
	if err := e.own.PurgeJobs(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind LIKE 'notify.%'`); n != 1 {
		t.Fatalf("%d notify jobs left, want only the queued one", n)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = 'other.kind'`); n != 1 {
		t.Fatal("purged another kind's jobs")
	}
}

// A saved ntfy token goes only to the server it was given for.
func TestNtfyTokenDroppedWhenServerChanges(t *testing.T) {
	e := newEnv(t)
	tok := "tk_servertoken"
	if err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: "https://ntfy.example.com", Topic: "t1", Token: &tok}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: "https://ntfy.attacker.example", Topic: "t1"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.s.App.Notify.Prefs(e.ctx, 1); p.NtfyTokenSet {
		t.Fatal("token kept for another server")
	}
	// The default public server says that topics are readable by anyone.
	if err := e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: "", Topic: "t1"}); err != nil {
		t.Fatal(err)
	}
	if _, body := e.do("GET", "/settings/notifications", nil, as("ann")); !strings.Contains(body, "Anyone who guesses a topic on ntfy.sh can read it") {
		t.Error("no hint about public topics")
	}
}
