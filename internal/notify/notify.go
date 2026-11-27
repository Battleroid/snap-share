// Package notify tells owners about their items, secrets and storage: in
// the app (the header bell; inbox.go) and by email and ntfy (off by
// default). Which events go to which channel is the user's events grid
// (events.go). See docs/notifications.md.
//
// Item opens arrive from the items counter flush (OnOpened), expiries from
// the items sweep (ItemExpired, SweepExpiring) and storage from uploads
// (QuotaChanged); secret events through the secrets.Notifier interface.
// Inbox rows are written at once and streamed to open pages (stream.go).
// Email and ntfy delivery runs as durable jobs: notify.open decides and
// renders, notify.secret sums up wrong passphrases, notify.send delivers
// one message to one channel with retries.
package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/metrics"
	"github.com/Battleroid/snap-share/internal/ratelimit"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// Admin settings.
var (
	// AllowHTTP lets users point ntfy at plain http servers.
	AllowHTTP = settings.Define("notify.allow_http", false, nil)
	// OutboundAllow lists CIDRs, IP addresses and exact host names that
	// outbound requests may reach although they are loopback, private or
	// link-local (e.g. a self-hosted ntfy container: "ntfy").
	OutboundAllow = settings.Define("notify.outbound_allow", []string{}, validAllowlist)
)

// Limits.
const (
	// MessagesPerHour caps email and ntfy messages per user, separately for
	// items and for secrets, so a busy item cannot crowd out secrets. Burns
	// after failed passphrase attempts and storage messages are not capped.
	MessagesPerHour = 30
	// jobsPurgeEvery is how often finished delivery jobs are deleted: their
	// payloads hold the visitor's address, client and the secret's note,
	// which must not outlive the stats retention in the jobs table.
	jobsPurgeEvery = 10 * time.Minute
	testsPerWindow = 5
	testWindow     = 10 * time.Minute
	sendAttempts   = 5
	// openDelay lets the stats writer (5 s) store the event a message names.
	openDelay = 6 * time.Second
	// expiringEvery is how often items expiring within a day are looked for.
	expiringEvery = time.Hour
)

// Items is what notify needs from the items service.
type Items interface {
	Get(ctx context.Context, id int64) (*items.Item, error)
	PublicURL(slug string) string
	// Quota is the user's storage use; ok is false without a quota.
	Quota(ctx context.Context, userID int64) (q web.Quota, ok bool)
	// ExpiringWithin lists owned items whose expiry falls in the next d.
	ExpiringWithin(ctx context.Context, d time.Duration) ([]*items.Item, error)
}

