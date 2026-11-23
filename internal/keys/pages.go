package keys

import (
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

// Notices after a redirect (?done=code). Fixed strings only.
var doneNotices = map[string]string{
	"revoked": "Key revoked.",
}

func (s *Service) page(w http.ResponseWriter, r *http.Request, status int, name, title string, data any, scripts ...string) {
	w.Header().Set("Cache-Control", "no-store")
	s.d.Render.Page(w, r, status, name, web.Page{Title: title, Nav: "settings", Data: data, Scripts: scripts})
}

type keyRow struct {
	ID         int64
	Name       string
	Prefix     string
	Scopes     []auth.Scope
	Created    string
	Expires    string
	LastUsed   string
	Status     string // badge status
	StatusText string
	Revocable  bool
}

func stamp(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

func (s *Service) rows(list []*Key) []keyRow {
	now := s.now()
	out := make([]keyRow, 0, len(list))
	for _, k := range list {
		row := keyRow{
			ID: k.ID, Name: k.Name, Prefix: k.Prefix() + "…", Scopes: k.Scopes,
			Created: k.CreatedAt.UTC().Format("2006-01-02"), Expires: "never", LastUsed: stamp(k.LastUsedAt),
		}
		if k.ExpiresAt != nil {
			row.Expires = stamp(k.ExpiresAt)
		}
		if k.LastUsedAt != nil && k.LastUsedIP != "" {
			row.LastUsed += " · " + k.LastUsedIP
		}
		switch st := k.Status(now); st {
		case StatusRevoked:
			row.Status, row.StatusText = "down", "revoked"
		case StatusExpired:
			row.Status, row.StatusText, row.Revocable = "unknown", "expired", true
		default:
			row.Status, row.StatusText, row.Revocable = "ok", "active", true
		}
		out = append(out, row)
	}
	return out
}

// listPage handles GET /settings/keys.
func (s *Service) listPage(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	list, err := s.store.List(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "keys/list", "API keys", map[string]any{
		"Keys": s.rows(list), "Notice": doneNotices[r.URL.Query().Get("done")],
	})
}

type scopeChoice struct {
	Name    auth.Scope
	Checked bool
}

// formView is the create form, filled in again after a failed submit.
type formView struct {
	Name         string
	Scopes       []scopeChoice
	Expires      string
	ExpiresOn    string
	CIDRs        string
	Visibility   string
	MaxExpiry    string
	MaxUploadMB  string
	Restricted   bool // open the Restrictions disclosure
	Errors       FieldErrors
	Error        string
	Expiries     []map[string]any
	MaxExpiries  []map[string]any
	Visibilities []map[string]any
	Today        string
	// Proof is set for an admin with two-factor authentication: the admin
	// scope then needs their password and a second factor too.
	Proof *accounts.Proof
}

// options converts choices for the pb.select partial.
func options(list []choice) []map[string]any {
	out := make([]map[string]any, len(list))
	for i, c := range list {
		out[i] = map[string]any{"value": c.Value, "label": c.Label}
	}
	return out
}

func (s *Service) newForm(role auth.Role, checked []auth.Scope) *formView {
	v := &formView{
		Expires: "never", Errors: FieldErrors{}, Today: s.now().UTC().Format(time.DateOnly),
		Expiries: options(keyExpiries), MaxExpiries: options(maxExpiries), Visibilities: options(forcedVisibilities),
	}
	for _, sc := range auth.AllScopes {
		if auth.RoleAllows(role, sc) {
			v.Scopes = append(v.Scopes, scopeChoice{Name: sc, Checked: slices.Contains(checked, sc)})
		}
	}
	return v
}

// adminProof returns the confirm-it's-you fields the admin scope needs
// from p, or nil when it needs none (p can't grant it, or has no 2FA).
func (s *Service) adminProof(r *http.Request, p *auth.Principal) (*accounts.Proof, error) {
	if !auth.RoleAllows(p.Role, auth.ScopeAdmin) {
		return nil, nil
	}
	if has, err := s.d.Owner.HasMFA(r.Context(), p.UserID); err != nil || !has {
		return nil, err
	}
	pr, err := s.d.Owner.Proof(r)
	if err != nil {
		return nil, err
	}
	return &pr, nil
}

// keyPage renders the create form with the scripts it needs.
func (s *Service) keyPage(w http.ResponseWriter, r *http.Request, status int, v *formView) {
	scripts := []string{"js/keys.js"}
	if v.Proof != nil && v.Proof.Factors.Passkeys > 0 {
		scripts = append(scripts, "js/webauthn.js") // the passkey in confirm it's you
	}
	s.page(w, r, status, "keys/new", "New API key", v, scripts...)
}

// newPage handles GET /settings/keys/new.
func (s *Service) newPage(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	v := s.newForm(p.Role, DefaultScopes)
	var err error
	if v.Proof, err = s.adminProof(r, p); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.keyPage(w, r, http.StatusOK, v)
}

// formInput reads the create form into an Input.
func formInput(r *http.Request) (Input, *formView, string) {
	f := r.PostForm
	in := Input{Name: f.Get("name"), Scopes: f["scope"], Expires: f.Get("expires")}
	view := &formView{
		Name: in.Name, Expires: in.Expires, ExpiresOn: f.Get("expires_on"), CIDRs: f.Get("allowed_cidrs"),
		Visibility: f.Get("visibility"), MaxExpiry: f.Get("max_expiry"), MaxUploadMB: strings.TrimSpace(f.Get("max_upload_mb")),
	}
	if in.Expires == "date" {
		in.Expires = strings.TrimSpace(view.ExpiresOn)
		if in.Expires == "" {
			return in, view, "Pick the date the key stops working."
		}
	}
	if view.CIDRs != "" {
		in.AllowedCIDRs = []string{view.CIDRs}
	}
	in.Constraints.Visibility = view.Visibility
	if view.MaxExpiry != "" {
		i := slices.IndexFunc(maxExpiries, func(c choice) bool { return c.Value == view.MaxExpiry })
		if i < 0 {
			return in, view, "Pick an expiry cap from the list."
		}
		in.Constraints.MaxExpirySeconds = maxExpiries[i].Secs
	}
	if view.MaxUploadMB != "" {
		mb, err := strconv.ParseFloat(view.MaxUploadMB, 64)
		if err != nil || mb <= 0 || mb > 1e9 || math.IsNaN(mb) {
			return in, view, "Enter the upload limit in MB, e.g. 100."
		}
		in.Constraints.MaxUploadBytes = int64(mb * 1e6)
	}
	view.Restricted = view.Visibility != "" || view.MaxExpiry != "" || view.MaxUploadMB != ""
	return in, view, ""
}

// createForm handles POST /settings/keys and shows the new key once.
func (s *Service) createForm(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		s.d.Errors.Error(w, r, http.StatusBadRequest, "The form could not be read.")
		return
	}
	in, entered, fieldMsg := formInput(r)
	rerender := func(status int, errs FieldErrors, msg string) {
		checked := make([]auth.Scope, 0, len(in.Scopes))
		for _, sc := range in.Scopes {
			checked = append(checked, auth.Scope(sc))
		}
		v := s.newForm(p.Role, checked)
		v.Name, v.Expires, v.ExpiresOn, v.CIDRs = entered.Name, entered.Expires, entered.ExpiresOn, entered.CIDRs
		v.Visibility, v.MaxExpiry, v.MaxUploadMB, v.Restricted = entered.Visibility, entered.MaxExpiry, entered.MaxUploadMB, entered.Restricted
		if errs != nil {
			v.Errors = errs
		}
		v.Error = msg
		var err error
		if v.Proof, err = s.adminProof(r, p); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.keyPage(w, r, status, v)
	}
	if fieldMsg != "" {
		rerender(http.StatusBadRequest, nil, fieldMsg)
		return
	}
	nk, errs := Validate(in, p.Role, s.now())
	if errs != nil {
		rerender(http.StatusBadRequest, errs, "Check the fields below.")
		return
	}
	// A key with the admin scope outlives the session that makes it, so a
	// user with 2FA confirms it's them first, as for other sign-in changes.
	if slices.Contains(nk.Scopes, auth.ScopeAdmin) {
		if pr, err := s.adminProof(r, p); err != nil {
			s.serverError(w, r, err)
			return
		} else if pr != nil {
			var ae *web.APIError
			if err := s.d.Owner.ConfirmOwner(r); errors.As(err, &ae) {
				rerender(ae.Status, nil, "Not created. "+ae.Msg)
				return
			} else if err != nil {
				s.serverError(w, r, err)
				return
			}
		}
	}
	token, k, err := s.store.Create(r.Context(), p.UserID, nk, accounts.Actor(r))
	if errors.Is(err, ErrTooManyKeys) {
		rerender(http.StatusConflict, nil, tooManyMsg())
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, http.StatusCreated, "keys/created", "Key created", map[string]any{
		"Key": s.rows([]*Key{k})[0], "Token": token, "Origin": s.d.Cfg.Origin(),
	})
}

// revokeForm handles POST /settings/keys/{id}/revoke.
func (s *Service) revokeForm(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.d.Errors.NotFound(w, r)
		return
	}
	switch err := s.store.Revoke(r.Context(), p.UserID, id, accounts.Actor(r)); {
	case errors.Is(err, ErrNotFound):
		s.d.Errors.NotFound(w, r)
	case err != nil:
		s.serverError(w, r, err)
	default:
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/settings/keys?done=revoked", http.StatusSeeOther)
	}
}
