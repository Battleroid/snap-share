package accounts

import "github.com/Battleroid/snap-share/internal/settings"

// Sign-up modes.
const (
	SignupClosed = "closed" // only admins create accounts (CLI); invites are refused
	SignupInvite = "invite" // invite links only
	SignupOpen   = "open"   // anyone with an email address; needs SMTP
)

// Site settings owned by the accounts package.
var (
	// SignupMode controls self-registration. "open" only takes effect when SMTP
	// is configured; without it the site behaves as "invite".
	SignupMode = settings.Define("auth.signup_mode", SignupInvite, settings.OneOf(SignupClosed, SignupInvite, SignupOpen))
	// PasswordLogin allows local sign-in: a password, or a passkey without
	// one. When off, only SSO works (passkeys still serve as a second step).
	PasswordLogin = settings.Define("auth.password_login", true, nil)
	// Require2FA makes the users it covers set up a second factor (a passkey
	// or an authenticator app) at their next password sign-in, before
	// anything else. SSO sign-ins leave 2FA to the identity provider.
	Require2FA = settings.Define("auth.require_2fa", Require2FAOff, settings.OneOf(Require2FAOff, Require2FAAdmins, Require2FAEveryone))
)
