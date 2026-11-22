package dashboard

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"errors"
	htmltemplate "html/template"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	snapmail "github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// "Send link": the server emails item links for their owner, with a fixed
// message and no text of the sender's, from a verified account, with
// Reply-To set to that verified address, within daily caps per user and per
// site. Every send is recorded in the audit log, which the caps count.
// Secrets are not items, so their links can never be sent this way.

// Settings.
var (
	SendLinks           = settings.Define("email.send_links", true, nil)
	SendLinksPerDay     = settings.Define("email.send_links_per_day", 20, settings.Range(0, 100000))
	SendLinksSitePerDay = settings.Define("email.send_links_site_per_day", 200, settings.Range(0, 10000000))
)

// Limits per send.
const (
	MaxRecipients    = 10
	MaxLinksPerEmail = 20
	auditSendLink    = "items.send_link"
)

var (
	errSendOff      = web.Errorf(http.StatusForbidden, "Sending links by email is turned off on this server.")
	errMailOff      = web.Errorf(http.StatusServiceUnavailable, "Email is not set up on this server.")
	errSenderEmail  = web.Errorf(http.StatusForbidden, "Add and verify an email address in Settings first: replies go to it.")
	errNoRecipients = web.Errorf(http.StatusBadRequest, "Enter at least one email address.")
	errRecipients   = web.Errorf(http.StatusBadRequest, "Send to at most %d addresses at once.", MaxRecipients)
	errLinks        = web.Errorf(http.StatusBadRequest, "Send at most %d links at once.", MaxLinksPerEmail)
	errSiteCap      = web.Errorf(http.StatusTooManyRequests, "This server has sent its daily limit of links. Try again tomorrow.")
)

//go:embed mail/*.html mail/*.txt
var mailFS embed.FS

type mailTemplates struct {
	text *texttemplate.Template
	html *htmltemplate.Template
}

func loadMailTemplates() (*mailTemplates, error) {
	t, err := texttemplate.ParseFS(mailFS, "mail/send_link.txt")
	if err != nil {
		return nil, err
	}
	h, err := htmltemplate.ParseFS(mailFS, "mail/layout.html", "mail/send_link.html")
	if err != nil {
		return nil, err
	}
	return &mailTemplates{text: t, html: h}, nil
}

type mailLink struct{ Name, URL string }

// mailData fills the fixed template. Nothing in it is free text from the sender.
type mailData struct {
	Site   string
	Sender string // username
	Title  string // the item's name, or "3 items"
	Links  []mailLink
}

func (t *mailTemplates) compose(to, replyTo string, d mailData) (snapmail.Message, error) {
	var subj, text, html bytes.Buffer
	if err := t.text.ExecuteTemplate(&subj, "subject", d); err != nil {
		return snapmail.Message{}, err
	}
	if err := t.text.ExecuteTemplate(&text, "text", d); err != nil {
		return snapmail.Message{}, err
	}
	if err := t.html.ExecuteTemplate(&html, "layout", d); err != nil {
		return snapmail.Message{}, err
	}
	return snapmail.Message{
		To: []string{to}, ReplyTo: replyTo,
		Subject: oneLine(subj.String()), Text: strings.TrimLeft(text.String(), "\n"), HTML: html.String(),
	}, nil
}

// oneLine keeps a header value on one line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, s)), " ")
}

