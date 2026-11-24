package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// Mode says which item opens are notified.
type Mode string

// Modes.
const (
	ModeNone  Mode = "none"
	ModeFirst Mode = "first" // the first time a person opens the item
	ModeEvery Mode = "every" // every open, at most one message per item per 15 minutes
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeNone || m == ModeFirst || m == ModeEvery }

// Channels.
const (
	ChannelEmail = "email"
	ChannelNtfy  = "ntfy"
)

// Prefs are a user's notification settings. A user without a row gets
// the zero Prefs with NtfyServer set: everything off.
type Prefs struct {
	UserID       int64
	EmailEnabled bool
	NtfyEnabled  bool
	NtfyServer   string
	NtfyTopic    string
	NtfyTokenSet bool // the token itself never leaves this package
	Default      Mode // the old item-open setting: the default of the item-open rows
	Events       Grid // which events go to which channel
	UpdatedAt    time.Time

	tokenCT []byte
}

// AnyChannel reports whether at least one channel is on.
func (p Prefs) AnyChannel() bool { return p.EmailEnabled || p.NtfyEnabled }

// Channels returns the channels that are on.
func (p Prefs) Channels() []string {
	var out []string
	if p.EmailEnabled {
		out = append(out, ChannelEmail)
	}
	if p.NtfyEnabled {
		out = append(out, ChannelNtfy)
	}
	return out
}

func defaultPrefs(userID int64) Prefs {
	return Prefs{UserID: userID, NtfyServer: defaultNtfyServer, Default: ModeNone}
}

// Prefs loads a user's settings.
func (s *Service) Prefs(ctx context.Context, userID int64) (Prefs, error) {
	p := defaultPrefs(userID)
	var email, ntfy int64
	var def string
	var updated int64
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT email_enabled, ntfy_enabled, ntfy_server_url, ntfy_topic, ntfy_token_ct, default_event, updated_at
FROM notify_prefs WHERE user_id = ?`, userID).Scan(&email, &ntfy, &p.NtfyServer, &p.NtfyTopic, &p.tokenCT, &def, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		p.Events, err = s.loadGrid(ctx, userID, ModeNone)
		return p, err
	}
	if err != nil {
		return p, fmt.Errorf("notify: prefs: %w", err)
	}
	p.EmailEnabled, p.NtfyEnabled = email != 0, ntfy != 0
	p.NtfyTokenSet = len(p.tokenCT) > 0
	p.Default = Mode(def)
	p.UpdatedAt = db.Time(updated)
	p.Events, err = s.loadGrid(ctx, userID, p.Default)
	return p, err
}

// Enabled reports whether the user has a channel on (the stats page shows
// the per-item override only then).
func (s *Service) Enabled(ctx context.Context, userID int64) bool {
	p, err := s.Prefs(ctx, userID)
	return err == nil && p.AnyChannel()
}

// updatePrefs creates the user's row if needed and applies set (an UPDATE
// fragment like "email_enabled = ?") in one transaction.
func (s *Service) updatePrefs(ctx context.Context, userID int64, set string, args ...any) error {
	now := db.Millis(s.now())
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notify_prefs (user_id, updated_at) VALUES (?, ?) ON CONFLICT (user_id) DO NOTHING`,
			userID, now); err != nil {
			return fmt.Errorf("notify: prefs: %w", err)
		}
		//nolint:gosec // G202: set is a constant fragment from this package
		if _, err := tx.ExecContext(ctx, `UPDATE notify_prefs SET `+set+`, updated_at = ? WHERE user_id = ?`,
			append(args, now, userID)...); err != nil {
			return fmt.Errorf("notify: prefs: %w", err)
		}
		return nil
	})
}

// SetChannel turns a channel on or off.
func (s *Service) SetChannel(ctx context.Context, userID int64, channel string, on bool) error {
	switch channel {
	case ChannelEmail:
		return s.updatePrefs(ctx, userID, "email_enabled = ?", boolInt(on))
	case ChannelNtfy:
		return s.updatePrefs(ctx, userID, "ntfy_enabled = ?", boolInt(on))
	}
	return fmt.Errorf("notify: unknown channel %q", channel)
}

