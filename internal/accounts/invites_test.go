package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
)

func sealingStore(t *testing.T, keys ...string) (*Store, *clock) {
	t.Helper()
	st, _, c := setup(t)
	var ks [][]byte
	for _, k := range keys {
		ks = append(ks, []byte(strings.Repeat(k, 32)))
	}
	if len(ks) == 0 {
		ks = [][]byte{[]byte(strings.Repeat("k", 32))}
	}
	var err error
	if st.inviteSeal, err = auth.NewSealer(inviteSealLabel, ks...); err != nil {
		t.Fatal(err)
	}
	return st, c
}

func sealed(t *testing.T, st *Store, id int64) []byte {
	t.Helper()
	var ct []byte
	if err := st.db.R.QueryRow(`SELECT token_ct FROM invites WHERE id = ?`, id).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	return ct
}

func auditCount(t *testing.T, st *Store, action string) int {
	t.Helper()
	var n int
	if err := st.db.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInviteLabel(t *testing.T) {
	ctx := context.Background()
	st, _ := sealingStore(t)
	_, inv, err := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour, Label: "  for Sam  "}, CLIActor())
	if err != nil || inv.Label != "for Sam" {
		t.Fatalf("label %q %v", inv.Label, err)
	}
	for _, bad := range []string{strings.Repeat("é", 81), "two\nlines", "tab\there"} {
		if _, _, err := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour, Label: bad}, CLIActor()); !errors.Is(err, ErrBadInviteLabel) {
			t.Errorf("label %q: %v", bad, err)
		}
	}
	if _, _, err := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour, Label: strings.Repeat("é", 80)}, CLIActor()); err != nil {
		t.Errorf("80 characters: %v", err)
	}
}

func TestInviteStatus(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	sent, revoked := now.Add(-time.Minute), now.Add(-time.Minute)
	base := Invite{MaxUses: 3, ExpiresAt: now.Add(time.Hour), CreatorAdmin: true}
	with := func(f func(*Invite)) Invite { i := base; f(&i); return i }
	cases := []struct {
		inv  Invite
		want string
	}{
		{base, StatusLink},
		{with(func(i *Invite) { i.Email, i.MailSentAt = "a@example.com", &sent }), StatusSent},
		{with(func(i *Invite) { i.Email, i.MailError = "a@example.com", "dial tcp: refused" }), StatusSendFailed},
		{with(func(i *Invite) { i.Email, i.MailSentAt, i.Uses = "a@example.com", &sent, 1 }), StatusPartlyUsed},
		{with(func(i *Invite) { i.Uses = 3 }), InviteUsed},
		{with(func(i *Invite) { i.ExpiresAt = now }), InviteExpired},
		{with(func(i *Invite) { i.RevokedAt = &revoked }), InviteRevoked},
		{with(func(i *Invite) { i.CreatorAdmin = false }), StatusUnusable},
		{with(func(i *Invite) { i.CreatorAdmin, i.Uses = false, 3 }), InviteUsed},
	}
	for _, c := range cases {
		if got := c.inv.Status(now); got != c.want {
			t.Errorf("%+v: %q, want %q", c.inv, got, c.want)
		}
	}
}

// The token is kept sealed while the invite works, and dropped once it is
// used up, revoked or expired; the link is never handed out after that.
func TestInviteSealedToken(t *testing.T) {
	ctx := context.Background()
	st, c := sealingStore(t)

	raw, inv, err := st.CreateInvite(ctx, NewInvite{MaxUses: 2, TTL: time.Hour}, CLIActor())
	if err != nil || !inv.HasLink {
		t.Fatalf("create: %+v %v", inv, err)
	}
	if ct := sealed(t, st, inv.ID); len(ct) == 0 || strings.Contains(string(ct), raw) {
		t.Fatal("token not sealed")
	}
	got, err := st.InviteToken(ctx, inv.ID, CLIActor())
	if err != nil || got != raw {
		t.Fatalf("unseal: %v", err)
	}
	if auditCount(t, st, "invite.link") != 1 {
		t.Error("link not audited")
	}

	use := func() error {
		return st.db.Tx(ctx, func(tx *sql.Tx) error { _, err := useInviteTx(ctx, tx, raw, c.t); return err })
	}
	if err := use(); err != nil || sealed(t, st, inv.ID) == nil {
		t.Fatalf("first use dropped the link: %v", err)
	}
	if err := use(); err != nil || sealed(t, st, inv.ID) != nil {
		t.Fatalf("last use kept the link: %v", err)
	}
	if _, err := st.InviteToken(ctx, inv.ID, CLIActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("used-up invite: %v", err)
	}

	_, rev, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour}, CLIActor())
	if err := st.RevokeInvite(ctx, rev.ID, CLIActor()); err != nil || sealed(t, st, rev.ID) != nil {
		t.Fatalf("revoke kept the link: %v", err)
	}

	_, exp, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour}, CLIActor())
	_, live, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: 3 * time.Hour}, CLIActor())
	c.advance(2 * time.Hour)
	if _, err := st.InviteToken(ctx, exp.ID, CLIActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired invite before the sweep: %v", err)
	}
	if err := st.SweepInviteLinks(ctx); err != nil {
		t.Fatal(err)
	}
	if sealed(t, st, exp.ID) != nil || sealed(t, st, live.ID) == nil {
		t.Error("sweep: expired kept or active dropped")
	}
	if auditCount(t, st, "invite.link") != 1 {
		t.Error("a refused link was audited")
	}

	// A sealed token copied onto another invite does not open there.
	_, other, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour}, CLIActor())
	if _, err := st.db.W.Exec(`UPDATE invites SET token_ct = (SELECT token_ct FROM invites WHERE id = ?) WHERE id = ?`, live.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InviteToken(ctx, other.ID, CLIActor()); !errors.Is(err, auth.ErrUnsealable) {
		t.Errorf("copied token: %v", err)
	}

	// Invites from before links were kept.
	if _, err := st.db.W.Exec(`UPDATE invites SET token_ct = NULL WHERE id = ?`, live.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InviteToken(ctx, live.ID, CLIActor()); !errors.Is(err, ErrInviteNoLink) {
		t.Errorf("no stored link: %v", err)
	}
}

func TestInviteTokenKeyRotation(t *testing.T) {
	ctx := context.Background()
	st, _ := sealingStore(t, "a")
	raw, inv, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour}, CLIActor())
	old := sealed(t, st, inv.ID)
	var err error
	if st.inviteSeal, err = auth.NewSealer(inviteSealLabel, []byte(strings.Repeat("b", 32)), []byte(strings.Repeat("a", 32))); err != nil {
		t.Fatal(err)
	}
	if got, err := st.InviteToken(ctx, inv.ID, CLIActor()); err != nil || got != raw {
		t.Fatalf("previous key: %v", err)
	}
	if string(sealed(t, st, inv.ID)) == string(old) {
		t.Error("not sealed again under the current key")
	}
	if st.inviteSeal, err = auth.NewSealer(inviteSealLabel, []byte(strings.Repeat("b", 32))); err != nil {
		t.Fatal(err)
	}
	if got, err := st.InviteToken(ctx, inv.ID, CLIActor()); err != nil || got != raw {
		t.Errorf("current key after resealing: %v", err)
	}
}

