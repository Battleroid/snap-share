package keys

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
)

// Errors callers branch on.
var (
	ErrNotFound    = errors.New("key not found")
	ErrTooManyKeys = fmt.Errorf("you have %d active keys; revoke one first", MaxActiveKeys)
)

// MaxActiveKeys caps the unrevoked, unexpired keys one user may hold.
const MaxActiveKeys = 50

// Key statuses.
const (
	StatusActive  = "active"
	StatusExpired = "expired"
	StatusRevoked = "revoked"
)

// Constraints are a key's limits on what it creates, as stored.
type Constraints struct {
	Visibility       string `json:"visibility,omitempty"`         // unlisted | private: forced
	MaxExpirySeconds int64  `json:"max_expiry_seconds,omitempty"` // 0: no cap
	MaxUploadBytes   int64  `json:"max_upload_bytes,omitempty"`   // 0: no cap
}

// IsZero reports whether the constraints limit nothing.
func (c Constraints) IsZero() bool { return c == Constraints{} }

// principal converts stored constraints to the form requests carry.
func (c Constraints) principal() *auth.KeyConstraints {
	if c.IsZero() {
		return nil
	}
	return &auth.KeyConstraints{
		Visibility:     c.Visibility,
		MaxExpiry:      time.Duration(c.MaxExpirySeconds) * time.Second,
		MaxUploadBytes: c.MaxUploadBytes,
	}
}

// Key is one api_keys row. The secret is never kept, only its hash.
type Key struct {
	ID          int64
	UserID      int64
	Name        string
	PublicID    string
	Scopes      []auth.Scope
	CIDRs       []netip.Prefix
	Constraints Constraints
	ExpiresAt   *time.Time
	LastUsedAt  *time.Time
	LastUsedIP  string
	CreatedAt   time.Time
	RevokedAt   *time.Time

	secretHash []byte
}

// Prefix is how the key is shown: its fixed start, which is not secret.
func (k *Key) Prefix() string { return tokenPrefix + k.PublicID }

// Status is active, expired or revoked at now.
func (k *Key) Status(now time.Time) string {
	switch {
	case k.RevokedAt != nil:
		return StatusRevoked
	case k.ExpiresAt != nil && !k.ExpiresAt.After(now):
		return StatusExpired
	}
	return StatusActive
}

// NewKey is a validated key to create.
type NewKey struct {
	Name        string
	Scopes      []auth.Scope
	CIDRs       []netip.Prefix
	Constraints Constraints
	ExpiresAt   *time.Time
}

// Store reads and writes keys. last_used is batched in memory and written by
// Flush, so authenticating a request never writes to the database.
type Store struct {
	db  *db.DB
	now func() time.Time

	mu      sync.Mutex
	pending map[int64]usage
}

type usage struct {
	at int64 // unix ms
	ip string
}

// NewStore returns a store on d. now may be nil (time.Now).
func NewStore(d *db.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: d, now: now, pending: map[int64]usage{}}
}

const keyCols = `id, user_id, name, public_id, secret_hash, scopes, allowed_cidrs, constraints, expires_at, last_used_at, last_used_ip, created_at, revoked_at`

type scanner interface{ Scan(dest ...any) error }

func scanKey(row scanner) (*Key, error) {
	var (
		k                          Key
		scopes, cidrs, constraints string
		exp, used, revoked         sql.NullInt64
		ip                         sql.NullString
		created                    int64
	)
	if err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.PublicID, &k.secretHash, &scopes, &cidrs, &constraints,
		&exp, &used, &ip, &created, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var names []string
	if err := json.Unmarshal([]byte(scopes), &names); err != nil {
		return nil, fmt.Errorf("key %d scopes: %w", k.ID, err)
	}
	for _, n := range names {
		if s := auth.Scope(n); s.Valid() {
			k.Scopes = append(k.Scopes, s)
		}
	}
	var nets []string
	if err := json.Unmarshal([]byte(cidrs), &nets); err != nil {
		return nil, fmt.Errorf("key %d networks: %w", k.ID, err)
	}
	for _, n := range nets {
		p, err := netip.ParsePrefix(n)
		if err != nil {
			return nil, fmt.Errorf("key %d networks: %w", k.ID, err)
		}
		k.CIDRs = append(k.CIDRs, p)
	}
	if err := json.Unmarshal([]byte(constraints), &k.Constraints); err != nil {
		return nil, fmt.Errorf("key %d constraints: %w", k.ID, err)
	}
	k.ExpiresAt, k.LastUsedAt, k.RevokedAt = optTime(exp), optTime(used), optTime(revoked)
	k.LastUsedIP = ip.String
	k.CreatedAt = db.Time(created)
	return &k, nil
}

func optTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := db.Time(v.Int64)
	return &t
}

// ByPublicID loads a key by its public id (any status).
func (s *Store) ByPublicID(ctx context.Context, publicID string) (*Key, error) {
	if !ValidPublicID(publicID) {
		return nil, ErrNotFound
	}
	return scanKey(s.db.R.QueryRowContext(ctx, `SELECT `+keyCols+` FROM api_keys WHERE public_id = ?`, publicID))
}

