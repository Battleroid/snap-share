package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/web"
)

// userView is a user as the pages show it.
type userView struct {
	ID          int64
	Username    string
	Email       string
	Verified    bool
	Role        string
	Status      string
	StatusBadge string
	Active      bool
	Self        bool
	SSO         bool
	Items       int
	Secrets     int
	Used        string
	Quota       string // effective quota, "no limit" for 0
	QuotaSite   bool   // the quota is the site default
	MaxUpload   string
	UploadSite  bool
	Created     string
	LastLogin   string
}

func (a *Admin) siteLimits() (quota, maxUpload int64) {
	return items.DefaultQuotaBytes.Get(a.d.Settings), items.MaxUploadBytes.Get(a.d.Settings)
}

func quotaText(n int64) string {
	if n <= 0 {
		return "no limit"
	}
	return web.HumanBytes(n)
}

func (a *Admin) userView(r *http.Request, u *accounts.UserRow, itemCount, secretCount int) userView {
	siteQuota, siteMax := a.siteLimits()
	v := userView{
		ID: u.ID, Username: u.Username, Email: u.Email, Verified: u.EmailVerifiedAt != nil, Role: string(u.Role),
		Status: u.Status, Active: u.Active(), SSO: u.SSOLinked, Items: itemCount, Secrets: secretCount,
		Used: web.HumanBytes(u.UsedBytes), Created: stamp(&u.CreatedAt), LastLogin: stamp(u.LastLoginAt),
	}
	if p := auth.FromContext(r.Context()); p != nil && p.UserID == u.ID {
		v.Self = true
	}
	v.StatusBadge = "ok"
	if !v.Active {
		v.StatusBadge = "paused"
	}
	quota := siteQuota
	if u.QuotaBytes != nil {
		quota = *u.QuotaBytes
	} else {
		v.QuotaSite = true
	}
	v.Quota = quotaText(quota)
	maxUp := siteMax
	if u.MaxUploadBytes != nil && *u.MaxUploadBytes < siteMax {
		maxUp = *u.MaxUploadBytes
	}
	v.UploadSite = u.MaxUploadBytes == nil
	v.MaxUpload = web.HumanBytes(maxUp)
	return v
}

// counts returns item and secret counts for the users.
func (a *Admin) counts(ctx context.Context, ids []int64) (map[int64]int, map[int64]int, error) {
	ic, err := a.d.Items.CountByOwner(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	sc, err := a.d.Secrets.CountByOwner(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	return ic, sc, nil
}

type usersData struct {
	base
	Q     string
	Rows  []userView
	Pager pager
}

func (a *Admin) usersPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	page, offset := pageOf(r)
	list, total, err := a.d.Accounts.Store().ListUsers(ctx, accounts.UserFilter{Query: q.Get("q"), Limit: perPage, Offset: offset})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ids := make([]int64, len(list))
	for i, u := range list {
		ids[i] = u.ID
	}
	ic, sc, err := a.counts(ctx, ids)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d := usersData{base: a.base(r, "/admin/users"), Q: q.Get("q")}
	for i := range list {
		d.Rows = append(d.Rows, a.userView(r, &list[i], ic[list[i].ID], sc[list[i].ID]))
	}
	d.Pager = newPager("/admin/users", q, page, total, len(list))
	a.page(w, r, http.StatusOK, "admin/users", "Users", d)
}

type identityView struct{ Provider, Username, LastLogin string }

type userData struct {
	base
	U                userView
	Identities       []identityView
	Quota, MaxUpload string // form values; "" is the site default
	SiteQuota        string
	SiteMaxUpload    string
	Errors           map[string]string
	Link             string // set-password link, shown once
	PasswordLogin    bool
	RoleOptions      []map[string]any
}

var roleOptions = []map[string]any{{"value": "user", "label": "user"}, {"value": "admin", "label": "admin"}}

// loadUser builds the user page for id; ok is false (and a 404 written) when there is no such user.
func (a *Admin) loadUser(w http.ResponseWriter, r *http.Request, id int64) (*userData, bool) {
	ctx := r.Context()
	u, err := a.d.Accounts.Store().UserRow(ctx, id)
	if errors.Is(err, accounts.ErrNotFound) {
		a.d.Errors.NotFound(w, r)
		return nil, false
	}
	if err != nil {
		a.fail(w, r, err)
		return nil, false
	}
	ic, sc, err := a.counts(ctx, []int64{id})
	if err != nil {
		a.fail(w, r, err)
		return nil, false
	}
	ids, err := a.d.Accounts.Store().Identities(ctx, id)
	if err != nil {
		a.fail(w, r, err)
		return nil, false
	}
	siteQuota, siteMax := a.siteLimits()
	d := &userData{
		base: a.base(r, "/admin/users"), U: a.userView(r, u, ic[id], sc[id]), Errors: map[string]string{},
		SiteQuota: quotaText(siteQuota), SiteMaxUpload: web.HumanBytes(siteMax),
		PasswordLogin: accounts.PasswordLogin.Get(a.d.Settings), RoleOptions: roleOptions,
	}
	if r.Method != http.MethodGet {
		d.Back = fmt.Sprintf("/admin/users/%d", id)
	}
	if u.QuotaBytes != nil {
		d.Quota = formatSize(*u.QuotaBytes)
	}
	if u.MaxUploadBytes != nil {
		d.MaxUpload = formatSize(*u.MaxUploadBytes)
	}
	for _, x := range ids {
		d.Identities = append(d.Identities, identityView{Provider: x.Provider, Username: dash(x.Username), LastLogin: stamp(x.LastLoginAt)})
	}
	return d, true
}

func (a *Admin) renderUser(w http.ResponseWriter, r *http.Request, status int, d *userData) {
	a.page(w, r, status, "admin/user", d.U.Username, d)
}

func (a *Admin) userPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.d.Errors.NotFound(w, r)
		return
	}
	if d, ok := a.loadUser(w, r, id); ok {
		a.renderUser(w, r, http.StatusOK, d)
	}
}

