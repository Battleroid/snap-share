package accounts

// Second factors: the policy, the factor summary, recovery codes, the
// rules shared by every factor (first factor ends the other sessions, the
// last one can't go while the policy covers the user) and the admin reset.
// Passkeys (WebAuthn) and authenticator apps (TOTP) add their own files.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/mail"
)

// Values of auth.require_2fa.
const (
	Require2FAOff      = "off"
	Require2FAAdmins   = "admins"
	Require2FAEveryone = "everyone"
)

// policyCovers reports whether auth.require_2fa applies to a user with role.
func (s *Service) policyCovers(role auth.Role) bool {
	switch Require2FA.Get(s.d.Settings) {
	case Require2FAEveryone:
		return true
	case Require2FAAdmins:
		return role == auth.RoleAdmin
	}
	return false
}

// hasMFASQL is true while user ?1 has a passkey or an authenticator app.
// It is evaluated on every request (sessions.lookup), never stored.
const hasMFASQL = `(EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = %s)
	OR EXISTS (SELECT 1 FROM user_totp t WHERE t.user_id = %s))`

// Factors summarises a user's second factors.
type Factors struct {
	Passkeys     int  // WebAuthn credentials (passkeys and security keys)
	TOTP         bool // an authenticator app
	RecoveryLeft int  // unused recovery codes
}

// Any reports whether the user has a second factor (recovery codes alone
// are not one: they only stand in for one).
func (f Factors) Any() bool { return f.Passkeys > 0 || f.TOTP }

// Factors returns uid's second factors.
func (s *Store) Factors(ctx context.Context, uid int64) (Factors, error) {
	return factorsQ(ctx, s.db.R, uid)
}

func factorsQ(ctx context.Context, q queryer, uid int64) (Factors, error) {
	var f Factors
	err := q.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM webauthn_credentials WHERE user_id = ?1),
		EXISTS (SELECT 1 FROM user_totp WHERE user_id = ?1),
		(SELECT count(*) FROM recovery_codes WHERE user_id = ?1 AND used_at IS NULL)`, uid).
		Scan(&f.Passkeys, &f.TOTP, &f.RecoveryLeft)
	return f, err
}

// errLastFactor refuses removing the only second factor of a user the policy covers.
var errLastFactor = &errReauth{status: 409, msg: "Two-factor authentication is required on this site: add another passkey or an authenticator app before removing this one."}

// mayRemoveFactorTx checks, inside the removing transaction, that u keeps a
// factor when the policy covers them. after is the factors once removed.
func (s *Service) mayRemoveFactorTx(ctx context.Context, tx *sql.Tx, u *User, after func(Factors) Factors) error {
	if !s.policyCovers(u.Role) {
		return nil
	}
	f, err := factorsQ(ctx, tx, u.ID)
	if err != nil {
		return err
	}
	if !after(f).Any() {
		return errLastFactor
	}
	return nil
}

// firstFactorTx runs inside the transaction that adds a factor, before the
// factor's row is written. When u had none it ends u's other sessions
// (keepSessionID, hex as in Principal.SessionID, stays) and makes recovery
// codes, which it returns for showing once. Otherwise it returns nil.
func (s *Service) firstFactorTx(ctx context.Context, tx *sql.Tx, uid int64, keepSessionID string) ([]string, error) {
	f, err := factorsQ(ctx, tx, uid)
	if err != nil || f.Any() {
		return nil, err
	}
	if err := s.sessions.revokeOthersTx(ctx, tx, uid, keepSessionID); err != nil {
		return nil, err
	}
	return replaceRecoveryCodesTx(ctx, tx, uid, s.store.now())
}

// Recovery codes: 10 codes of 16 Crockford base32 characters (80 bits),
// shown as xxxx-xxxx-xxxx-xxxx. Stored as SHA-256 of a label, the user id
// and the normalized code; marked used with a conditional UPDATE, so each
// works once even under concurrent requests.
const (
	recoveryCount  = 10
	recoveryChars  = 16
	recoveryLabel  = "snap recovery v1"
	crockfordAlpha = "0123456789abcdefghjkmnpqrstvwxyz"
)

func newRecoveryCode() string {
	b := make([]byte, recoveryChars)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(crockfordAlpha[c&31])
	}
	return sb.String()
}

// normalizeRecovery folds case, drops separators and maps Crockford's
// look-alikes (i, l → 1; o → 0). It returns "" for anything else.
func normalizeRecovery(code string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(code) {
		switch r {
		case '-', ' ':
			continue
		case 'i', 'l':
			r = '1'
		case 'o':
			r = '0'
		}
		if !strings.ContainsRune(crockfordAlpha, r) {
			return ""
		}
		sb.WriteRune(r)
	}
	if sb.Len() != recoveryChars {
		return ""
	}
	return sb.String()
}

func recoveryHash(uid int64, normalized string) []byte {
	h := sha256.New()
	h.Write([]byte(recoveryLabel))
	h.Write(binary.BigEndian.AppendUint64(nil, uint64(uid))) //nolint:gosec // G115: ids are positive
	h.Write([]byte(normalized))
	return h.Sum(nil)
}

// replaceRecoveryCodesTx swaps uid's recovery codes for new ones and returns them.
func replaceRecoveryCodesTx(ctx context.Context, tx *sql.Tx, uid int64, now time.Time) ([]string, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, uid); err != nil {
		return nil, err
	}
	codes := make([]string, 0, recoveryCount)
	for len(codes) < recoveryCount {
		c := newRecoveryCode()
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO recovery_codes (user_id, code_hash, created_at) VALUES (?, ?, ?)`,
			uid, recoveryHash(uid, normalizeRecovery(c)), db.Millis(now))
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			codes = append(codes, c)
		}
	}
	return codes, nil
}

