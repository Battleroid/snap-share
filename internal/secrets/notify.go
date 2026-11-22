package secrets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/stats"
)

// MailNotifier emails a secret's owner at their verified address. Sending
// runs in the background; failures are logged without the recipient or
// the secret.
type MailNotifier struct {
	DB      *db.DB
	Mail    mail.Sender
	BaseURL string // public URL, for the link to the owner's secrets page
	Log     *slog.Logger
}

// Notify implements Notifier.
func (m MailNotifier) Notify(ctx context.Context, n Notification) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		var email sql.NullString
		var verified sql.NullInt64
		err := m.DB.R.QueryRowContext(ctx, `SELECT email, email_verified_at FROM users WHERE id = ? AND status = 'active'`,
			n.OwnerID).Scan(&email, &verified)
		if err != nil || !email.Valid || !verified.Valid {
			return
		}
		msg := notificationMessage(n, m.BaseURL)
		msg.To = []string{email.String}
		if err := m.Mail.Send(ctx, msg); err != nil && !errors.Is(err, mail.ErrDisabled) && m.Log != nil {
			m.Log.Warn("secrets: notification not sent", "err", err)
		}
	}()
}

func notificationMessage(n Notification, base string) mail.Message {
	name := "Your secret"
	if n.Note != "" {
		name = fmt.Sprintf("Your secret %q", n.Note)
	}
	// The summary comes from the opener's User-Agent: keep it to one short,
	// printable line before it goes into a subject.
	who := oneLine(strings.Join(nonEmpty(n.UASummary, n.IP), " · "), 120)
	at := n.At.UTC().Format("2006-01-02 15:04 MST")
	var subject, body string
	switch n.Kind {
	case stats.KindBurned:
		subject = "Secret destroyed after failed passphrase attempts"
		body = fmt.Sprintf("%s was destroyed at %s after too many wrong passphrase attempts.\nThe last attempt came from %s.\n",
			name, at, orDash(who))
	default:
		subject = "Secret opened · " + orDash(who)
		body = fmt.Sprintf("%s was opened at %s from %s.\nViews left: %d of %d.\n", name, at, orDash(who), n.ViewsLeft, n.MaxViews)
	}
	body += "\nYour secrets: " + strings.TrimRight(base, "/") + "/dashboard/secrets\n"
	return mail.Message{Subject: subject, Text: body}
}

// oneLine replaces control and format characters with spaces and cuts s to
// limit runes.
func oneLine(s string, limit int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == limit {
			break
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