// userFailure renders the user page with an error for a failed action.
func (a *Admin) userFailure(w http.ResponseWriter, r *http.Request, id int64, err error) {
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		a.d.Errors.NotFound(w, r)
	case isLastAdmin(err):
		if d, ok := a.loadUser(w, r, id); ok {
			d.Error = msgLastAdmin
			a.renderUser(w, r, http.StatusConflict, d)
		}
	case errors.Is(err, errPasswordOff):
		if d, ok := a.loadUser(w, r, id); ok {
			d.Error = msgPasswordOff
			a.renderUser(w, r, http.StatusConflict, d)
		}
	default:
		a.fail(w, r, err)
	}
}

func (a *Admin) setRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	role := auth.Role(r.PostFormValue("role"))
	if !ok || (role != auth.RoleAdmin && role != auth.RoleUser) {
		a.d.Errors.Error(w, r, http.StatusBadRequest, "Pick a role.")
		return
	}
	if err := keyMayGrant(r, role); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.d.Accounts.Store().SetRoleChecked(r.Context(), id, role, accounts.Actor(r)); err != nil {
		a.userFailure(w, r, id, err)
		return
	}
	redirect(w, r, back(r, fmt.Sprintf("/admin/users/%d", id), "role"))
}

func (a *Admin) setStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	status := r.PostFormValue("status")
	if !ok || (status != accounts.StatusActive && status != accounts.StatusDisabled) {
		a.d.Errors.Error(w, r, http.StatusBadRequest, "Pick a status.")
		return
	}
	if err := a.d.Accounts.Store().SetStatusChecked(r.Context(), id, status, accounts.Actor(r)); err != nil {
		a.userFailure(w, r, id, err)
		return
	}
	code := "enabled"
	if status == accounts.StatusDisabled {
		code = "disabled"
	}
	redirect(w, r, back(r, fmt.Sprintf("/admin/users/%d", id), code))
}

// parseLimit reads an optional size: "" is the site default (nil).
func parseLimit(s string, zeroOK bool) (*int64, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ""
	}
	n, err := parseSize(s)
	if err != nil {
		return nil, "Use a size like 500 MB or 2 GiB."
	}
	if n == 0 && !zeroOK {
		return nil, "Use a size above 0, or leave it blank for the site default."
	}
	return &n, ""
}

