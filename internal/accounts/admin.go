package accounts

// Admin helpers: user listing with limits and usage, role and status changes
// that keep at least one active admin, per-user limits, deletion, and the
// audit log reader.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
)

// ErrLastAdmin means the change would leave the site without an active admin.
var ErrLastAdmin = errors.New("the site needs at least one active admin")

// UserRow is a user with the fields the admin pages show.
type UserRow struct {
	User
	QuotaBytes     *int64 // nil: site default
	MaxUploadBytes *int64 // nil: site default
	UsedBytes      int64
	ReservedBytes  int64
	SSOLinked      bool
}

// UserFilter selects users for ListUsers.
type UserFilter struct {
	Query  string // substring of the username or email; "" for all
	Limit  int
	Offset int
}

const userRowCols = `u.id, u.username, u.email, u.email_verified_at, u.password_hash, u.role, u.status, u.created_at, u.last_login_at,
	u.quota_bytes, u.max_upload_bytes, u.used_bytes, u.reserved_bytes,
	EXISTS (SELECT 1 FROM user_identities x WHERE x.user_id = u.id)`

func scanUserRow(row scanner) (*UserRow, error) {
	var (
		r              UserRow
		email, hash    sql.NullString
		verified, last sql.NullInt64
		quota, maxUp   sql.NullInt64
		created        int64
		role           string
	)
	err := row.Scan(&r.ID, &r.Username, &email, &verified, &hash, &role, &r.Status, &created, &last,
		&quota, &maxUp, &r.UsedBytes, &r.ReservedBytes, &r.SSOLinked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Email, r.PasswordHash, r.Role = email.String, hash.String, auth.Role(role)
	r.EmailVerifiedAt, r.LastLoginAt, r.CreatedAt = db.NullTime(verified), db.NullTime(last), db.Time(created)
	if quota.Valid {
		r.QuotaBytes = &quota.Int64
	}
	if maxUp.Valid {
		r.MaxUploadBytes = &maxUp.Int64
	}
	return &r, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likePattern returns a LIKE ... ESCAPE '\' pattern matching s anywhere.
func likePattern(s string) string { return "%" + likeEscaper.Replace(s) + "%" }

// ListUsers returns a page of users by username, and the number matching.
func (s *Store) ListUsers(ctx context.Context, f UserFilter) ([]UserRow, int, error) {
	where, args := `1`, []any{}
	if q := strings.TrimSpace(f.Query); q != "" {
		where = `(u.username LIKE ? ESCAPE '\' OR u.email LIKE ? ESCAPE '\')`
		args = append(args, likePattern(q), likePattern(q))
	}
	var total int
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM users u WHERE `+where, args...).Scan(&total); err != nil { // #nosec G202 -- where is a constant from this function
		return nil, 0, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+userRowCols+` FROM users u WHERE `+where+ // #nosec G202 -- where is a constant from this function
		` ORDER BY u.username LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []UserRow
	for rows.Next() {
		r, err := scanUserRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *r)
	}
	return out, total, rows.Err()
}

// UserRow returns one user with limits and usage.
func (s *Store) UserRow(ctx context.Context, id int64) (*UserRow, error) {
	return scanUserRow(s.db.R.QueryRowContext(ctx, `SELECT `+userRowCols+` FROM users u WHERE u.id = ?`, id))
}

// UserCounts returns how many accounts are active and disabled.
func (s *Store) UserCounts(ctx context.Context) (active, disabled int, err error) {
	err = s.db.R.QueryRowContext(ctx, `SELECT coalesce(sum(status = 'active'), 0), coalesce(sum(status = 'disabled'), 0) FROM users`).
		Scan(&active, &disabled)
	return active, disabled, err
}

// lastActiveAdminTx reports whether uid is the only active admin.
func lastActiveAdminTx(ctx context.Context, q queryer, uid int64) (bool, error) {
	var role, status string
	err := q.QueryRowContext(ctx, `SELECT role, status FROM users WHERE id = ?`, uid).Scan(&role, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if role != string(auth.RoleAdmin) || status != StatusActive {
		return false, nil
	}
	var others int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND status = 'active' AND id != ?`, uid).Scan(&others); err != nil {
		return false, err
	}
	return others == 0, nil
}

// IsLastActiveAdmin reports whether uid is the only active admin.
func (s *Store) IsLastActiveAdmin(ctx context.Context, uid int64) (bool, error) {
	return lastActiveAdminTx(ctx, s.db.R, uid)
}

// SetRoleChecked is SetRole that refuses (ErrLastAdmin) to demote the last active admin.
func (s *Store) SetRoleChecked(ctx context.Context, uid int64, role auth.Role, actor AuditEntry) error {
	if role != auth.RoleUser && role != auth.RoleAdmin {
		return fmt.Errorf("unknown role %q", role)
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		if role != auth.RoleAdmin {
			last, err := lastActiveAdminTx(ctx, tx, uid)
			if err != nil {
				return err
			}
			if last {
				return ErrLastAdmin
			}
		}
		return setRoleTx(ctx, tx, uid, role, actor, s.now())
	})
}

// SetStatusChecked is SetStatus that refuses (ErrLastAdmin) to disable the
// last active admin. Disabling signs the user out everywhere.
func (s *Store) SetStatusChecked(ctx context.Context, uid int64, status string, actor AuditEntry) error {
	if status != StatusActive && status != StatusDisabled {
		return fmt.Errorf("unknown status %q", status)
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := s.now()
		if status == StatusDisabled {
			last, err := lastActiveAdminTx(ctx, tx, uid)
			if err != nil {
				return err
			}
			if last {
				return ErrLastAdmin
			}
		}
		var old string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM users WHERE id = ?`, uid).Scan(&old); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if old == status {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`, status, db.Millis(now), uid); err != nil {
			return err
		}
		if status == StatusDisabled {
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, uid); err != nil {
				return err
			}
		}
		actor.Action, actor.Target = "user.status", userTarget(uid)
		actor.Details = mergeDetails(actor.Details, map[string]any{"status": status})
		return auditTx(ctx, tx, actor, now)
	})
}

// SetLimits sets a user's storage quota and per-file upload limit in bytes;
// nil means the site default. A quota of 0 means no limit.
func (s *Store) SetLimits(ctx context.Context, uid int64, quota, maxUpload *int64, actor AuditEntry) error {
	if (quota != nil && *quota < 0) || (maxUpload != nil && *maxUpload <= 0) {
		return errors.New("limits must be positive")
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		var oldQ, oldM sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT quota_bytes, max_upload_bytes FROM users WHERE id = ?`, uid).Scan(&oldQ, &oldM); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if sameLimit(oldQ, quota) && sameLimit(oldM, maxUpload) {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET quota_bytes = ?, max_upload_bytes = ?, updated_at = ? WHERE id = ?`,
			optInt(quota), optInt(maxUpload), db.Millis(s.now()), uid); err != nil {
			return err
		}
		actor.Action, actor.Target = "user.limits", userTarget(uid)
		actor.Details = mergeDetails(actor.Details, map[string]any{"quota_bytes": optInt(quota), "max_upload_bytes": optInt(maxUpload)})
		return auditTx(ctx, tx, actor, s.now())
	})
}

func sameLimit(old sql.NullInt64, v *int64) bool {
	if v == nil {
		return !old.Valid
	}
	return old.Valid && old.Int64 == *v
}

func optInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// DeleteUser deletes an account and, by cascade, its sessions, identities,
// tokens, invites, keys and any rows left in items, uploads and secrets.
// Delete the user's items and secrets first: their files are not removed
// here. It refuses (ErrLastAdmin) to delete the last active admin.
func (s *Store) DeleteUser(ctx context.Context, uid int64, actor AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		last, err := lastActiveAdminTx(ctx, tx, uid)
		if err != nil {
			return err
		}
		if last {
			return ErrLastAdmin
		}
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id = ?`, uid).Scan(&name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, uid); err != nil {
			return err
		}
		actor.Action, actor.Target = "user.delete", userTarget(uid)
		actor.Details = mergeDetails(actor.Details, map[string]any{"username": name})
		return auditTx(ctx, tx, actor, s.now())
	})
}

