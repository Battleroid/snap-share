// Package admin serves the admin pages. For now that is the bare site
// settings page: site name, sign-up mode, password sign-in, test email and
// invites. The admin track extends it.
package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// Deps are the admin pages' dependencies.
type Deps struct {
	Log      *slog.Logger
	Settings *settings.Store
	Render   *web.Renderer
	Errors   *web.ErrorPages
	Accounts *accounts.Service
	SiteName *settings.Key[string]
}

// Admin serves /admin.
type Admin struct{ d Deps }

// New returns the admin pages.
func New(d Deps) *Admin { return &Admin{d: d} }

// Register adds the admin routes.
func (a *Admin) Register(r *web.Router) {
	r.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
	}, web.Admin())
	r.HandleFunc("GET /admin/settings", a.settingsPage, web.Admin())
	r.HandleFunc("POST /admin/settings", a.saveSettings, web.Admin())
	r.HandleFunc("POST /admin/settings/test-email", a.testEmail, web.Admin())
	r.HandleFunc("POST /admin/invites", a.createInvite, web.Admin())
	r.HandleFunc("POST /admin/invites/{id}/revoke", a.revokeInvite, web.Admin())
}

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

type pageData struct {
	SiteName      string
	SignupMode    string
	EffectiveMode string
	PasswordLogin bool
	MailEnabled   bool
	OIDCEnabled   bool
	Invites       []inviteRow
	ExpiryOptions []map[string]any
	TestTo        string

	NewLink  string // shown once after creating an invite
	NewSent  bool
	NewEmail string

	Errors map[string]string
	Notice string
	Error  string
}

var savedNotices = map[string]string{
	"settings": "Settings saved.",
	"revoked":  "Invite revoked.",
}

func (a *Admin) data(r *http.Request) (*pageData, error) {
	acc := a.d.Accounts
	invites, err := acc.Store().ListInvites(r.Context(), 50)
	if err != nil {
		return nil, err
	}
	d := &pageData{
		SiteName:      a.d.SiteName.Get(a.d.Settings),
		SignupMode:    accounts.SignupMode.Get(a.d.Settings),
		EffectiveMode: acc.EffectiveSignupMode(),
		PasswordLogin: accounts.PasswordLogin.Get(a.d.Settings),
		MailEnabled:   acc.MailEnabled(),
		OIDCEnabled:   acc.OIDCEnabled(),
		Errors:        map[string]string{},
	}
	now := time.Now()
	for _, inv := range invites {
		st := inv.State(now)
		badge := map[string]string{accounts.InviteActive: "ok", accounts.InviteUsed: "info", accounts.InviteExpired: "unknown", accounts.InviteRevoked: "down"}[st]
		d.Invites = append(d.Invites, inviteRow{Invite: inv, State: st, Badge: badge})
	}
	for _, o := range inviteExpiry {
		d.ExpiryOptions = append(d.ExpiryOptions, map[string]any{"value": o.Value, "label": o.Label})
	}
	if u, err := acc.CurrentUser(r); err == nil {
		d.TestTo = u.Email
	}
	return d, nil
}

func (a *Admin) render(w http.ResponseWriter, r *http.Request, status int, d *pageData) {
	w.Header().Set("Cache-Control", "no-store")
	a.d.Render.Page(w, r, status, "admin/settings", web.Page{Title: "Settings", Nav: "admin", Data: d})
}

func (a *Admin) fail(w http.ResponseWriter, r *http.Request, err error) {
	a.d.Log.Error("admin", "route", r.Pattern, "err", err)
	a.d.Errors.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
}

