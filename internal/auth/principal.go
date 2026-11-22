// Package auth defines who a request acts as (Principal), the API scopes, and
// the interface the web layer uses to authenticate requests. Sessions, API keys,
// passwords and OIDC are implemented in this package by the accounts and keys tracks.
package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"
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

	// Key holds the limits of the API key the request authenticated with; nil
	// for browser sessions and for keys without restrictions.
	Key *KeyConstraints
}

// KeyConstraints limit what an API key may create. Every method is safe on a
// nil receiver, which limits nothing.
type KeyConstraints struct {
	// Visibility, when set ("unlisted" or "private"), is forced on everything
	// the key creates, whatever the request asks for.
	Visibility string
	// MaxExpiry caps how long anything the key creates may live; 0 is no cap.
	// "never" and later expiries are clamped to the cap.
	MaxExpiry time.Duration
	// MaxUploadBytes lowers the per-file size limit; 0 is no cap.
	MaxUploadBytes int64
}

// Constraints returns the principal's key constraints (nil for sessions,
// anonymous requests and unrestricted keys).
func (p *Principal) Constraints() *KeyConstraints {
	if p == nil {
		return nil
	}
	return p.Key
}

// ForcedVisibility returns the visibility the key forces, or "".
func (c *KeyConstraints) ForcedVisibility() string {
	if c == nil {
		return ""
	}
	return c.Visibility
}

// ClampExpiry applies the expiry cap to exp (nil means never) for something
// created or changed at now. It returns exp unchanged when it is within the cap.
func (c *KeyConstraints) ClampExpiry(exp *time.Time, now time.Time) *time.Time {
	if c == nil || c.MaxExpiry <= 0 {
		return exp
	}
	limit := now.Add(c.MaxExpiry).UTC().Truncate(time.Millisecond)
	if exp == nil || exp.After(limit) {
		return &limit
	}
	return exp
}

// UploadLimit returns limit lowered to the key's upload cap.
func (c *KeyConstraints) UploadLimit(limit int64) int64 {
	if c == nil || c.MaxUploadBytes <= 0 || c.MaxUploadBytes >= limit {
		return limit
	}
	return c.MaxUploadBytes
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
