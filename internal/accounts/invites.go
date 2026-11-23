package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
)

// Invite is an invite link's record. The token is stored as its SHA-256
// for checking, and sealed (token_ct) so admins can copy the link again
// while the invite is active; the sealed copy is never loaded here.
type Invite struct {
	ID            int64
	CreatedBy     int64 // 0: admin CLI
	CreatedByName string
	Label         string
	Email         string
	Role          auth.Role
	MaxUses       int
	Uses          int
	CreatedAt     time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
	HasLink       bool       // the sealed link is stored
	MailSentAt    *time.Time // the invite email went out
	MailError     string     // or why it did not
	CreatorAdmin  bool       // the creator is still an active admin (or it came from the CLI)
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

// Finished reports whether the invite can never work again (used up,
// expired or revoked).
func (i *Invite) Finished(now time.Time) bool { return i.State(now) != InviteActive }

// Invite statuses shown in lists (Status), besides the finished states.
const (
	StatusLink       = "link"        // active, nothing emailed
	StatusSent       = "sent"        // active, the email went out
	StatusSendFailed = "send failed" // active, the email could not be sent
	StatusPartlyUsed = "partly used" // active, some uses taken
	StatusUnusable   = "unusable"    // its creator is no longer an admin
)

// Status is the invite's state for lists: a finished state, or for an
// active invite what has happened to it so far.
func (i *Invite) Status(now time.Time) string {
	switch st := i.State(now); {
	case st != InviteActive:
		return st
	case !i.CreatorAdmin:
		return StatusUnusable
	case i.Uses > 0:
		return StatusPartlyUsed
	case i.MailError != "":
		return StatusSendFailed
	case i.MailSentAt != nil:
		return StatusSent
	}
	return StatusLink
}

// NewInvite describes an invite to create.
type NewInvite struct {
	CreatedBy int64 // 0 for the CLI
	Label     string
	Email     string
	Role      auth.Role
	MaxUses   int
	TTL       time.Duration
}

// Invite limits.
const (
	MaxInviteUses  = 1000
	MaxInviteTTL   = 365 * 24 * time.Hour
	MaxInviteLabel = 80 // characters
)

// Invite errors.
var (
	ErrBadInviteLabel = fmt.Errorf("label must be at most %d characters on one line", MaxInviteLabel)
	ErrInviteNoLink   = errors.New("invite link is not stored")
	ErrInviteActive   = errors.New("invite is still active")
)

// inviteSealLabel is the auth.Sealer label for stored invite tokens. The
// invite id is bound in, so a sealed token copied to another row does not open.
const inviteSealLabel = "snap invite token v1" //nolint:gosec // G101: an HKDF label, not a credential

// NormalizeInviteLabel trims a label and checks its length and characters.
func NormalizeInviteLabel(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxInviteLabel || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", ErrBadInviteLabel
	}
	return s, nil
}

