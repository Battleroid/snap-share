package accounts

// Challenges: short-lived server-side state between the steps of a sign-in
// or a WebAuthn ceremony (auth_challenges). The browser holds a random
// 256-bit token in a cookie per kind (__Host-snap_auth_<kind>, SameSite
// Strict, 5 minutes); the table holds its SHA-256. A challenge is taken
// with DELETE ... RETURNING, so it works once, also when the attempt then
// fails. Code attempts on an mfa_login challenge are counted with a
// conditional UPDATE, so parallel guesses can't exceed the limit.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// Challenge kinds.
const (
	ChallengePasskeyLogin = "passkey_login" // passkey sign-in (no user yet)
	ChallengeMFALogin     = "mfa_login"     // password verified; the second step is pending
	ChallengeRegister     = "register"      // adding a passkey or an authenticator app
	ChallengeReauth       = "reauth"        // a passkey confirming a sensitive change
)

const (
	challengeTTL      = 5 * time.Minute
	challengeAttempts = 5 // wrong codes before an mfa_login challenge ends
)

// ErrNoChallenge means the challenge is missing, expired, used up or of another kind.
var ErrNoChallenge = errors.New("accounts: no such challenge")

// challengeData is a challenge's JSON payload. Fields a kind doesn't use stay empty.
type challengeData struct {
	// WebAuthn is the library's session data (challenge, allowed
	// credentials, user verification) for a ceremony in progress.
	WebAuthn json.RawMessage `json:"webauthn,omitempty"`
	// TOTPSecret is an authenticator-app secret being set up, sealed.
	TOTPSecret []byte `json:"totp,omitempty"`
	// Reset holds a password reset waiting for the second factor: the
	// reset token's hash and the new password's hash. Nothing is changed
	// until the factor succeeds.
	ResetToken    []byte `json:"reset_token,omitempty"`
	ResetPassword string `json:"reset_password,omitempty"`
}

type challenge struct {
	Kind     string
	UserID   int64 // 0 for passkey_login
	Data     challengeData
	Next     string
	Attempts int
}

func (s *Service) challengeCookie(kind string) string { return s.cookieAuthPrefix + kind }

// newChallenge stores a challenge and sets its cookie, replacing any of the
// same kind this browser had.
func (s *Service) newChallenge(ctx context.Context, w http.ResponseWriter, r *http.Request, kind string, uid int64, data challengeData, next string) error {
	raw, hash := newSecret()
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	now := s.store.now()
	var user any
	if uid != 0 {
		user = uid
	}
	if err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if old := cookieValue(r, s.challengeCookie(kind)); validSecretShape(old) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE hash = ?`, hashSecret(old)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO auth_challenges (hash, kind, user_id, data, next, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, hash, kind, user, string(b), next, db.Millis(now), db.Millis(now.Add(challengeTTL)))
		return err
	}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the public URL scheme
		Name: s.challengeCookie(kind), Value: raw, Path: "/", MaxAge: int(challengeTTL.Seconds()),
		Secure: s.d.Cfg.Secure(), HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// peekChallenge reads this browser's live challenge of kind without taking it.
func (s *Service) peekChallenge(ctx context.Context, r *http.Request, kind string) (*challenge, error) {
	raw := cookieValue(r, s.challengeCookie(kind))
	if !validSecretShape(raw) {
		return nil, ErrNoChallenge
	}
	return scanChallenge(s.d.DB.R.QueryRowContext(ctx, `SELECT kind, coalesce(user_id, 0), data, next, attempts FROM auth_challenges
		WHERE hash = ? AND kind = ? AND expires_at > ?`, hashSecret(raw), kind, db.Millis(s.store.now())))
}

// takeChallenge removes and returns this browser's live challenge of kind,
// and clears its cookie. A second take of the same challenge fails.
func (s *Service) takeChallenge(ctx context.Context, w http.ResponseWriter, r *http.Request, kind string) (*challenge, error) {
	raw := cookieValue(r, s.challengeCookie(kind))
	s.clearChallengeCookie(w, kind)
	if !validSecretShape(raw) {
		return nil, ErrNoChallenge
	}
	var c *challenge
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		c, err = scanChallenge(tx.QueryRowContext(ctx, `DELETE FROM auth_challenges WHERE hash = ? AND kind = ? AND expires_at > ?
			RETURNING kind, coalesce(user_id, 0), data, next, attempts`, hashSecret(raw), kind, db.Millis(s.store.now())))
		return err
	})
	return c, err
}

// updateChallenge replaces the data of this browser's live challenge of kind
// (e.g. the WebAuthn session of a second step started on an mfa_login).
func (s *Service) updateChallenge(ctx context.Context, r *http.Request, kind string, data challengeData) error {
	raw := cookieValue(r, s.challengeCookie(kind))
	if !validSecretShape(raw) {
		return ErrNoChallenge
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE auth_challenges SET data = ? WHERE hash = ? AND kind = ? AND expires_at > ?`,
			string(b), hashSecret(raw), kind, db.Millis(s.store.now()))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrNoChallenge
		}
		return nil
	})
}

// chargeAttempt counts one attempt on this browser's challenge of kind. It
// returns ErrNoChallenge once the limit is reached (the challenge is then
// removed) or when there is no live challenge.
func (s *Service) chargeAttempt(ctx context.Context, w http.ResponseWriter, r *http.Request, kind string) error {
	raw := cookieValue(r, s.challengeCookie(kind))
	if !validSecretShape(raw) {
		return ErrNoChallenge
	}
	hash := hashSecret(raw)
	var n int
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `UPDATE auth_challenges SET attempts = attempts + 1
			WHERE hash = ? AND kind = ? AND expires_at > ? AND attempts < ? RETURNING attempts`,
			hash, kind, db.Millis(s.store.now()), challengeAttempts).Scan(&n)
		if errors.Is(err, sql.ErrNoRows) {
			_, _ = tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE hash = ?`, hash)
			return ErrNoChallenge
		}
		return err
	})
	if errors.Is(err, ErrNoChallenge) {
		s.clearChallengeCookie(w, kind)
	}
	return err
}

func (s *Service) clearChallengeCookie(w http.ResponseWriter, kind string) {
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure follows the public URL scheme
		Name: s.challengeCookie(kind), Value: "", Path: "/", MaxAge: -1,
		Secure: s.d.Cfg.Secure(), HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func scanChallenge(row *sql.Row) (*challenge, error) {
	var (
		c    challenge
		data string
	)
	if err := row.Scan(&c.Kind, &c.UserID, &data, &c.Next, &c.Attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoChallenge
		}
		return nil, err
	}
	if err := json.Unmarshal([]byte(data), &c.Data); err != nil {
		return nil, err
	}
	return &c, nil
}

// sweepChallenges deletes expired challenges.
func (s *Service) sweepChallenges(ctx context.Context) error {
	return s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM auth_challenges WHERE expires_at <= ?`, db.Millis(s.store.now()))
		return err
	})
}
