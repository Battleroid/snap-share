package admin

// JSON admin API under /api/v1/admin, for scripting. See docs/api.md.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/web"
)

func (a *Admin) registerAPI(r *web.Router) {
	adm := web.Admin()
	r.HandleFunc("GET /api/v1/admin/users", a.apiUsers, adm)
	r.HandleFunc("POST /api/v1/admin/users", a.apiCreateUser, adm)
	r.HandleFunc("GET /api/v1/admin/users/{id}", a.apiUser, adm)
	r.HandleFunc("PATCH /api/v1/admin/users/{id}", a.apiPatchUser, adm)
	r.HandleFunc("DELETE /api/v1/admin/users/{id}", a.apiDeleteUser, adm)
	r.HandleFunc("POST /api/v1/admin/users/{id}/reset", a.apiResetUser, adm)
	r.HandleFunc("POST /api/v1/admin/secrets/burn", a.apiBurn, adm)
	r.HandleFunc("GET /api/v1/admin/audit", a.apiAudit, adm)
}

const maxAPIBody = 16 << 10

func (a *Admin) apiError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *web.APIError
	switch {
	case errors.As(err, &ae):
		a.d.Errors.Error(w, r, ae.Status, ae.Msg)
	case errors.Is(err, accounts.ErrNotFound):
		a.d.Errors.Error(w, r, http.StatusNotFound, "No such user.")
	case isLastAdmin(err):
		a.d.Errors.Error(w, r, http.StatusConflict, msgLastAdmin)
	case errors.Is(err, errPasswordOff):
		a.d.Errors.Error(w, r, http.StatusConflict, msgPasswordOff)
	default:
		a.fail(w, r, err)
	}
}

// userJSON is a user in API responses.
type userJSON struct {
	ID             int64   `json:"id"`
	Username       string  `json:"username"`
	Email          string  `json:"email"`
	EmailVerified  bool    `json:"email_verified"`
	Role           string  `json:"role"`
	Status         string  `json:"status"`
	SSO            bool    `json:"sso"`
	Items          int     `json:"items"`
	Secrets        int     `json:"secrets"`
	UsedBytes      int64   `json:"used_bytes"`
	QuotaBytes     *int64  `json:"quota_bytes"`      // null: site default
	MaxUploadBytes *int64  `json:"max_upload_bytes"` // null: site default
	CreatedAt      string  `json:"created_at"`
	LastLoginAt    *string `json:"last_login_at"`
}

func toJSON(u *accounts.UserRow, items, secrets int) userJSON {
	j := userJSON{
		ID: u.ID, Username: u.Username, Email: u.Email, EmailVerified: u.EmailVerifiedAt != nil, Role: string(u.Role),
		Status: u.Status, SSO: u.SSOLinked, Items: items, Secrets: secrets, UsedBytes: u.UsedBytes,
		QuotaBytes: u.QuotaBytes, MaxUploadBytes: u.MaxUploadBytes, CreatedAt: u.CreatedAt.UTC().Format(time.RFC3339),
	}
	if u.LastLoginAt != nil {
		s := u.LastLoginAt.UTC().Format(time.RFC3339)
		j.LastLoginAt = &s
	}
	return j
}

func (a *Admin) userJSON(r *http.Request, id int64) (userJSON, error) {
	u, err := a.d.Accounts.Store().UserRow(r.Context(), id)
	if err != nil {
		return userJSON{}, err
	}
	ic, sc, err := a.counts(r.Context(), []int64{id})
	if err != nil {
		return userJSON{}, err
	}
	return toJSON(u, ic[id], sc[id]), nil
}

// limitOffset reads ?limit (1–200, default 50) and ?offset.
func limitOffset(r *http.Request) (int, int, error) {
	limit, offset := perPage, 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return 0, 0, web.Errorf(http.StatusBadRequest, "limit must be 1 to 200.")
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, 0, web.Errorf(http.StatusBadRequest, "offset must be 0 or more.")
		}
		offset = n
	}
	return limit, offset, nil
}

func (a *Admin) apiUsers(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := limitOffset(r)
	if err != nil {
		a.apiError(w, r, err)
		return
	}
	list, total, err := a.d.Accounts.Store().ListUsers(r.Context(), accounts.UserFilter{Query: r.URL.Query().Get("q"), Limit: limit, Offset: offset})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ids := make([]int64, len(list))
	for i, u := range list {
		ids[i] = u.ID
	}
	ic, sc, err := a.counts(r.Context(), ids)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]userJSON, 0, len(list))
	for i := range list {
		out = append(out, toJSON(&list[i], ic[list[i].ID], sc[list[i].ID]))
	}
	web.JSON(w, http.StatusOK, map[string]any{"users": out, "total": total})
}

