package accounts

// Passkeys and security keys (WebAuthn): registration and management in
// Settings › Security, sign-in without a password, the passkey path of
// /login/2fa, and a passkey assertion as proof in confirmOwner.
//
// Every ceremony keeps the library's session data in an auth_challenges row
// whose token is in this browser's cookie, so a ceremony can only be
// finished by the browser that began it. Registration, sign-in and proofs
// take their challenge (DELETE ... RETURNING) before checking the answer, so
// each works once; the second step peeks at its mfa_login challenge, counts
// a failed passkey as an attempt and takes it only on success.
//
// A sign-in without a password and a proof need user verification in that
// assertion (the library checks it, and so does checkAssertion). The second
// step after a password needs presence only. Every assertion must raise the
// signature counter unless it stays at zero; a counter that goes back is
// refused and audited as auth.passkey_clone.

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/ratelimit"
	"github.com/Battleroid/snap-share/internal/web"
)

const (
	passkeyNameMax = 64 // characters, as the column allows
	passkeysMax    = 20 // per user
)

// registerPasskeys adds the passkey routes.
func (s *Service) registerPasskeys(r *web.Router) {
	if _, err := s.webAuthn(); err != nil {
		s.d.Log.Warn("passkeys are unavailable: SNAP_PUBLIC_URL must name a domain", "err", err)
	}
	r.HandleFunc("POST /login/passkey/begin", s.passkeyLoginBegin, web.Public())
	r.HandleFunc("POST /login/passkey/finish", s.passkeyLoginFinish, web.Public())
	r.HandleFunc("POST /login/2fa/passkey/begin", s.mfaPasskeyBegin, web.Public())
	r.HandleFunc("POST /login/2fa/passkey/finish", s.mfaPasskeyFinish, web.Public())

	r.HandleFunc("POST /settings/security/passkeys/begin", s.sessionOnly(s.passkeyAddBegin), web.SignedIn(), web.DuringSetup())
	r.HandleFunc("POST /settings/security/passkeys/finish", s.sessionOnly(s.passkeyAddFinish), web.SignedIn(), web.DuringSetup())
	r.HandleFunc("POST /settings/security/passkeys/{id}/rename", s.sessionOnly(s.passkeyRename), web.SignedIn())
	r.HandleFunc("POST /settings/security/passkeys/{id}/remove", s.sessionOnly(s.passkeyRemove), web.SignedIn())
	r.HandleFunc("POST /settings/security/reauth/passkey/begin", s.sessionOnly(s.reauthPasskeyBegin), web.SignedIn(), web.DuringSetup())
}

// Credential store.

// storedPasskey is a webauthn_credentials row.
type storedPasskey struct {
	ID             int64
	CredentialID   []byte
	PublicKey      []byte // COSE key
	SignCount      uint32
	Transports     []protocol.AuthenticatorTransport
	BackupEligible bool
	BackupState    bool
	Name           string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

// credential is the row as the library wants it. The backup flags go back
// as stored: the library refuses an assertion whose backup-eligible flag
// differs from the one the credential was registered with.
func (p storedPasskey) credential() webauthn.Credential {
	var f protocol.AuthenticatorFlags
	if p.BackupEligible {
		f |= protocol.FlagBackupEligible
	}
	if p.BackupState {
		f |= protocol.FlagBackupState
	}
	return webauthn.Credential{
		ID:            p.CredentialID,
		PublicKey:     p.PublicKey,
		Transport:     p.Transports,
		Flags:         webauthn.NewCredentialFlags(f),
		Authenticator: webauthn.Authenticator{SignCount: p.SignCount},
	}
}

// passkeyUser is a user with their passkeys, as a webauthn.User.
type passkeyUser struct {
	user   *User
	handle []byte // users.webauthn_id
	keys   []storedPasskey
}

func (p *passkeyUser) WebAuthnID() []byte          { return p.handle }
func (p *passkeyUser) WebAuthnName() string        { return p.user.Username }
func (p *passkeyUser) WebAuthnDisplayName() string { return p.user.Username }

func (p *passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, len(p.keys))
	for i, k := range p.keys {
		out[i] = k.credential()
	}
	return out
}