func TestInviteUnusableGivesNoLink(t *testing.T) {
	ctx := context.Background()
	st, c := sealingStore(t)
	admin, err := st.CreateUser(ctx, NewUser{Username: "boss", Email: "boss@example.com", EmailVerified: true, Role: auth.RoleAdmin}, AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	mkUser(t, st, "other") // so boss is not the last admin once demoted
	if _, err := st.db.W.Exec(`UPDATE users SET role = 'admin' WHERE username = 'other'`); err != nil {
		t.Fatal(err)
	}
	_, inv, _ := st.CreateInvite(ctx, NewInvite{CreatedBy: admin.ID, MaxUses: 1, TTL: time.Hour}, CLIActor())
	if err := st.SetRole(ctx, admin.ID, auth.RoleUser, CLIActor()); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Invite(ctx, inv.ID)
	if got.Status(c.t) != StatusUnusable || got.Finished(c.t) {
		t.Errorf("status %q", got.Status(c.t))
	}
	if _, err := st.InviteToken(ctx, inv.ID, CLIActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("unusable invite gave its link: %v", err)
	}
	if err := st.RemoveInvite(ctx, inv.ID, CLIActor()); !errors.Is(err, ErrInviteActive) {
		t.Errorf("remove unusable: %v", err)
	}
	if act, _ := st.ListActiveInvites(ctx, 10); len(act) != 1 {
		t.Error("unusable invite left the active list")
	}
}

func TestRemoveInvite(t *testing.T) {
	ctx := context.Background()
	st, c := sealingStore(t)
	_, active, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour}, CLIActor())
	_, rev, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour}, CLIActor())
	_, exp, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Minute}, CLIActor())
	_ = st.RevokeInvite(ctx, rev.ID, CLIActor())
	c.advance(2 * time.Minute)

	if act, _ := st.ListActiveInvites(ctx, 10); len(act) != 1 || act[0].ID != active.ID {
		t.Errorf("active list %+v", act)
	}
	if err := st.RemoveInvite(ctx, active.ID, CLIActor()); !errors.Is(err, ErrInviteActive) {
		t.Errorf("removed an active invite: %v", err)
	}
	for _, id := range []int64{rev.ID, exp.ID} {
		if err := st.RemoveInvite(ctx, id, CLIActor()); err != nil {
			t.Errorf("remove %d: %v", id, err)
		}
	}
	if err := st.RemoveInvite(ctx, rev.ID, CLIActor()); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove twice: %v", err)
	}
	if all, _ := st.ListInvites(ctx, 10); len(all) != 1 || auditCount(t, st, "invite.remove") != 2 {
		t.Errorf("after remove: %d invites", len(all))
	}
}

func TestRecordInviteMail(t *testing.T) {
	ctx := context.Background()
	st, c := sealingStore(t)
	_, inv, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour, Email: "a@example.com"}, CLIActor())
	if err := st.RecordInviteMail(ctx, inv.ID, errors.New("smtp: 550 mailbox\r\nunavailable "+strings.Repeat("x", 300))); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Invite(ctx, inv.ID)
	if got.Status(c.t) != StatusSendFailed || strings.ContainsAny(got.MailError, "\r\n") || len([]rune(got.MailError)) > maxMailError {
		t.Errorf("failed: %q %q", got.Status(c.t), got.MailError)
	}
	if err := st.RecordInviteMail(ctx, inv.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ = st.Invite(ctx, inv.ID); got.Status(c.t) != StatusSent || got.MailError != "" {
		t.Errorf("sent: %q", got.Status(c.t))
	}
}
