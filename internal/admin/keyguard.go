package admin

import (
	"net/http"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

// An API key with the admin scope can do what the admin pages and API do,
// but it never gets a way into a browser session: set-password and invite
// links are only emailed when a key asks for them (never shown or
// returned), and a key can't give the admin role. Otherwise a key could
// create an admin account, take its link and sign in as an admin, with the
// powers keys are refused (creating keys, account settings).

var (
	errKeyLink = web.Errorf(http.StatusForbidden,
		"API keys don't get set-password or invite links. Use the admin pages, or give an email address (with email set up, the link is sent there).")
	errKeyAdminRole = web.Errorf(http.StatusForbidden, "API keys can't give the admin role. Use the admin pages.")
	errKeyMail      = web.Errorf(http.StatusBadGateway, "The email with the link could not be sent. Try again, or use the admin pages.")
)

func viaKey(r *http.Request) bool { return auth.FromContext(r.Context()).ViaKey() }

// keyMayGrant refuses the admin role to API keys.
func keyMayGrant(r *http.Request, role auth.Role) error {
	if viaKey(r) && role == auth.RoleAdmin {
		return errKeyAdminRole
	}
	return nil
}

// keyMayLink refuses an API key a link that could not be emailed to email.
// needed is false when no link would be made at all.
func (a *Admin) keyMayLink(r *http.Request, email string, needed bool) error {
	if viaKey(r) && needed && (email == "" || !a.d.Accounts.MailEnabled()) {
		return errKeyLink
	}
	return nil
}

// passwordLinks reports whether set-password links are issued (password sign-in is on).
func (a *Admin) passwordLinks() bool { return accounts.PasswordLogin.Get(a.d.Settings) }
