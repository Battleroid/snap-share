package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/ratelimit"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
)

// Job kinds.
const (
	jobOpen = "notify.open" // decide and render an item-open message, then fan out
	jobSend = "notify.send" // deliver one message to one channel
)

type openJob struct {
	ItemID  int64 `json:"item_id"`
	First   bool  `json:"first,omitempty"`
	Count   int64 `json:"count,omitempty"`
	From    int64 `json:"from,omitempty"` // unix ms of the first and last open
	To      int64 `json:"to,omitempty"`
	Summary bool  `json:"summary,omitempty"` // a coalescing window ended
}

type sendJob struct {
	UserID  int64   `json:"user_id"`
	Channel string  `json:"channel"`
	Message Message `json:"message"`
}

var _ secrets.Notifier = (*Service)(nil)

// effectiveModeSQL is the item's override (if its owner wrote it) or the
// owner's default, for owners with a channel on.
const effectiveModeSQL = `SELECT coalesce(o.event, p.default_event) FROM items i
JOIN notify_prefs p ON p.user_id = i.owner_id
LEFT JOIN notify_overrides o ON o.subject_type = 'item' AND o.subject_id = CAST(i.id AS TEXT) AND o.user_id = i.owner_id
WHERE i.id = ? AND (p.email_enabled = 1 OR p.ntfy_enabled = 1)`

// ItemsOpened is the items counter-flush hook (items.Deps.OnOpened). For
// items whose owner wants to hear about this open it queues a notify.open
// job, a few seconds out so the stats writer has stored the event the
// message names.
func (s *Service) ItemsOpened(ctx context.Context, opened []items.Opened) {
	if s.d.Jobs == nil || len(opened) == 0 {
		return
	}
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		for _, o := range opened {
			var mode string
			err := tx.QueryRowContext(ctx, effectiveModeSQL, o.ItemID).Scan(&mode)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if Mode(mode) == ModeNone || (Mode(mode) == ModeFirst && !o.First) {
				continue
			}
			j := openJob{ItemID: o.ItemID, First: o.First, Count: o.Count, From: db.Millis(o.From), To: db.Millis(o.To)}
			if _, err := s.d.Jobs.EnqueueTx(ctx, tx, jobOpen, j, jobs.Delay(openDelay)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.log.Error("notify: queue opens", "err", err)
	}
}

// itemMode is the effective mode for it ("" when no channel is on).
func (s *Service) itemMode(ctx context.Context, it *items.Item) (Mode, error) {
	var mode string
	err := s.d.DB.R.QueryRowContext(ctx, effectiveModeSQL, it.ID).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return Mode(mode), err
}

// handleOpen runs notify.open: first opens are sent as they are; every-open
// messages go out at most once per item per coalesceWindow, and the opens
// in between are summed into one message when the window ends.
func (s *Service) handleOpen(ctx context.Context, payload json.RawMessage) error {
	var j openJob
	if err := json.Unmarshal(payload, &j); err != nil {
		return jobs.Permanent(err)
	}
	it, err := s.d.Items.Get(ctx, j.ItemID)
	if errors.Is(err, items.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	mode, err := s.itemMode(ctx, it)
	if err != nil {
		return err
	}
	p, err := s.Prefs(ctx, it.OwnerID)
	if err != nil {
		return err
	}
	now := s.now()
	link := s.d.Items.PublicURL(it.Slug)

	if j.Summary {
		last, ok, err := stats.LastItemOpen(ctx, s.d.DB.R, it.ID, now)
		if err != nil {
			return err
		}
		return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			n, since, err := takePending(ctx, tx, it.ID, now)
			if err != nil || n == 0 || mode != ModeEvery {
				return err
			}
			var lv *visitor
			if ok {
				v := visitorOf(last.Event)
				lv = &v
			}
			return s.fanOutTx(ctx, tx, p, summaryMessage(it.Name(), link, n, since, lv, now), s.itemMsgs)
		})
	}

	if mode == ModeNone || mode == "" || (mode == ModeFirst && !j.First) {
		return nil
	}
	var ev stats.StoredEvent
	var ok bool
	if j.First {
		ev, ok, err = stats.FirstItemOpen(ctx, s.d.DB.R, it.ID)
	} else {
		ev, ok, err = stats.LastItemOpen(ctx, s.d.DB.R, it.ID, db.Time(j.To).Add(time.Second))
	}
	if err != nil {
		return err
	}
	v := visitor{At: db.Time(j.To)}
	if j.First {
		v.At = db.Time(j.From)
	}
	if ok {
		v = visitorOf(ev.Event)
	}
	msg := openMessage(it.Name(), link, v, now)
	if j.Count > 1 {
		msg.Title += fmt.Sprintf(" %d times", j.Count)
	}
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if mode == ModeEvery {
			send, err := s.coalesce(ctx, tx, j, now)
			if err != nil || !send {
				return err
			}
		}
		return s.fanOutTx(ctx, tx, p, msg, s.itemMsgs)
	})
}

