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
	Prefix     string // snap_<publicID>, never the secret
	Scopes     []auth.Scope
	AllPills   []scopePill // one per resource
	Pills      []scopePill // the first maxPills of them
	MoreScopes int         // resources left out of Pills
	AllScopes  []scopeHint // every scope with what it allows
	CreatedAt  time.Time
	ExpiresAt  *time.Time // nil: never
	LastUsedAt *time.Time // nil: never used
	LastIP     string     // where it was last used from
	Status     string     // "active", "expired" or "revoked"
	Revocable  bool
}

// maxPills is how many resource pills the list shows before "+N".
const maxPills = 2

// scopePill is the kit scope summary for one resource: items, read, write.
type scopePill struct {
	Resource string
	Access   string // "read, write"
	Write    bool   // more than read
	Admin    bool
}

type scopeHint struct {
	Name string
	Hint string
}

// scopeHints says what each scope allows (docs/api.md, Scopes).
var scopeHints = map[auth.Scope]string{
	auth.ScopeItemsRead:    "list and read items, tags and zip downloads",
	auth.ScopeItemsWrite:   "uploads, pastes, links and changes to items",
	auth.ScopeItemsDelete:  "delete items",
	auth.ScopeAlbumsRead:   "read albums",
	auth.ScopeAlbumsWrite:  "change albums",
	auth.ScopeSecretsRead:  "list one-time secrets",
	auth.ScopeSecretsWrite: "create one-time secrets",
	auth.ScopeAccountRead:  "read the account (GET /me)",
	auth.ScopeAdmin:        "admin pages and API; only admins can grant it",
}

// pills groups scopes by resource: items:read and items:write become
// "items: read, write", in the order they come.
func pills(scopes []auth.Scope) []scopePill {
	var out []scopePill
	idx := map[string]int{}
	for _, sc := range scopes {
		res, acc, _ := strings.Cut(string(sc), ":")
		if acc == "" {
			out = append(out, scopePill{Resource: res, Admin: res == "admin"})
			continue
		}
		i, ok := idx[res]
		if !ok {
			i = len(out)
			idx[res] = i
			out = append(out, scopePill{Resource: res})
		}
		if out[i].Access != "" {
			out[i].Access += ", "
		}
		out[i].Access += acc
		out[i].Write = out[i].Write || acc != "read"
	}
	return out
}

func (s *Service) rows(list []*Key) []keyRow {
	now := s.now()
	out := make([]keyRow, 0, len(list))
	for _, k := range list {
		row := keyRow{
			ID: k.ID, Name: k.Name, Prefix: k.Prefix(), Scopes: k.Scopes,
			CreatedAt: k.CreatedAt, ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt, LastIP: k.LastUsedIP,
		}
		row.AllPills = pills(k.Scopes)
		row.Pills = row.AllPills
		if len(row.Pills) > maxPills {
			row.MoreScopes = len(row.Pills) - maxPills
			row.Pills = row.Pills[:maxPills]
		}
		for _, sc := range k.Scopes {
			row.AllScopes = append(row.AllScopes, scopeHint{Name: string(sc), Hint: scopeHints[sc]})
		}
		switch st := k.Status(now); st {
		case StatusRevoked:
			row.Status = "revoked"
		case StatusExpired:
			row.Status, row.Revocable = "expired", true
		default:
			row.Status, row.Revocable = "active", true
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
	rows := s.rows(list)
	show := "active"
	if r.URL.Query().Get("show") == "all" {
		show = "all"
	}
	shown := rows
	if show == "active" {
		// Revoked and expired keys no longer work; they stay behind "all".
		shown = slices.DeleteFunc(slices.Clone(rows), func(k keyRow) bool { return k.Status != "active" })
	}
	s.page(w, r, http.StatusOK, "keys/list", "API keys", map[string]any{
		"Keys": shown, "Any": len(rows) > 0, "Hidden": len(rows) - len(shown), "Show": show,
		"Notice": doneNotices[r.URL.Query().Get("done")],
	})
}

type scopeChoice struct {
	Hint    string
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
			v.Scopes = append(v.Scopes, scopeChoice{Name: sc, Hint: scopeHints[sc], Checked: slices.Contains(checked, sc)})
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
	v := s.newForm(p.Role, FormScopes)
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
		to := "/settings/keys?done=revoked"
		if r.FormValue("show") == "all" {
			to = "/settings/keys?show=all&done=revoked"
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
}
