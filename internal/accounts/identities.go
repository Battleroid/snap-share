package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// Identity is an SSO account linked to a user.
type Identity struct {
	ID            int64
	UserID        int64
	Provider      string // issuer URL
	Subject       string
	Username      string
	Email         string
	EmailVerified bool
	Groups        []string
	CreatedAt     time.Time
	LastLoginAt   *time.Time
}

// ErrIdentityTaken means the SSO account is already linked to another user,
// or the user already has an identity from that provider.
var ErrIdentityTaken = errors.New("identity is already linked")

const identityCols = `id, user_id, provider, subject, coalesce(username, ''), coalesce(email, ''), email_verified, groups, created_at, last_login_at`

func scanIdentity(row scanner) (*Identity, error) {
	var (
		id       Identity
		verified int
		groups   string
		created  int64
		last     sql.NullInt64
	)
	if err := row.Scan(&id.ID, &id.UserID, &id.Provider, &id.Subject, &id.Username, &id.Email, &verified, &groups, &created, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	id.EmailVerified = verified == 1
	_ = json.Unmarshal([]byte(groups), &id.Groups)
	id.CreatedAt, id.LastLoginAt = db.Time(created), db.NullTime(last)
	return &id, nil
}

// IdentityBySubject finds the identity for (provider, subject).
func (s *Store) IdentityBySubject(ctx context.Context, provider, subject string) (*Identity, error) {
	return scanIdentity(s.db.R.QueryRowContext(ctx, `SELECT `+identityCols+` FROM user_identities WHERE provider = ? AND subject = ?`, provider, subject))
}

// Identities lists a user's linked SSO accounts.
func (s *Store) Identities(ctx context.Context, uid int64) ([]Identity, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+identityCols+` FROM user_identities WHERE user_id = ? ORDER BY created_at`, uid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Identity
	for rows.Next() {
		id, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *id)
	}
	return out, rows.Err()
}

// ssoProfile is what the provider told us at sign-in.
type ssoProfile struct {
	Provider      string
	Subject       string
	Username      string
	Email         string
	EmailVerified bool
	Groups        []string
}

func groupsJSON(g []string) string {
	if g == nil {
		g = []string{}
	}
	b, _ := json.Marshal(g)
	return string(b)
}

func linkIdentityTx(ctx context.Context, tx *sql.Tx, uid int64, p ssoProfile, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO user_identities (user_id, provider, subject, username, email, email_verified, groups, created_at, last_login_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, p.Provider, p.Subject, nullStr(p.Username), nullStr(p.Email), boolInt(p.EmailVerified),
		groupsJSON(p.Groups), db.Millis(now), db.Millis(now))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: user_identities") {
		return ErrIdentityTaken
	}
	return err
}

func updateIdentityTx(ctx context.Context, tx *sql.Tx, id int64, p ssoProfile, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE user_identities SET username = ?, email = ?, email_verified = ?, groups = ?, last_login_at = ? WHERE id = ?`,
		nullStr(p.Username), nullStr(p.Email), boolInt(p.EmailVerified), groupsJSON(p.Groups), db.Millis(now), id)
	return err
}

// ErrLastSignIn means removing the identity would leave no way to sign in.
var ErrLastSignIn = errors.New("the account needs another way to sign in first")

// UnlinkIdentity removes identity id from uid. passwordCounts says whether a
// password is a usable way to sign in (password login is on).
func (s *Store) UnlinkIdentity(ctx context.Context, uid, id int64, passwordCounts bool, actor AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var hasPassword bool
		var others int
		if err := tx.QueryRowContext(ctx, `SELECT password_hash IS NOT NULL,
			(SELECT count(*) FROM user_identities WHERE user_id = ? AND id != ?) FROM users WHERE id = ?`, uid, id, uid).Scan(&hasPassword, &others); err != nil {
			return err
		}
		if others == 0 && (!hasPassword || !passwordCounts) {
			return ErrLastSignIn
		}
		var provider, subject string
		err := tx.QueryRowContext(ctx, `DELETE FROM user_identities WHERE id = ? AND user_id = ? RETURNING provider, subject`, id, uid).Scan(&provider, &subject)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		actor.Action, actor.Target = "sso.unlink", userTarget(uid)
		actor.Details = map[string]any{"provider": provider, "subject": subject, "identity": strconv.FormatInt(id, 10)}
		return auditTx(ctx, tx, actor, s.now())
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