// coalesce reports whether a message for this open may go out now. If not,
// the open is added to the item's pending count, and the first one held
// back schedules the summary for the end of the window.
func (s *Service) coalesce(ctx context.Context, tx *sql.Tx, j openJob, now time.Time) (bool, error) {
	id := strconv.FormatInt(j.ItemID, 10)
	var until, pending int64
	err := tx.QueryRowContext(ctx, `SELECT until, pending FROM notify_windows WHERE subject_type = 'item' AND subject_id = ?`, id).
		Scan(&until, &pending)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && until <= db.Millis(now)):
		_, err = tx.ExecContext(ctx, `INSERT INTO notify_windows (subject_type, subject_id, until, pending, pending_from) VALUES ('item', ?, ?, 0, NULL)
ON CONFLICT (subject_type, subject_id) DO UPDATE SET until = excluded.until, pending = 0, pending_from = NULL`,
			id, db.Millis(now.Add(coalesceWindow)))
		return err == nil, err
	case err != nil:
		return false, err
	}
	if pending == 0 {
		delay := db.Time(until).Sub(now)
		if _, err := s.d.Jobs.EnqueueTx(ctx, tx, jobOpen, openJob{ItemID: j.ItemID, Summary: true}, jobs.Delay(delay)); err != nil {
			return false, err
		}
	}
	n := max(j.Count, 1)
	_, err = tx.ExecContext(ctx, `UPDATE notify_windows SET pending = pending + ?, pending_from = coalesce(pending_from, ?)
WHERE subject_type = 'item' AND subject_id = ?`, n, j.From, id)
	return false, err
}

// takePending ends an item's window: it returns the opens held back (and
// when the first was) and starts a new window, since the summary is a
// message too.
func takePending(ctx context.Context, tx *sql.Tx, itemID int64, now time.Time) (int64, time.Time, error) {
	id := strconv.FormatInt(itemID, 10)
	var pending int64
	var from sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT pending, pending_from FROM notify_windows WHERE subject_type = 'item' AND subject_id = ?`, id).
		Scan(&pending, &from)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && pending == 0) {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notify_windows SET until = ?, pending = 0, pending_from = NULL
WHERE subject_type = 'item' AND subject_id = ?`, db.Millis(now.Add(coalesceWindow)), id); err != nil {
		return 0, time.Time{}, err
	}
	var since time.Time
	if from.Valid {
		since = db.Time(from.Int64)
	}
	return pending, since, nil
}