// SetDefault sets which item opens go to email and ntfy, as the old single
// setting did: none, first opens, or first and every open. It writes the
// item-open rows of the grid for both channels (and the old column).
func (s *Service) SetDefault(ctx context.Context, userID int64, m Mode) error {
	if !m.Valid() {
		return fmt.Errorf("notify: unknown mode %q", m)
	}
	if err := s.updatePrefs(ctx, userID, "default_event = ?", string(m)); err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		for _, ch := range []string{ChannelEmail, ChannelNtfy} {
			for ev, on := range map[string]bool{EventItemFirstOpen: m != ModeNone, EventItemOpen: m == ModeEvery} {
				if _, err := tx.ExecContext(ctx, `INSERT INTO notify_event_prefs (user_id, event, channel, enabled) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, event, channel) DO UPDATE SET enabled = excluded.enabled`, userID, ev, ch, boolInt(on)); err != nil {
					return fmt.Errorf("notify: prefs: %w", err)
				}
			}
		}
		return nil
	})
}

// NtfyInput is the ntfy form. Token nil keeps the stored token; a pointer
// to "" removes it.
type NtfyInput struct {
	Server, Topic string
	Token         *string
}

// Field errors from SaveNtfy, keyed by form field.
type FieldErrors map[string]string

func (f FieldErrors) Error() string {
	keys := make([]string, 0, len(f))
	for k, v := range f {
		keys = append(keys, k+": "+v)
	}
	return strings.Join(keys, "; ")
}

// SaveNtfy validates and stores the ntfy server, topic and token. The
// token is sealed before it reaches the database.
func (s *Service) SaveNtfy(ctx context.Context, userID int64, in NtfyInput) error {
	errs := FieldErrors{}
	server, problem := s.normalizeServer(in.Server)
	if problem != "" {
		errs["server"] = problem
	}
	topic := strings.TrimSpace(in.Topic)
	if !validTopic(topic) {
		errs["topic"] = "Use 1–64 letters, digits, - or _."
	}
	if in.Token != nil && *in.Token != "" && !validToken(*in.Token) {
		errs["token"] = "Paste the access token without spaces."
	}
	if len(errs) > 0 {
		return errs
	}
	if in.Token == nil {
		// A kept token goes only to the server it was given for: pointing
		// ntfy at another server (perhaps someone else's) drops it.
		old, err := s.Prefs(ctx, userID)
		if err != nil {
			return err
		}
		if old.NtfyTokenSet && old.NtfyServer != server {
			return s.updatePrefs(ctx, userID, "ntfy_server_url = ?, ntfy_topic = ?, ntfy_token_ct = NULL", server, topic)
		}
		return s.updatePrefs(ctx, userID, "ntfy_server_url = ?, ntfy_topic = ?", server, topic)
	}
	var ct any // NULL removes the token
	if *in.Token != "" {
		sealed, err := s.seal.seal(userID, *in.Token)
		if err != nil {
			return err
		}
		ct = sealed
	}
	return s.updatePrefs(ctx, userID, "ntfy_server_url = ?, ntfy_topic = ?, ntfy_token_ct = ?", server, topic, ct)
}

// ntfyToken decrypts the user's stored token ("" when none).
func (s *Service) ntfyToken(p Prefs) (string, error) {
	if len(p.tokenCT) == 0 {
		return "", nil
	}
	return s.seal.open(p.UserID, p.tokenCT)
}

// normalizeServer checks an ntfy server URL: http(s), a host, no user
// info, query or fragment. The path is kept (ntfy may live under one) and
// the trailing slash dropped. problem is a message for the form.
func (s *Service) normalizeServer(raw string) (server, problem string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultNtfyServer, ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || len(raw) > 512 || u.Opaque != "" {
		return "", "Enter a URL like https://ntfy.sh."
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", "Leave out user names, queries and fragments."
	}
	if err := s.out.CheckURL(u.Scheme, u.Host, false); err != nil {
		if u.Scheme == "http" {
			return "", "Use https; this server does not allow plain http."
		}
		return "", "Enter a URL like https://ntfy.sh."
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), ""
}

