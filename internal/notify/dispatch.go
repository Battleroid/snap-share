package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/ratelimit"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/web"
)

// Job kinds.
const (
	jobOpen   = "notify.open"   // decide and render an item-open message, then fan out
	jobSend   = "notify.send"   // deliver one message to one channel
	jobSecret = "notify.secret" // the summary of wrong passphrases held back by coalescing
)

// Subject types of coalescing windows.
const (
	subjItem   = "item"
	subjSecret = "secret"
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

// secretJob is the wrong-passphrase summary. It holds the owner's note and
// the secret id (for the window and the inbox link), never key material.
type secretJob struct {
	OwnerID    int64  `json:"owner_id"`
	SecretID   string `json:"secret_id"`
	SecretKind string `json:"secret_kind,omitempty"`
	Note       string `json:"note,omitempty"`
}

var _ secrets.Notifier = (*Service)(nil)

// emit sends one event: an inbox entry when the user has the bell on for
// it, and m on each email and ntfy channel that is on for it. push false
// leaves email and ntfy out (an item or secret setting turned the subject
// off). With a limit, the message counts against it (MessagesPerHour).
func (s *Service) emit(ctx context.Context, p Prefs, event string, e *Entry, m Message, push bool, limit *ratelimit.Limiter) error {
	var errs []error
	if e != nil && p.Wants(event, ChannelInbox) {
		errs = append(errs, s.addInbox(ctx, *e))
	}
	if push && s.d.Jobs != nil {
		if chans := p.PushChannels(event); len(chans) > 0 {
			errs = append(errs, s.d.DB.Tx(ctx, func(tx *sql.Tx) error { return s.fanOutTx(ctx, tx, p.UserID, chans, m, limit) }))
		}
	}
	return errors.Join(errs...)
}

// ItemsOpened is the items counter-flush hook (items.Deps.OnOpened). A
// first open goes into the owner's inbox at once when the bell is on for
// first opens. When some channel wants this open (email or ntfy, or every
// open in the bell) it queues a notify.open job, a few seconds out so the
// stats writer has stored the event the message names.
func (s *Service) ItemsOpened(ctx context.Context, opened []items.Opened) {
	if len(opened) == 0 || s.d.Items == nil {
		return
	}
	prefs := map[int64]Prefs{}
	var entries []Entry
	var queue []openJob
	for _, o := range opened {
		it, err := s.d.Items.Get(ctx, o.ItemID)
		if err != nil {
			if !errors.Is(err, items.ErrNotFound) {
				s.log.Error("notify: open", "err", err)
			}
			continue
		}
		p, ok := prefs[it.OwnerID]
		if !ok {
			if p, err = s.Prefs(ctx, it.OwnerID); err != nil {
				s.log.Error("notify: open", "err", err)
				continue
			}
			prefs[it.OwnerID] = p
		}
		if o.First && p.Wants(EventItemFirstOpen, ChannelInbox) {
			// The visitor comes from the counter flush; the stored event is
			// the fallback.
			v := visitor{Client: o.Client, IP: o.IP, At: o.From}
			if v.Client == "" && v.IP == "" {
				if ev, ok, err := stats.FirstItemOpen(ctx, s.d.DB.R, it.ID); err == nil && ok {
					v = visitorOf(ev.Event)
				}
			}
			entries = append(entries, openEntry(it, v))
		}
		override, err := s.Override(ctx, it.OwnerID, it.ID)
		if err != nil {
			s.log.Error("notify: open", "err", err)
			continue
		}
		if first, every := itemOpenChannels(p, override, o.First); len(first)+len(every) > 0 {
			queue = append(queue, openJob{ItemID: o.ItemID, First: o.First, Count: o.Count, From: db.Millis(o.From), To: db.Millis(o.To)})
		}
	}
	if err := s.addInbox(ctx, entries...); err != nil {
		s.log.Error("notify: inbox opens", "err", err)
	}
	if s.d.Jobs == nil || len(queue) == 0 {
		return
	}
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		for _, j := range queue {
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

// handleOpen runs notify.open. First opens go out at once to the channels
// that want first opens. Every-open messages go out at most once per item
// per coalesceWindow; the opens in between are summed into one message
// when the window ends. The channels are worked out again here, so a
// setting changed since the open counts.
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
	p, err := s.Prefs(ctx, it.OwnerID)
	if err != nil {
		return err
	}
	override, err := s.Override(ctx, it.OwnerID, it.ID)
	if err != nil {
		return err
	}
	first, every := itemOpenChannels(p, override, j.First)
	now := s.now()
	link := s.d.Items.PublicURL(it.Slug)
	id := strconv.FormatInt(it.ID, 10)

	if j.Summary {
		last, ok, err := stats.LastItemOpen(ctx, s.d.DB.R, it.ID, now)
		if err != nil {
			return err
		}
		var entry *Entry
		err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			entry = nil
			n, since, err := takePending(ctx, tx, subjItem, id, now, coalesceWindow)
			if err != nil || n == 0 || len(every) == 0 {
				return err
			}
			var lv *visitor
			if ok {
				v := visitorOf(last.Event)
				lv = &v
			}
			m := summaryMessage(it.Name(), link, n, since, lv, now)
			if slices.Contains(every, ChannelInbox) {
				e := itemEntry(it, InboxItemOpened, "info", m.Title, m.Detail, now)
				entry = &e
			}
			return s.fanOutTx(ctx, tx, p.UserID, without(every, ChannelInbox), m, s.itemMsgs)
		})
		if err == nil && entry != nil {
			s.inboxOrLog(ctx, *entry)
		}
		return err
	}

	if len(first)+len(every) == 0 {
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
	var entry *Entry
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		entry = nil
		chans := first
		if len(every) > 0 {
			send, err := s.coalesce(ctx, tx, subjItem, id, j.Count, j.From, now, func(delay time.Duration) error {
				_, err := s.d.Jobs.EnqueueTx(ctx, tx, jobOpen, openJob{ItemID: j.ItemID, Summary: true}, jobs.Delay(delay))
				return err
			})
			if err != nil {
				return err
			}
			if send {
				e := every
				if j.First && p.Wants(EventItemFirstOpen, ChannelInbox) {
					e = without(e, ChannelInbox) // its row went in with the counter flush
				}
				chans = union(chans, e)
			} else {
				// Held: a channel that gets every open hears about this one
				// in the summary, not also now.
				chans = slices.DeleteFunc(slices.Clone(chans), func(ch string) bool { return slices.Contains(every, ch) })
			}
		}
		if slices.Contains(chans, ChannelInbox) {
			e := openEntry(it, v)
			e.Title = oneLine(msg.Title, maxTitleRunes)
			entry = &e
		}
		return s.fanOutTx(ctx, tx, p.UserID, without(chans, ChannelInbox), msg, s.itemMsgs)
	})
	if err == nil && entry != nil {
		s.inboxOrLog(ctx, *entry)
	}
	return err
}