// AuditRecord is one audit_log row with the actor's username.
type AuditRecord struct {
	ID          int64
	At          time.Time
	ActorUserID int64
	ActorKeyID  int64
	ActorName   string // "" when there is no actor or the user is gone
	IP          string
	Action      string
	Target      string
	Details     string // JSON object
}

// AuditFilter selects audit entries. Zero fields match everything.
type AuditFilter struct {
	Action string // exact action, or a prefix ending in "." (e.g. "user.")
	Actor  string // actor username
	From   time.Time
	To     time.Time // exclusive
	Limit  int
	Offset int
}

// ListAudit returns audit entries, newest first, and the number matching.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditRecord, int, error) {
	var conds []string
	var args []any
	if a := strings.TrimSpace(f.Action); a != "" {
		if strings.HasSuffix(a, ".") {
			conds, args = append(conds, `a.action LIKE ? ESCAPE '\'`), append(args, likeEscaper.Replace(a)+"%")
		} else {
			conds, args = append(conds, `a.action = ?`), append(args, a)
		}
	}
	if n := strings.TrimSpace(f.Actor); n != "" {
		conds, args = append(conds, `u.username = ?`), append(args, n)
	}
	if !f.From.IsZero() {
		conds, args = append(conds, `a.at >= ?`), append(args, db.Millis(f.From))
	}
	if !f.To.IsZero() {
		conds, args = append(conds, `a.at < ?`), append(args, db.Millis(f.To))
	}
	where := "1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	from := ` FROM audit_log a LEFT JOIN users u ON u.id = a.actor_user_id WHERE ` + where
	var total int
	if err := s.db.R.QueryRowContext(ctx, `SELECT count(*)`+from, args...).Scan(&total); err != nil { // #nosec G202 -- conditions are constants from this function
		return nil, 0, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT a.id, a.at, a.actor_user_id, a.actor_key_id, coalesce(u.username, ''), a.ip, a.action, a.target, a.details`+ // #nosec G202 -- conditions are constants from this function
		from+` ORDER BY a.at DESC, a.id DESC LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []AuditRecord
	for rows.Next() {
		var (
			e          AuditRecord
			at         int64
			uid, kid   sql.NullInt64
			ip, target sql.NullString
		)
		if err := rows.Scan(&e.ID, &at, &uid, &kid, &e.ActorName, &ip, &e.Action, &target, &e.Details); err != nil {
			return nil, 0, err
		}
		e.At, e.ActorUserID, e.ActorKeyID, e.IP, e.Target = db.Time(at), uid.Int64, kid.Int64, ip.String, target.String
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// AuditActions lists the distinct actions in the audit log.
func (s *Store) AuditActions(ctx context.Context) ([]string, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT DISTINCT action FROM audit_log ORDER BY action`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
