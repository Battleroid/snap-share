package web

import (
	"net/http"
	"net/url"

	"github.com/Battleroid/snap-share/internal/auth"
)

// ErrorPages renders errors as JSON for API requests and as a page otherwise.
type ErrorPages struct {
	R *Renderer // nil: plain text
}

// Error writes an error response with a short message.
func (e *ErrorPages) Error(w http.ResponseWriter, r *http.Request, status int, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	if IsAPI(r) || e == nil || e.R == nil {
		if IsAPI(r) {
			JSON(w, status, map[string]string{"error": msg})
			return
		}
		http.Error(w, msg, status)
		return
	}
	e.R.Page(w, r, status, "core/error", Page{
		Title: http.StatusText(status),
		Data:  map[string]any{"Status": status, "Message": msg},
	})
}

// NotFound writes a 404.
func (e *ErrorPages) NotFound(w http.ResponseWriter, r *http.Request) {
	e.Error(w, r, http.StatusNotFound, "Nothing here. The link may have expired or been deleted. If someone sent you this link, ask them for a new one.")
}

// Unauthenticated sends browsers to sign in and API clients a 401.
func (e *ErrorPages) Unauthenticated(w http.ResponseWriter, r *http.Request) {
	if !IsAPI(r) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="snap"`)
	e.Error(w, r, http.StatusUnauthorized, "Sign in first.")
}

// AdminLapsed answers an admin URL for a session whose admin rights paused
// (auth.Principal.AdminLapsed): a 403 that says why, with the SSO sign-in
// that restores them and comes back here (a GET; otherwise to /admin).
func (e *ErrorPages) AdminLapsed(w http.ResponseWriter, r *http.Request) {
	sso := "SSO"
	if e != nil && e.R != nil && e.R.site != nil {
		if n := e.R.site(r).OIDCName; n != "" {
			sso = n
		}
	}
	msg := AdminLapsedMessage(auth.FromContext(r.Context()), sso)
	if IsAPI(r) || e == nil || e.R == nil {
		e.Error(w, r, http.StatusForbidden, "Admin access paused. "+msg)
		return
	}
	next := "/admin"
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		next = r.URL.RequestURI()
	}
	w.Header().Set("Cache-Control", "no-store")
	e.R.Page(w, r, http.StatusForbidden, "core/error", Page{
		Title: "Admin access paused",
		Data: map[string]any{"Status": http.StatusForbidden, "Title": "Admin access paused", "Message": msg,
			"Action": map[string]any{"label": "Sign in with " + sso, "href": "/auth/oidc/start?next=" + url.QueryEscape(next)}},
	})
}

// AdminLapsedMessage says why p's admin rights paused and what restores
// them; sso names the provider ("Gitea").
func AdminLapsedMessage(p *auth.Principal, sso string) string {
	if p != nil && p.AdminSSOAt.IsZero() {
		return "Admin features need a recent " + sso + " sign-in; sign in with " + sso + " to use them."
	}
	return "Your " + sso + " sign-in is more than 24 hours old; sign in with " + sso + " again to restore admin features."
}