// recipients validates bare addresses ("a@example.com", no display names),
// dropping duplicates.
func recipients(in []string) ([]string, error) {
	var out []string
	for _, raw := range in {
		a := strings.TrimSpace(raw)
		if a == "" {
			continue
		}
		addr, err := mail.ParseAddress(a)
		if err != nil || addr.Name != "" || addr.Address != a || len(a) > 254 || !strings.Contains(a, "@") {
			return nil, web.Errorf(http.StatusBadRequest, "%q is not an email address.", a)
		}
		dup := false
		for _, x := range out {
			if strings.EqualFold(x, a) {
				dup = true
			}
		}
		if !dup {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, errNoRecipients
	}
	if len(out) > MaxRecipients {
		return nil, errRecipients
	}
	return out, nil
}

// verifiedSender returns the user if they have a verified address.
func (s *Service) verifiedSender(ctx context.Context, uid int64) (*accounts.User, error) {
	u, err := s.d.Users.User(ctx, uid)
	if errors.Is(err, accounts.ErrNotFound) {
		return nil, errSenderEmail
	}
	if err != nil {
		return nil, err
	}
	if u.Email == "" || u.EmailVerifiedAt == nil || !u.Active() {
		return nil, errSenderEmail
	}
	return u, nil
}

// sentSince counts link emails (one per recipient) sent since t, by uid or (uid 0) by everyone.
func sentSince(ctx context.Context, q items.Querier, uid int64, t time.Time) (int, error) {
	query := `SELECT coalesce(sum(json_extract(details, '$.recipients')), 0) FROM audit_log WHERE action = ? AND at > ?`
	args := []any{auditSendLink, db.Millis(t)}
	if uid != 0 {
		query += ` AND actor_user_id = ?`
		args = append(args, uid)
	}
	var n int
	err := q.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// MailEnabled reports whether "Send link" can work on this server.
func (s *Service) MailEnabled() bool { return s.d.Cfg.SMTP.Enabled() && SendLinks.Get(s.d.Settings) }

func (s *Service) sendEmail(w http.ResponseWriter, r *http.Request, b bulkCtx) error {
	var p struct {
		To []string `json:"to"`
	}
	if err := params(b.req.Params, &p); err != nil {
		return err
	}
	if !SendLinks.Get(s.d.Settings) {
		return errSendOff
	}
	if !s.d.Cfg.SMTP.Enabled() {
		return errMailOff
	}
	to, err := recipients(p.To)
	if err != nil {
		return err
	}
	ctx := r.Context()
	sender, err := s.verifiedSender(ctx, b.p.UserID)
	if err != nil {
		return err
	}
	list, err := s.resolve(ctx, s.d.DB.R, b.p.UserID, b.sel)
	if err != nil {
		return err
	}
	if len(list) > MaxLinksPerEmail {
		return errLinks
	}
	d := mailData{Site: s.d.SiteName(), Sender: sender.Username}
	ids := make([]int64, 0, len(list))
	for _, it := range list {
		d.Links = append(d.Links, mailLink{Name: it.Name(), URL: s.d.Items.PublicURL(it.Slug)})
		ids = append(ids, it.ID)
	}
	d.Title = list[0].Name()
	if len(list) > 1 {
		d.Title = strconv.Itoa(len(list)) + " items"
	}

	// Count against the caps and record the send before any mail goes out.
	var auditID int64
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		day := s.now().Add(-24 * time.Hour)
		mine, err := sentSince(ctx, tx, b.p.UserID, day)
		if err != nil {
			return err
		}
		if left := SendLinksPerDay.Get(s.d.Settings) - mine; len(to) > left {
			if left <= 0 {
				return web.Errorf(http.StatusTooManyRequests, "You have sent your daily limit of links. Try again tomorrow.")
			}
			return web.Errorf(http.StatusTooManyRequests, "You can send %d more %s today.", left, plural(left, "email", "emails"))
		}
		all, err := sentSince(ctx, tx, 0, day)
		if err != nil {
			return err
		}
		if all+len(to) > SendLinksSitePerDay.Get(s.d.Settings) {
			return errSiteCap
		}
		e := accounts.Actor(r)
		e.Action, e.Target = auditSendLink, "user:"+strconv.FormatInt(b.p.UserID, 10)
		e.Details = map[string]any{"recipients": len(to), "to": to, "items": ids}
		if err := accounts.AuditTx(ctx, tx, e); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT last_insert_rowid()`).Scan(&auditID)
	})
	if err != nil {
		return err
	}

	// No transaction is open while talking to the mail server.
	sent := 0
	var sendErr error
	for _, addr := range to {
		m, err := s.mail.compose(addr, sender.Email, d)
		if err != nil {
			return err
		}
		if err := s.d.Mail.Send(context.WithoutCancel(ctx), m); err != nil {
			sendErr = err
			if errors.Is(err, snapmail.ErrDisabled) {
				break
			}
			s.log.Warn("send link email", "err", err)
			continue
		}
		sent++
	}
	if sent < len(to) {
		_ = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE audit_log SET details = json_set(details, '$.failed', ?) WHERE id = ?`, len(to)-sent, auditID)
			return err
		})
	}
	switch {
	case errors.Is(sendErr, snapmail.ErrDisabled):
		return errMailOff
	case sent == 0:
		return web.Errorf(http.StatusBadGateway, "The email could not be sent. Try again later.")
	}
	respond(w, map[string]any{"action": b.req.Action, "count": len(list), "recipients": len(to), "sent": sent})
	return nil
}
