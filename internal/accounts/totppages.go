package accounts

// Settings › Security: setting up and removing an authenticator app, and
// making new recovery codes. Each needs confirmOwner (the password and,
// once the user has one, a second factor).

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"

	"rsc.io/qr"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/web"
)

// totpSetupData is the setup page.
type totpSetupData struct {
	QR      template.URL // data: PNG of the otpauth URI
	QRSize  int          // pixels on a side
	Secret  string       // base32, for typing in by hand
	Account string       // how the app lists it
	Error   string
}

// securityFailure shows the security page with why a change was refused
// (an *errReauth); prefix says what didn't happen.
func (s *Service) securityFailure(w http.ResponseWriter, r *http.Request, u *User, err error, prefix string) {
	var re *errReauth
	if !errors.As(err, &re) {
		s.serverError(w, r, err)
		return
	}
	s.securityForm(w, r, re.status, u, &securityData{Error: prefix + re.msg})
}

// totpBegin handles POST /settings/security/totp. After confirmOwner it
// makes a secret, keeps it sealed in this browser's register challenge and
// shows it. Nothing is stored for the user until totpConfirm checks a code
// from it, so an app already set up keeps working meanwhile.
func (s *Service) totpBegin(w http.ResponseWriter, r *http.Request) {
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	if err := s.confirmOwner(r, u, r.PostFormValue("current"), s.passwordCounts(u)); err != nil {
		s.securityFailure(w, r, u, err, "Not set up. ")
		return
	}
	secret := make([]byte, totpSecretLen)
	_, _ = rand.Read(secret) // crypto/rand.Read never fails
	sealed, err := s.totpSeal.Seal(u.ID, secret)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.newChallenge(r.Context(), w, r, ChallengeRegister, u.ID, challengeData{TOTPSecret: sealed}, ""); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.totpSetupPage(w, r, http.StatusOK, u, secret, "")
}

// totpSetupPage shows secret as a QR code and as text, with the code field.
func (s *Service) totpSetupPage(w http.ResponseWriter, r *http.Request, status int, u *User, secret []byte, msg string) {
	site := s.d.SiteName()
	code, err := qr.Encode(totpURI(site, u.Username, secret), qr.M)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	code.Scale = 4
	d := totpSetupData{
		QR:      template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())), // #nosec G203 -- a PNG encoded here
		QRSize:  (code.Size + 8) * code.Scale,                                                           // the PNG has a 4-module quiet zone
		Secret:  totpEncoding.EncodeToString(secret),
		Account: site + ":" + u.Username,
		Error:   msg,
	}
	s.page(w, r, status, "settings/totp", web.Page{Title: "Authenticator app", Nav: "settings", Scripts: []string{"js/code-input.js"}, NoIndex: true, NoReferrer: true, Data: d})
}

// totpConfirm handles POST /settings/security/totp/confirm: a code from the
// pending secret turns the app on. A wrong code shows the same secret
// again, up to the challenge's attempt limit. The challenge is taken
// before anything is stored, so parallel confirms store it once.
func (s *Service) totpConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	expired := func(msg string) {
		s.securityForm(w, r, http.StatusConflict, u, &securityData{Error: msg})
	}
	c, err := s.peekChallenge(ctx, r, ChallengeRegister)
	if err == nil && (c.UserID != u.ID || len(c.Data.TOTPSecret) == 0) {
		err = ErrNoChallenge // another user's, or a passkey registration
	}
	if errors.Is(err, ErrNoChallenge) {
		expired("The setup expired. Start again.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	secret, _, err := s.totpSeal.Open(u.ID, c.Data.TOTPSecret)
	if err != nil {
		expired("The setup expired. Start again.")
		return
	}
	step, good := totpMatch(secret, r.PostFormValue("code"), s.totpNow())
	if !good {
		if err := s.chargeAttempt(ctx, w, r, ChallengeRegister); errors.Is(err, ErrNoChallenge) {
			expired("Too many wrong codes. Start again.")
			return
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
		s.totpSetupPage(w, r, http.StatusUnprocessableEntity, u, secret, "That code didn't work. Check that the time on your device is right, then try the next code.")
		return
	}
	if _, err := s.takeChallenge(ctx, w, r, ChallengeRegister); errors.Is(err, ErrNoChallenge) {
		expired("The setup expired. Start again.")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	sealed, err := s.totpSeal.Seal(u.ID, secret)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var codes []string
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		now := s.store.now()
		var (
			err      error
			replaced bool
		)
		if codes, err = s.firstFactorTx(ctx, tx, u.ID, p.SessionID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_totp WHERE user_id = ?)`, u.ID).Scan(&replaced); err != nil {
			return err
		}
		// The setup code's step counts as used.
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_totp (user_id, secret, last_step, created_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET secret = excluded.secret, last_step = excluded.last_step, created_at = excluded.created_at`,
			u.ID, sealed, step, db.Millis(now)); err != nil {
			return err
		}
		a := Actor(r)
		a.Action, a.Target = "mfa.totp_enabled", userTarget(u.ID)
		if replaced {
			a.Details = map[string]any{"replaced": true}
		}
		return auditTx(ctx, tx, a, now)
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.securityNotice(ctx, u, SecurityTOTPAdded, "")
	if p.NeedsSetup {
		// The setup is done; the page shows the site again.
		cp := *p
		cp.NeedsSetup = false
		r = r.WithContext(auth.WithPrincipal(ctx, &cp))
	}
	s.securityForm(w, r, http.StatusOK, u, &securityData{Notice: "Authenticator app set up.", NewCodes: codes})
}

// totpRemove handles POST /settings/security/totp/remove. It is refused when
// the app is the last factor of a user auth.require_2fa covers. Without any
// factor left, the recovery codes go too: they stand in for one.
func (s *Service) totpRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	if err := s.confirmOwner(r, u, r.PostFormValue("current"), s.passwordCounts(u)); err != nil {
		s.securityFailure(w, r, u, err, "Not removed. ")
		return
	}
	removed := false
	err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := s.mayRemoveFactorTx(ctx, tx, u, func(f Factors) Factors { f.TOTP = false; return f }); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM user_totp WHERE user_id = ?`, u.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		removed = true
		f, err := factorsQ(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if !f.Any() {
			if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, u.ID); err != nil {
				return err
			}
		}
		a := Actor(r)
		a.Action, a.Target = "mfa.totp_removed", userTarget(u.ID)
		return auditTx(ctx, tx, a, s.store.now())
	})
	if err != nil {
		s.securityFailure(w, r, u, err, "Not removed. ")
		return
	}
	if removed {
		s.securityNotice(ctx, u, SecurityTOTPRemoved, "")
	}
	s.securityForm(w, r, http.StatusOK, u, &securityData{Notice: "Authenticator app removed."})
}

// recoveryNew handles POST /settings/security/recovery: a new set of
// recovery codes, shown once; the old ones stop working.
func (s *Service) recoveryNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	if err := s.confirmOwner(r, u, r.PostFormValue("current"), s.passwordCounts(u)); err != nil {
		s.securityFailure(w, r, u, err, "No new codes. ")
		return
	}
	f, err := s.store.Factors(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !f.Any() {
		s.securityForm(w, r, http.StatusConflict, u, &securityData{Error: "Recovery codes need a passkey or an authenticator app first."})
		return
	}
	codes, err := s.store.ReplaceRecoveryCodes(ctx, u.ID, Actor(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.securityNotice(ctx, u, SecurityRecoveryReplaced, "")
	s.securityForm(w, r, http.StatusOK, u, &securityData{Notice: "New recovery codes made. The old ones no longer work.", NewCodes: codes})
}
