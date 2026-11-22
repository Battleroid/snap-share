// Package authtest provides a fake Authenticator for tests and for tracks that
// need a signed-in user before the accounts track is merged.
package authtest

import (
	"net/http"
	"strings"
	"sync"

	"github.com/Battleroid/snap-share/internal/auth"
)

// Fake authenticates by header: "Authorization: Bearer test-<name>" for API-key
// style principals and "Cookie: snap_test=<name>" for session style principals.
type Fake struct {
	mu    sync.RWMutex
	users map[string]*auth.Principal
}

// New returns an empty Fake.
func New() *Fake { return &Fake{users: map[string]*auth.Principal{}} }

// Add registers a principal under name and returns it.
func (f *Fake) Add(name string, p auth.Principal) *auth.Principal {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := p
	if cp.Username == "" {
		cp.Username = name
	}
	if cp.Role == "" {
		cp.Role = auth.RoleUser
	}
	f.users[name] = &cp
	return &cp
}

// Authenticate implements auth.Authenticator.
func (f *Fake) Authenticate(r *http.Request) (*auth.Principal, auth.Method, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if h := r.Header.Get("Authorization"); h != "" {
		name, ok := strings.CutPrefix(h, "Bearer test-")
		p, found := f.users[name]
		if !ok || !found || !p.ViaKey() {
			return nil, auth.MethodNone, auth.ErrInvalidCredentials
		}
		return p, auth.MethodBearer, nil
	}
	if c, err := r.Cookie("snap_test"); err == nil {
		if p, ok := f.users[c.Value]; ok && !p.ViaKey() {
			return p, auth.MethodSession, nil
		}
	}
	return nil, auth.MethodNone, nil
}

// Session returns a request option that signs the request in as name.
func Session(r *http.Request, name string) { r.AddCookie(&http.Cookie{Name: "snap_test", Value: name}) }

// Bearer adds the bearer header for name.
func Bearer(r *http.Request, name string) { r.Header.Set("Authorization", "Bearer test-"+name) }
