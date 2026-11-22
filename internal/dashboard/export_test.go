package dashboard

import (
	"time"

	"github.com/Battleroid/snap-share/internal/mail"
)

// SetMail replaces the mail sender (tests).
func (s *Service) SetMail(m mail.Sender) { s.d.Mail = m }

// ExpireZipTickets makes every issued zip URL expire (tests).
func (s *Service) ExpireZipTickets() {
	s.zipMu.Lock()
	defer s.zipMu.Unlock()
	for k, t := range s.tickets {
		t.exp = time.Unix(0, 0)
		s.tickets[k] = t
	}
}
