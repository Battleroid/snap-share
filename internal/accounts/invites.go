package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
)

// Invite is an invite link's record. The token itself is never stored.
type Invite struct {
	ID            int64
	CreatedBy     int64 // 0: admin CLI
	CreatedByName string
	Email         string
	Role          auth.Role
	MaxUses       int
	Uses          int
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
}

// Invite states.
const (
	InviteActive  = "active"
	InviteExpired = "expired"
	InviteRevoked = "revoked"
	InviteUsed    = "used"
)

// State returns the invite's state at now.
func (i *Invite) State(now time.Time) string {
	switch {
	case i.RevokedAt != nil:
		return InviteRevoked
	case i.Uses >= i.MaxUses:
		return InviteUsed
	case !now.Before(i.ExpiresAt):
		return InviteExpired
	}
	return InviteActive
}

// NewInvite describes an invite to create.
type NewInvite struct {
	CreatedBy int64 // 0 for the CLI
	Email     string
	Role      auth.Role
	MaxUses   int
	TTL       time.Duration
}

// Invite limits.
const (
	MaxInviteUses = 1000
	MaxInviteTTL  = 365 * 24 * time.Hour
)

// CreateInvite stores an invite and returns its token.
func (s *Store) CreateInvite(ctx context.Context, ni NewInvite, actor AuditEntry) (string, *Invite, error) {
	if ni.Role == "" {
		ni.Role = auth.RoleUser
	}
	if ni.Role != auth.RoleUser && ni.Role != auth.RoleAdmin {
		return "", nil, fmt.Errorf("unknown role %q", ni.Role)
	}
	if ni.MaxUses < 1 || ni.MaxUses > MaxInviteUses {
		return "", nil, fmt.Errorf("uses must be 1-%d", MaxInviteUses)
	}
	if ni.TTL < time.Minute || ni.TTL > MaxInviteTTL {
		return "", nil, errors.New("expiry must be between a minute and a year")
	}
	if ni.Email != "" {
		var err error
		if ni.Email, err = NormalizeEmail(ni.Email); err != nil {
			return "", nil, err
		}
	}
	raw, hash := newSecret()
	now := s.now()
	var id int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO invites (hash, created_by, email, role, max_uses, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, hash, nullInt(ni.CreatedBy), nullStr(ni.Email), string(ni.Role), ni.MaxUses,
			db.Millis(now), db.Millis(now.Add(ni.TTL)))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		actor.Action, actor.Target = "invite.create", "invite:"+strconv.FormatInt(id, 10)
		actor.Details = mergeDetails(actor.Details, map[string]any{"role": ni.Role, "uses": ni.MaxUses, "email": ni.Email != ""})
		return auditTx(ctx, tx, actor, now)
	})
	if err != nil {
		return "", nil, err
	}
	inv, err := s.inviteWhere(ctx, s.db.R, `i.id = ?`, id)
	return raw, inv, err
}

const inviteCols = `i.id, i.created_by, coalesce(u.username, ''), i.email, i.role, i.max_uses, i.uses, i.created_at, i.expires_at, i.revoked_at`

func scanInvite(row scanner) (*Invite, error) {
	var (
		inv              Invite
		by               sql.NullInt64
		email            sql.NullString
		role             string
		created, expires int64
		revoked          sql.NullInt64
	)
	if err := row.Scan(&inv.ID, &by, &inv.CreatedByName, &email, &role, &inv.MaxUses, &inv.Uses, &created, &expires, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	inv.CreatedBy, inv.Email, inv.Role = by.Int64, email.String, auth.Role(role)
	inv.CreatedAt, inv.ExpiresAt, inv.RevokedAt = db.Time(created), db.Time(expires), db.NullTime(revoked)
	return &inv, nil
}

func (s *Store) inviteWhere(ctx context.Context, q queryer, where string, args ...any) (*Invite, error) {
	return scanInvite(q.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites i LEFT JOIN users u ON u.id = i.created_by WHERE `+where, args...)) // #nosec G202 -- where is a constant from this package
}

// InviteByToken returns the invite for raw if it can still be used.
func (s *Store) InviteByToken(ctx context.Context, raw string) (*Invite, error) {
	if !validSecretShape(raw) {
		return nil, ErrInvalidToken
	}
	inv, err := s.inviteWhere(ctx, s.db.R, `i.hash = ?`, hashSecret(raw))
	if errors.Is(err, ErrNotFound) || (err == nil && inv.State(s.now()) != InviteActive) {
		return nil, ErrInvalidToken
	}
	return inv, err
}

// useInviteTx takes one use of the invite; it fails unless the invite is usable.
func useInviteTx(ctx context.Context, tx *sql.Tx, raw string, now time.Time) (*Invite, error) {
	if !validSecretShape(raw) {
		return nil, ErrInvalidToken
	}
	var id int64
	err := tx.QueryRowContext(ctx, `UPDATE invites SET uses = uses + 1
		WHERE hash = ? AND revoked_at IS NULL AND expires_at > ? AND uses < max_uses RETURNING id`,
		hashSecret(raw), db.Millis(now)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	return scanInvite(tx.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites i LEFT JOIN users u ON u.id = i.created_by WHERE i.id = ?`, id))
}

// ListInvites returns the newest invites first.
func (s *Store) ListInvites(ctx context.Context, limit int) ([]Invite, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+inviteCols+` FROM invites i LEFT JOIN users u ON u.id = i.created_by
		ORDER BY i.created_at DESC, i.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *inv)
	}
	return out, rows.Err()
}

// RevokeInvite stops an invite from working.
func (s *Store) RevokeInvite(ctx context.Context, id int64, actor AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE invites SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, db.Millis(s.now()), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		actor.Action, actor.Target = "invite.revoke", "invite:"+strconv.FormatInt(id, 10)
		return auditTx(ctx, tx, actor, s.now())
	})
}

func mergeDetails(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}
