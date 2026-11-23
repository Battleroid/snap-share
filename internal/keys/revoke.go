package keys

import (
	"context"
	"database/sql"
	"fmt"
	"maps"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
)

// RevokeAllForUser revokes every active key of userID inside tx and audits
// it as key.revoke_all with the reason (e.g. "password_reset"). The accounts
// package calls it (through accounts.KeyRevoker) when an account's owner may
// have lost control of it. It returns how many keys it revoked.
func (s *Store) RevokeAllForUser(ctx context.Context, tx *sql.Tx, userID int64, reason string, actor accounts.AuditEntry) (int, error) {
	return s.revokeForUser(ctx, tx, userID, "", reason, actor)
}

// RevokeAdminForUser revokes userID's active keys that hold the admin scope
// (after a demotion), audited like RevokeAllForUser.
func (s *Store) RevokeAdminForUser(ctx context.Context, tx *sql.Tx, userID int64, reason string, actor accounts.AuditEntry) (int, error) {
	return s.revokeForUser(ctx, tx, userID, auth.ScopeAdmin, reason, actor)
}

func (s *Store) revokeForUser(ctx context.Context, tx *sql.Tx, userID int64, scope auth.Scope, reason string, actor accounts.AuditEntry) (int, error) {
	q := `UPDATE api_keys SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`
	args := []any{db.Millis(s.now()), userID}
	if scope != "" {
		q += ` AND EXISTS (SELECT 1 FROM json_each(api_keys.scopes) WHERE value = ?)`
		args = append(args, string(scope))
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("revoke keys: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, nil
	}
	details := map[string]any{}
	maps.Copy(details, actor.Details)
	details["reason"], details["keys"] = reason, n
	if scope != "" {
		details["scope"] = string(scope)
	}
	actor.Action, actor.Target, actor.Details = "key.revoke_all", fmt.Sprintf("user:%d", userID), details
	return int(n), accounts.AuditTx(ctx, tx, actor)
}
