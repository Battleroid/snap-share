package accounts

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
)

// Session lifetimes.
const (
	SessionIdle     = 30 * 24 * time.Hour // unused this long: signed out
	SessionAbsolute = 90 * 24 * time.Hour // signed out this long after sign-in regardless of use
	seenThrottle    = 5 * time.Minute     // last_seen moves at most this often per session
)

// Session is a browser session as listed on the security page.
type Session struct {
	ID        string // hex of the stored hash; safe to show and post back
	UserID    int64
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

// Sessions stores browser sessions. last_seen updates are kept in memory and
// written in batches by Flush, so a request never causes a write.
type Sessions struct {
	db  *db.DB
	now func() time.Time

	mu      sync.Mutex
	pending map[string]int64 // hash (raw bytes as string) -> last seen, unix ms
}

// NewSessions returns a session store on d.
func NewSessions(d *db.DB) *Sessions {
	return &Sessions{db: d, now: time.Now, pending: map[string]int64{}}
}

// Create starts a session for uid and returns the cookie value. Each sign-in
// gets a new id, so a session id planted before sign-in is never promoted.
func (m *Sessions) Create(ctx context.Context, tx *sql.Tx, uid int64, ip, ua string) (string, error) {
	raw, hash := newSecret()
	now := m.now()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, user_id, created_at, last_seen_at, expires_at, ip, user_agent)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, hash, uid, db.Millis(now), db.Millis(now), db.Millis(now.Add(SessionAbsolute)),
		nullStr(ip), nullStr(ua))
	return raw, err
}

// Principal resolves a cookie value. It returns ErrNotFound when the session
// does not exist, has expired, or belongs to a disabled user.
func (m *Sessions) Principal(ctx context.Context, raw string) (*auth.Principal, error) {
	if !validSecretShape(raw) {
		return nil, ErrNotFound
	}
	hash := hashSecret(raw)
	var (
		uid, seen, exp int64
		username, role string
		status         string
		email          sql.NullString
		verified       sql.NullInt64
	)
	err := m.db.R.QueryRowContext(ctx, `SELECT s.user_id, s.last_seen_at, s.expires_at, u.username, u.role, u.status, u.email, u.email_verified_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ?`, hash).
		Scan(&uid, &seen, &exp, &username, &role, &status, &email, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	now := m.now()
	nowMs := db.Millis(now)
	key := string(hash)
	m.mu.Lock()
	if p, ok := m.pending[key]; ok && p > seen {
		seen = p
	}
	m.mu.Unlock()
	if nowMs >= exp || nowMs-seen >= SessionIdle.Milliseconds() || status != StatusActive {
		return nil, ErrNotFound
	}
	if nowMs-seen >= seenThrottle.Milliseconds() {
		m.mu.Lock()
		m.pending[key] = nowMs
		m.mu.Unlock()
	}
	return &auth.Principal{
		UserID:        uid,
		Username:      username,
		Role:          auth.Role(role),
		EmailVerified: !email.Valid || email.String == "" || verified.Valid,
		SessionID:     hex.EncodeToString(hash),
	}, nil
}

// Flush writes batched last_seen updates.
func (m *Sessions) Flush(ctx context.Context) error {
	m.mu.Lock()
	batch := m.pending
	m.pending = map[string]int64{}
	m.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := m.db.Tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `UPDATE sessions SET last_seen_at = max(last_seen_at, ?) WHERE id = ?`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for k, seen := range batch {
			if _, err := stmt.ExecContext(ctx, seen, []byte(k)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Put the batch back so a transient failure loses nothing.
		m.mu.Lock()
		for k, v := range batch {
			if v > m.pending[k] {
				m.pending[k] = v
			}
		}
		m.mu.Unlock()
	}
	return err
}

// pendingCount reports how many sessions have an unwritten last_seen (tests).
func (m *Sessions) pendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

// Delete ends one session by cookie value.
func (m *Sessions) Delete(ctx context.Context, raw string) error {
	if !validSecretShape(raw) {
		return nil
	}
	_, err := m.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, hashSecret(raw))
	return err
}

// Revoke ends the session with the given listed ID, if it belongs to uid.
func (m *Sessions) Revoke(ctx context.Context, uid int64, id string) error {
	hash, err := hex.DecodeString(id)
	if err != nil || len(hash) != 32 {
		return ErrNotFound
	}
	res, err := m.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, hash, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAll ends every session of uid.
func (m *Sessions) RevokeAll(ctx context.Context, tx *sql.Tx, uid int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, uid)
	return err
}

// List returns uid's live sessions, most recently used first.
func (m *Sessions) List(ctx context.Context, uid int64) ([]Session, error) {
	now := db.Millis(m.now())
	rows, err := m.db.R.QueryContext(ctx, `SELECT id, created_at, last_seen_at, expires_at, coalesce(ip, ''), coalesce(user_agent, '')
		FROM sessions WHERE user_id = ? AND expires_at > ? AND last_seen_at > ?`, uid, now, now-SessionIdle.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Session
	m.mu.Lock()
	defer m.mu.Unlock()
	for rows.Next() {
		var (
			hash                   []byte
			created, seen, expires int64
			s                      Session
		)
		if err := rows.Scan(&hash, &created, &seen, &expires, &s.IP, &s.UserAgent); err != nil {
			return nil, err
		}
		if p := m.pending[string(hash)]; p > seen {
			seen = p
		}
		s.ID, s.UserID = hex.EncodeToString(hash), uid
		s.CreatedAt, s.LastSeen, s.ExpiresAt = db.Time(created), db.Time(seen), db.Time(expires)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, rows.Err()
}

// Sweep deletes expired sessions and stale tokens and invites.
func (m *Sessions) Sweep(ctx context.Context) error {
	now := db.Millis(m.now())
	return m.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?`,
			now, now-SessionIdle.Milliseconds()); err != nil {
			return err
		}
		day := (24 * time.Hour).Milliseconds()
		if _, err := tx.ExecContext(ctx, `DELETE FROM tokens WHERE expires_at <= ? OR used_at <= ?`, now-day, now-day); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM invites WHERE expires_at <= ? OR revoked_at <= ?`, now-30*day, now-30*day)
		return err
	})
}
