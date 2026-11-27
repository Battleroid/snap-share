// Package web holds the HTTP plumbing shared by every feature: the router with
// per-route access rules, middleware (logging, security headers, CSRF, auth),
// static assets, templates and JSON helpers.
package web

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
)

// Access is who may call a route.
type Access int

// Access levels.
const (
	accessUnset Access = iota
	AccessPublic
	AccessUser  // any signed-in principal
	AccessScope // signed in and holding Route.Scope
	AccessAdmin // admin scope, and inside AdminCIDRs when configured
)

// CSPKind selects the Content-Security-Policy for a route.
type CSPKind int

// CSP kinds.
const (
	CSPApp  CSPKind = iota // default app policy
	CSPWasm                // app policy plus 'wasm-unsafe-eval' (secret pages)
	CSPNone                // handler sets its own (raw user content)
)

// Route describes one registered route.
type Route struct {
	Pattern string
	Access  Access
	Scope   auth.Scope
	Redact  bool // the path carries a token or id (paths are never logged; this marks it for review)
	CSP     CSPKind
	// LargeBody exempts the route from the default request body limits
	// (MaxFormBody, BodyReadTimeout, no multipart); see body.go.
	LargeBody bool
	// DuringSetup lets a session that must set up a second factor first
	// (auth.Principal.NeedsSetup) use the route; see wrap.
	DuringSetup bool
}

// RouteOpt configures a route.
type RouteOpt func(*Route)

// Public allows anyone.
func Public() RouteOpt { return func(r *Route) { r.Access = AccessPublic } }

// SignedIn requires a browser session. API keys are refused: they reach only
// routes that declare a scope, so a key can never do more than its scopes say.
func SignedIn() RouteOpt { return func(r *Route) { r.Access = AccessUser } }

// Scope requires an authenticated principal holding s.
func Scope(s auth.Scope) RouteOpt {
	return func(r *Route) { r.Access, r.Scope = AccessScope, s }
}

// Admin requires the admin scope (and an allowed network when AdminCIDRs is set).
func Admin() RouteOpt { return func(r *Route) { r.Access, r.Scope = AccessAdmin, auth.ScopeAdmin } }

// Redact marks a route whose path carries a token or id. Request logs carry
// only route patterns, so no path is logged; the mark keeps that intent
// visible next to the route.
func Redact() RouteOpt { return func(r *Route) { r.Redact = true } }

// WithCSP selects a non-default CSP.
func WithCSP(k CSPKind) RouteOpt { return func(r *Route) { r.CSP = k } }

// DuringSetup marks a route a session may use while it must set up a second
// factor first: the security settings, their factor endpoints and sign-out.
func DuringSetup() RouteOpt { return func(r *Route) { r.DuringSetup = true } }

// SetupPath is where a session that must set up a second factor is sent.
const SetupPath = "/settings/security#two-factor"

// Router wraps http.ServeMux. Every route must declare its access.
type Router struct {
	mux        *http.ServeMux
	routes     []Route
	csp        *cspSet
	adminCIDRs []netip.Prefix
	errors     *ErrorPages
	fallback   http.Handler
	fbRoute    Route

	bodyTimeout time.Duration // BodyReadTimeout; shorter in tests
}

// NewRouter creates a router. errs renders error pages; may be nil in tests.
func NewRouter(csp *cspSet, adminCIDRs []netip.Prefix, errs *ErrorPages) *Router {
	return &Router{mux: http.NewServeMux(), csp: csp, adminCIDRs: adminCIDRs, errors: errs, bodyTimeout: BodyReadTimeout}
}

// Handle registers h for pattern (Go 1.22+ ServeMux syntax, e.g. "POST /api/v1/upload").
func (rt *Router) Handle(pattern string, h http.Handler, opts ...RouteOpt) {
	route := Route{Pattern: pattern}
	for _, o := range opts {
		o(&route)
	}
	if route.Access == accessUnset {
		panic(fmt.Sprintf("web: route %q must declare access (Public, SignedIn, Scope or Admin)", pattern))
	}
	if route.Access == AccessScope && !route.Scope.Valid() {
		panic(fmt.Sprintf("web: route %q has invalid scope %q", pattern, route.Scope))
	}
	rt.routes = append(rt.routes, route)
	rt.mux.Handle(pattern, rt.wrap(route, h))
}

