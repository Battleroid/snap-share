package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
)

// Store errors callers branch on.
var (
	ErrNotFound      = errors.New("not found")
	ErrUsernameTaken = errors.New("username is taken")
	ErrEmailTaken    = errors.New("email address is in use")
	ErrInvalidToken  = errors.New("link is invalid or has expired")
	ErrBadUsername   = errors.New("username must be 1-32 letters, digits, '.', '_' or '-', starting with a letter or digit")
	ErrBadEmail      = errors.New("email address is not valid")
)

// User statuses.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// User is an account.
type User struct {
	ID              int64
	Username        string
	Email           string // "" when the account has none
	EmailVerifiedAt *time.Time
	PasswordHash    string // "" for SSO-only accounts
	Role            auth.Role
	Status          string
	CreatedAt       time.Time
	LastLoginAt     *time.Time
}

// EmailVerified reports whether the account is cleared to create content: its
// email is verified, or it has no email (admin-created, invited and SSO
// accounts never hold an unverified address; only self-registration does).
func (u *User) EmailVerified() bool { return u.Email == "" || u.EmailVerifiedAt != nil }

// HasPassword reports whether the account can sign in with a password.
func (u *User) HasPassword() bool { return u.PasswordHash != "" }

// Active reports whether the account may sign in.
func (u *User) Active() bool { return u.Status == StatusActive }

// Principal returns the session principal for u (SessionID unset).
func (u *User) Principal() *auth.Principal {
	return &auth.Principal{UserID: u.ID, Username: u.Username, Role: u.Role, EmailVerified: u.EmailVerified()}
}

// Store reads and writes accounts. Every write is one db.Tx.
type Store struct {
	db  *db.DB
	now func() time.Time
}

// NewStore returns a store on d.
func NewStore(d *db.DB) *Store { return &Store{db: d, now: time.Now} }

// DB returns the underlying database (for callers composing their own transactions).
func (s *Store) DB() *db.DB { return s.db }