func (a *Admin) apiUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.apiError(w, r, accounts.ErrNotFound)
		return
	}
	u, err := a.userJSON(r, id)
	if err != nil {
		a.apiError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, u)
}

// fieldError joins form-style field messages into one API error.
func fieldError(errs map[string]string) error {
	var parts []string
	for _, k := range []string{"username", "email", "role", "quota_bytes", "max_upload_bytes"} {
		if m := errs[k]; m != "" {
			parts = append(parts, k+": "+m)
		}
	}
	return web.Errorf(http.StatusUnprocessableEntity, "%s", strings.Join(parts, " "))
}

type createReq struct {
	Username       string `json:"username"`
	Email          string `json:"email"`
	Role           string `json:"role"`
	QuotaBytes     *int64 `json:"quota_bytes"`
	MaxUploadBytes *int64 `json:"max_upload_bytes"`
}

func checkLimits(quota, maxUpload *int64) map[string]string {
	errs := map[string]string{}
	if quota != nil && *quota < 0 {
		errs["quota_bytes"] = "Use 0 (no limit) or more."
	}
	if maxUpload != nil && *maxUpload <= 0 {
		errs["max_upload_bytes"] = "Use a size above 0, or null for the site default."
	}
	return errs
}

func (a *Admin) apiCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := web.DecodeJSON(w, r, maxAPIBody, &req); err != nil {
		a.apiError(w, r, err)
		return
	}
	if errs := checkLimits(req.QuotaBytes, req.MaxUploadBytes); len(errs) > 0 {
		a.apiError(w, r, fieldError(errs))
		return
	}
	u, link, sent, errs, err := a.createAccount(r, newAccount{Username: req.Username, Email: req.Email, Role: req.Role})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if len(errs) > 0 {
		a.apiError(w, r, fieldError(errs))
		return
	}
	if req.QuotaBytes != nil || req.MaxUploadBytes != nil {
		if err := a.d.Accounts.Store().SetLimits(r.Context(), u.ID, req.QuotaBytes, req.MaxUploadBytes, accounts.Actor(r)); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	j, err := a.userJSON(r, u.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := map[string]any{"user": j, "emailed": sent}
	if link != "" {
		out["link"] = link
	}
	web.JSON(w, http.StatusCreated, out)
}

// apiPatchUser handles PATCH /api/v1/admin/users/{id}: any of role, status,
// quota_bytes and max_upload_bytes (null for the site default).
func (a *Admin) apiPatchUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		a.apiError(w, r, accounts.ErrNotFound)
		return
	}
	var req map[string]json.RawMessage
	if err := web.DecodeJSON(w, r, maxAPIBody, &req); err != nil {
		a.apiError(w, r, err)
		return
	}
	for k := range req {
		switch k {
		case "role", "status", "quota_bytes", "max_upload_bytes":
		default:
			a.apiError(w, r, web.Errorf(http.StatusBadRequest, "Unknown field %q.", k))
			return
		}
	}
	u, err := a.d.Accounts.Store().UserRow(ctx, id)
	if err != nil {
		a.apiError(w, r, err)
		return
	}
	var role auth.Role
	var status string
	if v, ok := req["role"]; ok {
		if json.Unmarshal(v, &role) != nil || (role != auth.RoleAdmin && role != auth.RoleUser) {
			a.apiError(w, r, web.Errorf(http.StatusUnprocessableEntity, `role must be "user" or "admin".`))
			return
		}
		if err := keyMayGrant(r, role); err != nil {
			a.apiError(w, r, err)
			return
		}
	}
	if v, ok := req["status"]; ok {
		if json.Unmarshal(v, &status) != nil || (status != accounts.StatusActive && status != accounts.StatusDisabled) {
			a.apiError(w, r, web.Errorf(http.StatusUnprocessableEntity, `status must be "active" or "disabled".`))
			return
		}
	}
	quota, maxUp := u.QuotaBytes, u.MaxUploadBytes
	_, setQ := req["quota_bytes"]
	_, setM := req["max_upload_bytes"]
	for k, dst := range map[string]**int64{"quota_bytes": &quota, "max_upload_bytes": &maxUp} {
		if v, ok := req[k]; ok {
			*dst = nil
			if string(v) != "null" {
				var n int64
				if json.Unmarshal(v, &n) != nil {
					a.apiError(w, r, web.Errorf(http.StatusUnprocessableEntity, "%s must be a number of bytes or null.", k))
					return
				}
				*dst = &n
			}
		}
	}
	if errs := checkLimits(quota, maxUp); len(errs) > 0 {
		a.apiError(w, r, fieldError(errs))
		return
	}
	st := a.d.Accounts.Store()
	if role != "" {
		if err := st.SetRoleChecked(ctx, id, role, accounts.Actor(r)); err != nil {
			a.apiError(w, r, err)
			return
		}
	}
	if status != "" {
		if err := st.SetStatusChecked(ctx, id, status, accounts.Actor(r)); err != nil {
			a.apiError(w, r, err)
			return
		}
	}
	if setQ || setM {
		if err := st.SetLimits(ctx, id, quota, maxUp, accounts.Actor(r)); err != nil {
			a.apiError(w, r, err)
			return
		}
	}
	j, err := a.userJSON(r, id)
	if err != nil {
		a.apiError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, j)
}

