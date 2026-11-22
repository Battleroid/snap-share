package mail

import (
	"context"
	"errors"
	"fmt"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/Battleroid/snap-share/internal/config"
)

// SMTP sends mail through an SMTP server. A connection is opened per message;
// snap sends little mail and this avoids holding idle connections.
type SMTP struct {
	cfg     config.SMTP
	timeout time.Duration
}

// NewSMTP returns a sender for cfg. TLS modes: implicit (TLS from the first
// byte, usually port 465), starttls (upgrade required, usually 587), none
// (plain text, for a local relay such as Mailpit).
func NewSMTP(cfg config.SMTP) *SMTP {
	return &SMTP{cfg: cfg, timeout: 15 * time.Second}
}

func (s *SMTP) client() (*gomail.Client, error) {
	opts := []gomail.Option{gomail.WithTimeout(s.timeout)}
	switch s.cfg.TLS {
	case "implicit":
		opts = append(opts, gomail.WithSSL())
	case "starttls":
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	case "none":
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		return nil, fmt.Errorf("smtp: unknown TLS mode %q", s.cfg.TLS)
	}
	opts = append(opts, gomail.WithPort(s.cfg.Port))
	if s.cfg.User != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(s.cfg.User),
			gomail.WithPassword(s.cfg.Password))
	}
	return gomail.NewClient(s.cfg.Host, opts...)
}

// Send implements Sender. Errors never include the message body.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if len(m.To) == 0 {
		return errors.New("smtp: no recipients")
	}
	msg := gomail.NewMsg()
	if err := msg.From(s.cfg.From); err != nil {
		return fmt.Errorf("smtp: from address: %w", err)
	}
	if err := msg.To(m.To...); err != nil {
		return fmt.Errorf("smtp: recipient: %w", err)
	}
	if m.ReplyTo != "" {
		if err := msg.ReplyTo(m.ReplyTo); err != nil {
			return fmt.Errorf("smtp: reply-to: %w", err)
		}
	}
	msg.Subject(m.Subject)
	msg.SetDate()
	msg.SetMessageID()
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}
	c, err := s.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*s.timeout)
	defer cancel()
	if err := c.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("smtp: send via %s:%d: %w", s.cfg.Host, s.cfg.Port, err)
	}
	return nil
}
