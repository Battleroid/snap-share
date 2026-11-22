// Package oidctest is a stub OpenID Connect provider for tests: an httptest
// TLS server with discovery, JWKS, an authorize endpoint that approves at once,
// and token and userinfo endpoints. It enforces PKCE (S256), client
// credentials and single-use codes, and signs ID tokens with RS256.
//
// Point the app at it with SNAP_OIDC_ISSUER=p.URL and inject p.Client() (for
// example through oidc.ClientContext) so the app trusts its certificate.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// User is the account the provider signs in as.
type User struct {
	Subject           string
	PreferredUsername string
	Email             string
	EmailVerified     bool
	Groups            []string
}

// Provider is a running stub provider.
type Provider struct {
	URL          string // issuer
	ClientID     string
	ClientSecret string

	srv *httptest.Server
	key *rsa.PrivateKey
	kid string

	mu    sync.Mutex
	user  User
	codes map[string]grant
	// access token -> user, for userinfo
	tokens map[string]User

	// Knobs for negative tests and provider quirks.
	GroupsInIDToken bool   // also put groups in the ID token (Gitea sends them via userinfo only)
	NonceOverride   string // sign ID tokens with this nonce instead of the requested one
	IssuerSlash     bool   // advertise and sign the issuer with a trailing slash, as Gitea does
	Tokens          int    // successful token exchanges so far
}

type grant struct {
	user        User
	nonce       string
	challenge   string
	redirectURI string
	clientID    string
	expires     time.Time
}

// New starts a provider; it stops when the test ends.
func New(t testing.TB) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{
		ClientID:     "snap-test",
		ClientSecret: "snap-test-secret",
		key:          key,
		kid:          "test-key",
		codes:        map[string]grant{},
		tokens:       map[string]User{},
		user:         User{Subject: "1", PreferredUsername: "ann", Email: "ann@example.com", EmailVerified: true},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /jwks", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /userinfo", p.userinfo)
	p.srv = httptest.NewTLSServer(mux)
	t.Cleanup(p.srv.Close)
	p.URL = p.srv.URL
	return p
}

// Client returns an HTTP client that trusts the provider (and any other
// httptest TLS server) and does not follow redirects.
func (p *Provider) Client() *http.Client {
	c := p.srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// SetUser sets who the next authorization signs in as.
func (p *Provider) SetUser(u User) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.user = u
}

func (p *Provider) issuer() string {
	if p.IssuerSlash {
		return p.URL + "/"
	}
	return p.URL
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.issuer(),
		"authorization_endpoint":                p.URL + "/authorize",
		"token_endpoint":                        p.URL + "/token",
		"userinfo_endpoint":                     p.URL + "/userinfo",
		"jwks_uri":                              p.URL + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"openid", "profile", "email", "groups"},
	})
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (p *Provider) jwks(w http.ResponseWriter, _ *http.Request) {
	pub := p.key.PublicKey
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": p.kid,
		"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return b64(b)
}

// authorize approves immediately and redirects back with a code.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme != "https" || q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "PKCE S256 required", http.StatusBadRequest)
		return
	}
	scopes := strings.Fields(q.Get("scope"))
	if !slices.Contains(scopes, "openid") {
		http.Error(w, "openid scope required", http.StatusBadRequest)
		return
	}
	code := randomToken()
	p.mu.Lock()
	p.codes[code] = grant{user: p.user, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"),
		redirectURI: redirect.String(), clientID: p.ClientID, expires: time.Now().Add(time.Minute)}
	p.mu.Unlock()
	v := redirect.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	redirect.RawQuery = v.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound) // #nosec G710 -- redirecting to redirect_uri is the protocol; test-only server
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || subtle.ConstantTimeCompare([]byte(secret), []byte(p.ClientSecret)) != 1 {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, found := p.codes[code]
	delete(p.codes, code) // single use, even when the rest fails
	p.mu.Unlock()
	if !found || time.Now().After(g.expires) || g.redirectURI != r.PostForm.Get("redirect_uri") {
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if subtle.ConstantTimeCompare([]byte(b64(sum[:])), []byte(g.challenge)) != 1 {
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	nonce := g.nonce
	p.mu.Lock()
	if p.NonceOverride != "" {
		nonce = p.NonceOverride
	}
	withGroups := p.GroupsInIDToken
	p.mu.Unlock()
	now := time.Now()
	claims := map[string]any{
		"iss": p.issuer(), "sub": g.user.Subject, "aud": g.clientID,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "nonce": nonce,
		"preferred_username": g.user.PreferredUsername,
		"email":              g.user.Email, "email_verified": g.user.EmailVerified,
	}
	if withGroups {
		claims["groups"] = nonNil(g.user.Groups)
	}
	idToken, err := p.sign(claims)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error")
		return
	}
	access := randomToken()
	p.mu.Lock()
	p.tokens[access] = g.user
	p.Tokens++
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": 300, "id_token": idToken,
	})
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	p.mu.Lock()
	u, found := p.tokens[tok]
	p.mu.Unlock()
	if !ok || !found {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sub": u.Subject, "preferred_username": u.PreferredUsername,
		"email": u.Email, "email_verified": u.EmailVerified, "groups": nonNil(u.Groups),
	})
}

func (p *Provider) sign(claims map[string]any) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": p.kid})
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := b64(header) + "." + b64(body)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return input + "." + b64(sig), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
