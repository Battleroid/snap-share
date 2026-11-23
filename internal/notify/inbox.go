package notify

// The in-app inbox: the header bell and /notifications. It is always on and
// lists, for the owner, every item's first open, every reveal of a secret
// and every burn after wrong passphrases. Email and ntfy (dispatch.go) are
// extra channels with their own settings. Rows hold the visitor's browser
// and IP, so they are deleted after stats.retention_days.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
)

// Inbox entry kinds.
const (
	InboxItemOpened     = "item_opened"
	InboxSecretRevealed = "secret_revealed"
	InboxSecretBurned   = "secret_burned"
)

// Inbox limits.
const (
	// InboxPerUser is how many entries a user keeps; older ones are deleted.
	InboxPerUser = 200
	// DefaultRetentionDays applies when Deps.RetentionDays is nil (tests).
	DefaultRetentionDays = 90

	inboxPanelSize  = 20 // entries in the bell's popover
	inboxPageSize   = 50 // entries per page of /notifications
	inboxSweepEvery = time.Hour
)

// ErrNoEntry is returned for a notification that does not exist or belongs
// to someone else.
var ErrNoEntry = errors.New("notify: no such notification")

// Entry is one in-app notification.
type Entry struct {
	ID          int64
	UserID      int64
	Kind        string
	Status      string // "info", or "down" for a burn
	Title       string // "photo.jpg opened"
	Body        string // "Firefox · macOS · 203.0.113.4"
	Link        string // a path on the app origin: the stats page or the timeline
	SubjectType string // "item" or "secret"
	SubjectID   string
	At          time.Time
	ReadAt      time.Time // zero while unread
}

// Unread reports whether the entry has not been read.
func (e Entry) Unread() bool { return e.ReadAt.IsZero() }

// Icon is the Patchbay icon for the entry.
func (e Entry) Icon() string {
	switch {
	case e.Status == "down":
		return "status-down"
	case e.Kind == InboxItemOpened:
		return "eye"
	case e.Kind == InboxSecretRevealed:
		return "unlock"
	}
	return "bell"
}

// ago is the entry's time as the inbox shows it: "now", "2m", "1h",
// "yesterday", "3d", then the date.
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	}
	return t.UTC().Format("2006-01-02")
}

func openEntry(it *items.Item, v visitor) Entry {
	id := strconv.FormatInt(it.ID, 10)
	return Entry{
		UserID: it.OwnerID, Kind: InboxItemOpened, Status: "info",
		Title: oneLine(it.Name(), maxFieldRunes) + " opened", Body: v.who(),
		Link: "/dashboard/items/" + id + "/stats", SubjectType: "item", SubjectID: id, At: v.At,
	}
}

// inboxSecretName is `Secret "prod db"`, or "Text secret" without a note.
func inboxSecretName(note, kind string) string {
	if note = oneLine(note, maxFieldRunes); note != "" {
		return fmt.Sprintf("Secret %q", note)
	}
	switch kind {
	case "text":
		return "Text secret"
	case "file":
		return "File secret"
	}
	return "Secret"
}

// secretLink is the owner's timeline of the secret.
func secretLink(id string) string {
	if !validSecretID(id) {
		return "/dashboard/secrets"
	}
	return "/dashboard/secrets/" + id + "/timeline"
}

// validSecretID reports whether id looks like a secret id (base64url of 16
// bytes), so it can go into a path as it is.
func validSecretID(id string) bool {
	if len(id) != 22 {
		return false
	}
	for _, c := range []byte(id) {
		if !slugByte(c) {
			return false
		}
	}
	return true
}

func slugByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