// find returns the passkey with credential id cid, or nil.
func (p *passkeyUser) find(cid []byte) *storedPasskey {
	for i := range p.keys {
		if bytes.Equal(p.keys[i].CredentialID, cid) {
			return &p.keys[i]
		}
	}
	return nil
}

// webauthnHandle returns uid's WebAuthn user handle. With create it makes
// one (32 random bytes) when the user has none yet; a parallel request may
// win the race, so the handle is always read back.
func (s *Store) webauthnHandle(ctx context.Context, uid int64, create bool) ([]byte, error) {
	var h []byte
	err := s.db.R.QueryRowContext(ctx, `SELECT webauthn_id FROM users WHERE id = ?`, uid).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || len(h) > 0 || !create {
		return h, err
	}
	h = make([]byte, 32)
	_, _ = rand.Read(h) // crypto/rand.Read never fails
	if err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET webauthn_id = ? WHERE id = ? AND webauthn_id IS NULL`, h, uid)
		return err
	}); err != nil {
		return nil, err
	}
	err = s.db.R.QueryRowContext(ctx, `SELECT webauthn_id FROM users WHERE id = ?`, uid).Scan(&h)
	return h, err
}

//nolint:gosec // G101: column names, not credentials
const passkeyCols = `id, credential_id, public_key, sign_count, transports, backup_eligible, backup_state, name, created_at, last_used_at`

// passkeys lists uid's passkeys, oldest first.
func (s *Store) passkeys(ctx context.Context, uid int64) ([]storedPasskey, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+passkeyCols+` FROM webauthn_credentials WHERE user_id = ? ORDER BY id`, uid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []storedPasskey
	for rows.Next() {
		var (
			p          storedPasskey
			count      int64
			transports string
			created    int64
			used       sql.NullInt64
		)
		if err := rows.Scan(&p.ID, &p.CredentialID, &p.PublicKey, &count, &transports, &p.BackupEligible, &p.BackupState,
			&p.Name, &created, &used); err != nil {
			return nil, err
		}
		p.SignCount = uint32(min(max(count, 0), 1<<32-1)) //nolint:gosec // G115: clamped to uint32
		if err := json.Unmarshal([]byte(transports), &p.Transports); err != nil {
			p.Transports = nil
		}
		p.CreatedAt, p.LastUsedAt = db.Time(created), db.NullTime(used)
		out = append(out, p)
	}
	return out, rows.Err()
}

// passkeyUser loads u's handle and passkeys. With create, a user without a
// handle gets one (registration); otherwise the handle may be empty.
func (s *Store) passkeyUser(ctx context.Context, u *User, create bool) (*passkeyUser, error) {
	h, err := s.webauthnHandle(ctx, u.ID, create)
	if err != nil {
		return nil, err
	}
	keys, err := s.passkeys(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &passkeyUser{user: u, handle: h, keys: keys}, nil
}

// passkeyUserByHandle finds the user a discoverable credential names.
func (s *Store) passkeyUserByHandle(ctx context.Context, handle []byte) (*passkeyUser, error) {
	if len(handle) != 32 {
		return nil, ErrNotFound
	}
	u, err := s.userWhere(ctx, `webauthn_id = ?`, handle)
	if err != nil {
		return nil, err
	}
	return s.passkeyUser(ctx, u, false)
}

// recordPasskeyUse stores a verified assertion's counter, backup state and
// time. The counter must go up, or stay at zero for authenticators that
// don't count; checked in the UPDATE, so two assertions with the same
// counter can't both pass. It reports false when the row did not qualify.
func (s *Store) recordPasskeyUse(ctx context.Context, uid, id int64, count uint32, backupState bool) (bool, error) {
	var ok bool
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE webauthn_credentials SET sign_count = ?1, backup_state = ?2, last_used_at = ?3
			WHERE id = ?4 AND user_id = ?5 AND (sign_count < ?1 OR (sign_count = 0 AND ?1 = 0))`,
			int64(count), backupState, db.Millis(s.now()), id, uid)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		ok = n == 1
		return err
	})
	return ok, err
}

// passkeyRow is a passkey on the security page.
type passkeyRow struct {
	ID       int64
	Name     string
	Created  string
	LastUsed string // "" when never used
	Synced   bool   // backed up (a synced passkey)
}

