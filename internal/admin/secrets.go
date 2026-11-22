package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/secrets"
)

// secretID reads a secret link (https://host/s/ID#KEY, with or without the
// key) or a bare id. The key part is dropped as soon as the link is parsed:
// it is never logged, stored or echoed back.
func secretID(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, "#") {
		_, id, key, err := e2e.ParseLink(ref)
		clear(key[:])
		if err == nil {
			return e2e.Encode(id[:]), true
		}
		ref, _, _ = strings.Cut(ref, "#")
	}
	if i := strings.LastIndex(ref, "/s/"); i >= 0 {
		ref = ref[i+len("/s/"):]
	}
	ref, _, _ = strings.Cut(ref, "?")
	ref = strings.TrimSuffix(ref, "/")
	id, err := e2e.Decode16(ref)
	if err != nil {
		return "", false
	}
	return e2e.Encode(id[:]), true
}

const msgBadRef = "Paste a secret link (https://…/s/…) or its 22-character id."

// burn revokes a secret by link or id and audits it. found is false when
// there is no such secret; state is what it is now.
func (a *Admin) burn(ctx context.Context, r *http.Request, id string) (state string, found bool, err error) {
	state, found, err = a.d.Secrets.Revoke(ctx, id, 0)
	if err != nil || !found {
		return state, found, err
	}
	e := accounts.Actor(r)
	e.Action, e.Target, e.Details = "moderation.burn", "secret:"+id, map[string]any{"state": state}
	return state, true, a.d.Accounts.Store().Audit(ctx, e)
}

type secretRow struct {
	secrets.Meta
	Owner, StateBadge, Created, Expires string
}

type secretsData struct {
	base
	Owner  string
	Show   string // active or all
	Rows   []secretRow
	Pager  pager
	RefErr string
}

var stateBadges = map[string]string{"ready": "ok", "uploading": "info", "burned": "unknown", "expired": "unknown", "revoked": "down"}

func (a *Admin) secretsData(r *http.Request) (*secretsData, error) {
	q := r.URL.Query()
	d := &secretsData{base: a.base(r, "/admin/secrets"), Owner: strings.TrimSpace(q.Get("owner")), Show: q.Get("show")}
	f := secrets.MetaFilter{Limit: perPage}
	if d.Show != "all" {
		d.Show, f.Active = "active", true
	}
	if d.Owner == "anonymous" {
		f.Anonymous = true
	} else {
		f.Owner = d.Owner
	}
	page, offset := pageOf(r)
	f.Offset = offset
	list, total, err := a.d.Secrets.ListMeta(r.Context(), f)
	if err != nil {
		return nil, err
	}
	for _, m := range list {
		row := secretRow{Meta: m, Owner: m.OwnerName, StateBadge: stateBadges[m.State], Created: stamp(&m.CreatedAt), Expires: "—"}
		if m.OwnerID == 0 {
			row.Owner = "anonymous"
		} else if row.Owner == "" {
			row.Owner = "user " + strconv.FormatInt(m.OwnerID, 10)
		}
		if m.Burnable() {
			row.Expires = stamp(&m.ExpiresAt)
		}
		d.Rows = append(d.Rows, row)
	}
	d.Pager = newPager("/admin/secrets", q, page, total, len(list))
	return d, nil
}

func (a *Admin) secretsPage(w http.ResponseWriter, r *http.Request) {
	d, err := a.secretsData(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.page(w, r, http.StatusOK, "admin/secrets", "Secrets", d)
}

// burnSecretForm handles POST /admin/secrets/burn (ref: a link or an id).
func (a *Admin) burnSecretForm(w http.ResponseWriter, r *http.Request) {
	refail := func(status int, msg string) {
		d, err := a.secretsData(r)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		d.RefErr = msg
		a.page(w, r, status, "admin/secrets", "Secrets", d)
	}
	id, ok := secretID(r.PostFormValue("ref"))
	if !ok {
		refail(http.StatusUnprocessableEntity, msgBadRef)
		return
	}
	state, found, err := a.burn(r.Context(), r, id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !found {
		refail(http.StatusNotFound, "No secret has that id. It may have been deleted already.")
		return
	}
	code := "burned"
	if state != "revoked" {
		code = "gone"
	}
	redirect(w, r, back(r, "/admin/secrets", code))
}