// HandleFunc registers f for pattern.
func (rt *Router) HandleFunc(pattern string, f http.HandlerFunc, opts ...RouteOpt) {
	rt.Handle(pattern, f, opts...)
}

// Fallback sets the handler for GET/HEAD requests no other route matches (the
// slug dispatcher). Without one, unmatched requests get 404.
func (rt *Router) Fallback(h http.Handler, opts ...RouteOpt) {
	route := Route{Pattern: "GET /"}
	for _, o := range opts {
		o(&route)
	}
	if route.Access == accessUnset {
		panic("web: fallback must declare access")
	}
	rt.fallback, rt.fbRoute = h, route
}

// Routes lists registered routes (for tests that audit access rules).
func (rt *Router) Routes() []Route { return append([]Route(nil), rt.routes...) }

// Handler returns the mux with the fallback installed. Call once, after all Handle calls.
func (rt *Router) Handler() http.Handler {
	fb := rt.fallback
	if fb == nil {
		fb = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rt.errors.NotFound(w, r) })
		rt.fbRoute = Route{Pattern: "GET /", Access: AccessPublic}
	}
	rt.routes = append(rt.routes, rt.fbRoute)
	rt.mux.Handle("GET /", rt.wrap(rt.fbRoute, fb))
	// Anything else unmatched (other methods on unknown paths).
	rt.mux.Handle("/", rt.wrap(Route{Pattern: "/", Access: AccessPublic}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt.errors.Error(w, r, http.StatusNotFound, "Not found.")
	})))
	return rt.mux
}

func (rt *Router) wrap(route Route, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if li := logInfoFrom(r.Context()); li != nil {
			li.pattern = route.Pattern
		}
		if route.CSP != CSPNone && rt.csp != nil {
			w.Header().Set("Content-Security-Policy", rt.csp.policy(route.CSP))
		}
		if !rt.limitBody(route, w, r) {
			return
		}
		p := auth.FromContext(r.Context())
		// A session that must set up a second factor (auth.require_2fa)
		// reaches only DuringSetup routes. Public routes run as anonymous,
		// so nothing that acts for the signed-in user (the composer, an
		// owner's private items, new secrets) works for it; every other
		// route sends it to the setup page.
		if p != nil && p.NeedsSetup && !route.DuringSetup {
			if route.Access != AccessPublic {
				rt.needsSetup(w, r)
				return
			}
			r = r.WithContext(auth.WithPrincipal(r.Context(), nil))
			p = nil
		}
		switch route.Access {
		case AccessPublic:
		case AccessUser:
			if p == nil {
				rt.errors.Unauthenticated(w, r)
				return
			}
			if p.ViaKey() {
				rt.errors.Error(w, r, http.StatusForbidden, "API keys can't use this; sign in with a browser.")
				return
			}
		case AccessScope:
			if p == nil {
				rt.errors.Unauthenticated(w, r)
				return
			}
			if !p.Can(route.Scope) {
				rt.errors.Error(w, r, http.StatusForbidden, "This needs the "+string(route.Scope)+" permission.")
				return
			}
		case AccessAdmin:
			if p == nil {
				rt.errors.Unauthenticated(w, r)
				return
			}
			allowedNet := len(rt.adminCIDRs) == 0 || clientip.In(clientip.From(r.Context()), rt.adminCIDRs)
			if !p.IsAdmin() {
				// An SSO admin whose sign-in is too old learns why, and how
				// to get admin rights back (from where they would work).
				if p.AdminLapsed && !p.ViaKey() && allowedNet {
					rt.errors.AdminLapsed(w, r)
					return
				}
				rt.errors.Error(w, r, http.StatusForbidden, "Admins only.")
				return
			}
			if !allowedNet {
				rt.errors.Error(w, r, http.StatusForbidden, "Admin pages are not available from this network.")
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// needsSetup answers a request from a session that must set up a second
// factor: htmx gets HX-Redirect, the API 403, pages a redirect.
func (rt *Router) needsSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case IsAPI(r):
		rt.errors.Error(w, r, http.StatusForbidden, "Set up two-factor authentication first.")
	case r.Header.Get("HX-Request") == "true":
		w.Header().Set("HX-Redirect", SetupPath)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Redirect(w, r, SetupPath, http.StatusSeeOther)
	}
}

// IsAPI reports whether r should get JSON errors.
func IsAPI(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}