func secretEntry(n secrets.Notification) (Entry, bool) {
	v := visitor{Client: n.UASummary, IP: n.IP, At: n.At}
	e := Entry{UserID: n.OwnerID, Link: secretLink(n.SecretID), SubjectType: "secret", SubjectID: n.SecretID, At: n.At}
	name := inboxSecretName(n.Note, n.SecretKind)
	switch n.Kind {
	case stats.KindRevealed:
		e.Kind, e.Status = InboxSecretRevealed, "info"
		e.Title = name + " revealed"
		e.Body = joinDot(v.who(), viewsLeftText(n.ViewsLeft, n.MaxViews))
	case stats.KindBurned:
		e.Kind, e.Status = InboxSecretBurned, "down"
		e.Title = name + " burned after wrong passphrases"
		if who := v.who(); who != "" {
			e.Body = "last attempt " + who
		}
	default:
		return Entry{}, false
	}
	return e, true
}

// inboxOpens records first opens. It needs neither the job queue nor a
// push channel. The visitor comes from the counter flush; the stored event
// is the fallback.
func (s *Service) inboxOpens(ctx context.Context, opened []items.Opened) {
	if s.d.Items == nil {
		return
	}
	var entries []Entry
	for _, o := range opened {
		if !o.First {
			continue
		}
		it, err := s.d.Items.Get(ctx, o.ItemID)
		if err != nil {
			if !errors.Is(err, items.ErrNotFound) {
				s.log.Error("notify: inbox open", "err", err)
			}
			continue
		}
		v := visitor{Client: o.Client, IP: o.IP, At: o.From}
		if v.Client == "" && v.IP == "" {
			if ev, ok, err := stats.FirstItemOpen(ctx, s.d.DB.R, it.ID); err == nil && ok {
				v = visitorOf(ev.Event)
			}
		}
		entries = append(entries, openEntry(it, v))
	}
	if err := s.addInbox(ctx, entries...); err != nil {
		s.log.Error("notify: inbox opens", "err", err)
	}
}

// inboxSecret records a reveal or a burn of an owned secret.
func (s *Service) inboxSecret(ctx context.Context, n secrets.Notification) error {
	if n.OwnerID == 0 {
		return nil
	}
	e, ok := secretEntry(n)
	if !ok {
		return nil
	}
	return s.addInbox(ctx, e)
}

// capSQL deletes a user's entries beyond the newest InboxPerUser.
const capSQL = `DELETE FROM inbox WHERE user_id = ? AND id IN
  (SELECT id FROM inbox WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT -1 OFFSET ?)`