// Get loads one of userID's keys.
func (s *Store) Get(ctx context.Context, userID, id int64) (*Key, error) {
	k, err := scanKey(s.db.R.QueryRowContext(ctx, `SELECT `+keyCols+` FROM api_keys WHERE id = ? AND user_id = ?`, id, userID))
	if err == nil {
		s.mergeUsage(k)
	}
	return k, err
}

// List returns userID's keys, newest first, with unflushed last-use applied.
func (s *Store) List(ctx context.Context, userID int64) ([]*Key, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+keyCols+` FROM api_keys WHERE user_id = ? ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		s.mergeUsage(k)
		out = append(out, k)
	}
	return out, rows.Err()
}

// Create stores a new key for userID and returns the full key, which is never
// stored and cannot be shown again. The key's scopes must already be checked
// against the owner's role (Validate does).
func (s *Store) Create(ctx context.Context, userID int64, nk NewKey, actor accounts.AuditEntry) (string, *Key, error) {
	if nk.Name == "" || len(nk.Scopes) == 0 {
		return "", nil, errors.New("keys: name and scopes are required")
	}
	scopes := make([]string, len(nk.Scopes))
	for i, sc := range nk.Scopes {
		if !sc.Valid() {
			return "", nil, fmt.Errorf("keys: invalid scope %q", sc)
		}
		scopes[i] = string(sc)
	}
	nets := make([]string, len(nk.CIDRs))
	for i, p := range nk.CIDRs {
		nets[i] = p.Masked().String()
	}
	scopesJSON, _ := json.Marshal(scopes)
	netsJSON, _ := json.Marshal(nets)
	consJSON, _ := json.Marshal(nk.Constraints)
	now := s.now()
	var exp any
	if nk.ExpiresAt != nil {
		exp = db.Millis(*nk.ExpiresAt)
	}
	var (
		token string
		id    int64
	)
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM api_keys WHERE user_id = ? AND revoked_at IS NULL
			AND (expires_at IS NULL OR expires_at > ?)`, userID, db.Millis(now)).Scan(&active); err != nil {
			return err
		}
		if active >= MaxActiveKeys {
			return ErrTooManyKeys
		}
		for attempt := 0; ; attempt++ {
			var pub string
			var hash []byte
			token, pub, hash = newToken()
			res, err := tx.ExecContext(ctx, `INSERT INTO api_keys (user_id, name, public_id, secret_hash, scopes, allowed_cidrs, constraints, expires_at, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, userID, nk.Name, pub, hash, string(scopesJSON), string(netsJSON), string(consJSON), exp, db.Millis(now))
			if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: api_keys.public_id") && attempt < 3 {
				continue
			}
			if err != nil {
				return err
			}
			if id, err = res.LastInsertId(); err != nil {
				return err
			}
			break
		}
		actor.Action, actor.Target = "key.create", fmt.Sprintf("key:%d", id)
		actor.Details = map[string]any{"name": nk.Name, "scopes": scopes}
		return accounts.AuditTx(ctx, tx, actor)
	})
	if err != nil {
		return "", nil, err
	}
	k, err := scanKey(s.db.W.QueryRowContext(ctx, `SELECT `+keyCols+` FROM api_keys WHERE id = ?`, id))
	return token, k, err
}

// Revoke ends one of userID's keys at once. Revoking a revoked key is a no-op.
func (s *Store) Revoke(ctx context.Context, userID, id int64, actor accounts.AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var revoked sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT revoked_at FROM api_keys WHERE id = ? AND user_id = ?`, id, userID).Scan(&revoked)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || revoked.Valid {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET revoked_at = ? WHERE id = ?`, db.Millis(s.now()), id); err != nil {
			return err
		}
		actor.Action, actor.Target = "key.revoke", fmt.Sprintf("key:%d", id)
		return accounts.AuditTx(ctx, tx, actor)
	})
}

// touch records a use of key id in memory.
func (s *Store) touch(id int64, at time.Time, ip string) {
	s.mu.Lock()
	s.pending[id] = usage{at: db.Millis(at), ip: ip}
	s.mu.Unlock()
}

func (s *Store) mergeUsage(k *Key) {
	s.mu.Lock()
	u, ok := s.pending[k.ID]
	s.mu.Unlock()
	if ok && (k.LastUsedAt == nil || db.Millis(*k.LastUsedAt) < u.at) {
		t := db.Time(u.at)
		k.LastUsedAt, k.LastUsedIP = &t, u.ip
	}
}

// Flush writes batched last-use times and addresses in one transaction.
func (s *Store) Flush(ctx context.Context) error {
	s.mu.Lock()
	batch := s.pending
	s.pending = map[int64]usage{}
	s.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `UPDATE api_keys SET last_used_at = ?, last_used_ip = ?
			WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for id, u := range batch {
			var ip any
			if u.ip != "" {
				ip = u.ip
			}
			if _, err := stmt.ExecContext(ctx, u.at, ip, id, u.at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Put the batch back so a transient failure loses nothing.
		s.mu.Lock()
		for id, u := range batch {
			if cur, ok := s.pending[id]; !ok || cur.at < u.at {
				s.pending[id] = u
			}
		}
		s.mu.Unlock()
	}
	return err
}

// pendingCount reports how many keys have an unwritten last use (tests).
func (s *Store) pendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}