// CreateInvite stores an invite and returns its token. With a sealer set
// (Service does), the token is also kept sealed for copying the link again.
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
	var err error
	if ni.Email != "" {
		if ni.Email, err = NormalizeEmail(ni.Email); err != nil {
			return "", nil, err
		}
	}
	if ni.Label, err = NormalizeInviteLabel(ni.Label); err != nil {
		return "", nil, err
	}
	raw, hash := newSecret()
	now := s.now()
	var id int64
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO invites (hash, created_by, label, email, role, max_uses, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, hash, nullInt(ni.CreatedBy), nullStr(ni.Label), nullStr(ni.Email), string(ni.Role), ni.MaxUses,
			db.Millis(now), db.Millis(now.Add(ni.TTL)))
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		// Sealed under the row's own id, so the row comes first.
		if s.inviteSeal != nil {
			ct, err := s.inviteSeal.Seal(id, []byte(raw))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE invites SET token_ct = ? WHERE id = ?`, ct, id); err != nil {
				return err
			}
		}
		actor.Action, actor.Target = "invite.create", "invite:"+strconv.FormatInt(id, 10)
		actor.Details = mergeDetails(actor.Details, map[string]any{"role": ni.Role, "uses": ni.MaxUses, "email": ni.Email != "", "label": ni.Label != ""})
		return auditTx(ctx, tx, actor, now)
	})
	if err != nil {
		return "", nil, err
	}
	inv, err := s.inviteWhere(ctx, s.db.R, `i.id = ?`, id)
	return raw, inv, err
}

// inviteCols never includes token_ct itself, only whether it is set.
const inviteCols = `i.id, i.created_by, coalesce(u.username, ''), i.label, i.email, i.role, i.max_uses, i.uses, i.created_at, i.expires_at, i.revoked_at,
	i.token_ct IS NOT NULL, i.mail_sent_at, i.mail_error, ` + creatorStillAdmin

func scanInvite(row scanner) (*Invite, error) {
	var (
		inv                   Invite
		by                    sql.NullInt64
		label, email, mailErr sql.NullString
		role                  string
		created, expires      int64
		revoked, mailSent     sql.NullInt64
	)
	if err := row.Scan(&inv.ID, &by, &inv.CreatedByName, &label, &email, &role, &inv.MaxUses, &inv.Uses, &created, &expires, &revoked,
		&inv.HasLink, &mailSent, &mailErr, &inv.CreatorAdmin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	inv.CreatedBy, inv.Label, inv.Email, inv.Role = by.Int64, label.String, email.String, auth.Role(role)
	inv.CreatedAt, inv.ExpiresAt, inv.RevokedAt = db.Time(created), db.Time(expires), db.NullTime(revoked)
	inv.MailSentAt, inv.MailError = db.NullTime(mailSent), mailErr.String
	return &inv, nil
}

func (s *Store) inviteWhere(ctx context.Context, q queryer, where string, args ...any) (*Invite, error) {
	return scanInvite(q.QueryRowContext(ctx, `SELECT `+inviteCols+` FROM invites i LEFT JOIN users u ON u.id = i.created_by WHERE `+where, args...)) // #nosec G202 -- where is a constant from this package
}

// creatorStillAdmin limits invites to those whose creator is still an
// active admin (or that came from the CLI): an admin who is disabled or
// demoted leaves no working way back in behind.
const creatorStillAdmin = `(i.created_by IS NULL OR EXISTS (SELECT 1 FROM users c
	WHERE c.id = i.created_by AND c.status = 'active' AND c.role = 'admin'))`

// The use that takes the last one also drops the sealed link (the
// right-hand sides read the row as it was).
const useInviteSQL = `UPDATE invites AS i SET uses = uses + 1, token_ct = CASE WHEN uses + 1 >= max_uses THEN NULL ELSE token_ct END
	WHERE hash = ? AND revoked_at IS NULL AND expires_at > ? AND uses < max_uses AND ` + creatorStillAdmin + ` RETURNING id`

// InviteByToken returns the invite for raw if it can still be used.
func (s *Store) InviteByToken(ctx context.Context, raw string) (*Invite, error) {
	if !validSecretShape(raw) {
		return nil, ErrInvalidToken
	}
	inv, err := s.inviteWhere(ctx, s.db.R, `i.hash = ? AND `+creatorStillAdmin, hashSecret(raw))
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
	err := tx.QueryRowContext(ctx, useInviteSQL, hashSecret(raw), db.Millis(now)).Scan(&id)
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
	return s.listInvites(ctx, `1`, limit)
}

// ListActiveInvites returns the newest invites that are not finished,
// including those whose creator is no longer an admin.
func (s *Store) ListActiveInvites(ctx context.Context, limit int) ([]Invite, error) {
	return s.listInvites(ctx, `NOT `+finishedSQL, limit, db.Millis(s.now()))
}

func (s *Store) listInvites(ctx context.Context, where string, limit int, args ...any) ([]Invite, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+inviteCols+` FROM invites i LEFT JOIN users u ON u.id = i.created_by
		WHERE `+where+` ORDER BY i.created_at DESC, i.id DESC LIMIT ?`, append(args, limit)...) // #nosec G202 -- where is a constant from this package
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

// Invite returns one invite.
func (s *Store) Invite(ctx context.Context, id int64) (*Invite, error) {
	return s.inviteWhere(ctx, s.db.R, `i.id = ?`, id)
}

// RevokeInvite stops an invite from working and drops its sealed link.
func (s *Store) RevokeInvite(ctx context.Context, id int64, actor AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE invites SET revoked_at = ?, token_ct = NULL WHERE id = ? AND revoked_at IS NULL`, db.Millis(s.now()), id)
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

