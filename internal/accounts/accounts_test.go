package accounts

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/db"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)} }
func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func setup(t *testing.T) (*Store, *Sessions, *clock) {
	t.Helper()
	d := testDB(t)
	c := newClock()
	st, se := NewStore(d), NewSessions(d)
	st.now, se.now = c.now, c.now
	return st, se, c
}

func mkUser(t *testing.T, st *Store, name string) *User {
	t.Helper()
	u, err := st.CreateUser(context.Background(), NewUser{Username: name, Email: name + "@example.com", EmailVerified: true}, AuditEntry{})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func newSession(t *testing.T, se *Sessions, uid int64) string {
	t.Helper()
	var raw string
	err := se.db.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		raw, err = se.Create(context.Background(), tx, uid, "192.0.2.1", "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	st, se, c := setup(t)
	u := mkUser(t, st, "ann")

	a, b := newSession(t, se, u.ID), newSession(t, se, u.ID)
	if a == b || len(a) != 43 {
		t.Fatalf("session ids must be fresh 256-bit values: %q %q", a, b)
	}
	p, err := se.Principal(ctx, a)
	if err != nil || p.UserID != u.ID || p.Username != "ann" || !p.EmailVerified || p.SessionID == "" || p.ViaKey() {
		t.Fatalf("principal %+v %v", p, err)
	}
	if strings.Contains(p.SessionID, a) {
		t.Fatal("SessionID must not reveal the cookie value")
	}
	for _, bad := range []string{"", "nope", strings.Repeat("A", 43)} {
		if _, err := se.Principal(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	// Idle expiry: unused for 30 days.
	c.advance(SessionIdle - time.Minute)
	if _, err := se.Principal(ctx, b); err != nil {
		t.Fatal("still inside the idle window")
	}
	if err := se.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	c.advance(SessionIdle - time.Minute) // b was used, a was not
	if _, err := se.Principal(ctx, a); !errors.Is(err, ErrNotFound) {
		t.Error("idle session still valid")
	}
	if _, err := se.Principal(ctx, b); err != nil {
		t.Errorf("recently used session expired: %v", err)
	}

	// Absolute expiry: 90 days after sign-in, however busy.
	for c.t.Before(time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC).Add(SessionAbsolute)) {
		if _, err := se.Principal(ctx, b); err != nil {
			t.Fatalf("expired early at %v", c.t)
		}
		_ = se.Flush(ctx)
		c.advance(10 * 24 * time.Hour)
	}
	if _, err := se.Principal(ctx, b); !errors.Is(err, ErrNotFound) {
		t.Error("session outlived the absolute limit")
	}
	if err := se.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = se.db.R.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("sweep left %d sessions", n)
	}
}

func TestLastSeenIsThrottled(t *testing.T) {
	ctx := context.Background()
	st, se, c := setup(t)
	u := mkUser(t, st, "ann")
	raw := newSession(t, se, u.ID)
	seen := func() time.Time {
		var ms int64
		_ = se.db.R.QueryRow(`SELECT last_seen_at FROM sessions`).Scan(&ms)
		return db.Time(ms)
	}
	start := seen()

	// Requests inside the throttle window record nothing.
	for range 50 {
		if _, err := se.Principal(ctx, raw); err != nil {
			t.Fatal(err)
		}
	}
	if se.pendingCount() != 0 {
		t.Fatal("last_seen queued inside the throttle window")
	}
	// After the window, one update is queued in memory, not written.
	c.advance(seenThrottle + time.Second)
	for range 50 {
		_, _ = se.Principal(ctx, raw)
	}
	if se.pendingCount() != 1 || !seen().Equal(start) {
		t.Fatalf("pending %d, db last_seen moved: %v", se.pendingCount(), !seen().Equal(start))
	}
	// The list already shows the newer time.
	list, _ := se.List(ctx, u.ID)
	if len(list) != 1 || !list[0].LastSeen.Equal(c.t) {
		t.Errorf("list last seen %v want %v", list, c.t)
	}
	// Flush writes the batch.
	if err := se.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if se.pendingCount() != 0 || !seen().Equal(c.t) {
		t.Errorf("flush: pending %d, last_seen %v want %v", se.pendingCount(), seen(), c.t)
	}
	c.advance(time.Minute)
	_, _ = se.Principal(ctx, raw)
	if se.pendingCount() != 0 {
		t.Error("queued again right after a flush")
	}
}

func TestDisabledAndDeletedUsersLoseAccess(t *testing.T) {
	ctx := context.Background()
	st, se, _ := setup(t)
	u := mkUser(t, st, "ann")
	v := mkUser(t, st, "bob")
	ua, ub := newSession(t, se, u.ID), newSession(t, se, v.ID)

	if err := st.SetStatus(ctx, u.ID, StatusDisabled, AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	if _, err := se.Principal(ctx, ua); !errors.Is(err, ErrNotFound) {
		t.Error("disabled user's session still works")
	}
	if p, err := st.PrincipalFor(ctx, u.ID); err == nil || p != nil {
		t.Error("PrincipalFor a disabled user")
	}
	// A disabled user re-enabled does not get old sessions back (they were deleted).
	_ = st.SetStatus(ctx, u.ID, StatusActive, AuditEntry{})
	if _, err := se.Principal(ctx, ua); !errors.Is(err, ErrNotFound) {
		t.Error("session survived disable")
	}

	if _, err := st.db.W.Exec(`DELETE FROM users WHERE id = ?`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := se.Principal(ctx, ub); !errors.Is(err, ErrNotFound) {
		t.Error("deleted user's session still works")
	}
}

func TestRoleChangeAppliesImmediately(t *testing.T) {
	ctx := context.Background()
	st, se, _ := setup(t)
	u := mkUser(t, st, "ann")
	raw := newSession(t, se, u.ID)
	if err := st.SetRole(ctx, u.ID, auth.RoleAdmin, CLIActor()); err != nil {
		t.Fatal(err)
	}
	if p, _ := se.Principal(ctx, raw); !p.IsAdmin() {
		t.Fatal("promotion not visible")
	}
	var details string
	_ = st.db.R.QueryRow(`SELECT details FROM audit_log WHERE action = 'user.role_change'`).Scan(&details)
	if !strings.Contains(details, `"to":"admin"`) || !strings.Contains(details, `"via":"cli"`) {
		t.Errorf("audit details %s", details)
	}
}

func TestPasswordChangeKeepsOnlyCurrentSession(t *testing.T) {
	ctx := context.Background()
	st, se, _ := setup(t)
	u := mkUser(t, st, "ann")
	keep, other := newSession(t, se, u.ID), newSession(t, se, u.ID)
	if err := st.SetPassword(ctx, u.ID, "$argon2id$v=19$m=19456,t=2,p=1$x$y", hashSecret(keep), AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	if _, err := se.Principal(ctx, keep); err != nil {
		t.Error("current session was signed out")
	}
	if _, err := se.Principal(ctx, other); !errors.Is(err, ErrNotFound) {
		t.Error("other session survived a password change")
	}
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	st, _, c := setup(t)
	u := mkUser(t, st, "ann")

	raw, err := st.CreateToken(ctx, TokenReset, u.ID, u.Email, ResetTTL)
	if err != nil || len(raw) != 43 {
		t.Fatalf("token %q %v", raw, err)
	}
	var stored []byte
	_ = st.db.R.QueryRow(`SELECT hash FROM tokens`).Scan(&stored)
	if strings.Contains(string(stored), raw) || len(stored) != 32 {
		t.Fatal("token must be stored as its SHA-256 only")
	}
	if _, err := st.PeekToken(ctx, raw, TokenVerify); !errors.Is(err, ErrInvalidToken) {
		t.Error("kind not checked")
	}
	if tk, err := st.PeekToken(ctx, raw, TokenReset); err != nil || tk.UserID != u.ID {
		t.Fatalf("peek %v %v", tk, err)
	}

	// A newer token of the same kind replaces the older one.
	raw2, _ := st.CreateToken(ctx, TokenReset, u.ID, u.Email, ResetTTL)
	if _, err := st.PeekToken(ctx, raw, TokenReset); !errors.Is(err, ErrInvalidToken) {
		t.Error("old reset token still valid")
	}

	// Single use.
	if _, err := st.ResetPassword(ctx, raw2, "$argon2id$v=19$m=19456,t=2,p=1$x$y", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResetPassword(ctx, raw2, "$argon2id$v=19$m=19456,t=2,p=1$x$z", ""); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("reused token: %v", err)
	}

	// Expiry.
	raw3, _ := st.CreateToken(ctx, TokenVerify, u.ID, u.Email, VerifyTTL)
	c.advance(VerifyTTL)
	if _, err := st.ConfirmEmail(ctx, raw3); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired token: %v", err)
	}

	// Malformed values never reach the database.
	for _, bad := range []string{"", "short", strings.Repeat("!", 43)} {
		if _, err := st.PeekToken(ctx, bad, TokenReset); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestEmailChangeToken(t *testing.T) {
	ctx := context.Background()
	st, _, _ := setup(t)
	u := mkUser(t, st, "ann")
	raw, _ := st.CreateToken(ctx, TokenEmailChange, u.ID, "new@example.com", VerifyTTL)
	if got := st.PendingEmail(ctx, u.ID); got != "new@example.com" {
		t.Fatalf("pending %q", got)
	}
	// The old address stays until the link is used.
	if cur, _ := st.User(ctx, u.ID); cur.Email != "ann@example.com" || !cur.EmailVerified() {
		t.Fatal("verified address replaced early")
	}
	got, err := st.ConfirmEmail(ctx, raw)
	if err != nil || got.Email != "new@example.com" || got.EmailVerifiedAt == nil {
		t.Fatalf("confirm %+v %v", got, err)
	}
	// Taken in the meantime: refused.
	mkUser(t, st, "bob")
	raw, _ = st.CreateToken(ctx, TokenEmailChange, u.ID, "bob@example.com", VerifyTTL)
	if _, err := st.ConfirmEmail(ctx, raw); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("taken address: %v", err)
	}
}

func TestInvites(t *testing.T) {
	ctx := context.Background()
	st, _, c := setup(t)
	raw, inv, err := st.CreateInvite(ctx, NewInvite{MaxUses: 2, TTL: time.Hour, Role: auth.RoleAdmin, Email: " x@example.com "}, CLIActor())
	if err != nil || inv.Email != "x@example.com" || inv.CreatedBy != 0 || inv.State(c.t) != InviteActive {
		t.Fatalf("create %+v %v", inv, err)
	}
	use := func() error {
		return st.db.Tx(ctx, func(tx *sql.Tx) error { _, err := useInviteTx(ctx, tx, raw, c.t); return err })
	}
	if err := use(); err != nil {
		t.Fatal(err)
	}
	if err := use(); err != nil {
		t.Fatal(err)
	}
	if err := use(); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("third use of a 2-use invite: %v", err)
	}
	if _, err := st.InviteByToken(ctx, raw); !errors.Is(err, ErrInvalidToken) {
		t.Error("used-up invite still offered")
	}

	raw2, inv2, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour}, CLIActor())
	if err := st.RevokeInvite(ctx, inv2.ID, CLIActor()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InviteByToken(ctx, raw2); !errors.Is(err, ErrInvalidToken) {
		t.Error("revoked invite still valid")
	}
	raw3, _, _ := st.CreateInvite(ctx, NewInvite{MaxUses: 1, TTL: time.Hour}, CLIActor())
	c.advance(time.Hour)
	if _, err := st.InviteByToken(ctx, raw3); !errors.Is(err, ErrInvalidToken) {
		t.Error("expired invite still valid")
	}
	list, _ := st.ListInvites(ctx, 10)
	if len(list) != 3 || list[0].State(c.t) != InviteExpired || list[1].State(c.t) != InviteRevoked || list[2].State(c.t) != InviteUsed {
		t.Errorf("list states: %+v", list)
	}
	for _, bad := range []NewInvite{{MaxUses: 0, TTL: time.Hour}, {MaxUses: 1, TTL: 0}, {MaxUses: 1, TTL: time.Hour, Role: "root"}, {MaxUses: 1, TTL: time.Hour, Email: "nope"}} {
		if _, _, err := st.CreateInvite(ctx, bad, CLIActor()); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestEmailVerifiedRule(t *testing.T) {
	now := time.Now()
	cases := []struct {
		u    User
		want bool
	}{
		{User{}, true},                 // no address: admin-created, invited or SSO
		{User{Email: "a@x.io"}, false}, // self-registered, not confirmed yet
		{User{Email: "a@x.io", EmailVerifiedAt: &now}, true},
	}
	for _, c := range cases {
		if got := c.u.EmailVerified(); got != c.want {
			t.Errorf("%+v: %v", c.u, got)
		}
	}
}

func TestUsernames(t *testing.T) {
	for in, want := range map[string]string{
		"ann":                   "ann",
		"Ann.Lee":               "Ann.Lee",
		"ann lee":               "ann-lee",
		"__ann":                 "ann",
		"ünïcode":               "n-code",
		"":                      "user",
		"!!!":                   "user",
		strings.Repeat("a", 40): strings.Repeat("a", 32),
	} {
		got := sanitizeUsername(in)
		if got != want || ValidUsername(got) != nil {
			t.Errorf("sanitize(%q) = %q want %q", in, got, want)
		}
	}
	st, _, _ := setup(t)
	mkUser(t, st, "ann")
	mkUser(t, st, "ann-2")
	err := st.db.Tx(context.Background(), func(tx *sql.Tx) error {
		name, err := freeUsername(context.Background(), tx, "ANN")
		if name != "ANN-3" {
			t.Errorf("free name %q", name)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "-ann", "a b", strings.Repeat("a", 33), "ann@x"} {
		if ValidUsername(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestOIDCStateCookie(t *testing.T) {
	cfg := &config.Config{SecretKey: []byte(strings.Repeat("k", 32)), OIDC: config.OIDC{Issuer: "https://id.example"}}
	cfg.PublicURL = mustURL("https://snap.example")
	f, err := newOIDCFlow(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newClock()
	f.now = c.now
	st := oidcState{State: "s", Nonce: "n", Verifier: "v", Next: "/x", Expires: c.t.Add(oidcStateTTL).Unix()}
	sealed := f.seal(st)
	got, err := f.open(sealed)
	if err != nil || *got != st {
		t.Fatalf("round trip %+v %v", got, err)
	}
	payload, sig, _ := strings.Cut(sealed, ".")
	for _, bad := range []string{"", "x", payload, payload + ".AAAA", "e30." + sig, payload[:len(payload)-2] + "." + sig} {
		if _, err := f.open(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// Rotation: a cookie signed with the previous key still opens.
	rotated := &config.Config{SecretKey: []byte(strings.Repeat("n", 32)), SecretKeyPrevious: cfg.SecretKey, OIDC: cfg.OIDC, PublicURL: cfg.PublicURL}
	f2, _ := newOIDCFlow(rotated, nil)
	f2.now = c.now
	if _, err := f2.open(sealed); err != nil {
		t.Errorf("previous key not accepted: %v", err)
	}
	other := &config.Config{SecretKey: []byte(strings.Repeat("z", 32)), OIDC: cfg.OIDC, PublicURL: cfg.PublicURL}
	f3, _ := newOIDCFlow(other, nil)
	f3.now = c.now
	if _, err := f3.open(sealed); err == nil {
		t.Error("foreign key accepted")
	}
	c.advance(oidcStateTTL)
	if _, err := f.open(sealed); err == nil {
		t.Error("expired state accepted")
	}
}

func TestGroupsClaim(t *testing.T) {
	cases := map[string]struct {
		want    []string
		present bool
	}{
		``:                     {nil, false},
		`null`:                 {nil, false},
		`[]`:                   {[]string{}, true},
		`["ops","ops:admins"]`: {[]string{"ops", "ops:admins"}, true},
		`["ops",3,null,""]`:    {[]string{"ops"}, true},
		`"ops"`:                {[]string{"ops"}, true},
	}
	for raw, want := range cases {
		got, present := ssoClaims{Groups: []byte(raw)}.groups()
		if present != want.present || strings.Join(got, ",") != strings.Join(want.want, ",") {
			t.Errorf("%s: %v %v", raw, got, present)
		}
	}
}

func TestDeviceSummary(t *testing.T) {
	for ua, want := range map[string]string{
		"":            "—",
		"curl/8.11.0": "curl",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15":    "Safari · macOS",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36 Edg/131.0": "Edge · Windows",
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                                "Firefox · Linux",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Mobile Safari/537.36":              "Chrome · Android",
	} {
		if got := DeviceSummary(ua); got != want {
			t.Errorf("%q: %q want %q", ua, got, want)
		}
	}
}