// validTopic follows ntfy's rule: letters, digits, _ and -, up to 64.
func validTopic(t string) bool {
	if t == "" || len(t) > 64 {
		return false
	}
	for _, r := range t {
		if !wordChar(r) {
			return false
		}
	}
	return true
}

// validToken accepts printable ASCII without spaces, up to 256 bytes.
func validToken(t string) bool {
	if t == "" || len(t) > 256 {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] <= ' ' || t[i] > '~' {
			return false
		}
	}
	return true
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- per-item overrides ----

// Override returns the user's override for an item ("" means the default).
func (s *Service) Override(ctx context.Context, userID, itemID int64) (Mode, error) {
	var m string
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT event FROM notify_overrides WHERE subject_type = 'item' AND subject_id = ? AND user_id = ?`,
		strconv.FormatInt(itemID, 10), userID).Scan(&m)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("notify: override: %w", err)
	}
	return Mode(m), nil
}

// SetOverride sets the user's override for one of their items; "" goes
// back to the default. The caller checks ownership.
func (s *Service) SetOverride(ctx context.Context, userID, itemID int64, m Mode) error {
	id := strconv.FormatInt(itemID, 10)
	if m == "" {
		_, err := s.d.DB.W.ExecContext(ctx, `DELETE FROM notify_overrides WHERE subject_type = 'item' AND subject_id = ?`, id)
		return err
	}
	if !m.Valid() {
		return fmt.Errorf("notify: unknown mode %q", m)
	}
	_, err := s.d.DB.W.ExecContext(ctx, `INSERT INTO notify_overrides (subject_type, subject_id, user_id, event, updated_at)
VALUES ('item', ?, ?, ?, ?) ON CONFLICT (subject_type, subject_id) DO UPDATE SET user_id = excluded.user_id, event = excluded.event,
updated_at = excluded.updated_at`, id, userID, string(m), db.Millis(s.now()))
	return err
}

// ---- delivery status ----

// Status is the last delivery result on one channel.
type Status struct {
	LastOK      *time.Time
	LastError   string
	LastErrorAt *time.Time
}

// Failing reports whether the last attempt failed.
func (st Status) Failing() bool {
	return st.LastErrorAt != nil && (st.LastOK == nil || st.LastErrorAt.After(*st.LastOK))
}

// Statuses returns the user's last delivery results by channel.
func (s *Service) Statuses(ctx context.Context, userID int64) (map[string]Status, error) {
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT channel, last_ok_at, last_error, last_error_at FROM notify_status WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("notify: status: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Status{}
	for rows.Next() {
		var ch string
		var ok, errAt sql.NullInt64
		var st Status
		if err := rows.Scan(&ch, &ok, &st.LastError, &errAt); err != nil {
			return nil, fmt.Errorf("notify: status: %w", err)
		}
		st.LastOK, st.LastErrorAt = db.NullTime(ok), db.NullTime(errAt)
		out[ch] = st
	}
	return out, rows.Err()
}

// record stores a delivery result. msg must be safe to show (Describe).
func (s *Service) record(ctx context.Context, userID int64, channel string, failure string) {
	s.d.Metrics.NotifyDelivery(channel, failure == "")
	if err := s.recordOn(ctx, s.d.DB.W, userID, channel, failure); err != nil {
		s.log.Warn("notify: status not saved", "err", err)
	}
}

// recordTx is record inside a transaction.
func (s *Service) recordTx(ctx context.Context, tx *sql.Tx, userID int64, channel string, failure string) error {
	return s.recordOn(ctx, tx, userID, channel, failure)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Service) recordOn(ctx context.Context, x execer, userID int64, channel string, failure string) error {
	now := db.Millis(s.now())
	var err error
	if failure == "" {
		_, err = x.ExecContext(ctx, `INSERT INTO notify_status (user_id, channel, last_ok_at) VALUES (?, ?, ?)
ON CONFLICT (user_id, channel) DO UPDATE SET last_ok_at = excluded.last_ok_at`, userID, channel, now)
	} else {
		_, err = x.ExecContext(ctx, `INSERT INTO notify_status (user_id, channel, last_error, last_error_at) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, channel) DO UPDATE SET last_error = excluded.last_error, last_error_at = excluded.last_error_at`,
			userID, channel, oneLine(failure, 200), now)
	}
	return err
}
