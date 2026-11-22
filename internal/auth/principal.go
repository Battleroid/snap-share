// Package auth defines who a request acts as (Principal), the API scopes, and
// the interface the web layer uses to authenticate requests. Sessions, API keys,
// passwords and OIDC are implemented in this package by the accounts and keys tracks.
package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
)

// Role is a user's site role.
type Role string

// Roles.
const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// Scope is a permission an API key can carry. Sessions implicitly hold every
// scope their role allows.
type Scope string

// Scopes.
const (
	ScopeItemsRead    Scope = "items:read"
	ScopeItemsWrite   Scope = "items:write"
	ScopeItemsDelete  Scope = "items:delete"
	ScopeAlbumsRead   Scope = "albums:read"
	ScopeAlbumsWrite  Scope = "albums:write"
	ScopeSecretsRead  Scope = "secrets:read"
	ScopeSecretsWrite Scope = "secrets:write"
	ScopeAccountRead  Scope = "account:read"
	ScopeAdmin        Scope = "admin"
)

// AllScopes lists every scope in display order.
var AllScopes = []Scope{
	ScopeItemsRead, ScopeItemsWrite, ScopeItemsDelete,
	ScopeAlbumsRead, ScopeAlbumsWrite,
	ScopeSecretsRead, ScopeSecretsWrite,
	ScopeAccountRead, ScopeAdmin,
}

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool { return slices.Contains(AllScopes, s) }

// RoleAllows reports whether role r may hold scope s at all.
func RoleAllows(r Role, s Scope) bool {
	if s == ScopeAdmin {
		return r == RoleAdmin
	}
	return s.Valid()
}

// Principal is the authenticated actor of a request.
type Principal struct {
	UserID        int64
	Username      string
	Role          Role
	EmailVerified bool

	// Set for API-key requests; nil Scopes with KeyID 0 means a browser session.
	KeyID  int64
	Scopes []Scope

	// Set for browser sessions.
	SessionID string
}

// ViaKey reports whether the principal authenticated with an API key.
func (p *Principal) ViaKey() bool { return p != nil && p.KeyID != 0 }

// IsAdmin reports whether the principal can act as an admin.
func (p *Principal) IsAdmin() bool { return p.Can(ScopeAdmin) }

// Can reports whether the principal holds scope s. A nil principal can do nothing.
func (p *Principal) Can(s Scope) bool {
	if p == nil || !RoleAllows(p.Role, s) {
		return false
	}
	if !p.ViaKey() {
		return true
	}
	return slices.Contains(p.Scopes, s)
}

type ctxKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the request's principal, or nil for anonymous requests.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// Errors returned by authenticators.
var (
	// ErrInvalidCredentials means credentials were presented but are not valid.
	// The web layer answers 401 and never falls back to other credentials.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrForbiddenNetwork means a key was used from an address it does not allow.
	ErrForbiddenNetwork = errors.New("address not allowed for this key")
)

// Method says how a request authenticated.
type Method int

// Methods.
const (
	MethodNone    Method = iota
	MethodSession        // cookie; subject to CSRF checks
	MethodBearer         // Authorization header; cookies ignored, exempt from CSRF
)

// Authenticator resolves a request's principal.
//
// Implementations must: use only the Authorization header when it is present
// (returning ErrInvalidCredentials if it is not valid, never falling back to
// cookies); otherwise use the session cookie; return (nil, MethodNone, nil) for
// anonymous requests.
type Authenticator interface {
	Authenticate(r *http.Request) (*Principal, Method, error)
}

// Anonymous treats every request as anonymous. Used until the accounts track lands.
type Anonymous struct{}

// Authenticate implements Authenticator.
func (Anonymous) Authenticate(r *http.Request) (*Principal, Method, error) {
	if r.Header.Get("Authorization") != "" {
		return nil, MethodNone, ErrInvalidCredentials
	}
	return nil, MethodNone, nil
}