func (s *Service) inboxOrLog(ctx context.Context, e Entry) {
	if err := s.addInbox(ctx, e); err != nil {
		s.log.Error("notify: inbox", "err", err)
	}
}

// coalesce reports whether a message about a subject may go out now. If
// not, the event is added to the subject's pending count, and the first
// one held back calls summary with the time left in the window.
func (s *Service) coalesce(ctx context.Context, tx *sql.Tx, typ, id string, count, from int64, now time.Time, summary func(time.Duration) error) (bool, error) {
	var until, pending int64
	err := tx.QueryRowContext(ctx, `SELECT until, pending FROM notify_windows WHERE subject_type = ? AND subject_id = ?`, typ, id).
		Scan(&until, &pending)
	switch {
	case errors.Is(err, sql.ErrNoRows) || (err == nil && until <= db.Millis(now)):
		_, err = tx.ExecContext(ctx, `INSERT INTO notify_windows (subject_type, subject_id, until, pending, pending_from) VALUES (?, ?, ?, 0, NULL)
ON CONFLICT (subject_type, subject_id) DO UPDATE SET until = excluded.until, pending = 0, pending_from = NULL`,
			typ, id, db.Millis(now.Add(coalesceWindow)))
		return err == nil, err
	case err != nil:
		return false, err
	}
	if pending == 0 {
		if err := summary(db.Time(until).Sub(now)); err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE notify_windows SET pending = pending + ?, pending_from = coalesce(pending_from, ?)
WHERE subject_type = ? AND subject_id = ?`, max(count, 1), from, typ, id)
	return false, err
}

// takePending ends a subject's window: it returns the events held back
// (and when the first was) and starts a new window, since the summary is
// a message too.
func takePending(ctx context.Context, tx *sql.Tx, typ, id string, now time.Time, window time.Duration) (int64, time.Time, error) {
	var pending int64
	var from sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT pending, pending_from FROM notify_windows WHERE subject_type = ? AND subject_id = ?`, typ, id).
		Scan(&pending, &from)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && pending == 0) {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notify_windows SET until = ?, pending = 0, pending_from = NULL
WHERE subject_type = ? AND subject_id = ?`, db.Millis(now.Add(window)), typ, id); err != nil {
		return 0, time.Time{}, err
	}
	var since time.Time
	if from.Valid {
		since = db.Time(from.Int64)
	}
	return pending, since, nil
}

// fanOutTx queues m for each of chans (email, ntfy). With a limit, the
// message counts against it (MessagesPerHour) once, whatever the number of
// channels; over it, it is dropped. Email is skipped, and its status set,
// when SMTP is not set up.
func (s *Service) fanOutTx(ctx context.Context, tx *sql.Tx, userID int64, chans []string, m Message, limit *ratelimit.Limiter) error {
	if len(chans) == 0 {
		return nil
	}
	if limit != nil && !allow(limit, userID) {
		s.log.Info("notify: message dropped by the hourly limit", "user", userID, "kind", m.Kind)
		return nil
	}
	for _, ch := range chans {
		if ch == ChannelEmail && !s.mailEnabled() {
			if err := s.recordTx(ctx, tx, userID, ch, errNoMail().Error()); err != nil {
				return err
			}
			continue
		}
		if _, err := s.d.Jobs.EnqueueTx(ctx, tx, jobSend, sendJob{UserID: userID, Channel: ch, Message: m},
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

// Notify implements secrets.Notifier. Each event goes to the channels the
// owner's grid picks. Email and ntfy report a reveal, or an expiry
// unopened, only for a secret with notify set (n.Requested); burns,
// wrong passphrases and an admin's removal whatever the flag. Burns are
// not capped. Wrong passphrases are coalesced per secret like every-open
// messages. Pushed messages name the owner's note and the secrets
// dashboard, never the secret's link, id or key.
func (s *Service) Notify(ctx context.Context, n secrets.Notification) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := s.secretEvent(ctx, n); err != nil {
			s.log.Warn("notify: secret notification not stored or queued", "err", err)
		}
	}()
}

// secretEventName maps a secrets notification to its event.
func secretEventName(k stats.Kind) string {
	switch k {
	case stats.KindRevealed:
		return EventSecretRevealed
	case stats.KindBurned:
		return EventSecretBurned
	case stats.KindFailed:
		return EventSecretWrongPassphrase
	case secrets.NotifyExpired:
		return EventSecretExpiredUnopened
	case secrets.NotifyRemoved:
		return EventSecretRevokedByAdmin
	}
	return ""
}

func (s *Service) secretEvent(ctx context.Context, n secrets.Notification) error {
	event := secretEventName(n.Kind)
	if n.OwnerID == 0 || event == "" {
		return nil
	}
	p, err := s.Prefs(ctx, n.OwnerID)
	if err != nil {
		return err
	}
	now := s.now()
	e, m := secretEntry(n), s.secretMessage(n, now)
	push, limit := true, s.secretMsgs
	switch event {
	case EventSecretRevealed, EventSecretExpiredUnopened:
		push = n.Requested
	case EventSecretBurned:
		limit = nil
		// The burn ends the wrong-passphrase window: no summary after it.
		if _, err := s.d.DB.W.ExecContext(ctx, `DELETE FROM notify_windows WHERE subject_type = ? AND subject_id = ?`,
			subjSecret, n.SecretID); err != nil {
			return err
		}
	case EventSecretWrongPassphrase:
		if s.d.Jobs != nil && n.SecretID != "" {
			var send bool
			err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
				var err error
				send, err = s.coalesce(ctx, tx, subjSecret, n.SecretID, 1, db.Millis(n.At), now, func(delay time.Duration) error {
					_, err := s.d.Jobs.EnqueueTx(ctx, tx, jobSecret, secretJob{OwnerID: n.OwnerID, SecretID: n.SecretID,
						SecretKind: n.SecretKind, Note: n.Note}, jobs.Delay(delay))
					return err
				})
				return err
			})
			if err != nil || !send {
				return err
			}
		}
	}
	return s.emit(ctx, p, event, &e, m, push, limit)
}

// handleSecret runs notify.secret: the wrong passphrases held back since
// the last message about the secret, as one message.
func (s *Service) handleSecret(ctx context.Context, payload json.RawMessage) error {
	var j secretJob
	if err := json.Unmarshal(payload, &j); err != nil {
		return jobs.Permanent(err)
	}
	now := s.now()
	var n int64
	var since time.Time
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		n, since, err = takePending(ctx, tx, subjSecret, j.SecretID, now, coalesceWindow)
		return err
	})
	if err != nil || n == 0 {
		return err
	}
	p, err := s.Prefs(ctx, j.OwnerID)
	if err != nil {
		return err
	}
	m := wrongPassSummary(j.Note, n, since, s.secretsURL(), now)
	e := Entry{UserID: j.OwnerID, Kind: InboxSecretWrongPass, Status: "warn", Link: secretLink(j.SecretID),
		SubjectType: subjSecret, SubjectID: j.SecretID, At: now,
		Title: inboxSecretName(j.Note, j.SecretKind) + ": " + moreWrong(n), Body: sinceText(since, now)}
	return s.emit(ctx, p, EventSecretWrongPassphrase, &e, m, true, s.secretMsgs)
}

// secretMessage renders n for email and ntfy.
func (s *Service) secretMessage(n secrets.Notification, now time.Time) Message {
	v := visitor{Client: n.UASummary, IP: n.IP, At: n.At}
	dash := s.secretsURL()
	switch n.Kind {
	case stats.KindBurned:
		return burnMessage(n.Note, v, dash, now)
	case stats.KindFailed:
		return wrongPassMessage(n.Note, n.MaxFailures-n.Failures, v, dash, now)
	case secrets.NotifyExpired:
		return Message{Kind: KindSecretExpired, Title: secretName(n.Note) + " expired unopened", Detail: clock(n.At, now),
			Click: dash, Tags: []string{"snap", "expired"}}
	case secrets.NotifyRemoved:
		return Message{Kind: KindSecretRemoved, Title: secretName(n.Note) + " removed by an admin", Detail: clock(n.At, now),
			Click: dash, Tags: []string{"snap", "removed"}}
	}
	return revealMessage(n.Note, n.ViewsLeft, n.MaxViews, v, dash, now)
}

// ---- item expiry and storage ----

// expiringWithin is how far ahead item.expiring looks.
const expiringWithin = 24 * time.Hour

// ItemExpired is items.Deps.OnExpired: the expiry sweep deleted the item
// or made it private. The item's own setting "off" keeps it off email and
// ntfy. A deleted item's inbox row is about the account, since the item
// (and its stats page) is gone.
func (s *Service) ItemExpired(ctx context.Context, it *items.Item, deleted bool) {
	if it == nil || it.OwnerID == 0 {
		return
	}
	if err := s.itemExpired(ctx, it, deleted); err != nil {
		s.log.Error("notify: item expired", "err", err)
	}
}

func (s *Service) itemExpired(ctx context.Context, it *items.Item, deleted bool) error {
	p, override, err := s.itemPrefs(ctx, it)
	if err != nil {
		return err
	}
	now := s.now()
	name := oneLine(it.Name(), maxFieldRunes)
	e := itemEntry(it, InboxItemExpired, "info", name+" expired", "now private", now)
	m := Message{Kind: KindItemExpired, Title: name + " expired", Detail: "now private", Click: s.d.Items.PublicURL(it.Slug),
		Tags: []string{"snap", "expired"}}
	if deleted {
		e.Body, e.Link, e.SubjectType, e.SubjectID = "deleted", "/dashboard", "account", strconv.FormatInt(it.OwnerID, 10)
		m.Detail, m.Click = "deleted", s.origin()+"/dashboard"
	}
	return s.emit(ctx, p, EventItemExpired, &e, m, override != ModeNone, s.itemMsgs)
}

// SweepExpiring reports items that expire within a day, once per item and
// expiry (notify_marks): a new expiry date reports again.
func (s *Service) SweepExpiring(ctx context.Context) error {
	if s.d.Items == nil {
		return nil
	}
	list, err := s.d.Items.ExpiringWithin(ctx, expiringWithin)
	if err != nil {
		return err
	}
	now := s.now()
	var errs []error
	for _, it := range list {
		if it.OwnerID == 0 || it.ExpiresAt == nil {
			continue
		}
		id, at := strconv.FormatInt(it.ID, 10), db.Millis(*it.ExpiresAt)
		old, ok, err := s.mark(ctx, it.OwnerID, EventItemExpiring, id)
		if err != nil || (ok && old == at) {
			errs = append(errs, err)
			continue
		}
		if err := s.setMark(ctx, it.OwnerID, EventItemExpiring, id, at); err != nil {
			errs = append(errs, err)
			continue
		}
		p, override, err := s.itemPrefs(ctx, it)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		name := oneLine(it.Name(), maxFieldRunes)
		when := "at " + clock(*it.ExpiresAt, now)
		e := itemEntry(it, InboxItemExpiring, "warn", name+" expires within a day", when, now)
		m := Message{Kind: KindItemExpiring, Title: name + " expires within a day", Detail: when, Click: s.d.Items.PublicURL(it.Slug),
			Tags: []string{"snap", "expiring"}}
		errs = append(errs, s.emit(ctx, p, EventItemExpiring, &e, m, override != ModeNone, s.itemMsgs))
	}
	return errors.Join(errs...)
}

func (s *Service) itemPrefs(ctx context.Context, it *items.Item) (Prefs, Mode, error) {
	p, err := s.Prefs(ctx, it.OwnerID)
	if err != nil {
		return p, "", err
	}
	override, err := s.Override(ctx, it.OwnerID, it.ID)
	return p, override, err
}

// Storage levels.
const (
	markQuota = "quota"
	level80   = 80
	level95   = 95
)

// QuotaChanged checks the user's storage after an upload. Passing 80% or
// 95% fires once; the level is remembered (notify_marks) until usage is
// seen below it again. A jump past both fires only quota.95. Users without
// a quota get nothing.
func (s *Service) QuotaChanged(ctx context.Context, userID int64) {
	if s.d.Items == nil || userID == 0 {
		return
	}
	if err := s.quotaChanged(ctx, userID); err != nil {
		s.log.Error("notify: quota", "err", err)
	}
}

func (s *Service) quotaChanged(ctx context.Context, userID int64) error {
	q, ok := s.d.Items.Quota(ctx, userID)
	level := int64(0)
	var pct float64
	if ok && q.Total > 0 {
		pct = float64(q.Used) / float64(q.Total) * 100
		switch {
		case pct >= level95:
			level = level95
		case pct >= level80:
			level = level80
		}
	}
	var fire string
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		fire = ""
		var old int64
		err := tx.QueryRowContext(ctx, `SELECT value FROM notify_marks WHERE user_id = ? AND mark = ? AND subject_id = ''`, userID, markQuota).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old == level {
			return nil
		}
		if level > old {
			fire = EventQuota80
			if level == level95 {
				fire = EventQuota95
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO notify_marks (user_id, mark, subject_id, value) VALUES (?, ?, '', ?)
ON CONFLICT (user_id, mark, subject_id) DO UPDATE SET value = excluded.value`, userID, markQuota, level)
		return err
	})
	if err != nil || fire == "" {
		return err
	}
	p, err := s.Prefs(ctx, userID)
	if err != nil {
		return err
	}
	now := s.now()
	title := fmt.Sprintf("Storage %d%% used", int(pct))
	detail := web.HumanBytes(q.Used) + " of " + web.HumanBytes(q.Total)
	status := "warn"
	if fire == EventQuota95 {
		status = "down"
	}
	e := Entry{UserID: userID, Kind: InboxQuota, Status: status, Title: title, Body: detail, Link: "/dashboard",
		SubjectType: "account", SubjectID: strconv.FormatInt(userID, 10), At: now}
	m := Message{Kind: KindQuota, Title: title, Detail: detail, Click: s.origin() + "/dashboard", Tags: []string{"snap", "quota"}}
	return s.emit(ctx, p, fire, &e, m, true, nil)
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
