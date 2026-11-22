package web

import (
	"net/http"
	"strings"
)

// cspSet holds the precomputed policies.
type cspSet struct {
	app, wasm string
}

// NewCSP builds the app policies for the public origin and optional raw origin.
// Scripts and styles are limited to the app's own static paths, so files users
// upload to the same origin can never be loaded as scripts or styles.
func NewCSP(origin, rawOrigin string) *cspSet {
	extra := func(base string) string {
		if rawOrigin == "" {
			return base
		}
		return base + " " + rawOrigin
	}
	frame := "'none'"
	if rawOrigin != "" {
		frame = rawOrigin
	}
	parts := []string{
		"default-src 'none'",
		"script-src " + origin + "/static/js/ " + origin + "/static/vendor/",
		"style-src " + origin + "/static/",
		extra("img-src 'self' data: blob:"),
		extra("media-src 'self' blob:"),
		"font-src 'self'",
		"connect-src 'self'",
		"frame-src " + frame,
		"form-action 'self'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"manifest-src 'self'",
	}
	app := strings.Join(parts, "; ")
	wasmParts := append([]string(nil), parts...)
	wasmParts[1] += " 'wasm-unsafe-eval'"
	return &cspSet{app: app, wasm: strings.Join(wasmParts, "; ")}
}

func (c *cspSet) policy(k CSPKind) string {
	if k == CSPWasm {
		return c.wasm
	}
	return c.app
}

// RawContentCSP is the policy for user content served inline. sandbox gives the
// response an opaque origin, so even an HTML or SVG file cannot run script or
// read cookies.
const RawContentCSP = "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'"

// SetRawHeaders applies the isolation headers for inline user content.
func SetRawHeaders(h http.Header) {
	h.Set("Content-Security-Policy", RawContentCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	h.Set("X-Robots-Tag", "noindex")
	h.Del("Cross-Origin-Opener-Policy")
}
