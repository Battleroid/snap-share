package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/version"
)

// deliveryError is a failed delivery with a message that is safe to show
// on the settings page and to log: never a URL, topic, token or address.
type deliveryError struct {
	msg       string
	permanent bool
}

func (e *deliveryError) Error() string { return e.msg }

func failed(permanent bool, format string, a ...any) error {
	return &deliveryError{msg: fmt.Sprintf(format, a...), permanent: permanent}
}

// asJobError marks permanent failures so the job is not retried.
func asJobError(err error) error {
	var de *deliveryError
	if errors.As(err, &de) && de.permanent {
		return jobs.Permanent(err)
	}
	return err
}

// deliver sends m to one of the user's channels.
func (s *Service) deliver(ctx context.Context, p Prefs, channel string, m Message) error {
	switch channel {
	case ChannelEmail:
		return s.sendEmail(ctx, p.UserID, m)
	case ChannelNtfy:
		return s.sendNtfy(ctx, p, m)
	}
	return failed(true, "unknown channel")
}

// sendNtfy publishes m to <server>/<topic>: the detail as the plain-text
// body, Title, Tags, Click and Priority headers, and the user's access
// token as a bearer token. Non-ASCII header values are RFC 2047 encoded.
func (s *Service) sendNtfy(ctx context.Context, p Prefs, m Message) error {
	if p.NtfyTopic == "" {
		return failed(true, "ntfy: no topic set")
	}
	token, err := s.ntfyToken(p)
	if err != nil {
		return failed(true, "ntfy: %v", err)
	}
	server := strings.TrimRight(p.NtfyServer, "/")
	body := oneLine(m.Detail, maxDetailRunes)
	if body == "" {
		body = oneLine(m.Title, maxTitleRunes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/"+p.NtfyTopic, strings.NewReader(body))
	if err != nil {
		return failed(true, "ntfy: invalid server URL")
	}
	h := req.Header
	h.Set("User-Agent", "snap/"+version.Version)
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Title", headerValue(oneLine(m.Title, maxTitleRunes)))
	if len(m.Tags) > 0 {
		h.Set("Tags", strings.Join(m.Tags, ","))
	}
	if m.Click != "" {
		h.Set("Click", m.Click)
	}
	if m.Priority > 0 {
		h.Set("Priority", strconv.Itoa(m.Priority))
	}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	res, err := s.out.Do(req)
	if err != nil {
		perm := errors.Is(err, ErrBlockedAddress) || errors.Is(err, ErrScheme) || errors.Is(err, ErrRedirect)
		return failed(perm, "ntfy: %s", Describe(err))
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return failed(false, "ntfy: server answered %d", res.StatusCode)
	}
	return failed(true, "ntfy: server answered %d", res.StatusCode)
}

// headerValue returns s as is when it is printable ASCII, else as an
// RFC 2047 encoded word, which ntfy decodes.
func headerValue(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] > '~' {
			if !utf8.ValidString(s) {
				s = strings.ToValidUTF8(s, "?")
			}
			return mime.BEncoding.Encode("UTF-8", s)
		}
	}
	return s
}

// sendEmail mails m to the user's verified address.
func (s *Service) sendEmail(ctx context.Context, userID int64, m Message) error {
	var email sql.NullString
	var verified sql.NullInt64
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT email, email_verified_at FROM users WHERE id = ? AND status = 'active'`,
		userID).Scan(&email, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return failed(true, "email: account not active")
	}
	if err != nil {
		return fmt.Errorf("email: %w", err)
	}
	if !email.Valid || email.String == "" || !verified.Valid {
		return failed(true, "email: address not verified")
	}
	subject, text, html, err := m.emailParts(s.siteName(), s.settingsURL())
	if err != nil {
		return failed(true, "email: %v", err)
	}
	err = s.d.Mail.Send(ctx, mail.Message{To: []string{email.String}, Subject: subject, Text: text, HTML: html})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mail.ErrDisabled):
		return failed(true, "email: not set up on this server")
	}
	return failed(false, "email: could not send")
}
