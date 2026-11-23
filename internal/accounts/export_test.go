package accounts

import "time"

// TOTPCode is the authenticator-app code for secret at t.
func TOTPCode(secret []byte, t time.Time) string { return totpCode(secret, totpStep(t)) }

// SetTOTPTime fixes the time codes are checked against.
func (s *Service) SetTOTPTime(t time.Time) { s.totpAt.Store(&t) }