// addInbox stores entries, keeps each user at InboxPerUser, and sends the
// entries to their owners' open streams once the transaction has committed.
func (s *Service) addInbox(ctx context.Context, entries ...Entry) error {
	if len(entries) == 0 {
		return nil
	}
	now := s.now()
	unread := map[int64]int{}
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		clear(unread)
		for i := range entries {
			e := &entries[i]
			if e.At.IsZero() {
				e.At = now
			}
			e.Title, e.Body = oneLine(e.Title, maxTitleRunes), oneLine(e.Body, maxDetailRunes)
			res, err := tx.ExecContext(ctx, `INSERT INTO inbox (user_id, kind, status, title, body, link, subject_type, subject_id, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.UserID, e.Kind, e.Status, e.Title, e.Body, e.Link, e.SubjectType, e.SubjectID, db.Millis(e.At))
			if err != nil {
				return fmt.Errorf("insert: %w", err)
			}
			if e.ID, err = res.LastInsertId(); err != nil {
				return err
			}
			unread[e.UserID] = 0
		}
		for u := range unread {
			if _, err := tx.ExecContext(ctx, capSQL, u, u, InboxPerUser); err != nil {
				return fmt.Errorf("cap: %w", err)
			}
			n, err := unreadCount(ctx, tx, u)
			if err != nil {
				return err
			}
			unread[u] = n
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("notify: inbox: %w", err)
	}
	for _, e := range entries {
		s.streams.publish(e.UserID, entryEvent(e, unread[e.UserID], now))
	}
	return nil
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func unreadCount(ctx context.Context, q queryRower, userID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM inbox WHERE user_id = ? AND read_at IS NULL`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("notify: unread: %w", err)
	}
	return n, nil
}

// Unread is the user's number of unread notifications, for the header
// bell; 0 when it can't be read.
func (s *Service) Unread(ctx context.Context, userID int64) int {
	n, err := unreadCount(ctx, s.d.DB.R, userID)
	if err != nil && ctx.Err() == nil {
		s.log.Error("notify: unread", "err", err)
	}
	return n
}

// Inbox returns the user's entries, newest first.
func (s *Service) Inbox(ctx context.Context, userID int64, limit, offset int) ([]Entry, error) {
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT id, kind, status, title, body, link, subject_type, subject_id, created_at, read_at
FROM inbox WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("notify: inbox: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		e := Entry{UserID: userID}
		var at int64
		var read sql.NullInt64
		if err := rows.Scan(&e.ID, &e.Kind, &e.Status, &e.Title, &e.Body, &e.Link, &e.SubjectType, &e.SubjectID, &at, &read); err != nil {
			return nil, fmt.Errorf("notify: inbox: %w", err)
		}
		e.At = db.Time(at)
		if read.Valid {
			e.ReadAt = db.Time(read.Int64)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) inboxTotal(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.d.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM inbox WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("notify: inbox: %w", err)
	}
	return n, nil
}

// MarkRead marks one of the user's entries read and returns the unread
// count. Another user's entry is ErrNoEntry and is left alone.
func (s *Service) MarkRead(ctx context.Context, userID, id int64) (int, error) {
	var unread int
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE inbox SET read_at = coalesce(read_at, ?) WHERE id = ? AND user_id = ?`,
			db.Millis(s.now()), id, userID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return errors.Join(ErrNoEntry, err)
		}
		unread, err = unreadCount(ctx, tx, userID)
		return err
	})
	if err != nil {
		return 0, err
	}
	s.streams.publish(userID, readEvent(unread, id))
	return unread, nil
}

// MarkAllRead marks all of the user's entries read.
func (s *Service) MarkAllRead(ctx context.Context, userID int64) error {
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE inbox SET read_at = ? WHERE user_id = ? AND read_at IS NULL`, db.Millis(s.now()), userID)
		return err
	})
	if err != nil {
		return fmt.Errorf("notify: read all: %w", err)
	}
	s.streams.publish(userID, readEvent(0, 0))
	return nil
}

// DeleteSecretTx deletes the entries about a secret inside the transaction
// that deletes the secret itself (secrets.Deps.OnDelete): a purged secret's
// note and id must not outlive it in the database.
func (s *Service) DeleteSecretTx(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM inbox WHERE subject_type = 'secret' AND subject_id = ?`, id); err != nil {
		return fmt.Errorf("notify: inbox: %w", err)
	}
	return nil
}

func (s *Service) retentionDays() int {
	if s.d.RetentionDays != nil {
		return s.d.RetentionDays()
	}
	return DefaultRetentionDays
}

// SweepInbox deletes entries older than the stats retention period, those
// beyond the newest InboxPerUser of each user, and those whose item or
// secret is gone or has another owner.
func (s *Service) SweepInbox(ctx context.Context) error {
	cutoff := db.Millis(s.now().AddDate(0, 0, -s.retentionDays()))
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM inbox WHERE created_at < ?`, []any{cutoff}},
			{`DELETE FROM inbox WHERE id IN (SELECT id FROM (SELECT id, row_number() OVER
  (PARTITION BY user_id ORDER BY created_at DESC, id DESC) AS n FROM inbox) WHERE n > ?)`, []any{InboxPerUser}},
			{`DELETE FROM inbox WHERE subject_type = 'item' AND NOT EXISTS
  (SELECT 1 FROM items WHERE items.id = CAST(inbox.subject_id AS INTEGER) AND items.owner_id = inbox.user_id)`, nil},
			{`DELETE FROM inbox WHERE subject_type = 'secret' AND NOT EXISTS
  (SELECT 1 FROM secrets WHERE secrets.id = inbox.subject_id AND secrets.owner_id = inbox.user_id)`, nil},
		} {
			if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
				return fmt.Errorf("notify: inbox sweep: %w", err)
			}
		}
		return nil
	})
}
