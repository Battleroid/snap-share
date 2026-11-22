package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/db"
)

// AuditEntry is one audit_log row. Action and Target are set by the store
// method that records it when it knows them better than the caller.
type AuditEntry struct {
	ActorUserID int64 // 0: none (CLI, SSO mapping, anonymous)
	ActorKeyID  int64
	IP          string
	Action      string
	Target      string
	Details     map[string]any
}

// Actor returns an entry for the request's principal and client address.
func Actor(r *http.Request) AuditEntry {
	e := AuditEntry{}
	if ip := clientip.From(r.Context()); ip.IsValid() {
		e.IP = ip.String()
	}
	if p := auth.FromContext(r.Context()); p != nil {
		e.ActorUserID, e.ActorKeyID = p.UserID, p.KeyID
	}
	return e
}

// CLIActor is the actor for admin CLI changes.
func CLIActor() AuditEntry { return AuditEntry{Details: map[string]any{"via": "cli"}} }

// Audit writes one entry in its own transaction.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error { return auditTx(ctx, tx, e, s.now()) })
}

// AuditTx writes one entry inside the caller's transaction.
func AuditTx(ctx context.Context, tx *sql.Tx, e AuditEntry) error {
	return auditTx(ctx, tx, e, time.Now())
}

func auditTx(ctx context.Context, tx *sql.Tx, e AuditEntry, now time.Time) error {
	details := "{}"
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return err
		}
		details = string(b)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (at, actor_user_id, actor_key_id, ip, action, target, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, db.Millis(now), nullInt(e.ActorUserID), nullInt(e.ActorKeyID), nullStr(e.IP),
		e.Action, nullStr(e.Target), details)
	return err
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}