// Deps are the service's dependencies.
type Deps struct {
	Cfg      *config.Config // SecretKey (+ previous) seal ntfy tokens; PublicURL for links
	Log      *slog.Logger
	Metrics  *metrics.Metrics // nil: nothing is counted
	DB       *db.DB
	Settings *settings.Store
	Jobs     *jobs.Queue // nil: no jobs are registered (tests that only use the store)
	Mail     mail.Sender
	Items    Items
	Render   *web.Renderer
	Errors   *web.ErrorPages
	SiteName func() string
	// MailEnabled reports whether SMTP is configured (nil: Cfg.SMTP).
	MailEnabled func() bool
	// RetentionDays is how long inbox rows are kept: the stats retention
	// setting (nil: DefaultRetentionDays).
	RetentionDays func() int
	// ReadRetentionHours is how long a read inbox row is kept, 0 for as
	// long as RetentionDays (nil: DefaultReadRetentionHours).
	ReadRetentionHours func() int
	// Streams bounds the live inbox streams; zero fields take
	// DefaultStreamLimits.
	Streams StreamLimits

	// Lookup and TLS override the outbound client's resolver and TLS
	// config (tests).
	Lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	TLS    *tls.Config
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

// Service implements notification settings, the open hooks and delivery.
type Service struct {
	d    Deps
	log  *slog.Logger
	now  func() time.Time
	seal *sealer
	out  *Outbound

	itemMsgs   *ratelimit.Limiter // item messages per user per hour
	secretMsgs *ratelimit.Limiter // secret messages per user per hour
	tests      *ratelimit.Limiter // "Send test" per user

	streams *broker                      // live inbox streams
	limits  atomic.Pointer[StreamLimits] // their bounds

	wg sync.WaitGroup // secret notifications being queued
}

// New builds the service and registers its jobs on d.Jobs.
func New(d Deps) (*Service, error) {
	if d.Cfg == nil || d.DB == nil || d.Settings == nil || d.Mail == nil {
		return nil, errors.New("notify: missing dependency")
	}
	seal, err := newSealer(d.Cfg.SecretKey, d.Cfg.SecretKeyPrevious)
	if err != nil {
		return nil, err
	}
	s := &Service{
		d: d, log: d.Log, now: d.Now, seal: seal,
		itemMsgs:   ratelimit.New(MessagesPerHour, time.Hour, MessagesPerHour).Named("notify_item", d.Metrics),
		secretMsgs: ratelimit.New(MessagesPerHour, time.Hour, MessagesPerHour).Named("notify_secret", d.Metrics),
		tests:      ratelimit.New(testsPerWindow, testWindow, testsPerWindow).Named("notify_test", d.Metrics),
		streams:    newBroker(),
	}
	lim := d.Streams.withDefaults()
	s.limits.Store(&lim)
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.d.SiteName == nil {
		s.d.SiteName = func() string { return "snap" }
	}
	s.out = NewOutbound(OutboundOptions{
		AllowHTTP: func() bool { return AllowHTTP.Get(d.Settings) },
		Allow:     func() []string { return OutboundAllow.Get(d.Settings) },
		Lookup:    d.Lookup,
		TLS:       d.TLS,
	})
	if d.Jobs != nil {
		d.Jobs.Handle(jobOpen, 2, time.Minute, s.handleOpen)
		d.Jobs.Handle(jobSend, 2, time.Minute, s.handleSend)
		d.Jobs.Handle(jobSecret, 2, time.Minute, s.handleSecret)
		d.Jobs.Every("notify.sweep", 24*time.Hour, s.Sweep)
		d.Jobs.Every("notify.expiring", expiringEvery, s.SweepExpiring)
		d.Jobs.Every("notify.jobs.purge", jobsPurgeEvery, s.PurgeJobs)
		d.Jobs.Every("notify.inbox.sweep", inboxSweepEvery, s.SweepInbox)
	}
	return s, nil
}

// PurgeJobs deletes finished notify jobs. The queue keeps finished jobs for
// a week, but these carry visitor details (address, client, a secret's
// note); the delivery result is kept in notify_status instead.
func (s *Service) PurgeJobs(ctx context.Context) error {
	_, err := s.d.DB.W.ExecContext(ctx, `DELETE FROM jobs WHERE kind IN (?, ?, ?) AND state IN ('done', 'failed')`, jobOpen, jobSend, jobSecret)
	return err
}

// Outbound returns the service's outbound HTTP client.
func (s *Service) Outbound() *Outbound { return s.out }

// Close waits for secret notifications still being queued.
func (s *Service) Close() { s.wg.Wait() }

func (s *Service) mailEnabled() bool {
	if s.d.MailEnabled != nil {
		return s.d.MailEnabled()
	}
	return s.d.Cfg.SMTP.Enabled()
}

func (s *Service) siteName() string { return s.d.SiteName() }

func (s *Service) origin() string { return strings.TrimRight(s.d.Cfg.Origin(), "/") }

func (s *Service) settingsURL() string { return s.origin() + "/settings/notifications" }

func (s *Service) secretsURL() string { return s.origin() + "/dashboard/secrets" }

// Sweep deletes coalescing windows long past, expiring-item marks whose
// expiry has passed, and overrides whose item is gone or has another owner.
func (s *Service) Sweep(ctx context.Context) error {
	cutoff := db.Millis(s.now().Add(-24 * time.Hour))
	if _, err := s.d.DB.W.ExecContext(ctx, `DELETE FROM notify_windows WHERE until < ? AND pending = 0`, cutoff); err != nil {
		return err
	}
	if _, err := s.d.DB.W.ExecContext(ctx, `DELETE FROM notify_marks WHERE mark = ? AND value < ?`, EventItemExpiring, db.Millis(s.now())); err != nil {
		return err
	}
	_, err := s.d.DB.W.ExecContext(ctx, `DELETE FROM notify_overrides WHERE subject_type = 'item' AND NOT EXISTS
  (SELECT 1 FROM items WHERE items.id = CAST(notify_overrides.subject_id AS INTEGER) AND items.owner_id = notify_overrides.user_id)`)
	return err
}
