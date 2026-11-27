package accounts

// When roles come from the SSO mapping, a session keeps admin rights for
// SSOAdminMaxAge after an SSO sign-in. Past that it acts as a user, and the
// principal says so (AdminLapsed), so the layout and the admin pages can say
// why admin features are gone and offer the SSO sign-in that restores them.
// The first lapsed request of each SSO sign-in also tells the user once
// through the AdminLapsed hook (notify's inbox, email and ntfy).

import (
	"context"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
)

// AdminEndsSoon is how long before the lapse the heads-up shows.
const AdminEndsSoon = time.Hour

// AdminLapsedFunc is told that an SSO admin's session lapsed: userID, and
// the SSO sign-in it is older than. It must not fire twice for one ssoAt
// (notify keeps a mark); the service calls it once per user and sign-in
// per process.
type AdminLapsedFunc func(ctx context.Context, userID int64, ssoAt time.Time)

// lapseNotices is the in-memory guard in front of the hook: the newest
// SSO sign-in each user was told about, so later requests cost a map read.
type lapseNotices struct {
	mu   sync.Mutex
	fn   AdminLapsedFunc
	seen map[int64]time.Time
}

// SetAdminLapsed installs the hook. Call before serving.
func (s *Service) SetAdminLapsed(fn AdminLapsedFunc) {
	s.lapse.mu.Lock()
	defer s.lapse.mu.Unlock()
	s.lapse.fn = fn
}

// ssoAdminAge applies SSOAdminMaxAge to p, an admin with an SSO identity
// whose role comes from the mapping: past it p acts as a user and is
// marked lapsed; in its last AdminEndsSoon p carries the time left.
func (s *Service) ssoAdminAge(ctx context.Context, p *auth.Principal, ssoAt time.Time) {
	left := SSOAdminMaxAge - s.sessions.now().Sub(ssoAt)
	if !ssoAt.IsZero() && left >= 0 {
		if left <= AdminEndsSoon {
			p.AdminEndsIn = left
		}
		return
	}
	p.Role = auth.RoleUser
	p.AdminLapsed, p.AdminSSOAt = true, ssoAt
	if !ssoAt.IsZero() { // a password sign-in never had SSO admin rights to lose
		s.tellLapsed(ctx, p.UserID, ssoAt)
	}
}

// tellLapsed calls the hook the first time this process sees the lapse of
// the user's SSO sign-in at ssoAt; older sign-ins (another session) are
// not news.
func (s *Service) tellLapsed(ctx context.Context, userID int64, ssoAt time.Time) {
	s.lapse.mu.Lock()
	fn := s.lapse.fn
	last, ok := s.lapse.seen[userID]
	if fn == nil || (ok && !ssoAt.After(last)) {
		s.lapse.mu.Unlock()
		return
	}
	if s.lapse.seen == nil {
		s.lapse.seen = map[int64]time.Time{}
	}
	s.lapse.seen[userID] = ssoAt
	s.lapse.mu.Unlock()
	fn(context.WithoutCancel(ctx), userID, ssoAt)
}
