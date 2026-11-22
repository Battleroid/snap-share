package dashboard

import (
	"errors"
	"testing"
	"time"
)

// One user's zip tickets never push out another user's: a user keeps at
// most maxZipTicketsUser (new ones replace their own oldest), and past the
// site cap new tickets are refused rather than evicting anyone.
func TestZipTicketCaps(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := &Service{now: func() time.Time { return now }, tickets: map[string]zipTicket{}}
	issue := func(owner int64) (string, error) {
		now = now.Add(time.Millisecond) // distinct expiries, oldest first
		tok, _, err := s.issueZip(owner, []int64{1})
		return tok, err
	}
	other, err := issue(2)
	if err != nil {
		t.Fatal(err)
	}
	var mine []string
	for range 3 * maxZipTicketsUser {
		tok, err := issue(1)
		if err != nil {
			t.Fatal(err)
		}
		mine = append(mine, tok)
	}
	if _, ok := s.ticket(other, 2); !ok {
		t.Fatal("user 1's tickets evicted user 2's")
	}
	for i, tok := range mine {
		_, ok := s.ticket(tok, 1)
		if want := i >= len(mine)-maxZipTicketsUser; ok != want {
			t.Errorf("ticket %d live %v, want %v", i, ok, want)
		}
	}
	// Fill the site with other users' tickets.
	for owner := int64(3); len(s.tickets) < maxZipTickets; owner++ {
		for range maxZipTicketsUser {
			if len(s.tickets) == maxZipTickets {
				break
			}
			if _, err := issue(owner); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := issue(1000); !errors.Is(err, errZipBusy) {
		t.Fatalf("site full: %v, want errZipBusy", err)
	}
	// A user at their own cap still gets one, replacing their oldest.
	if _, err := issue(1); err != nil {
		t.Fatalf("user at cap on a full site: %v", err)
	}
	if _, ok := s.ticket(other, 2); !ok {
		t.Fatal("a full site evicted user 2's ticket")
	}
}
