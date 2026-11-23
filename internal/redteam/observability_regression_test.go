package redteam_test

// Regression tests for the observability outputs: request logs (JSON and
// text) carry only route patterns and fixed fields, never credentials,
// query strings, cookies, tokens or secret ids; the request id is the
// server's own; and the audit stream never carries a secret id.

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/app"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
	"github.com/Battleroid/snap-share/internal/obs"
	"github.com/Battleroid/snap-share/internal/testutil"
)

// requestFields are the only attributes a request line may have. A new one
// must be checked against the never-log rules before it is added here.
var requestFields = []string{"time", "level", "msg", "method", "route", "status", "bytes", "ms", "ip", "principal", "user", "key", "request_id"}

func TestRequestLogsCarryNoCredentials(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			e := newEnv(t, testutil.LogFormat(format))
			alice := e.user("alice", auth.RoleUser)
			c := e.session(alice)
			tok := e.mint(alice, keys.NewKey{Scopes: []auth.Scope{auth.ScopeItemsRead, auth.ScopeItemsWrite}})

			const (
				query    = "QUERYSECRETqqqqqqqq"
				cookie   = "COOKIESECRETcccccccc"
				bogus    = "snap_BOGUSTOKENbbbbbbbbbbbbbbbbbbbbbbb"
				secretID = "SECRETIDssssssssssssss"
				emailTok = "EMAILTOKENeeeeeeeeeeeeeeeeeee"
				clientID = "CLIENTREQUESTIDrrrrrr"
			)
			reqs := []struct {
				method, path string
				opts         []opt
			}{
				{"GET", "/api/v1/items?token=" + query, []opt{bearer(tok)}},
				{"GET", "/api/v1/items", []opt{bearer(bogus)}}, // 401
				{"GET", "/dashboard?q=" + query, []opt{withCookie(c), withCookie(&http.Cookie{Name: "other", Value: cookie})}},
				{"GET", "/s/" + secretID, nil},
				{"GET", "/s/" + secretID + "/", nil},
				{"POST", "/api/v1/secrets/" + secretID + "/info", []opt{anonymous()}},
				{"GET", "/api/v1/secrets/" + secretID + "/blob/0", []opt{withCookie(c)}},
				{"GET", "/dashboard/secrets/" + secretID + "/timeline", []opt{withCookie(c)}},
				{"GET", "/verify/" + emailTok, nil},
				{"GET", "/reset/" + emailTok + "?x=" + query, nil},
				{"GET", "/invite/" + emailTok, nil},
				{"GET", "/x/" + emailTok, nil},
				{"GET", "/unlock/" + secretID, nil},
				{"GET", "/no-such-" + secretID + "?" + query, []opt{header("X-Request-Id", clientID), header("Referer", "https://ref.example/?"+query)}},
			}
			var ids []string
			for _, r := range reqs {
				res, _ := e.do(r.method, r.path, nil, r.opts...)
				id := res.Header.Get("X-Request-Id")
				if len(id) != 16 || id == clientID {
					t.Errorf("%s %s: X-Request-Id %q", r.method, r.path, id)
				}
				ids = append(ids, id)
			}

			log := e.log.String()
			needles := []string{tok, c.Value, query, cookie, bogus, secretID, emailTok, clientID, "Bearer", "ref.example"}
			for _, line := range strings.Split(log, "\n") {
				for _, n := range needles {
					if n != "" && strings.Contains(line, n) {
						t.Errorf("logged %q: %s", n, line)
					}
				}
			}
			for _, id := range ids {
				if !strings.Contains(log, id) {
					t.Errorf("request id %s not logged", id)
				}
			}
			if format != "json" {
				return
			}
			n := 0
			for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatalf("not JSON: %s", line)
				}
				if m["msg"] != "request" {
					continue
				}
				n++
				for k := range m {
					if !slices.Contains(requestFields, k) {
						t.Errorf("request line has field %q: %s", k, line)
					}
				}
				if p := m["principal"]; p != "anonymous" && p != "session" && p != "key" {
					t.Errorf("principal %v", p)
				}
			}
			if n < len(reqs) {
				t.Fatalf("%d request lines for %d requests", n, len(reqs))
			}
		})
	}
}

// The admin burn audit entry targets "secret:<id>"; the stream must not
// carry the id (the table keeps it for Admin › Audit).
func TestAuditStreamRedactsSecretIDs(t *testing.T) {
	out := &syncBuf{}
	e := newEnv(t, testutil.AppOptions(func(o *app.Options) { o.AuditSink = obs.JSONLines{W: out} }))
	ctx := context.Background()
	const id = "AbCdEfGhIjKlMnOpQrStUv"
	if err := e.s.App.Accounts.Store().Audit(ctx, accounts.AuditEntry{Action: "moderation.burn", Target: "secret:" + id,
		Details: map[string]any{"state": "burned", "link": "https://snap.example/s/" + id + "#key=abc"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.App.AuditStream.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, `"moderation.burn"`) {
		t.Fatalf("no line: %q", s)
	}
	if strings.Contains(s, id) || strings.Contains(s, "key=abc") {
		t.Fatalf("secret id or key in the audit stream: %s", s)
	}
}
