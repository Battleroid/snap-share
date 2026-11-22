// Package notify tells owners when their items and secrets are opened, by
// email and ntfy. Everything is off by default. See docs/notifications.md.
//
// Item opens arrive from the items counter flush (OnOpened); secret reveals
// and burns through the secrets.Notifier interface. Delivery runs as
// durable jobs: notify.open decides and renders, notify.send delivers one
// message to one channel with retries.
package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/jobs"
	"github.com/Battleroid/snap-share/internal/mail"
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
	// MessagesPerHour caps open and reveal messages per user. Burns after
	// failed passphrase attempts are not capped.
	MessagesPerHour = 30
	testsPerWindow  = 5
	testWindow      = 10 * time.Minute
	sendAttempts    = 5
	// openDelay lets the stats writer (5 s) store the event a message names.
	openDelay = 6 * time.Second
)

// Items is what notify needs from the items service.
type Items interface {
	Get(ctx context.Context, id int64) (*items.Item, error)
	PublicURL(slug string) string
}

// Deps are the service's dependencies.
type Deps struct {
	Cfg      *config.Config // SecretKey (+ previous) seal ntfy tokens; PublicURL for links
	Log      *slog.Logger
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

	perUser *ratelimit.Limiter // messages per user per hour
	tests   *ratelimit.Limiter // "Send test" per user

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
		perUser: ratelimit.New(MessagesPerHour, time.Hour, MessagesPerHour),
		tests:   ratelimit.New(testsPerWindow, testWindow, testsPerWindow),
	}
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
		d.Jobs.Every("notify.sweep", 24*time.Hour, s.Sweep)
	}
	return s, nil
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

// Sweep deletes coalescing windows long past and overrides whose item is
// gone or has another owner.
func (s *Service) Sweep(ctx context.Context) error {
	cutoff := db.Millis(s.now().Add(-24 * time.Hour))
	if _, err := s.d.DB.W.ExecContext(ctx, `DELETE FROM notify_windows WHERE until < ? AND pending = 0`, cutoff); err != nil {
		return err
	}
	_, err := s.d.DB.W.ExecContext(ctx, `DELETE FROM notify_overrides WHERE subject_type = 'item' AND NOT EXISTS
  (SELECT 1 FROM items WHERE items.id = CAST(notify_overrides.subject_id AS INTEGER) AND items.owner_id = notify_overrides.user_id)`)
	return err
}