const userCols = `id, username, email, email_verified_at, password_hash, role, status, created_at, last_login_at`

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (*User, error) {
	var (
		u              User
		email, hash    sql.NullString
		verified, last sql.NullInt64
		created        int64
		role           string
	)
	if err := row.Scan(&u.ID, &u.Username, &email, &verified, &hash, &role, &u.Status, &created, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Email, u.PasswordHash, u.Role = email.String, hash.String, auth.Role(role)
	u.EmailVerifiedAt, u.LastLoginAt, u.CreatedAt = db.NullTime(verified), db.NullTime(last), db.Time(created)
	return &u, nil
}

func (s *Store) userWhere(ctx context.Context, where string, args ...any) (*User, error) {
	return scanUser(s.db.R.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE `+where, args...)) // #nosec G202 -- where is a constant from this package
}

// User returns the user with id.
func (s *Store) User(ctx context.Context, id int64) (*User, error) {
	return s.userWhere(ctx, `id = ?`, id)
}

// UserByUsername finds a user by name (case-insensitive).
func (s *Store) UserByUsername(ctx context.Context, name string) (*User, error) {
	return s.userWhere(ctx, `username = ?`, name)
}

// UserByEmail finds a user by email (case-insensitive).
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	if email == "" {
		return nil, ErrNotFound
	}
	return s.userWhere(ctx, `email = ?`, email)
}

// UserByLogin finds a user by username, or by email when login contains '@'.
func (s *Store) UserByLogin(ctx context.Context, login string) (*User, error) {
	if strings.Contains(login, "@") {
		return s.UserByEmail(ctx, login)
	}
	return s.UserByUsername(ctx, login)
}

// PrincipalFor returns the principal for an active user, or ErrNotFound when
// the user does not exist or is disabled. Other authenticators (API keys) use
// it so disabled accounts lose access everywhere.
func (s *Store) PrincipalFor(ctx context.Context, id int64) (*auth.Principal, error) {
	u, err := s.User(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.Active() {
		return nil, ErrNotFound
	}
	return u.Principal(), nil
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)

// ValidUsername checks the username rules.
func ValidUsername(name string) error {
	if !usernameRE.MatchString(name) {
		return ErrBadUsername
	}
	return nil
}

// NormalizeEmail trims and checks an address. It accepts the common
// local@domain shape only; delivery is what proves it.
func NormalizeEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || len(email) > 254 || strings.ContainsAny(email, " \t\r\n<>,;\"") ||
		strings.Contains(domain, "@") || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrBadEmail
	}
	return email, nil
}

// NewUser describes an account to create.
type NewUser struct {
	Username      string
	Email         string // optional
	EmailVerified bool
	PasswordHash  string // optional (SSO-only)
	Role          auth.Role
}

func mapConstraint(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed: users.username"):
		return ErrUsernameTaken
	case strings.Contains(msg, "UNIQUE constraint failed: users.email"):
		return ErrEmailTaken
	}
	return err
}

func createUserTx(ctx context.Context, tx *sql.Tx, nu NewUser, now time.Time) (int64, error) {
	if err := ValidUsername(nu.Username); err != nil {
		return 0, err
	}
	if nu.Role == "" {
		nu.Role = auth.RoleUser
	}
	var email, hash, verified any
	if nu.Email != "" {
		email = nu.Email
		if nu.EmailVerified {
			verified = db.Millis(now)
		}
	}
	if nu.PasswordHash != "" {
		hash = nu.PasswordHash
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users (username, email, email_verified_at, password_hash, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, nu.Username, email, verified, hash, string(nu.Role), db.Millis(now), db.Millis(now))
	if err != nil {
		return 0, mapConstraint(err)
	}
	return res.LastInsertId()
}

// CreateUser creates an account.
func (s *Store) CreateUser(ctx context.Context, nu NewUser, actor AuditEntry) (*User, error) {
	var id int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if id, err = createUserTx(ctx, tx, nu, s.now()); err != nil {
			return err
		}
		actor.Action, actor.Target = "user.create", userTarget(id)
		actor.Details = map[string]any{"username": nu.Username, "role": nu.Role}
		return auditTx(ctx, tx, actor, s.now())
	})
	if err != nil {
		return nil, err
	}
	return s.User(ctx, id)
}

// SetPassword replaces the password hash and signs out every session except
// keep (the hash of the current session; nil signs out all).
func (s *Store) SetPassword(ctx context.Context, uid int64, hash string, keep []byte, actor AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := setPasswordTx(ctx, tx, uid, hash, keep, s.now()); err != nil {
			return err
		}
		actor.Action, actor.Target = "user.password_change", userTarget(uid)
		return auditTx(ctx, tx, actor, s.now())
	})
}

func setPasswordTx(ctx context.Context, tx *sql.Tx, uid int64, hash string, keep []byte, now time.Time) error {
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, db.Millis(now), uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if keep == nil {
		keep = []byte{}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id != ?`, uid, keep); err != nil {
		return err
	}
	// Outstanding reset links stop working once the password changes.
	_, err = tx.ExecContext(ctx, `DELETE FROM tokens WHERE user_id = ? AND kind = 'reset' AND used_at IS NULL`, uid)
	return err
}

// Rehash replaces the hash without touching sessions (parameter upgrades on login).
func (s *Store) Rehash(ctx context.Context, uid int64, hash string) error {
	_, err := s.db.W.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND password_hash IS NOT NULL`, hash, uid)
	return err
}

// SetRole changes a user's role and audits it when it changed.
func (s *Store) SetRole(ctx context.Context, uid int64, role auth.Role, actor AuditEntry) error {
	if role != auth.RoleUser && role != auth.RoleAdmin {
		return fmt.Errorf("unknown role %q", role)
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error { return setRoleTx(ctx, tx, uid, role, actor, s.now()) })
}

func setRoleTx(ctx context.Context, tx *sql.Tx, uid int64, role auth.Role, actor AuditEntry, now time.Time) error {
	var old string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, uid).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if old == string(role) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`, string(role), db.Millis(now), uid); err != nil {
		return err
	}
	actor.Action, actor.Target = "user.role_change", userTarget(uid)
	actor.Details = mergeDetails(actor.Details, map[string]any{"from": old, "to": string(role)})
	return auditTx(ctx, tx, actor, now)
}

