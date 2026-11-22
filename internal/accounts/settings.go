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
	// PasswordLogin allows signing in with a password. When off, only SSO works.
	PasswordLogin = settings.Define("auth.password_login", true, nil)
)