// passkeyRows lists uid's passkeys for the security page.
func (s *Service) passkeyRows(ctx context.Context, uid int64) ([]passkeyRow, error) {
	keys, err := s.store.passkeys(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]passkeyRow, 0, len(keys))
	for _, k := range keys {
		row := passkeyRow{ID: k.ID, Name: k.Name, Created: k.CreatedAt.UTC().Format("2006-01-02"), Synced: k.BackupState}
		if k.LastUsedAt != nil {
			row.LastUsed = k.LastUsedAt.UTC().Format("2006-01-02 15:04")
		}
		out = append(out, row)
	}
	return out, nil
}

// Ceremony helpers.

// Messages. A failed sign-in never says whether the passkey belongs to an account.
//
//nolint:gosec // G101: messages, not credentials
const (
	msgPasskeyFailed  = "That passkey didn't work. Try again, or sign in another way."
	msgPasskeyCopied  = "This passkey was refused because it may have been copied. Sign in another way and remove it in Settings."
	msgPasskeyExpired = "That took too long. Try again."
	msgPasskeyOff     = "Password sign-in is off, and passkey sign-in with it."
	msgPasskeyHost    = "Passkeys don't work on this site's address."
)

var (
	errPasskeyNoUV   = errors.New("accounts: assertion without user verification")
	errPasskeyCopied = errors.New("accounts: passkey counter went back")
)

// passkeyError answers a JSON ceremony request with {"error": msg}.
func passkeyError(w http.ResponseWriter, status int, msg string) {
	web.JSON(w, status, map[string]string{"error": msg})
}

func (s *Service) passkeyServerError(w http.ResponseWriter, r *http.Request, err error) {
	s.d.Log.Error("accounts", "route", r.Pattern, "err", err)
	passkeyError(w, http.StatusInternalServerError, "Something went wrong.")
}

func (s *Service) passkeyTooMany(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
	passkeyError(w, http.StatusTooManyRequests, "Too many attempts. Try again in "+humanWait(wait)+".")
}

// relyingParty is webAuthn() for a request that begins a ceremony. When
// SNAP_PUBLIC_URL names no domain (an IP address, a single label) passkeys
// can't work, and the answer says so instead of being a server error.
func (s *Service) relyingParty(w http.ResponseWriter) (*webauthn.WebAuthn, bool) {
	wa, err := s.webAuthn()
	if err != nil {
		passkeyError(w, http.StatusServiceUnavailable, msgPasskeyHost)
		return nil, false
	}
	return wa, true
}

// sessionData decodes the library's session data kept in a challenge.
func sessionData(c *challenge) (webauthn.SessionData, error) {
	var sd webauthn.SessionData
	if len(c.Data.WebAuthn) == 0 {
		return sd, ErrNoChallenge
	}
	err := json.Unmarshal(c.Data.WebAuthn, &sd)
	return sd, err
}