// SetStatus enables or disables an account. Disabling signs it out everywhere.
func (s *Store) SetStatus(ctx context.Context, uid int64, status string, actor AuditEntry) error {
	if status != StatusActive && status != StatusDisabled {
		return fmt.Errorf("unknown status %q", status)
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, db.Millis(s.now()), uid)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if status == StatusDisabled {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, uid); err != nil {
				return err
			}
		}
		actor.Action, actor.Target = "user.status", userTarget(uid)
		actor.Details = map[string]any{"status": status}
		return auditTx(ctx, tx, actor, s.now())
	})
}

// SetUsername renames a user.
func (s *Store) SetUsername(ctx context.Context, uid int64, name string) error {
	if err := ValidUsername(name); err != nil {
		return err
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET username = ?, updated_at = ? WHERE id = ?`, name, db.Millis(s.now()), uid)
		return mapConstraint(err)
	})
}

// replaceUnverifiedEmail swaps an unverified address for another unverified one.
func (s *Store) replaceUnverifiedEmail(ctx context.Context, uid int64, email string) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET email = ?, email_verified_at = NULL, updated_at = ?
			WHERE id = ? AND email_verified_at IS NULL AND email IS NOT NULL`, email, db.Millis(s.now()), uid)
		if err != nil {
			return mapConstraint(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM tokens WHERE user_id = ? AND kind = 'verify' AND used_at IS NULL`, uid)
		return err
	})
}

func (s *Store) touchLogin(ctx context.Context, tx *sql.Tx, uid int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, db.Millis(s.now()), uid)
	return err
}

// Secrets: 256-bit random values, URL-safe base64; only their SHA-256 is stored.

func newSecret() (raw string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashSecret(raw)
}

func hashSecret(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

// validSecretShape rejects obviously malformed tokens before any lookup.
func validSecretShape(raw string) bool {
	if len(raw) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(raw)
	return err == nil
}

// Token kinds.
const (
	TokenVerify      = "verify"
	TokenReset       = "reset"
	TokenEmailChange = "email_change"
)

// Token lifetimes.
const (
	VerifyTTL     = 48 * time.Hour
	ResetTTL      = time.Hour
	AdminResetTTL = 24 * time.Hour // links printed by the admin CLI
)

// Token is a stored emailed token.
type Token struct {
	Kind      string
	UserID    int64
	Email     string // email_change and verify: the address it confirms
	ExpiresAt time.Time
}

type tokenPayload struct {
	Email string `json:"email,omitempty"`
}

// CreateToken issues a token of kind for uid, replacing unused ones of the same kind.
func (s *Store) CreateToken(ctx context.Context, kind string, uid int64, email string, ttl time.Duration) (string, error) {
	var raw string
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		raw, err = createTokenTx(ctx, tx, kind, uid, email, ttl, s.now())
		return err
	})
	return raw, err
}

func createTokenTx(ctx context.Context, tx *sql.Tx, kind string, uid int64, email string, ttl time.Duration, now time.Time) (string, error) {
	raw, hash := newSecret()
	payload, _ := json.Marshal(tokenPayload{Email: email})
	if _, err := tx.ExecContext(ctx, `DELETE FROM tokens WHERE user_id = ? AND kind = ? AND used_at IS NULL`, uid, kind); err != nil {
		return "", err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO tokens (hash, kind, user_id, payload, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		hash, kind, uid, string(payload), db.Millis(now), db.Millis(now.Add(ttl)))
	return raw, err
}

// PeekToken returns a valid, unused token of one of kinds without consuming it.
func (s *Store) PeekToken(ctx context.Context, raw string, kinds ...string) (*Token, error) {
	return peekToken(ctx, s.db.R, raw, s.now(), kinds...)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func peekToken(ctx context.Context, q queryer, raw string, now time.Time, kinds ...string) (*Token, error) {
	if !validSecretShape(raw) {
		return nil, ErrInvalidToken
	}
	var (
		t       Token
		payload string
		exp     int64
	)
	err := q.QueryRowContext(ctx, `SELECT kind, user_id, payload, expires_at FROM tokens
		WHERE hash = ? AND used_at IS NULL AND expires_at > ?`, hashSecret(raw), db.Millis(now)).Scan(&t.Kind, &t.UserID, &payload, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if !slices.Contains(kinds, t.Kind) {
		return nil, ErrInvalidToken
	}
	var p tokenPayload
	_ = json.Unmarshal([]byte(payload), &p)
	t.Email, t.ExpiresAt = p.Email, db.Time(exp)
	return &t, nil
}

// consumeTokenTx marks a token used; it fails unless the token is valid and unused.
func consumeTokenTx(ctx context.Context, tx *sql.Tx, raw string, now time.Time, kinds ...string) (*Token, error) {
	t, err := peekToken(ctx, tx, raw, now, kinds...)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE tokens SET used_at = ? WHERE hash = ? AND used_at IS NULL`, db.Millis(now), hashSecret(raw))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrInvalidToken
	}
	return t, nil
}

