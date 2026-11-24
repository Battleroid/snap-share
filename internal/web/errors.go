package web

import (
	"net/http"
	"net/url"
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