// finishedSQL matches invites that can never work again (one argument: now).
const finishedSQL = `(i.revoked_at IS NOT NULL OR i.uses >= i.max_uses OR i.expires_at <= ?)`

// RemoveInvite deletes a finished invite. An active one gives
// ErrInviteActive: revoke it first.
func (s *Store) RemoveInvite(ctx context.Context, id int64, actor AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		res, err := tx.ExecContext(ctx, `DELETE FROM invites AS i WHERE i.id = ? AND `+finishedSQL, id, db.Millis(now))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var one int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM invites WHERE id = ?`, id).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			return ErrInviteActive
		}
		actor.Action, actor.Target = "invite.remove", "invite:"+strconv.FormatInt(id, 10)
		return auditTx(ctx, tx, actor, now)
	})
}

// InviteToken unseals an active invite's token for copying its link again,
// and audits it (invite.link). It gives ErrNotFound unless the invite is
// active and usable, ErrInviteNoLink when no link is stored (an invite from
// before links were kept), and auth.ErrUnsealable when the secret key
// changed without SNAP_SECRET_KEY_PREVIOUS.
func (s *Store) InviteToken(ctx context.Context, id int64, actor AuditEntry) (string, error) {
	if s.inviteSeal == nil {
		return "", ErrInviteNoLink
	}
	var raw string
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		var ct []byte
		err := tx.QueryRowContext(ctx, `SELECT token_ct FROM invites i WHERE i.id = ? AND NOT `+finishedSQL+` AND `+creatorStillAdmin, // #nosec G202 -- constants
			id, db.Millis(now)).Scan(&ct)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if ct == nil {
			return ErrInviteNoLink
		}
		pt, current, err := s.inviteSeal.Open(id, ct)
		if err != nil {
			return err
		}
		if !current { // opened with SNAP_SECRET_KEY_PREVIOUS: seal it again under the current key
			if ct, err = s.inviteSeal.Seal(id, pt); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE invites SET token_ct = ? WHERE id = ?`, ct, id); err != nil {
				return err
			}
		}
		raw = string(pt)
		actor.Action, actor.Target = "invite.link", "invite:"+strconv.FormatInt(id, 10)
		return auditTx(ctx, tx, actor, now)
	})
	return raw, err
}

// SweepInviteLinks drops the sealed link of every finished invite. The link
// is refused for those anyway; this keeps it from staying at rest.
func (s *Store) SweepInviteLinks(ctx context.Context) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE invites AS i SET token_ct = NULL WHERE i.token_ct IS NOT NULL AND `+finishedSQL, db.Millis(s.now()))
		return err
	})
}

// maxMailError bounds the stored reason an invite email failed.
const maxMailError = 200

// RecordInviteMail stores whether the invite email went out (sendErr nil)
// or why it did not.
func (s *Store) RecordInviteMail(ctx context.Context, id int64, sendErr error) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if sendErr == nil {
			_, err := tx.ExecContext(ctx, `UPDATE invites SET mail_sent_at = ?, mail_error = NULL WHERE id = ?`, db.Millis(s.now()), id)
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE invites SET mail_sent_at = NULL, mail_error = ? WHERE id = ?`, mailErrorText(sendErr), id)
		return err
	})
}

func mailErrorText(err error) string {
	msg := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(err.Error()))
	if msg == "" {
		return "send failed"
	}
	if utf8.RuneCountInString(msg) > maxMailError {
		msg = string([]rune(msg)[:maxMailError-1]) + "…"
	}
	return msg
}

func mergeDetails(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}