// PendingEmail returns the address of an outstanding email change, if any.
func (s *Store) PendingEmail(ctx context.Context, uid int64) string {
	var payload string
	err := s.db.R.QueryRowContext(ctx, `SELECT payload FROM tokens WHERE user_id = ? AND kind = 'email_change'
		AND used_at IS NULL AND expires_at > ? ORDER BY created_at DESC LIMIT 1`, uid, db.Millis(s.now())).Scan(&payload)
	if err != nil {
		return ""
	}
	var p tokenPayload
	_ = json.Unmarshal([]byte(payload), &p)
	return p.Email
}

// ConfirmEmail consumes a verify or email_change token and marks the address verified.
func (s *Store) ConfirmEmail(ctx context.Context, raw string) (*User, error) {
	var uid int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		t, err := consumeTokenTx(ctx, tx, raw, now, TokenVerify, TokenEmailChange)
		if err != nil {
			return err
		}
		uid = t.UserID
		switch t.Kind {
		case TokenVerify:
			// Only the address the token was issued for; a changed address needs a new link.
			res, err := tx.ExecContext(ctx, `UPDATE users SET email_verified_at = ?, updated_at = ? WHERE id = ? AND email = ?`,
				db.Millis(now), db.Millis(now), uid, t.Email)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrInvalidToken
			}
		case TokenEmailChange:
			if _, err := tx.ExecContext(ctx, `UPDATE users SET email = ?, email_verified_at = ?, updated_at = ? WHERE id = ?`,
				t.Email, db.Millis(now), db.Millis(now), uid); err != nil {
				return mapConstraint(err)
			}
			if err := auditTx(ctx, tx, AuditEntry{ActorUserID: uid, Action: "user.email_change", Target: userTarget(uid)}, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.User(ctx, uid)
}

// ResetPassword consumes a reset token and sets the new hash, signing out every session.
func (s *Store) ResetPassword(ctx context.Context, raw, hash string, ip string) (*User, error) {
	var uid int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		t, err := consumeTokenTx(ctx, tx, raw, now, TokenReset)
		if err != nil {
			return err
		}
		uid = t.UserID
		if err := setPasswordTx(ctx, tx, uid, hash, nil, now); err != nil {
			return err
		}
		if t.Email != "" {
			// The link reached this address, which proves it.
			if _, err := tx.ExecContext(ctx, `UPDATE users SET email_verified_at = coalesce(email_verified_at, ?) WHERE id = ? AND email = ?`,
				db.Millis(now), uid, t.Email); err != nil {
				return err
			}
		}
		return auditTx(ctx, tx, AuditEntry{ActorUserID: uid, IP: ip, Action: "user.password_reset", Target: userTarget(uid)}, now)
	})
	if err != nil {
		return nil, err
	}
	return s.User(ctx, uid)
}

func userTarget(id int64) string { return fmt.Sprintf("user:%d", id) }