func (a *Admin) setLimits(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.d.Errors.NotFound(w, r)
		return
	}
	quota, qErr := parseLimit(r.PostFormValue("quota"), true)
	maxUp, mErr := parseLimit(r.PostFormValue("max_upload"), false)
	if qErr != "" || mErr != "" {
		d, ok := a.loadUser(w, r, id)
		if !ok {
			return
		}
		d.Quota, d.MaxUpload = r.PostFormValue("quota"), r.PostFormValue("max_upload")
		d.Errors["quota"], d.Errors["max_upload"] = qErr, mErr
		a.renderUser(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	if err := a.d.Accounts.Store().SetLimits(r.Context(), id, quota, maxUp, accounts.Actor(r)); err != nil {
		a.userFailure(w, r, id, err)
		return
	}
	redirect(w, r, back(r, fmt.Sprintf("/admin/users/%d", id), "limits"))
}

var errPasswordOff = errors.New("password sign-in is off")

const msgPasswordOff = "Password sign-in is off, so a set-password link would not work."

// resetLinkTTL is how long admin-issued set-password links work.
const resetLinkTTL = accounts.AdminResetTTL

// issueLink creates a set-password link for u and emails it when mail is
// set up and u has an address (kind is the template). It returns the link
// only when it was not emailed.
func (a *Admin) issueLink(r *http.Request, u *accounts.User, kind string) (link string, sent bool, err error) {
	ctx := r.Context()
	if !a.passwordLinks() {
		return "", false, errPasswordOff
	}
	if err := a.keyMayLink(r, u.Email, true); err != nil {
		return "", false, err
	}
	link, err = a.d.Accounts.ResetLink(ctx, u.ID, resetLinkTTL)
	if err != nil {
		return "", false, err
	}
	e := accounts.Actor(r)
	e.Action, e.Target, e.Details = "user.reset_link", fmt.Sprintf("user:%d", u.ID), map[string]any{"via": "admin"}
	if u.Email != "" && a.d.Accounts.MailEnabled() {
		by := ""
		if p := auth.FromContext(ctx); p != nil {
			by = p.Username
		}
		if err := a.d.Accounts.SendMail(ctx, kind, u.Email, mail.Data{URL: link, Expires: "24 hours", Username: u.Username, By: by}); err == nil {
			sent = true
			e.Details["emailed"] = true
		}
	}
	if err := a.d.Accounts.Store().Audit(ctx, e); err != nil {
		return "", false, err
	}
	if sent {
		return "", true, nil
	}
	if viaKey(r) {
		return "", false, errKeyMail
	}
	return link, false, nil
}

func (a *Admin) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.d.Errors.NotFound(w, r)
		return
	}
	u, err := a.d.Accounts.Store().User(r.Context(), id)
	if err != nil {
		a.userFailure(w, r, id, err)
		return
	}
	link, sent, err := a.issueLink(r, u, mail.KindReset)
	if err != nil {
		a.userFailure(w, r, id, err)
		return
	}
	if sent {
		redirect(w, r, back(r, fmt.Sprintf("/admin/users/%d", id), "reset_sent"))
		return
	}
	d, ok := a.loadUser(w, r, id)
	if !ok {
		return
	}
	d.Link = link
	a.renderUser(w, r, http.StatusOK, d)
}

// deletion is what deleting a user removed.
type deletion struct {
	Items   int `json:"items"`
	Secrets int `json:"secrets"`
}

// removeUser deletes a user with their items and secrets, files included.
// The account is disabled first, so nothing new arrives while it is emptied.
func (a *Admin) removeUser(r *http.Request, id int64) (deletion, error) {
	ctx := r.Context()
	st := a.d.Accounts.Store()
	if last, err := st.IsLastActiveAdmin(ctx, id); err != nil || last {
		if last {
			err = accounts.ErrLastAdmin
		}
		return deletion{}, err
	}
	u, err := st.User(ctx, id)
	if err != nil {
		return deletion{}, err
	}
	if u.Active() {
		if err := st.SetStatusChecked(ctx, id, accounts.StatusDisabled, accounts.Actor(r)); err != nil {
			return deletion{}, err
		}
	}
	counts, err := a.d.Items.CountByOwner(ctx, []int64{id})
	if err != nil {
		return deletion{}, err
	}
	var del deletion
	del.Items = counts[id]
	if err := a.d.Items.DeleteOwnedBy(ctx, id); err != nil {
		return deletion{}, fmt.Errorf("delete items: %w", err)
	}
	if del.Secrets, err = a.d.Secrets.RevokeOwnedBy(ctx, id); err != nil {
		return deletion{}, fmt.Errorf("burn secrets: %w", err)
	}
	e := accounts.Actor(r)
	e.Details = map[string]any{"items": del.Items, "secrets": del.Secrets}
	if err := st.DeleteUser(ctx, id, e); err != nil {
		return deletion{}, err
	}
	return del, nil
}

