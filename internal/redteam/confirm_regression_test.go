package redteam_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/keys"
)

// confirmButton is a button that asks before an irreversible POST, as the
// page serves it.
type confirmButton struct {
	title, name string
	disabled    bool
	inDialog    bool
	submit      bool
}

// pageButtons returns the buttons of a page that carry data-confirm-title
// or name="confirm", noting whether each sits inside a <dialog>.
func pageButtons(t *testing.T, page string) []confirmButton {
	t.Helper()
	var out []confirmButton
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(n *html.Node, inDialog bool)
	walk = func(n *html.Node, inDialog bool) {
		if n.Type == html.ElementNode && n.Data == "dialog" {
			inDialog = true
		}
		if n.Type == html.ElementNode && n.Data == "button" {
			b := confirmButton{inDialog: inDialog, submit: true}
			keep := false
			for _, a := range n.Attr {
				switch a.Key {
				case "data-confirm-title":
					b.title, keep = a.Val, true
				case "name":
					b.name = a.Val
					keep = keep || a.Val == "confirm"
				case "disabled":
					b.disabled = true
				case "type":
					b.submit = a.Val == "" || a.Val == "submit"
				}
			}
			if keep {
				out = append(out, b)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inDialog)
		}
	}
	walk(doc, false)
	return out
}

// Review finding: the shared confirm partial rendered a live submit button
// that app.js intercepted, so without scripts (or before the modules load)
// one click ran an irreversible action unconfirmed, and the admin
// burn-by-link button sent confirm=1 together with the full /s/<id>#<key>
// link, past the server's guard and with the decryption key. Every
// irreversible confirm is now served disabled (app.js enables it once it
// asks), and no button outside a dialog sends confirm itself.
func TestConfirmButtonsFailClosedWithoutScripts(t *testing.T) {
	e := newEnv(t)
	root := e.user("root", auth.RoleAdmin)
	ann := e.user("ann", auth.RoleUser)
	rootC, annC := withCookie(e.session(root)), withCookie(e.session(ann))
	annSess := e.session(ann)

	e.paste(annSess, map[string]any{"content": "notes", "name": "notes.txt"})
	e.mint(ann, keys.NewKey{Name: "laptop", Scopes: []auth.Scope{auth.ScopeItemsRead}})
	if res, m := e.doJSON("POST", "/api/v1/secrets", map[string]any{"kind": "text", "size": 1024}, withCookie(annSess)); res.StatusCode != http.StatusCreated {
		t.Fatalf("secret: %d %v", res.StatusCode, m)
	}
	if _, _, err := e.s.App.Accounts.Store().CreateInvite(e.ctx, accounts.NewInvite{MaxUses: 1, TTL: time.Hour}, accounts.AuditEntry{}); err != nil {
		t.Fatal(err)
	}

	pages := []struct {
		path  string
		as    opt
		title string // a confirm the page must offer
	}{
		{"/admin/secrets", rootC, "Burn this secret?"},
		{"/admin/content", rootC, "Delete notes.txt?"},
		{"/admin/invites", rootC, "Revoke this invite?"},
		{"/dashboard/secrets", annC, "Burn this secret?"},
		{"/settings/keys", annC, "Revoke laptop?"},
		{"/settings/security", annC, "Sign out everywhere?"},
	}
	for _, p := range pages {
		res, page := e.do("GET", p.path, nil, p.as)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", p.path, res.StatusCode)
		}
		found := false
		for _, b := range pageButtons(t, page) {
			if b.inDialog {
				continue
			}
			found = found || b.title == p.title
			if b.title != "" && !b.disabled {
				t.Errorf("%s: %q is a live button without scripts", p.path, b.title)
			}
			if b.name == "confirm" && b.submit && !b.disabled {
				t.Errorf("%s: an enabled submit outside a dialog sends confirm", p.path)
			}
		}
		if !found {
			t.Errorf("%s: no %q confirm on the page", p.path, p.title)
		}
	}
}