// checkAssertion runs the checks the library leaves to the relying party on
// an assertion it verified for pu: user verification in this assertion
// (the credential's own UV flag is a remembered value, not this one), and
// a counter that goes up. It then records the use.
func (s *Service) checkAssertion(ctx context.Context, r *http.Request, pu *passkeyUser, parsed *protocol.ParsedCredentialAssertionData, cred *webauthn.Credential, requireUV bool) error {
	if requireUV && !parsed.Response.AuthenticatorData.Flags.HasUserVerified() {
		return errPasskeyNoUV
	}
	key := pu.find(cred.ID)
	if key == nil {
		return ErrNotFound
	}
	if !cred.Authenticator.CloneWarning {
		ok, err := s.store.recordPasskeyUse(ctx, pu.user.ID, key.ID, parsed.Response.AuthenticatorData.Counter,
			parsed.Response.AuthenticatorData.Flags.HasBackupState())
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		// Gone since it was loaded, or another assertion took this counter.
		var n int
		if err := s.d.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM webauthn_credentials WHERE id = ?`, key.ID).Scan(&n); err != nil || n == 0 {
			return ErrNotFound
		}
	}
	e := Actor(r)
	e.Action, e.Target = "auth.passkey_clone", userTarget(pu.user.ID)
	e.Details = map[string]any{"passkey": key.ID, "stored": key.SignCount, "sent": parsed.Response.AuthenticatorData.Counter}
	_ = s.store.Audit(ctx, e)
	return errPasskeyCopied
}

// Sign-in without a password.

// passkeyLoginBegin handles POST /login/passkey/begin: options for a
// discoverable sign-in that requires user verification. Started on every
// sign-in page load for autofill, so it has its own per-address limit
// rather than the password budget.
func (s *Service) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.passwordLogin() {
		passkeyError(w, http.StatusForbidden, msgPasskeyOff)
		return
	}
	if ok, wait := s.allow(s.lim.passkeyBegin, r); !ok {
		s.passkeyTooMany(w, wait)
		return
	}
	wa, ok := s.relyingParty(w)
	if !ok {
		return
	}
	assertion, sd, err := wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	if err := s.newChallenge(ctx, w, r, ChallengePasskeyLogin, 0, challengeData{WebAuthn: raw}, web.SafeNext(r.PostFormValue("next"))); err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, assertion)
}

// passkeyLoginFinish handles POST /login/passkey/finish: the assertion as
// JSON. A failure spends a token of the address's sign-in budget (shared
// with passwords). It answers {"redirect": path}.
func (s *Service) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.passwordLogin() {
		passkeyError(w, http.StatusForbidden, msgPasskeyOff)
		return
	}
	tok, ok, wait := s.reserveLoginIP(ctx)
	if !ok {
		s.passkeyTooMany(w, wait)
		return
	}
	defer tok.Keep() // a no-op once refunded
	c, err := s.takeChallenge(ctx, w, r, ChallengePasskeyLogin)
	if errors.Is(err, ErrNoChallenge) {
		passkeyError(w, http.StatusBadRequest, msgPasskeyExpired)
		return
	}
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		passkeyError(w, http.StatusBadRequest, msgPasskeyFailed)
		return
	}
	pu, err := s.verifyPasskeyLogin(ctx, r, c, body)
	switch {
	case errors.Is(err, errPasskeyCopied):
		passkeyError(w, http.StatusUnauthorized, msgPasskeyCopied)
		return
	case err != nil:
		e := Actor(r)
		e.Action, e.Details = "auth.login_failed", map[string]any{"method": "passkey"}
		if pu != nil {
			e.Target = userTarget(pu.user.ID)
		}
		_ = s.store.Audit(ctx, e)
		passkeyError(w, http.StatusUnauthorized, msgPasskeyFailed)
		return
	}
	if !pu.user.Active() {
		passkeyError(w, http.StatusForbidden, "This account is disabled.")
		return
	}
	tok.Refund()
	if err := s.signIn(ctx, w, r, pu.user, "passkey"); err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, map[string]string{"redirect": web.SafeNext(c.Next)})
}

// verifyPasskeyLogin checks a discoverable assertion against the taken
// challenge c: the user handle names the user, the credential must be
// theirs, and the assertion must carry user verification. It returns the
// user once known, also on failure.
func (s *Service) verifyPasskeyLogin(ctx context.Context, r *http.Request, c *challenge, body []byte) (*passkeyUser, error) {
	sd, err := sessionData(c)
	if err != nil {
		return nil, err
	}
	if sd.UserVerification != protocol.VerificationRequired || len(sd.UserID) != 0 {
		return nil, errPasskeyNoUV
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return nil, err
	}
	wa, err := s.webAuthn()
	if err != nil {
		return nil, err
	}
	var pu *passkeyUser
	_, cred, err := wa.ValidatePasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
		found, err := s.store.passkeyUserByHandle(ctx, handle)
		if err != nil {
			return nil, err
		}
		pu = found
		if found.find(rawID) == nil {
			return nil, ErrNotFound
		}
		return found, nil
	}, sd, parsed)
	if err != nil {
		return pu, err
	}
	return pu, s.checkAssertion(ctx, r, pu, parsed, cred, true)
}

// ipToken is a token of the address's sign-in budget (none for an exempt address).
type ipToken struct{ res *ratelimit.Reservation }

func (t ipToken) Keep() {
	if t.res != nil {
		t.res.Keep()
	}
}

func (t ipToken) Refund() {
	if t.res != nil {
		t.res.Refund()
	}
}

// reserveLoginIP takes a token from the address's sign-in budget before an
// attempt, so parallel attempts get at most what is left of it.
func (s *Service) reserveLoginIP(ctx context.Context) (ipToken, bool, time.Duration) {
	ip := clientip.From(ctx)
	if ip.IsValid() && clientip.In(ip, s.d.Cfg.RateLimitExempt) {
		return ipToken{}, true, 0
	}
	res, ok, wait := s.lim.loginIP.Reserve(ctx, clientip.LimitKey(ip), 0)
	return ipToken{res}, ok, wait
}

// The second step after a password.

// mfaPasskeyBegin handles POST /login/2fa/passkey/begin: options for the
// user of this browser's mfa_login challenge, listing their passkeys. User
// presence is enough here, so PIN-less security keys work.
func (s *Service) mfaPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.peekChallenge(ctx, r, ChallengeMFALogin)
	if errors.Is(err, ErrNoChallenge) {
		web.JSON(w, http.StatusUnauthorized, map[string]string{"error": "This sign-in has expired. Sign in again.", "redirect": "/login"})
		return
	}
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	u, err := s.store.User(ctx, c.UserID)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	pu, err := s.store.passkeyUser(ctx, u, false)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	if len(pu.keys) == 0 || len(pu.handle) == 0 {
		passkeyError(w, http.StatusConflict, "Your account has no passkeys.")
		return
	}
	wa, ok := s.relyingParty(w)
	if !ok {
		return
	}
	assertion, sd, err := wa.BeginLogin(pu, webauthn.WithUserVerification(protocol.VerificationPreferred))
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	d := c.Data // keeps a held password reset
	d.WebAuthn = raw
	if err := s.updateChallenge(ctx, r, ChallengeMFALogin, d); errors.Is(err, ErrNoChallenge) {
		web.JSON(w, http.StatusUnauthorized, map[string]string{"error": "This sign-in has expired. Sign in again.", "redirect": "/login"})
		return
	} else if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, assertion)
}

// mfaPasskeyFinish handles POST /login/2fa/passkey/finish. A failed
// passkey counts as an attempt on the challenge but spends neither the
// address's nor the account's budget, so someone with only the password
// can't lock the owner out of their passkey. It answers {"redirect": path}.
func (s *Service) mfaPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	expired := func() {
		web.JSON(w, http.StatusUnauthorized, map[string]string{"error": "This sign-in has expired. Sign in again.", "redirect": "/login"})
	}
	c, err := s.peekChallenge(ctx, r, ChallengeMFALogin)
	if errors.Is(err, ErrNoChallenge) {
		expired()
		return
	}
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		passkeyError(w, http.StatusBadRequest, msgPasskeyFailed)
		return
	}
	if err := s.verifyMFAPasskey(ctx, r, c, body); err != nil {
		if cerr := s.chargeAttempt(ctx, w, r, ChallengeMFALogin); errors.Is(cerr, ErrNoChallenge) {
			web.JSON(w, http.StatusUnauthorized, map[string]string{"error": "Too many attempts. Sign in again.", "redirect": "/login"})
			return
		} else if cerr != nil {
			s.passkeyServerError(w, r, cerr)
			return
		}
		msg := msgPasskeyFailed
		if errors.Is(err, errPasskeyCopied) {
			msg = msgPasskeyCopied
		}
		passkeyError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	taken, err := s.takeChallenge(ctx, w, r, ChallengeMFALogin)
	if errors.Is(err, ErrNoChallenge) {
		expired() // finished or ended by a parallel request
		return
	}
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	next, err := s.finishMFA(ctx, w, r, taken, "key")
	var re *errReauth
	switch {
	case errors.As(err, &re):
		passkeyError(w, re.status, re.msg)
	case errors.Is(err, ErrInvalidToken):
		passkeyError(w, http.StatusGone, "The reset link has expired or was already used. Ask for a new one.")
	case err != nil:
		s.passkeyServerError(w, r, err)
	default:
		web.JSON(w, http.StatusOK, map[string]string{"redirect": web.SafeNext(next)})
	}
}

// verifyMFAPasskey checks an assertion against the session data the begin
// step stored in c, for c's user and their current passkeys.
func (s *Service) verifyMFAPasskey(ctx context.Context, r *http.Request, c *challenge, body []byte) error {
	sd, err := sessionData(c)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return err
	}
	u, err := s.store.User(ctx, c.UserID)
	if err != nil {
		return err
	}
	pu, err := s.store.passkeyUser(ctx, u, false)
	if err != nil {
		return err
	}
	wa, err := s.webAuthn()
	if err != nil {
		return err
	}
	cred, err := wa.ValidateLogin(pu, sd, parsed)
	if err != nil {
		return err
	}
	return s.checkAssertion(ctx, r, pu, parsed, cred, false)
}

// Proof for confirmOwner.

// reauthPasskeyBegin handles POST /settings/security/reauth/passkey/begin:
// options for a passkey of the signed-in user, with user verification
// required, kept in this browser's reauth challenge. The form that needs
// the proof then sends the assertion as its "passkey" field.
func (s *Service) reauthPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if ok, wait := s.allow(s.lim.passkeyBegin, r); !ok {
		s.passkeyTooMany(w, wait)
		return
	}
	u, err := s.CurrentUser(r)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	pu, err := s.store.passkeyUser(ctx, u, false)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	if len(pu.keys) == 0 || len(pu.handle) == 0 {
		passkeyError(w, http.StatusConflict, "You have no passkeys.")
		return
	}
	wa, ok := s.relyingParty(w)
	if !ok {
		return
	}
	assertion, sd, err := wa.BeginLogin(pu, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	if err := s.newChallenge(ctx, w, r, ChallengeReauth, u.ID, challengeData{WebAuthn: raw}, ""); err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, assertion)
}

// checkPasskeyProof verifies a passkey assertion (with user verification)
// sent with a sensitive change, against this browser's reauth challenge.
// The challenge is taken first, so an assertion works once.
func (s *Service) checkPasskeyProof(r *http.Request, u *User, assertion string) error {
	ctx := r.Context()
	// No response to clear the cookie on here: the row is gone, so the
	// cookie opens nothing, and the next reauth challenge replaces it.
	c, err := s.takeChallenge(ctx, discardResponse{}, r, ChallengeReauth)
	if err != nil {
		return errNoFactor
	}
	if c.UserID != u.ID {
		return errNoFactor
	}
	sd, err := sessionData(c)
	if err != nil || sd.UserVerification != protocol.VerificationRequired {
		return errNoFactor
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes([]byte(assertion))
	if err != nil {
		return errNoFactor
	}
	pu, err := s.store.passkeyUser(ctx, u, false)
	if err != nil {
		return err
	}
	wa, err := s.webAuthn()
	if err != nil {
		return err
	}
	cred, err := wa.ValidateLogin(pu, sd, parsed)
	if err != nil {
		return errNoFactor
	}
	if err := s.checkAssertion(ctx, r, pu, parsed, cred, true); errors.Is(err, errPasskeyCopied) {
		return &errReauth{http.StatusUnprocessableEntity, msgPasskeyCopied}
	} else if err != nil {
		return errNoFactor
	}
	return nil
}

// discardResponse is a ResponseWriter for helpers that set headers when no
// response is being written.
type discardResponse struct{}

func (discardResponse) Header() http.Header         { return http.Header{} }
func (discardResponse) Write(b []byte) (int, error) { return len(b), nil }
func (discardResponse) WriteHeader(int)             {}

// Registration and management (Settings › Security).

// passkeyAddBegin handles POST /settings/security/passkeys/begin. It takes
// the form confirmOwner reads (the password, plus a code or a passkey when
// the user already has a factor) and answers creation options as JSON,
// excluding the user's passkeys so an authenticator isn't added twice.
func (s *Service) passkeyAddBegin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if ok, wait := s.allow(s.lim.passkeyBegin, r); !ok {
		s.passkeyTooMany(w, wait)
		return
	}
	u, err := s.CurrentUser(r)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	if err := s.confirmOwner(r, u, r.PostFormValue("current"), s.passwordCounts(u)); err != nil {
		var re *errReauth
		if !errors.As(err, &re) {
			s.passkeyServerError(w, r, err)
			return
		}
		passkeyError(w, re.status, re.msg)
		return
	}
	pu, err := s.store.passkeyUser(ctx, u, true)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	if len(pu.keys) >= passkeysMax {
		passkeyError(w, http.StatusConflict, msgTooManyPasskeys)
		return
	}
	wa, ok := s.relyingParty(w)
	if !ok {
		return
	}
	creation, sd, err := wa.BeginRegistration(pu,
		webauthn.WithExclusions(webauthn.Credentials(pu.WebAuthnCredentials()).CredentialDescriptors()),
		webauthn.WithExtensions(webauthn.WithExtensionCredProps()))
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	raw, err := json.Marshal(sd)
	if err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	if err := s.newChallenge(ctx, w, r, ChallengeRegister, u.ID, challengeData{WebAuthn: raw}, ""); err != nil {
		s.passkeyServerError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, creation)
}

// passkeyAddFinish handles POST /settings/security/passkeys/finish, a form
// with the new credential (PublicKeyCredential.toJSON()) as "credential".
// The first factor ends the user's other sessions and makes recovery codes:
// the page is then shown with the codes (once). Otherwise it redirects.
func (s *Service) passkeyAddFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	fail := func(status int, msg string) { s.securityForm(w, r, status, u, &securityData{Error: msg}) }
	c, err := s.takeChallenge(ctx, w, r, ChallengeRegister)
	if errors.Is(err, ErrNoChallenge) {
		fail(http.StatusConflict, "Adding the passkey took too long. Try again.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sd, err := sessionData(c)
	if err != nil || c.UserID != u.ID {
		fail(http.StatusConflict, "Adding the passkey took too long. Try again.")
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes([]byte(r.PostFormValue("credential")))
	if err != nil {
		fail(http.StatusUnprocessableEntity, "The passkey couldn't be added. Try again.")
		return
	}
	pu, err := s.store.passkeyUser(ctx, u, false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	wa, err := s.webAuthn()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	cred, err := wa.CreateCredential(pu, sd, parsed)
	if err != nil {
		fail(http.StatusUnprocessableEntity, "The passkey couldn't be added. Try again.")
		return
	}
	if pu.find(cred.ID) != nil {
		fail(http.StatusConflict, "That passkey is already added.")
		return
	}
	name := defaultPasskeyName(pu.keys, cred)
	if cred.Transport == nil {
		cred.Transport = []protocol.AuthenticatorTransport{}
	}
	transports, err := json.Marshal(cred.Transport)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var codes []string
	actor := Actor(r)
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM webauthn_credentials WHERE user_id = ?`, u.ID).Scan(&n); err != nil {
			return err
		}
		if n >= passkeysMax {
			return errTooManyPasskeys
		}
		if codes, err = s.firstFactorTx(ctx, tx, u.ID, p.SessionID); err != nil {
			return err
		}
		now := s.store.now()
		res, err := tx.ExecContext(ctx, `INSERT INTO webauthn_credentials
			(user_id, credential_id, public_key, sign_count, transports, backup_eligible, backup_state, name, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			u.ID, cred.ID, cred.PublicKey, int64(cred.Authenticator.SignCount), string(transports),
			cred.Flags.BackupEligible, cred.Flags.BackupState, name, db.Millis(now))
		if err != nil {
			return mapPasskeyConstraint(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		actor.Action, actor.Target = "mfa.passkey_added", userTarget(u.ID)
		actor.Details = map[string]any{"passkey": id, "synced": cred.Flags.BackupState}
		return auditTx(ctx, tx, actor, now)
	})
	switch {
	case errors.Is(err, errPasskeyTaken):
		fail(http.StatusConflict, "That passkey is already added.")
		return
	case errors.Is(err, errTooManyPasskeys):
		fail(http.StatusConflict, msgTooManyPasskeys)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.securityNotice(ctx, u, SecurityPasskeyAdded, "")
	if len(codes) == 0 {
		redirect(w, r, "/settings/security?saved=passkey")
		return
	}
	// The session no longer needs setup (the factor is in): the page shows
	// the full layout with the codes.
	if p.NeedsSetup {
		np := *p
		np.NeedsSetup = false
		r = r.WithContext(auth.WithPrincipal(ctx, &np))
	}
	s.securityForm(w, r, http.StatusOK, u, &securityData{Notice: "Passkey added. Your other sessions were signed out.", NewCodes: codes})
}

var (
	errPasskeyTaken    = errors.New("accounts: credential already registered")
	errTooManyPasskeys = errors.New("accounts: too many passkeys")
)

var msgTooManyPasskeys = fmt.Sprintf("An account can have %d passkeys. Remove one first.", passkeysMax)

func mapPasskeyConstraint(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: webauthn_credentials.credential_id") {
		return errPasskeyTaken
	}
	return err
}

// defaultPasskeyName names a new credential: "Passkey" when it is
// discoverable or can sync, otherwise "Security key", numbered after the
// first of its kind.
func defaultPasskeyName(existing []storedPasskey, cred *webauthn.Credential) string {
	base := "Security key"
	if cred.Flags.BackupEligible || (cred.Extensions.RK != nil && *cred.Extensions.RK) {
		base = "Passkey"
	}
	taken := map[string]bool{}
	for _, k := range existing {
		taken[k.Name] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		if n := base + " " + strconv.Itoa(i); !taken[n] {
			return n
		}
	}
}

// passkeyID reads the {id} of a passkey route.
func passkeyID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// validPasskeyName reports whether name (trimmed) can name a passkey.
func validPasskeyName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > passkeyNameMax || !utf8.ValidString(name) {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return false
		}
	}
	return true
}

// passkeyRename handles POST /settings/security/passkeys/{id}/rename. A name
// is only a label, so this needs no confirmation.
func (s *Service) passkeyRename(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	id, ok := passkeyID(r)
	if !ok {
		s.d.Errors.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if !validPasskeyName(name) {
		s.securityForm(w, r, http.StatusUnprocessableEntity, u, &securityData{Error: fmt.Sprintf("Not renamed. Use 1–%d characters.", passkeyNameMax)})
		return
	}
	actor := Actor(r)
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE webauthn_credentials SET name = ? WHERE id = ? AND user_id = ?`, name, id, u.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrNotFound
		}
		actor.Action, actor.Target, actor.Details = "mfa.passkey_renamed", userTarget(u.ID), map[string]any{"passkey": id}
		return auditTx(ctx, tx, actor, s.store.now())
	})
	switch {
	case errors.Is(err, ErrNotFound):
		s.d.Errors.NotFound(w, r)
	case err != nil:
		s.serverError(w, r, err)
	default:
		redirect(w, r, "/settings/security?saved=passkey_renamed")
	}
}