func (a *Admin) deleteUserForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.d.Errors.NotFound(w, r)
		return
	}
	if _, err := a.removeUser(r, id); err != nil {
		a.userFailure(w, r, id, err)
		return
	}
	redirect(w, r, "/admin/users?done=deleted")
}

type newUserData struct {
	base
	Username, Email, Role string
	MailEnabled           bool
	PasswordLogin         bool
	RoleOptions           []map[string]any
	Errors                map[string]string
}

func (a *Admin) newUserData(r *http.Request) *newUserData {
	return &newUserData{
		base: a.base(r, "/admin/users"), Role: string(auth.RoleUser), MailEnabled: a.d.Accounts.MailEnabled(),
		PasswordLogin: accounts.PasswordLogin.Get(a.d.Settings), RoleOptions: roleOptions, Errors: map[string]string{},
	}
}

func (a *Admin) newUserPage(w http.ResponseWriter, r *http.Request) {
	a.page(w, r, http.StatusOK, "admin/user-new", "New user", a.newUserData(r))
}

// newAccount is a user to create.
type newAccount struct {
	Username, Email, Role string
}

// createAccount validates and creates an account; errs holds field messages.
// With password sign-in on, it also issues a set-password link: emailed
// when it can be, otherwise returned.
func (a *Admin) createAccount(r *http.Request, in newAccount) (u *accounts.User, link string, sent bool, errs map[string]string, err error) {
	errs = map[string]string{}
	nu := accounts.NewUser{Username: strings.TrimSpace(in.Username), Role: auth.Role(in.Role)}
	if accounts.ValidUsername(nu.Username) != nil {
		errs["username"] = "Use 1–32 letters, digits, '.', '_' or '-', starting with a letter or digit."
	}
	if e := strings.TrimSpace(in.Email); e != "" {
		if nu.Email, err = accounts.NormalizeEmail(e); err != nil {
			errs["email"] = "Enter a valid email address."
		}
		nu.EmailVerified = true // the admin vouches for it, as with the CLI
	}
	if nu.Role == "" {
		nu.Role = auth.RoleUser
	}
	if nu.Role != auth.RoleUser && nu.Role != auth.RoleAdmin {
		errs["role"] = "Pick a role."
	}
	if len(errs) > 0 {
		return nil, "", false, errs, nil
	}
	if err := keyMayGrant(r, nu.Role); err != nil {
		return nil, "", false, nil, err
	}
	if err := a.keyMayLink(r, nu.Email, a.passwordLinks()); err != nil {
		return nil, "", false, nil, err
	}
	u, err = a.d.Accounts.Store().CreateUser(r.Context(), nu, accounts.Actor(r))
	switch {
	case errors.Is(err, accounts.ErrUsernameTaken):
		errs["username"] = "That username is taken."
		return nil, "", false, errs, nil
	case errors.Is(err, accounts.ErrEmailTaken):
		errs["email"] = "Another account uses that address."
		return nil, "", false, errs, nil
	case err != nil:
		return nil, "", false, nil, err
	}
	link, sent, err = a.issueLink(r, u, mail.KindInvite)
	if errors.Is(err, errPasswordOff) {
		err = nil
	}
	return u, link, sent, nil, err
}

func (a *Admin) createUser(w http.ResponseWriter, r *http.Request) {
	in := newAccount{Username: r.PostFormValue("username"), Email: r.PostFormValue("email"), Role: r.PostFormValue("role")}
	u, link, sent, errs, err := a.createAccount(r, in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if len(errs) > 0 {
		d := a.newUserData(r)
		d.Username, d.Email, d.Role, d.Errors = in.Username, in.Email, in.Role, errs
		a.page(w, r, http.StatusUnprocessableEntity, "admin/user-new", "New user", d)
		return
	}
	d, ok := a.loadUser(w, r, u.ID)
	if !ok {
		return
	}
	switch {
	case sent:
		d.Notice = "Created " + u.Username + ". A link to set a password was sent to " + u.Email + "."
	case link != "":
		d.Notice, d.Link = "Created "+u.Username+".", link
	default:
		d.Notice = "Created " + u.Username + ". Password sign-in is off, so they can't sign in until it is on."
	}
	a.renderUser(w, r, http.StatusCreated, d)
}