// ReplaceRecoveryCodes makes a new set of recovery codes for uid.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, uid int64, actor AuditEntry) ([]string, error) {
	var codes []string
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if codes, err = replaceRecoveryCodesTx(ctx, tx, uid, s.now()); err != nil {
			return err
		}
		actor.Action, actor.Target = "mfa.recovery_regenerated", userTarget(uid)
		return auditTx(ctx, tx, actor, s.now())
	})
	return codes, err
}

// UseRecoveryCode marks code used for uid. It reports false when the code
// is malformed, unknown or already used.
func (s *Store) UseRecoveryCode(ctx context.Context, uid int64, code string) (bool, error) {
	n := normalizeRecovery(code)
	if n == "" {
		return false, nil
	}
	var used bool
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`,
			db.Millis(s.now()), uid, recoveryHash(uid, n))
		if err != nil {
			return err
		}
		k, err := res.RowsAffected()
		used = k == 1
		return err
	})
	return used, err
}

// ResetMFA removes every second factor of uid (passkeys, authenticator app,
// recovery codes and challenges in progress) and ends their sessions, for
// an owner who lost their factors. actor is the admin or the CLI.
func (s *Store) ResetMFA(ctx context.Context, uid int64, actor AuditEntry) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM webauthn_credentials WHERE user_id = ?`,
			`DELETE FROM user_totp WHERE user_id = ?`,
			`DELETE FROM recovery_codes WHERE user_id = ?`,
			`DELETE FROM auth_challenges WHERE user_id = ?`,
			`DELETE FROM sessions WHERE user_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, uid); err != nil {
				return err
			}
		}
		actor.Action, actor.Target = "user.mfa_reset", userTarget(uid)
		return auditTx(ctx, tx, actor, s.now())
	})
}

// Security notices: fixed-text emails to the account's verified address
// about changes to how it signs in. They go out whenever SMTP is set up,
// whatever the notification settings, and carry no link but the security
// settings page. The in-app inbox is not used: whoever holds a stolen
// session could read and clear it.
//
//nolint:gosec // G101: notice texts, not credentials
const (
	SecurityPasskeyAdded     = "A passkey was added"
	SecurityPasskeyRemoved   = "A passkey was removed"
	SecurityTOTPAdded        = "An authenticator app was set up"
	SecurityTOTPRemoved      = "The authenticator app was removed"
	SecurityRecoveryUsed     = "A recovery code was used"
	SecurityRecoveryReplaced = "New recovery codes were made"
	SecurityMFAReset         = "An admin reset two-factor authentication"
	SecurityPasswordReset    = "The password was reset"
)

// securityNotice emails u about event (one of the Security* texts) in the
// background. detail is a fixed, non-user string such as "7 recovery codes left".
func (s *Service) securityNotice(ctx context.Context, u *User, event, detail string) {
	if u == nil || !u.EmailVerified() || !s.MailEnabled() {
		return
	}
	data := mail.Data{URL: s.d.Cfg.Origin() + "/settings/security", Username: u.Username, Event: event, Detail: detail}
	to := u.Email
	go func() { _ = s.SendMail(context.WithoutCancel(ctx), mail.KindSecurity, to, data) }()
}

// errNoFactor is returned where a second factor was needed but none was given or it was wrong.
var errNoFactor = errors.New("accounts: second factor missing or wrong")