// passkeyRemove handles POST /settings/security/passkeys/{id}/remove. It
// needs confirmOwner, and refuses to remove the last factor of a user the
// policy covers.
func (s *Service) passkeyRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	id, ok := passkeyID(r)
	if !ok {
		s.d.Errors.NotFound(w, r)
		return
	}
	if err := s.confirmOwner(r, u, r.PostFormValue("current"), s.passwordCounts(u)); err != nil {
		var re *errReauth
		if !errors.As(err, &re) {
			s.serverError(w, r, err)
			return
		}
		s.securityForm(w, r, re.status, u, &securityData{Error: "Not removed. " + re.msg})
		return
	}
	actor := Actor(r)
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`, id, u.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrNotFound
		}
		// The row is already gone inside this transaction, so the factors
		// read now are the ones left; a refusal rolls the delete back.
		if err := s.mayRemoveFactorTx(ctx, tx, u, func(f Factors) Factors { return f }); err != nil {
			return err
		}
		actor.Action, actor.Target, actor.Details = "mfa.passkey_removed", userTarget(u.ID), map[string]any{"passkey": id}
		return auditTx(ctx, tx, actor, s.store.now())
	})
	var re *errReauth
	switch {
	case errors.Is(err, ErrNotFound):
		s.d.Errors.NotFound(w, r)
	case errors.As(err, &re):
		s.securityForm(w, r, re.status, u, &securityData{Error: re.msg})
	case err != nil:
		s.serverError(w, r, err)
	default:
		s.securityNotice(ctx, u, SecurityPasskeyRemoved, "")
		redirect(w, r, "/settings/security?saved=passkey_removed")
	}
}
