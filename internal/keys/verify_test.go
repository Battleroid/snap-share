package keys

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/ratelimit"
)

type fakeOwners struct {
	mu sync.Mutex
	m  map[int64]*auth.Principal
}

func (f *fakeOwners) PrincipalFor(_ context.Context, id int64) (*auth.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[id]
	if !ok {
		return nil, accounts.ErrNotFound
	}
	cp := *p
	return &cp, nil
}

func (f *fakeOwners) set(p auth.Principal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[p.UserID] = &p
}

func (f *fakeOwners) remove(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, id)
}

type testEnv struct {
	t      *testing.T
	s      *Service
	db     *db.DB
	owners *fakeOwners
	clock  time.Time
	cmps   int
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	e := &testEnv{t: t, db: d, owners: &fakeOwners{m: map[int64]*auth.Principal{}}, clock: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	now := func() time.Time { return e.clock }
	e.s = &Service{
		d: Deps{DB: d, Owners: e.owners}, log: slog.New(slog.NewTextHandler(io.Discard, nil)), now: now,
		store: NewStore(d, now), limit: ratelimit.New(keyRate, time.Minute, keyBurst).WithClock(now),
	}
	e.s.cmp = func(want []byte, secret string) bool { e.cmps++; return matchSecret(want, secret) }
	return e
}

func (e *testEnv) user(id int64, name string, role auth.Role) {
	e.t.Helper()
	if _, err := e.db.W.Exec(`INSERT INTO users (id, username, role, created_at, updated_at) VALUES (?, ?, ?, 1, 1)`, id, name, string(role)); err != nil {
		e.t.Fatal(err)
	}
	e.owners.set(auth.Principal{UserID: id, Username: name, Role: role, EmailVerified: true})
}

func (e *testEnv) key(uid int64, nk NewKey) (string, *Key) {
	e.t.Helper()
	if nk.Name == "" {
		nk.Name = "test"
	}
	if nk.Scopes == nil {
		nk.Scopes = []auth.Scope{auth.ScopeItemsRead}
	}
	tok, k, err := e.s.store.Create(context.Background(), uid, nk, accounts.AuditEntry{})
	if err != nil {
		e.t.Fatal(err)
	}
	return tok, k
}

// verify runs VerifyBearer for a request from remote, through the client IP middleware.
func (e *testEnv) verify(token, remote string) (*auth.Principal, error) {
	var (
		p   *auth.Principal
		err error
	)
	h := clientip.Resolver{}.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p, err = e.s.VerifyBearer(r, token)
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.RemoteAddr = remote
	h.ServeHTTP(httptest.NewRecorder(), r)
	return p, err
}

func TestVerifyBearer(t *testing.T) {
	e := newTestEnv(t)
	e.user(1, "ann", auth.RoleUser)
	e.user(2, "bob", auth.RoleUser)
	ctx := context.Background()

	good, k := e.key(1, NewKey{Scopes: []auth.Scope{auth.ScopeItemsWrite, auth.ScopeAccountRead}})
	p, err := e.verify(good, "198.51.100.7:4000")
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != 1 || p.Username != "ann" || p.KeyID != k.ID || !p.ViaKey() || !p.EmailVerified || p.Key != nil {
		t.Fatalf("principal %+v", p)
	}
	if !p.Can(auth.ScopeItemsWrite) || p.Can(auth.ScopeItemsRead) || p.Can(auth.ScopeItemsDelete) {
		t.Errorf("scopes %v", p.Scopes)
	}

	pub, secret, _ := ParseToken(good)
	other, _, _ := newToken()
	_, otherSecret, _ := ParseToken(other)
	unknownPub := "000000000000"
	if unknownPub == pub {
		unknownPub = "000000000001"
	}
	revoked, rk := e.key(1, NewKey{})
	if err := e.s.store.Revoke(ctx, 1, rk.ID, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	past := e.clock.Add(-time.Minute)
	expired, _ := e.key(1, NewKey{ExpiresAt: &past})
	soon := e.clock.Add(time.Hour)
	expiring, _ := e.key(1, NewKey{ExpiresAt: &soon})
	bobs, _ := e.key(2, NewKey{})

	cases := []struct {
		name     string
		token    string
		wantErr  error
		compares int // secret comparisons expected
	}{
		{"valid", good, nil, 1},
		{"not expired yet", expiring, nil, 1},
		{"bad checksum", good[:len(good)-1] + flip(good[len(good)-1]), auth.ErrInvalidCredentials, 0},
		{"garbage", "snap_nope", auth.ErrInvalidCredentials, 0},
		{"wrong secret", withSum("snap_" + pub + "_" + otherSecret), auth.ErrInvalidCredentials, 1},
		{"unknown id", withSum("snap_" + unknownPub + "_" + secret), auth.ErrInvalidCredentials, 1},
		{"revoked", revoked, auth.ErrInvalidCredentials, 1},
		{"expired", expired, auth.ErrInvalidCredentials, 1},
		{"other user", bobs, nil, 1},
	}
	for _, c := range cases {
		before := e.cmps
		_, err := e.verify(c.token, "198.51.100.7:4000")
		if (err == nil) != (c.wantErr == nil) || (c.wantErr != nil && !errors.Is(err, c.wantErr)) {
			t.Errorf("%s: err %v, want %v", c.name, err, c.wantErr)
		}
		if got := e.cmps - before; got != c.compares {
			t.Errorf("%s: %d secret comparisons, want %d", c.name, got, c.compares)
		}
	}

	// The key stops working when it expires, without any write.
	e.clock = soon
	if _, err := e.verify(expiring, "198.51.100.7:4000"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("expired at its time: %v", err)
	}

	// A disabled (or deleted) owner loses every key.
	e.owners.remove(2)
	if _, err := e.verify(bobs, "198.51.100.7:4000"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("disabled owner: %v", err)
	}
}

func flip(c byte) string {
	if c == 'A' {
		return "B"
	}
	return "A"
}

func TestVerifyBearerNetworks(t *testing.T) {
	e := newTestEnv(t)
	e.user(1, "ann", auth.RoleUser)
	tok, _ := e.key(1, NewKey{CIDRs: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24"), netip.MustParsePrefix("2001:db8::/32")}})
	for remote, ok := range map[string]bool{
		"192.168.1.20:5000":        true,
		"[2001:db8::5]:5000":       true,
		"[::ffff:192.168.1.9]:443": true, // IPv4-mapped clients are compared as IPv4
		"192.168.2.1:5000":         false,
		"10.0.0.1:5000":            false,
		"[2001:db9::1]:5000":       false,
		"garbage":                  false,
	} {
		_, err := e.verify(tok, remote)
		switch {
		case ok && err != nil:
			t.Errorf("%s: %v", remote, err)
		case !ok && !errors.Is(err, auth.ErrForbiddenNetwork):
			t.Errorf("%s: err %v, want ErrForbiddenNetwork", remote, err)
		}
	}
	// A wrong secret from an allowed or refused address is just invalid: the
	// network rule is not revealed to someone without the key.
	pub, _, _ := ParseToken(tok)
	other, _, _ := newToken()
	_, sec, _ := ParseToken(other)
	if _, err := e.verify(withSum("snap_"+pub+"_"+sec), "10.0.0.1:5000"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("wrong secret from outside: %v", err)
	}
}

func TestScopesFollowOwnerRole(t *testing.T) {
	e := newTestEnv(t)
	e.user(1, "root", auth.RoleAdmin)
	tok, _ := e.key(1, NewKey{Scopes: []auth.Scope{auth.ScopeAdmin, auth.ScopeItemsRead}})
	p, err := e.verify(tok, "198.51.100.7:1")
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsAdmin() || !slices.Contains(p.Scopes, auth.ScopeAdmin) {
		t.Fatalf("admin key: %v", p.Scopes)
	}
	// Demotion takes effect on the next request; the key keeps its other scopes.
	e.owners.set(auth.Principal{UserID: 1, Username: "root", Role: auth.RoleUser, EmailVerified: true})
	p, err = e.verify(tok, "198.51.100.7:1")
	if err != nil {
		t.Fatal(err)
	}
	if p.IsAdmin() || slices.Contains(p.Scopes, auth.ScopeAdmin) || !p.Can(auth.ScopeItemsRead) {
		t.Fatalf("demoted: admin=%v scopes=%v", p.IsAdmin(), p.Scopes)
	}
	// And comes back with the role.
	e.owners.set(auth.Principal{UserID: 1, Username: "root", Role: auth.RoleAdmin, EmailVerified: true})
	if p, _ := e.verify(tok, "198.51.100.7:1"); !p.IsAdmin() {
		t.Fatal("promoted again: no admin")
	}
}

func TestEffectiveScopes(t *testing.T) {
	all := auth.AllScopes
	user := EffectiveScopes(all, auth.RoleUser)
	if slices.Contains(user, auth.ScopeAdmin) || len(user) != len(all)-1 {
		t.Errorf("user: %v", user)
	}
	if admin := EffectiveScopes(all, auth.RoleAdmin); len(admin) != len(all) {
		t.Errorf("admin: %v", admin)
	}
	if got := EffectiveScopes([]auth.Scope{"bogus", auth.ScopeItemsRead, auth.ScopeItemsRead}, auth.RoleUser); !slices.Equal(got, []auth.Scope{auth.ScopeItemsRead}) {
		t.Errorf("dedupe and drop unknown: %v", got)
	}
}

func TestConstraintsOnPrincipal(t *testing.T) {
	e := newTestEnv(t)
	e.user(1, "ann", auth.RoleUser)
	tok, _ := e.key(1, NewKey{Constraints: Constraints{Visibility: "private", MaxExpirySeconds: 3600, MaxUploadBytes: 1000}})
	p, err := e.verify(tok, "198.51.100.7:1")
	if err != nil {
		t.Fatal(err)
	}
	kc := p.Constraints()
	if kc.ForcedVisibility() != "private" || kc.MaxExpiry != time.Hour || kc.UploadLimit(5000) != 1000 || kc.UploadLimit(500) != 500 {
		t.Fatalf("constraints %+v", kc)
	}
	now := e.clock
	if got := kc.ClampExpiry(nil, now); got == nil || !got.Equal(now.Add(time.Hour)) {
		t.Errorf("never: %v", got)
	}
	later := now.Add(48 * time.Hour)
	if got := kc.ClampExpiry(&later, now); !got.Equal(now.Add(time.Hour)) {
		t.Errorf("later: %v", got)
	}
	sooner := now.Add(time.Minute)
	if got := kc.ClampExpiry(&sooner, now); !got.Equal(sooner) {
		t.Errorf("sooner: %v", got)
	}
	var none *auth.KeyConstraints
	if none.ClampExpiry(nil, now) != nil || none.UploadLimit(7) != 7 || none.ForcedVisibility() != "" {
		t.Error("nil constraints limit something")
	}
}

func TestLastUsedIsBatched(t *testing.T) {
	e := newTestEnv(t)
	e.user(1, "ann", auth.RoleUser)
	tok, k := e.key(1, NewKey{})
	for range 25 {
		if _, err := e.verify(tok, "203.0.113.4:999"); err != nil {
			t.Fatal(err)
		}
	}
	var used any
	if err := e.db.R.QueryRow(`SELECT last_used_at FROM api_keys WHERE id = ?`, k.ID).Scan(&used); err != nil || used != nil {
		t.Fatalf("requests wrote last_used_at directly: %v %v", used, err)
	}
	if n := e.s.store.pendingCount(); n != 1 {
		t.Fatalf("pending %d", n)
	}
	// The list shows the pending value before it is flushed.
	got, err := e.s.store.Get(context.Background(), 1, k.ID)
	if err != nil || got.LastUsedAt == nil || got.LastUsedIP != "203.0.113.4" {
		t.Fatalf("merged usage: %+v %v", got, err)
	}
	if err := e.s.store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var at int64
	var ip string
	if err := e.db.R.QueryRow(`SELECT last_used_at, last_used_ip FROM api_keys WHERE id = ?`, k.ID).Scan(&at, &ip); err != nil {
		t.Fatal(err)
	}
	if at != db.Millis(e.clock) || ip != "203.0.113.4" || e.s.store.pendingCount() != 0 {
		t.Fatalf("flushed %d %q (pending %d)", at, ip, e.s.store.pendingCount())
	}
	// An older pending value never overwrites a newer stored one.
	e.s.store.touch(k.ID, e.clock.Add(-time.Hour), "198.51.100.1")
	if err := e.s.store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = e.db.R.QueryRow(`SELECT last_used_ip FROM api_keys WHERE id = ?`, k.ID).Scan(&ip)
	if ip != "203.0.113.4" {
		t.Errorf("older use overwrote the newer one: %s", ip)
	}
}

func TestRateLimit(t *testing.T) {
	e := newTestEnv(t)
	e.s.d.Errors = nil // ErrorPages handles a nil receiver with plain text
	e.s.limit = ratelimit.New(2, time.Minute, 2).WithClock(func() time.Time { return e.clock })
	h := e.s.RateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	call := func(p *auth.Principal) int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/items", nil)
		if p != nil {
			r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	key := &auth.Principal{UserID: 1, KeyID: 9, Scopes: []auth.Scope{auth.ScopeItemsRead}}
	other := &auth.Principal{UserID: 1, KeyID: 10, Scopes: []auth.Scope{auth.ScopeItemsRead}}
	session := &auth.Principal{UserID: 1}
	got := []int{call(key), call(key), call(key), call(other), call(session), call(session), call(session), call(nil)}
	want := []int{204, 204, 429, 204, 204, 204, 204, 204}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	e.clock = e.clock.Add(time.Minute)
	if call(key) != 204 {
		t.Error("budget did not refill")
	}
}

func TestStoreCreateLimits(t *testing.T) {
	e := newTestEnv(t)
	e.user(1, "ann", auth.RoleUser)
	ctx := context.Background()
	for range MaxActiveKeys {
		e.key(1, NewKey{})
	}
	if _, _, err := e.s.store.Create(ctx, 1, NewKey{Name: "x", Scopes: []auth.Scope{auth.ScopeItemsRead}}, accounts.AuditEntry{}); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("over the cap: %v", err)
	}
	list, _ := e.s.store.List(ctx, 1)
	if err := e.s.store.Revoke(ctx, 1, list[0].ID, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.store.Create(ctx, 1, NewKey{Name: "x", Scopes: []auth.Scope{auth.ScopeItemsRead}}, accounts.AuditEntry{}); err != nil {
		t.Fatalf("after revoking one: %v", err)
	}
	// Another user's key is not found, and revoking twice is a no-op.
	if err := e.s.store.Revoke(ctx, 2, list[1].ID, accounts.AuditEntry{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("other user's revoke: %v", err)
	}
	if err := e.s.store.Revoke(ctx, 1, list[0].ID, accounts.AuditEntry{}); err != nil {
		t.Errorf("revoke twice: %v", err)
	}
	var audits int
	_ = e.db.R.QueryRow(`SELECT count(*) FROM audit_log WHERE action IN ('key.create', 'key.revoke')`).Scan(&audits)
	if audits != MaxActiveKeys+2 {
		t.Errorf("audit rows %d", audits)
	}
}
