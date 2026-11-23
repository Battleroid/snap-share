package secrets_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
)

// The fallback MailNotifier keeps its rules now that every reveal reaches
// the notifier: a reveal only with notify set, a burn always.
func TestMailNotifierRules(t *testing.T) {
	e := newEnv(t)
	fake := &mail.Fake{}
	m := secrets.MailNotifier{DB: e.s.App.DB, Mail: fake, BaseURL: e.s.URL}
	n := secrets.Notification{OwnerID: 1, Note: "prod db", Kind: stats.KindRevealed, ViewsLeft: 1, MaxViews: 2, At: time.Now()}
	m.Notify(context.Background(), n)
	burn := n
	burn.Kind = stats.KindBurned
	m.Notify(context.Background(), burn)
	eventually(t, "the burn mail", func() bool { return len(fake.Sent()) == 1 })
	n.Requested = true
	m.Notify(context.Background(), n)
	eventually(t, "the reveal mail", func() bool { return len(fake.Sent()) == 2 })
	sent := fake.Sent()
	if !strings.Contains(sent[0].Subject, "destroyed") || !strings.HasPrefix(sent[1].Subject, "Secret opened") {
		t.Fatalf("mails %q, %q", sent[0].Subject, sent[1].Subject)
	}
}
