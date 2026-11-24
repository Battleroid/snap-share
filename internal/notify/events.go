package notify

// The events × channels grid: which events go to the bell, email and ntfy.
// A user's saved choices are rows in notify_event_prefs; an event and
// channel without a row take the default below. Per-item overrides
// (notify_overrides) and the per-secret notify flag still decide whether a
// subject is wanted on email and ntfy at all; the grid picks the channels.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Battleroid/snap-share/internal/db"
)

// Event names.
const (
	EventSecretBurned          = "secret.burned_failures"  // destroyed after too many wrong passphrases
	EventSecretWrongPassphrase = "secret.wrong_passphrase" // one wrong passphrase, coalesced
	EventSecretRevokedByAdmin  = "secret.revoked_by_admin"
	EventSecretRevealed        = "secret.revealed"
	EventSecretExpiredUnopened = "secret.expired_unopened"
	EventItemFirstOpen         = "item.first_open" // the first open by a person
	EventItemOpen              = "item.open"       // every open, coalesced
	EventItemExpiring          = "item.expiring"   // a day before the expiry
	EventItemExpired           = "item.expired"
	EventQuota95               = "quota.95"
	EventQuota80               = "quota.80"
)

// ChannelInbox is the in-app inbox (the bell).
const ChannelInbox = "inbox"

// AllChannels are the grid's columns, in order.
var AllChannels = []string{ChannelInbox, ChannelEmail, ChannelNtfy}

// EventInfo describes one row of the grid.
type EventInfo struct {
	Name  string
	Label string // "A secret is opened"
	Hint  string // how often it can fire
	// Defaults for a user who has not saved the grid.
	Inbox, Push bool
}

// Events are the grid's rows, loudest first.
var Events = []EventInfo{
	{EventSecretBurned, "A secret is destroyed by wrong passphrases", "Right away, every time.", true, true},
	{EventSecretWrongPassphrase, "A wrong passphrase is tried", "At most once per secret every 15 minutes.", true, false},
	{EventSecretRevokedByAdmin, "An admin removes a secret", "Right away.", true, false},
	{EventSecretRevealed, "A secret is opened", "Every reveal. By email and ntfy only for secrets with notify me.", true, true},
	{EventSecretExpiredUnopened, "A secret expires unopened", "Once per secret. By email and ntfy only with notify me.", true, false},
	{EventItemFirstOpen, "An item is opened by someone new", "The first open by each person.", true, false},
	{EventItemOpen, "An item is opened", "Every open, at most once per item every 15 minutes.", false, false},
	{EventItemExpiring, "An item expires within a day", "Once per item.", true, false},
	{EventItemExpired, "An item expires", "Once per item.", true, false},
	{EventQuota95, "Storage passes 95%", "Once each time, on upload.", true, false},
	{EventQuota80, "Storage passes 80%", "Once each time, on upload.", true, false},
}

func eventInfo(name string) (EventInfo, bool) {
	for _, e := range Events {
		if e.Name == name {
			return e, true
		}
	}
	return EventInfo{}, false
}

func validChannel(ch string) bool { return slices.Contains(AllChannels, ch) }

// Grid is a user's choices: event → channel → on.
type Grid map[string]map[string]bool

// On reports whether event goes to channel.
func (g Grid) On(event, channel string) bool { return g[event][channel] }

func (g Grid) set(event, channel string, on bool) {
	if g[event] == nil {
		g[event] = map[string]bool{}
	}
	g[event][channel] = on
}

// defaultGrid is the grid of a user who saved nothing. legacy is their old
// item-open setting (notify_prefs.default_event), which still gives the
// item-open rows for email and ntfy until they save the grid.
func defaultGrid(legacy Mode) Grid {
	g := Grid{}
	for _, e := range Events {
		g.set(e.Name, ChannelInbox, e.Inbox)
		push := e.Push
		switch e.Name {
		case EventItemFirstOpen:
			push = legacy == ModeFirst || legacy == ModeEvery
		case EventItemOpen:
			push = legacy == ModeEvery
		}
		g.set(e.Name, ChannelEmail, push)
		g.set(e.Name, ChannelNtfy, push)
	}
	return g
}

