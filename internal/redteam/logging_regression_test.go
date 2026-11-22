package redteam_test

// Regression test: near misses of redacted routes (zip tickets, the owner's
// secret timeline, unlock) fell to the catch-all routes, which logged the
// full path.

import (
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/auth"
)

func TestNearMissesOfRedactedRoutesAreNotLogged(t *testing.T) {
	e := newEnv(t)
	alice := e.user("alice", auth.RoleUser)
	c := e.session(alice)

	zipTok := "ZIPTICKETzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	e.do("POST", "/api/v1/zip/"+zipTok, nil, withCookie(c))    // wrong method -> "/" catch-all
	e.do("GET", "/api/v1/zip/"+zipTok+"/", nil, withCookie(c)) // trailing slash -> fallback
	secretID := "SECRETIDyyyyyyyyyyyyyy"
	e.do("GET", "/dashboard/secrets/"+secretID, nil, withCookie(c))              // missing /timeline
	e.do("GET", "/dashboard/secrets/"+secretID+"/timeline/", nil, withCookie(c)) // trailing slash
	m := e.paste(c, map[string]any{"content": "x", "password": "correct horse battery", "slug": "unlock-me-slug"})
	slug := m["slug"].(string)
	e.do("GET", "/unlock/"+slug, nil, withCookie(c)) // wrong method -> fallback

	log := e.log.String()
	if !strings.Contains(log, `route="GET /"`) && !strings.Contains(log, `"route":"GET /"`) {
		t.Fatalf("requests not logged:\n%s", log)
	}
	for _, leak := range []string{zipTok, secretID, "/unlock/" + slug} {
		for _, line := range strings.Split(log, "\n") {
			if strings.Contains(line, leak) {
				t.Errorf("logged: %s", line)
			}
		}
	}
}