func (a *Admin) settingsPage(w http.ResponseWriter, r *http.Request) {
	d, err := a.data(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d.Notice = savedNotices[r.URL.Query().Get("saved")]
	a.render(w, r, http.StatusOK, d)
}

// saveSettings handles POST /admin/settings.
func (a *Admin) saveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := a.data(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("site_name"))
	mode := r.PostFormValue("signup_mode")
	password := r.PostFormValue("password_login") != ""

	if name == "" || len(name) > 64 {
		d.Errors["site_name"] = "Use 1–64 characters."
	}
	switch mode {
	case accounts.SignupClosed, accounts.SignupInvite:
	case accounts.SignupOpen:
		if !d.MailEnabled {
			d.Errors["signup_mode"] = "Open sign-up needs SMTP, which is not set up."
		}
	default:
		d.Errors["signup_mode"] = "Pick a sign-up mode."
	}
	if !password && !d.OIDCEnabled {
		d.Errors["password_login"] = "Without SSO configured, nobody could sign in."
	}
	if len(d.Errors) > 0 {
		d.SiteName, d.SignupMode, d.PasswordLogin = name, mode, password
		a.render(w, r, http.StatusUnprocessableEntity, d)
		return
	}

	type change struct {
		key  string
		old  any
		new  any
		save func() error
	}
	changes := []change{
		{a.d.SiteName.Name(), d.SiteName, name, func() error { return a.d.SiteName.Set(ctx, a.d.Settings, name) }},
		{accounts.SignupMode.Name(), d.SignupMode, mode, func() error { return accounts.SignupMode.Set(ctx, a.d.Settings, mode) }},
		{accounts.PasswordLogin.Name(), d.PasswordLogin, password, func() error { return accounts.PasswordLogin.Set(ctx, a.d.Settings, password) }},
	}
	for _, c := range changes {
		if c.old == c.new {
			continue
		}
		if err := c.save(); err != nil {
			a.fail(w, r, err)
			return
		}
		e := accounts.Actor(r)
		e.Action, e.Target, e.Details = "settings.change", c.key, map[string]any{"from": c.old, "to": c.new}
		if err := a.d.Accounts.Store().Audit(ctx, e); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	http.Redirect(w, r, "/admin/settings?saved=settings", http.StatusSeeOther)
}

// testEmail handles POST /admin/settings/test-email.
func (a *Admin) testEmail(w http.ResponseWriter, r *http.Request) {
	d, err := a.data(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	to, err := accounts.NormalizeEmail(r.PostFormValue("to"))
	if err != nil {
		d.TestTo = r.PostFormValue("to")
		d.Errors["to"] = "Enter a valid email address."
		a.render(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	d.TestTo = to
	p := auth.FromContext(r.Context())
	if err := a.d.Accounts.SendMail(r.Context(), mail.KindTest, to, mail.Data{By: p.Username}); err != nil {
		d.Error = "Sending failed: " + err.Error()
		a.render(w, r, http.StatusBadGateway, d)
		return
	}
	d.Notice = "Test email sent to " + to + "."
	a.render(w, r, http.StatusOK, d)
}

// createInvite handles POST /admin/invites and shows the link once.
func (a *Admin) createInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	d, err := a.data(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ni := accounts.NewInvite{CreatedBy: p.UserID, Role: auth.RoleUser, MaxUses: 1}
	if email := strings.TrimSpace(r.PostFormValue("email")); email != "" {
		if ni.Email, err = accounts.NormalizeEmail(email); err != nil {
			d.Errors["email"] = "Enter a valid email address."
		}
	}
	if n, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("uses"))); err != nil || n < 1 || n > accounts.MaxInviteUses {
		d.Errors["uses"] = "Use a number from 1 to 1000."
	} else {
		ni.MaxUses = n
	}
	for _, o := range inviteExpiry {
		if o.Value == r.PostFormValue("expires") {
			ni.TTL = o.TTL
		}
	}
	if ni.TTL == 0 {
		d.Errors["expires"] = "Pick an expiry."
	}
	if r.PostFormValue("role") == string(auth.RoleAdmin) {
		ni.Role = auth.RoleAdmin
	}
	if len(d.Errors) > 0 {
		a.render(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	link, _, sent, err := a.d.Accounts.CreateInvite(ctx, ni, accounts.Actor(r), p.Username)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if d, err = a.data(r); err != nil {
		a.fail(w, r, err)
		return
	}
	d.NewLink, d.NewSent, d.NewEmail = link, sent, ni.Email
	a.render(w, r, http.StatusCreated, d)
}

// revokeInvite handles POST /admin/invites/{id}/revoke.
func (a *Admin) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		a.d.Errors.NotFound(w, r)
		return
	}
	err = a.d.Accounts.Store().RevokeInvite(r.Context(), id, accounts.Actor(r))
	if errors.Is(err, accounts.ErrNotFound) {
		a.d.Errors.NotFound(w, r)
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/settings?saved=revoked", http.StatusSeeOther)
}