func (a *Admin) apiDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.apiError(w, r, accounts.ErrNotFound)
		return
	}
	del, err := a.removeUser(r, id)
	if err != nil {
		a.apiError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, map[string]any{"deleted": true, "items": del.Items, "secrets": del.Secrets})
}

func (a *Admin) apiResetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		a.apiError(w, r, accounts.ErrNotFound)
		return
	}
	u, err := a.d.Accounts.Store().User(r.Context(), id)
	if err != nil {
		a.apiError(w, r, err)
		return
	}
	link, sent, err := a.issueLink(r, u, mail.KindReset)
	if err != nil {
		a.apiError(w, r, err)
		return
	}
	out := map[string]any{"emailed": sent}
	if link != "" {
		out["link"] = link
	}
	web.JSON(w, http.StatusOK, out)
}

type burnReq struct {
	Link string `json:"link"`
	ID   string `json:"id"`
}

// apiBurn handles POST /api/v1/admin/secrets/burn with {"link": …} or {"id": …}.
func (a *Admin) apiBurn(w http.ResponseWriter, r *http.Request) {
	var req burnReq
	if err := web.DecodeJSON(w, r, maxAPIBody, &req); err != nil {
		a.apiError(w, r, err)
		return
	}
	ref := req.ID
	if ref == "" {
		ref = req.Link
	}
	id, ok := secretID(ref)
	if !ok {
		a.apiError(w, r, web.Errorf(http.StatusUnprocessableEntity, "%s", msgBadRef))
		return
	}
	state, found, err := a.burn(r.Context(), r, id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !found {
		a.apiError(w, r, web.Errorf(http.StatusNotFound, "No secret has that id."))
		return
	}
	web.JSON(w, http.StatusOK, map[string]string{"id": id, "state": state})
}

type auditJSON struct {
	ID      int64           `json:"id"`
	At      string          `json:"at"`
	Actor   string          `json:"actor"`
	ActorID int64           `json:"actor_id,omitempty"`
	KeyID   int64           `json:"key_id,omitempty"`
	IP      string          `json:"ip"`
	Action  string          `json:"action"`
	Target  string          `json:"target"`
	Details json.RawMessage `json:"details"`
}

func (a *Admin) apiAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := limitOffset(r)
	if err != nil {
		a.apiError(w, r, err)
		return
	}
	f, msg := auditFilter(r.URL.Query())
	if msg != "" {
		a.apiError(w, r, web.Errorf(http.StatusBadRequest, "%s", msg))
		return
	}
	f.Limit, f.Offset = limit, offset
	list, total, err := a.d.Accounts.Store().ListAudit(r.Context(), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]auditJSON, 0, len(list))
	for _, e := range list {
		details := json.RawMessage(e.Details)
		if !json.Valid(details) {
			details = json.RawMessage("{}")
		}
		out = append(out, auditJSON{
			ID: e.ID, At: e.At.UTC().Format(time.RFC3339Nano), Actor: e.ActorName, ActorID: e.ActorUserID, KeyID: e.ActorKeyID,
			IP: e.IP, Action: e.Action, Target: e.Target, Details: details,
		})
	}
	web.JSON(w, http.StatusOK, map[string]any{"entries": out, "total": total})
}
