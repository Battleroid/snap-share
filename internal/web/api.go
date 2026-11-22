package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// JSON writes v as JSON with status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// APIError is an error with an HTTP status, returned by handlers and rendered by Fail.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// Errorf builds an APIError.
func Errorf(status int, format string, a ...any) *APIError {
	return &APIError{Status: status, Msg: fmt.Sprintf(format, a...)}
}

// DecodeJSON reads a JSON body of at most max bytes into v, rejecting unknown fields.
func DecodeJSON(w http.ResponseWriter, r *http.Request, max int64, v any) error {
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return Errorf(http.StatusUnsupportedMediaType, "Send JSON (Content-Type: application/json).")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return Errorf(http.StatusRequestEntityTooLarge, "Request body is too large.")
		}
		return Errorf(http.StatusBadRequest, "Invalid JSON: %v", err)
	}
	if dec.More() {
		return Errorf(http.StatusBadRequest, "Invalid JSON: trailing data.")
	}
	return nil
}

// WantsURL reports whether the client asked for a bare URL instead of JSON
// (curl users: `Accept: text/plain` or `?format=url`).
func WantsURL(r *http.Request) bool {
	if r.URL.Query().Get("format") == "url" {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.HasPrefix(accept, "text/plain")
}

// URLText writes a URL followed by a newline as text/plain.
func URLText(w http.ResponseWriter, status int, u string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, u+"\n")
}

// SafeNext returns next if it is a same-origin relative path, else "/".
func SafeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	return u.RequestURI()
}
