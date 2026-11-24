package accounts

// Authenticator apps (TOTP, RFC 6238) and recovery codes as a second
// factor: the code check, the code step of /login/2fa and codes as proof
// in confirmOwner. Setup and removal in Settings › Security are in
// totppages.go.

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 authenticator apps use HMAC-SHA1
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

const (
	totpSealLabel = "snap totp secret v1" // auth.Sealer label; the user id is bound too
	totpSecretLen = 20                    // bytes: 160 bits, as RFC 4226 recommends
	totpPeriod    = 30                    // seconds per time step
	totpDigits    = 6
	totpWindow    = 1 // steps accepted either side of the current one
)

// totpEncoding is how secrets are shown and put in otpauth URIs.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// totpStep is the RFC 6238 time step of t.
func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// totpCode is the RFC 4226 value of secret at step: HMAC-SHA1, dynamic
// truncation, 6 digits.
func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // G115: steps are positive
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// normalizeTOTP drops spaces. It returns "" unless six digits remain.
func normalizeTOTP(code string) string {
	code = strings.ReplaceAll(code, " ", "")
	if len(code) != totpDigits {
		return ""
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return code
}

// totpMatch returns the newest step within totpWindow of now whose code is
// code. Every candidate is compared, in constant time.
func totpMatch(secret []byte, code string, now time.Time) (int64, bool) {
	code = normalizeTOTP(code)
	if code == "" {
		return 0, false
	}
	cur := totpStep(now)
	var (
		step int64
		ok   bool
	)
	for d := int64(-totpWindow); d <= totpWindow; d++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, cur+d)), []byte(code)) == 1 {
			step, ok = cur+d, true
		}
	}
	return step, ok
}

// totpURI is the otpauth URI the setup QR code carries. Spaces are %20:
// some apps don't read "+".
func totpURI(site, username string, secret []byte) string {
	esc := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	return "otpauth://totp/" + esc(site) + ":" + esc(username) + "?secret=" + totpEncoding.EncodeToString(secret) +
		"&issuer=" + esc(site) + "&algorithm=SHA1&digits=6&period=30"
}

// totpNow is the time codes are checked against.
func (s *Service) totpNow() time.Time {
	if t := s.totpAt.Load(); t != nil {
		return *t
	}
	return s.store.now()
}

// totpReadable reports whether uid's authenticator-app secret opens with
// a configured key (it doesn't after a key rotation without
// SNAP_SECRET_KEY_PREVIOUS); the code option is then not offered.
func (s *Service) totpReadable(ctx context.Context, uid int64) bool {
	var sealed []byte
	if err := s.d.DB.R.QueryRowContext(ctx, `SELECT secret FROM user_totp WHERE user_id = ?`, uid).Scan(&sealed); err != nil {
		return false
	}
	_, _, err := s.totpSeal.Open(uid, sealed)
	return err == nil
}

// checkTOTP verifies code against uid's authenticator app and uses up its
// time step in one conditional UPDATE, so a code (or an older one) works
// once even under parallel requests. A secret that opened only with
// SNAP_SECRET_KEY_PREVIOUS is sealed again with the current key by the
// same statement. It reports false for a wrong or used code, and when
// there is no readable secret.
func (s *Service) checkTOTP(ctx context.Context, uid int64, code string) (bool, error) {
	var sealed []byte
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT secret FROM user_totp WHERE user_id = ?`, uid).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	secret, current, err := s.totpSeal.Open(uid, sealed)
	if errors.Is(err, auth.ErrUnsealable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	step, ok := totpMatch(secret, code, s.totpNow())
	if !ok {
		return false, nil
	}
	resealed := sealed
	if !current {
		if resealed, err = s.totpSeal.Seal(uid, secret); err != nil {
			return false, err
		}
	}
	var used bool
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE user_totp SET last_step = ?, secret = ?
			WHERE user_id = ? AND secret = ? AND last_step < ?`, step, resealed, uid, sealed, step)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		used = n == 1
		return err
	})
	return used, err
}

// mfaKey is the per-account budget for second-factor codes (loginAccount).
func mfaKey(uid int64) string { return "mfa:" + strconv.FormatInt(uid, 10) }

// recoveryLeftText is the detail of a SecurityRecoveryUsed notice.
func recoveryLeftText(n int) string {
	if n == 1 {
		return "1 recovery code left"
	}
	return fmt.Sprintf("%d recovery codes left", n)
}

// recoveryUsed tells uid that a recovery code was used, and how many are left.
func (s *Service) recoveryUsed(ctx context.Context, uid int64) {
	u, err := s.store.User(ctx, uid)
	if err != nil {
		return
	}
	f, err := s.store.Factors(ctx, uid)
	if err != nil {
		return
	}
	s.securityNotice(ctx, u, SecurityRecoveryUsed, recoveryLeftText(f.RecoveryLeft))
}

