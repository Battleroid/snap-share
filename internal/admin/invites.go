package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
)

// Invite expiry choices on the form.
var inviteExpiry = []struct {
	Value, Label string
	TTL          time.Duration
}{
	{"1d", "1 day", 24 * time.Hour},
	{"7d", "7 days", 7 * 24 * time.Hour},
	{"30d", "30 days", 30 * 24 * time.Hour},
}

type inviteRow struct {
	accounts.Invite
	State string
	Badge string // pb.badge status
}

type invitesData struct {
	base
	Closed        bool // sign-up is closed: invite links do not work
	MailEnabled   bool
	Invites       []inviteRow
	ExpiryOptions []map[string]any
	Errors        map[string]string
	Form          inviteForm

	NewLink  string // shown once after creating an invite
	NewSent  bool
	NewEmail string
}

type inviteForm struct{ Email, Uses, Expires, Role string }

var inviteBadges = map[string]string{
	accounts.InviteActive: "ok", accounts.InviteUsed: "info", accounts.InviteExpired: "unknown", accounts.InviteRevoked: "down",
}

func (a *Admin) invitesData(r *http.Request) (*invitesData, error) {
	list, err := a.d.Accounts.Store().ListInvites(r.Context(), 100)
	if err != nil {
		return nil, err
	}
	d := &invitesData{
		base:        a.base(r, "/admin/invites"),
		Closed:      a.d.Accounts.EffectiveSignupMode() == accounts.SignupClosed,
		MailEnabled: a.d.Accounts.MailEnabled(),
		Errors:      map[string]string{},
		Form:        inviteForm{Uses: "1", Expires: "7d", Role: string(auth.RoleUser)},
	}
	now := a.now()
	for _, inv := range list {
		st := inv.State(now)
		d.Invites = append(d.Invites, inviteRow{Invite: inv, State: st, Badge: inviteBadges[st]})
	}
	for _, o := range inviteExpiry {
		d.ExpiryOptions = append(d.ExpiryOptions, map[string]any{"value": o.Value, "label": o.Label})
	}
	return d, nil
}

func (a *Admin) invitesPage(w http.ResponseWriter, r *http.Request) {
	d, err := a.invitesData(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.page(w, r, http.StatusOK, "admin/invites", "Invites", d)
}

// createInvite handles POST /admin/invites and shows the link once.
func (a *Admin) createInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	d, err := a.invitesData(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d.Form = inviteForm{
		Email: strings.TrimSpace(r.PostFormValue("email")), Uses: strings.TrimSpace(r.PostFormValue("uses")),
		Expires: r.PostFormValue("expires"), Role: r.PostFormValue("role"),
	}
	ni := accounts.NewInvite{CreatedBy: p.UserID, Role: auth.RoleUser, MaxUses: 1}
	if d.Form.Email != "" {
		if ni.Email, err = accounts.NormalizeEmail(d.Form.Email); err != nil {
			d.Errors["email"] = "Enter a valid email address."
		}
	}
	if n, err := strconv.Atoi(d.Form.Uses); err != nil || n < 1 || n > accounts.MaxInviteUses {
		d.Errors["uses"] = "Use a number from 1 to 1000."
	} else {
		ni.MaxUses = n
	}
	for _, o := range inviteExpiry {
		if o.Value == d.Form.Expires {
			ni.TTL = o.TTL
		}
	}
	if ni.TTL == 0 {
		d.Errors["expires"] = "Pick an expiry."
	}
	if d.Form.Role == string(auth.RoleAdmin) {
		ni.Role = auth.RoleAdmin
	}
	if len(d.Errors) > 0 {
		a.page(w, r, http.StatusUnprocessableEntity, "admin/invites", "Invites", d)
		return
	}
	if err := keyMayGrant(r, ni.Role); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.keyMayLink(r, ni.Email, true); err != nil {
		a.fail(w, r, err)
		return
	}
	link, _, sent, err := a.d.Accounts.CreateInvite(ctx, ni, accounts.Actor(r), p.Username)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if viaKey(r) {
		if !sent {
			a.fail(w, r, errKeyMail)
			return
		}
		link = "" // shown only to browser sessions
	}
	if d, err = a.invitesData(r); err != nil {
		a.fail(w, r, err)
		return
	}
	d.NewLink, d.NewSent, d.NewEmail = link, sent, ni.Email
	if link == "" && sent {
		d.Notice = "Invite created and sent to " + ni.Email + "."
	}
	a.page(w, r, http.StatusCreated, "admin/invites", "Invites", d)
}

// revokeInvite handles POST /admin/invites/{id}/revoke.
func (a *Admin) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.d.Errors.NotFound(w, r)
		return
	}
	err := a.d.Accounts.Store().RevokeInvite(r.Context(), id, accounts.Actor(r))
	if errors.Is(err, accounts.ErrNotFound) {
		a.d.Errors.NotFound(w, r)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	redirect(w, r, "/admin/invites?done=revoked")
}
