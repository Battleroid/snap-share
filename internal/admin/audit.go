package admin

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
)

// auditRow is one audit entry as the pages show it.
type auditRow struct {
	At, Actor, IP, Action, Target, Details string
}

// actorName says who did it: a username (with the key, for API keys), "cli"
// for the admin CLI, or "—".
func actorName(e accounts.AuditRecord) string {
	switch {
	case e.ActorName != "" && e.ActorKeyID != 0:
		return e.ActorName + " · key " + strconv.FormatInt(e.ActorKeyID, 10)
	case e.ActorName != "":
		return e.ActorName
	case e.ActorUserID != 0:
		return "user " + strconv.FormatInt(e.ActorUserID, 10)
	case strings.Contains(e.Details, `"via":"cli"`):
		return "cli"
	}
	return "—"
}

func auditRows(list []accounts.AuditRecord) []auditRow {
	out := make([]auditRow, 0, len(list))
	for _, e := range list {
		details := e.Details
		if details == "{}" {
			details = ""
		}
		out = append(out, auditRow{
			At: e.At.UTC().Format("2006-01-02 15:04:05"), Actor: actorName(e), IP: dash(e.IP),
			Action: e.Action, Target: dash(e.Target), Details: dash(details),
		})
	}
	return out
}

const dateLayout = "2006-01-02"

// auditFilter reads action, actor, from and to (YYYY-MM-DD, both inclusive)
// from q. It returns a message for a bad date.
func auditFilter(q url.Values) (accounts.AuditFilter, string) {
	f := accounts.AuditFilter{Action: strings.TrimSpace(q.Get("action")), Actor: strings.TrimSpace(q.Get("actor"))}
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		t, err := time.Parse(dateLayout, v)
		if err != nil {
			return f, "Dates look like 2026-11-22."
		}
		f.From = t
	}
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		t, err := time.Parse(dateLayout, v)
		if err != nil {
			return f, "Dates look like 2026-11-22."
		}
		f.To = t.AddDate(0, 0, 1)
	}
	return f, ""
}

type auditData struct {
	base
	Action, Actor, From, To string
	Actions                 []map[string]any
	Rows                    []auditRow
	Pager                   pager
}

func (a *Admin) auditPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	d := auditData{base: a.base(r, "/admin/audit"), Action: q.Get("action"), Actor: q.Get("actor"), From: q.Get("from"), To: q.Get("to")}
	f, msg := auditFilter(q)
	status := http.StatusOK
	if msg != "" {
		d.Error, status = msg, http.StatusBadRequest
		f = accounts.AuditFilter{}
	}
	page, offset := pageOf(r)
	f.Limit, f.Offset = perPage, offset
	list, total, err := a.d.Accounts.Store().ListAudit(ctx, f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	actions, err := a.d.Accounts.Store().AuditActions(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d.Actions = []map[string]any{{"value": "", "label": "all actions"}}
	for _, act := range actions {
		d.Actions = append(d.Actions, map[string]any{"value": act, "label": act})
	}
	d.Rows = auditRows(list)
	d.Pager = newPager("/admin/audit", q, page, total, len(list))
	a.page(w, r, status, "admin/audit", "Audit log", d)
}
