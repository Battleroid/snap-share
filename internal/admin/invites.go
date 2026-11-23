package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
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
	Name     string // label, email or "open invite"
	Status   string // accounts.Invite.Status
	Badge    string // pb.badge status for an exception on an active invite; "" for plain text
	Done     string // pb.status for a finished invite; "" while active
	Active   bool   // not finished: Revoke
	CopyLink bool   // active, usable and stored: Copy link
	NoLink   bool   // active, but made before links were kept
}

type invitesData struct {
	base
	Closed        bool // sign-up is closed: invite links do not work
	MailEnabled   bool
	Invites       []inviteRow
	Show          string // "active" or "all"
	Any           bool   // some invite exists (an empty active list still shows the table)
	ExpiryOptions []map[string]any
	Errors        map[string]string
	Form          inviteForm

	NewLink   string // the link, after creating an invite or asking for it again (no script)
	NewTitle  string
	NewStatus string // pb.notice status
	NewBody   string
}

type inviteForm struct{ Label, Email, Uses, Expires, Role string }

// Finished invites are muted rows with a quiet status word; on active ones
// only the exceptions get a badge.
var (
	inviteDone   = map[string]string{accounts.InviteUsed: "info", accounts.InviteExpired: "unknown", accounts.InviteRevoked: "down"}
	inviteBadges = map[string]string{accounts.StatusSendFailed: "warn", accounts.StatusUnusable: "warn"}
)

func (a *Admin) invitesData(r *http.Request) (*invitesData, error) {
	ctx := r.Context()
	d := &invitesData{
		base:        a.base(r, "/admin/invites"),
		Closed:      a.d.Accounts.EffectiveSignupMode() == accounts.SignupClosed,
		MailEnabled: a.d.Accounts.MailEnabled(),
		Show:        "active",
		Errors:      map[string]string{},
		Form:        inviteForm{Uses: "1", Expires: "7d", Role: string(auth.RoleUser)},
	}
	st := a.d.Accounts.Store()
	var (
		list []accounts.Invite
		err  error
	)
	if r.URL.Query().Get("show") == "all" {
		d.Show = "all"
		list, err = st.ListInvites(ctx, 100)
	} else {
		list, err = st.ListActiveInvites(ctx, 100)
	}
	if err != nil {
		return nil, err
	}
	d.Any = len(list) > 0
	if !d.Any && d.Show == "active" {
		some, err := st.ListInvites(ctx, 1)
		if err != nil {
			return nil, err
		}
		d.Any = len(some) > 0
	}
	now := a.now()
	for _, inv := range list {
		status := inv.Status(now)
		row := inviteRow{Invite: inv, Name: inv.Label, Status: status, Badge: inviteBadges[status], Done: inviteDone[status], Active: !inv.Finished(now)}
		if row.Name == "" {
			row.Name = inv.Email
		}
		if row.Name == "" {
			row.Name = "open invite"
		}
		row.CopyLink = row.Active && inv.CreatorAdmin && inv.HasLink
		row.NoLink = row.Active && inv.CreatorAdmin && !inv.HasLink
		d.Invites = append(d.Invites, row)
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

// createInvite handles POST /admin/invites and shows the link.
func (a *Admin) createInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	d, err := a.invitesData(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d.Form = inviteForm{
		Label: strings.TrimSpace(r.PostFormValue("label")),
		Email: strings.TrimSpace(r.PostFormValue("email")), Uses: strings.TrimSpace(r.PostFormValue("uses")),
		Expires: r.PostFormValue("expires"), Role: r.PostFormValue("role"),
	}
	ni := accounts.NewInvite{CreatedBy: p.UserID, Role: auth.RoleUser, MaxUses: 1}
	if ni.Label, err = accounts.NormalizeInviteLabel(d.Form.Label); err != nil {
		d.Errors["label"] = "Use at most 80 characters on one line."
	}
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
	link, inv, sent, err := a.d.Accounts.CreateInvite(ctx, ni, accounts.Actor(r), p.Username)
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
	d.NewLink, d.NewStatus, d.NewTitle = link, "ok", "Invite created"
	switch {
	case sent:
		d.NewTitle = "Invite created and sent to " + ni.Email
	case inv.MailError != "":
		d.NewStatus, d.NewBody = "warn", "Email failed — copy the link instead."
	}
	if link == "" && sent {
		d.Notice = "Invite created and sent to " + ni.Email + "."
	}
	a.page(w, r, http.StatusCreated, "admin/invites", "Invites", d)
}

// errKeyInviteLink: API keys never get invite links (keyguard.go).
var errKeyInviteLink = web.Errorf(http.StatusForbidden, "API keys don't get invite links. Use the admin pages.")

// inviteLink handles POST /admin/invites/{id}/link: an active invite's link
// again, audited. admin.js asks for JSON and shows it in a dialog; without
// scripts the page comes back with the link in a CopyField.
func (a *Admin) inviteLink(w http.ResponseWriter, r *http.Request) {
	asJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	fail := func(err *web.APIError) {
		if asJSON {
			web.JSON(w, err.Status, map[string]string{"error": err.Msg})
			return
		}
		a.fail(w, r, err)
	}
	if viaKey(r) {
		fail(errKeyInviteLink)
		return
	}
	id, ok := pathID(r)
	if !ok {
		fail(web.Errorf(http.StatusNotFound, "No such invite."))
		return
	}
	link, err := a.d.Accounts.InviteLink(r.Context(), id, accounts.Actor(r))
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		fail(web.Errorf(http.StatusNotFound, "This invite is no longer active, so its link is gone."))
		return
	case errors.Is(err, accounts.ErrInviteNoLink):
		fail(web.Errorf(http.StatusNotFound, "This invite's link is not stored. Make a new invite instead."))
		return
	case errors.Is(err, auth.ErrUnsealable):
		fail(web.Errorf(http.StatusConflict, "This invite's link can't be read since the secret key changed. Make a new invite instead."))
		return
	case err != nil:
		a.d.Log.Error("admin", "route", r.Pattern, "err", err)
		fail(web.Errorf(http.StatusInternalServerError, "Something went wrong."))
		return
	}
	if asJSON {
		web.JSON(w, http.StatusOK, map[string]string{"url": link})
		return
	}
	d, err := a.invitesData(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d.NewLink, d.NewStatus, d.NewTitle = link, "ok", "Invite link"
	a.page(w, r, http.StatusOK, "admin/invites", "Invites", d)
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
	redirect(w, r, back(r, "/admin/invites", "revoked"))
}

// removeInvite handles POST /admin/invites/{id}/remove: deletes a finished invite.
func (a *Admin) removeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.d.Errors.NotFound(w, r)
		return
	}
	err := a.d.Accounts.Store().RemoveInvite(r.Context(), id, accounts.Actor(r))
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		a.d.Errors.NotFound(w, r)
		return
	case errors.Is(err, accounts.ErrInviteActive):
		a.d.Errors.Error(w, r, http.StatusConflict, "This invite is still active. Revoke it first.")
		return
	case err != nil:
		a.fail(w, r, err)
		return
	}
	redirect(w, r, back(r, "/admin/invites", "invite_removed"))
}