// mfaCode handles POST /login/2fa/code: a code from the authenticator app
// (kind=totp) or a recovery code (kind=recovery) as the second step. Each
// attempt spends the address's sign-in budget, the account's code budget
// (mfa:<uid>, kept across challenges) and one of the challenge's attempts,
// before the code is checked.
func (s *Service) mfaCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.peekChallenge(ctx, r, ChallengeMFALogin)
	if errors.Is(err, ErrNoChallenge) {
		redirect(w, r, "/login")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	kind, code := r.PostFormValue("kind"), strings.TrimSpace(r.PostFormValue("code"))
	if (kind != "totp" && kind != "recovery") || code == "" {
		s.mfaCodeForm(w, r, http.StatusBadRequest, kind, "Enter a code.")
		return
	}
	if ok, wait := s.allow(s.lim.loginIP, r); !ok {
		s.mfaForm(w, r, http.StatusTooManyRequests, "Too many attempts. Try again in "+humanWait(wait)+".")
		return
	}
	key := mfaKey(c.UserID)
	res, ok, wait := s.lim.loginAccount.Reserve(ctx, key, loginQueueWait)
	if !ok {
		s.mfaForm(w, r, http.StatusTooManyRequests, "Too many attempts. Try again in "+humanWait(wait)+".")
		return
	}
	defer res.Keep() // a no-op once refunded
	if err := s.chargeAttempt(ctx, w, r, ChallengeMFALogin); err != nil {
		if errors.Is(err, ErrNoChallenge) {
			s.tooManyCodes(w, r, c.Next)
			return
		}
		res.Refund()
		s.serverError(w, r, err)
		return
	}
	var good bool
	if kind == "totp" {
		good, err = s.checkTOTP(ctx, c.UserID, code)
	} else {
		good, err = s.store.UseRecoveryCode(ctx, c.UserID, code)
	}
	if err != nil {
		res.Refund()
		s.serverError(w, r, err)
		return
	}
	if !good {
		e := Actor(r)
		e.Action, e.Target, e.Details = "auth.login_failed", userTarget(c.UserID), map[string]any{"method": kind}
		_ = s.store.Audit(ctx, e)
		s.d.Metrics.AuthAttempt(kind, false)
		if c.Attempts+1 >= challengeAttempts {
			_, _ = s.takeChallenge(ctx, w, r, ChallengeMFALogin)
			s.tooManyCodes(w, r, c.Next)
			return
		}
		msg := "That code didn't work. Wait for the next one and try again."
		if kind == "recovery" {
			msg = "That code didn't work. Each recovery code works once."
		}
		s.mfaCodeForm(w, r, http.StatusUnauthorized, kind, msg)
		return
	}
	res.Refund()
	// Single use: of parallel finishes, only one takes the challenge.
	taken, err := s.takeChallenge(ctx, w, r, ChallengeMFALogin)
	if errors.Is(err, ErrNoChallenge) {
		redirect(w, r, "/login")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	next, err := s.finishMFA(ctx, w, r, taken, kind)
	var re *errReauth
	switch {
	case errors.As(err, &re):
		s.loginForm(w, r, re.status, "", taken.Next, re.msg)
		return
	case errors.Is(err, ErrInvalidToken): // a held reset whose link was used meanwhile
		s.d.Errors.Error(w, r, http.StatusNotFound, msgBadLink)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	if kind == "recovery" {
		s.recoveryUsed(ctx, c.UserID)
	}
	redirect(w, r, next)
}

// tooManyCodes ends the second step after its last wrong code.
func (s *Service) tooManyCodes(w http.ResponseWriter, r *http.Request, next string) {
	s.loginForm(w, r, http.StatusUnauthorized, "", next, "Too many wrong codes. Sign in again.")
}

// checkCodeProof verifies a code sent with a sensitive change: six digits
// are an authenticator-app code, anything else a recovery code. It spends
// the account's code budget (mfa:<uid>), shared with the second step.
func (s *Service) checkCodeProof(r *http.Request, u *User, code string) error {
	ctx := r.Context()
	res, ok, wait := s.lim.loginAccount.Reserve(ctx, mfaKey(u.ID), loginQueueWait)
	if !ok {
		return &errReauth{http.StatusTooManyRequests, "Too many attempts. Try again in " + humanWait(wait) + "."}
	}
	defer res.Keep()
	var (
		good     bool
		err      error
		recovery = normalizeTOTP(code) == ""
	)
	if recovery {
		good, err = s.store.UseRecoveryCode(ctx, u.ID, code)
	} else {
		good, err = s.checkTOTP(ctx, u.ID, code)
	}
	if err != nil {
		res.Refund()
		return err
	}
	if !good {
		return errNoFactor
	}
	res.Refund()
	if recovery {
		s.recoveryUsed(ctx, u.ID)
	}
	return nil
}

// afterSignIn sends a browser that just signed u in with a password (or a
// reset or sign-up) to next, or to the setup page while auth.require_2fa
// wants a second factor from them first: next is often a public page, which
// would show them as signed out until then.
func (s *Service) afterSignIn(w http.ResponseWriter, r *http.Request, u *User, next string) {
	if s.policyCovers(u.Role) {
		if has, err := s.HasMFA(r.Context(), u.ID); err == nil && !has {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, web.SetupPath, http.StatusSeeOther)
			return
		}
	}
	redirect(w, r, next)
}

// registerTOTP adds the authenticator-app and recovery-code routes. Setting
// up an app works during a forced setup (auth.require_2fa); removing one
// and making new codes need a factor already, so a session in setup has
// no use for them.
func (s *Service) registerTOTP(r *web.Router) {
	r.HandleFunc("POST /login/2fa/code", s.mfaCode, web.Public())
	r.HandleFunc("POST /settings/security/totp", s.sessionOnly(s.totpBegin), web.SignedIn(), web.DuringSetup())
	r.HandleFunc("POST /settings/security/totp/confirm", s.sessionOnly(s.totpConfirm), web.SignedIn(), web.DuringSetup())
	r.HandleFunc("POST /settings/security/totp/remove", s.sessionOnly(s.totpRemove), web.SignedIn())
	r.HandleFunc("POST /settings/security/recovery", s.sessionOnly(s.recoveryNew), web.SignedIn())
}