// loadGrid reads the user's saved rows over the defaults. Rows for events
// that no longer exist are ignored.
func (s *Service) loadGrid(ctx context.Context, userID int64, legacy Mode) (Grid, error) {
	g := defaultGrid(legacy)
	rows, err := s.d.DB.R.QueryContext(ctx, `SELECT event, channel, enabled FROM notify_event_prefs WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("notify: events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ev, ch string
		var on int64
		if err := rows.Scan(&ev, &ch, &on); err != nil {
			return nil, fmt.Errorf("notify: events: %w", err)
		}
		if _, ok := eventInfo(ev); ok && validChannel(ch) {
			g.set(ev, ch, on != 0)
		}
	}
	return g, rows.Err()
}

// ErrUnknownEvent is returned by SetEvents for an event or channel it does
// not know.
var ErrUnknownEvent = errors.New("notify: unknown event or channel")

// SetEvents stores the user's choices for the given channels: every event
// in on (as "event:channel") is turned on, every other event off. Other
// channels keep their rows, so a column that was disabled on the page
// (its channel off) is not wiped. Unknown events or channels are an error.
func (s *Service) SetEvents(ctx context.Context, userID int64, channels []string, on []string) error {
	want := map[string]bool{}
	for _, v := range on {
		ev, ch, ok := strings.Cut(v, ":")
		if _, known := eventInfo(ev); !ok || !known || !validChannel(ch) {
			return fmt.Errorf("%w: %q", ErrUnknownEvent, v)
		}
		want[v] = true
	}
	for _, ch := range channels {
		if !validChannel(ch) {
			return fmt.Errorf("%w: channel %q", ErrUnknownEvent, ch)
		}
	}
	now := db.Millis(s.now())
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notify_prefs (user_id, updated_at) VALUES (?, ?) ON CONFLICT (user_id) DO UPDATE SET updated_at = excluded.updated_at`,
			userID, now); err != nil {
			return fmt.Errorf("notify: events: %w", err)
		}
		for _, ch := range channels {
			for _, e := range Events {
				if _, err := tx.ExecContext(ctx, `INSERT INTO notify_event_prefs (user_id, event, channel, enabled) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, event, channel) DO UPDATE SET enabled = excluded.enabled`, userID, e.Name, ch, boolInt(want[e.Name+":"+ch])); err != nil {
					return fmt.Errorf("notify: events: %w", err)
				}
			}
		}
		return nil
	})
}

// Wants reports whether event goes to channel: the grid, and for email
// and ntfy the channel's switch.
func (p Prefs) Wants(event, channel string) bool {
	switch channel {
	case ChannelEmail:
		return p.EmailEnabled && p.Events.On(event, channel)
	case ChannelNtfy:
		return p.NtfyEnabled && p.Events.On(event, channel)
	}
	return p.Events.On(event, channel)
}

// PushChannels are the email and ntfy channels that are on for event.
func (p Prefs) PushChannels(event string) []string {
	var out []string
	for _, ch := range []string{ChannelEmail, ChannelNtfy} {
		if p.Wants(event, ch) {
			out = append(out, ch)
		}
	}
	return out
}

// itemOverrideChannels are where an item's own setting (first or every
// open) sends its opens: the channels ticked for either item-open event,
// or, when none is, every channel that is on. A user who never ticked
// item opens but set an item to "every open" keeps getting its messages.
func (p Prefs) itemOverrideChannels() []string {
	out := union(p.PushChannels(EventItemFirstOpen), p.PushChannels(EventItemOpen))
	if len(out) == 0 {
		return p.Channels()
	}
	return out
}

// itemOpenChannels splits the channels for one open of an item: first are
// those that get a first open at once, every those that get every open
// (coalesced). override is the item's own setting ("" for none). The
// inbox appears in every only; the inbox's first opens are written when
// the counters are flushed.
func itemOpenChannels(p Prefs, override Mode, isFirst bool) (first, every []string) {
	switch override {
	case "":
		if isFirst {
			first = p.PushChannels(EventItemFirstOpen)
		}
		every = p.PushChannels(EventItemOpen)
	case ModeFirst:
		if isFirst {
			first = p.itemOverrideChannels()
		}
	case ModeEvery:
		every = p.itemOverrideChannels()
	}
	if p.Wants(EventItemOpen, ChannelInbox) {
		every = append(every, ChannelInbox)
	}
	return first, every
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func without(l []string, x string) []string {
	return slices.DeleteFunc(slices.Clone(l), func(s string) bool { return s == x })
}

// ---- once-per-subject marks ----

func (s *Service) mark(ctx context.Context, userID int64, mark, subject string) (int64, bool, error) {
	var v int64
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT value FROM notify_marks WHERE user_id = ? AND mark = ? AND subject_id = ?`,
		userID, mark, subject).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("notify: mark: %w", err)
	}
	return v, true, nil
}

func (s *Service) setMark(ctx context.Context, userID int64, mark, subject string, value int64) error {
	_, err := s.d.DB.W.ExecContext(ctx, `INSERT INTO notify_marks (user_id, mark, subject_id, value) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, mark, subject_id) DO UPDATE SET value = excluded.value`, userID, mark, subject, value)
	if err != nil {
		return fmt.Errorf("notify: mark: %w", err)
	}
	return nil
}