// fanOutTx queues m for each of the user's channels that is on. With a
// limit, the message counts against it (MessagesPerHour); over it, it is
// dropped. Email is skipped, and its status set, when SMTP is not set up.
func (s *Service) fanOutTx(ctx context.Context, tx *sql.Tx, p Prefs, m Message, limit *ratelimit.Limiter) error {
	chans := p.Channels()
	if len(chans) == 0 {
		return nil
	}
	if limit != nil && !allow(limit, p.UserID) {
		s.log.Info("notify: message dropped by the hourly limit", "user", p.UserID, "kind", m.Kind)
		return nil
	}
	for _, ch := range chans {
		if ch == ChannelEmail && !s.mailEnabled() {
			if err := s.recordTx(ctx, tx, p.UserID, ch, errNoMail().Error()); err != nil {
				return err
			}
			continue
		}
		if _, err := s.d.Jobs.EnqueueTx(ctx, tx, jobSend, sendJob{UserID: p.UserID, Channel: ch, Message: m},
			jobs.MaxAttempts(sendAttempts)); err != nil {
			return err
		}
	}
	return nil
}

func allow(l *ratelimit.Limiter, userID int64) bool {
	ok, _ := l.Allow(strconv.FormatInt(userID, 10))
	return ok
}

// handleSend runs notify.send: one message to one channel. A channel
// switched off since the message was queued is skipped. 4xx answers,
// blocked addresses and missing configuration fail at once; everything
// else is retried with backoff.
func (s *Service) handleSend(ctx context.Context, payload json.RawMessage) error {
	var j sendJob
	if err := json.Unmarshal(payload, &j); err != nil {
		return jobs.Permanent(err)
	}
	p, err := s.Prefs(ctx, j.UserID)
	if err != nil {
		return err
	}
	if (j.Channel == ChannelEmail && !p.EmailEnabled) || (j.Channel == ChannelNtfy && !p.NtfyEnabled) {
		return nil
	}
	err = s.deliver(ctx, p, j.Channel, j.Message)
	if err != nil {
		s.record(ctx, j.UserID, j.Channel, err.Error())
		s.log.Warn("notify: delivery failed", "user", j.UserID, "channel", j.Channel, "kind", j.Message.Kind, "err", err.Error())
		return asJobError(err)
	}
	s.record(ctx, j.UserID, j.Channel, "")
	return nil
}

// Notify implements secrets.Notifier: a reveal of a secret created with
// notify on, and a burn after too many wrong passphrases (always reported
// when a channel is on, and not capped). The message names the owner's
// note and the secrets dashboard, never the secret's link, id or key.
func (s *Service) Notify(ctx context.Context, n secrets.Notification) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.notifySecret(ctx, n); err != nil {
			s.log.Warn("notify: secret notification not queued", "err", err)
		}
	}()
}

func (s *Service) notifySecret(ctx context.Context, n secrets.Notification) error {
	if s.d.Jobs == nil {
		return nil
	}
	p, err := s.Prefs(ctx, n.OwnerID)
	if err != nil || !p.AnyChannel() {
		return err
	}
	now := s.now()
	v := visitor{Client: n.UASummary, IP: n.IP, At: n.At}
	var m Message
	limit := s.secretMsgs
	switch n.Kind {
	case stats.KindBurned:
		m, limit = burnMessage(n.Note, v, s.secretsURL(), now), nil
	case stats.KindRevealed:
		m = revealMessage(n.Note, n.ViewsLeft, n.MaxViews, v, s.secretsURL(), now)
	default:
		return nil
	}
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error { return s.fanOutTx(ctx, tx, p, m, limit) })
}

// ErrTestLimit is returned by SendTest when the user sent too many tests.
var ErrTestLimit = errors.New("too many test messages; try again in a few minutes")

// SendTest delivers a test message to one channel now (not queued), so the
// settings page can show the result. The channel need not be on.
func (s *Service) SendTest(ctx context.Context, userID int64, channel string) error {
	if channel != ChannelEmail && channel != ChannelNtfy {
		return failed(true, "unknown channel")
	}
	if !allow(s.tests, userID) {
		return ErrTestLimit
	}
	p, err := s.Prefs(ctx, userID)
	if err != nil {
		return err
	}
	err = s.deliver(ctx, p, channel, testMessage(s.siteName(), s.settingsURL(), s.now()))
	if err != nil {
		s.record(ctx, userID, channel, err.Error())
		return err
	}
	s.record(ctx, userID, channel, "")
	return nil
}
