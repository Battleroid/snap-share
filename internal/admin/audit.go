package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
)

// auditRow is one audit entry as the pages show it.
type auditRow struct {
	At                                 time.Time
	Actor, IP, Action, Target, Details string
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
			At: e.At, Actor: actorName(e), IP: dash(e.IP),
			Action: e.Action, Target: dash(e.Target), Details: dash(details),
		})
	}
	return out
}

// dtTable is the JSON block of a kit TableTools table (pb-dt): its columns
// and rows. The kit renders it in the browser.
type dtTable struct {
	Columns []dtColumn `json:"columns"`
	Rows    []dtRow    `json:"rows"`
}

// dtColumn is one TableTools column. Sort is always sent: the audit table is
// sorted and filtered on the server, so its columns never sort one page.
type dtColumn struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Mono   bool   `json:"mono,omitempty"`
	Sticky bool   `json:"sticky,omitempty"`
	Width  int    `json:"width,omitempty"`
	Sort   bool   `json:"sort"`
}

// dtRow is one audit entry for the table: its cells, and the pairs its
// expanded row shows.
type dtRow struct {
	At      string  `json:"at"`
	Actor   string  `json:"actor"`
	IP      string  `json:"ip"`
	Action  string  `json:"action"`
	Target  string  `json:"target"`
	Details string  `json:"details"`
	Detail  dtPairs `json:"detail,omitempty"`
}

// dtPairs is a row's detail, label → value, kept in order (a map would sort
// its keys).
type dtPairs [][2]string

func (p dtPairs) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kv := range p {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(kv[0])
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(kv[1])
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

var auditColumns = []dtColumn{
	{Key: "at", Label: "Time", Mono: true, Sticky: true},
	{Key: "actor", Label: "Actor", Mono: true},
	{Key: "ip", Label: "IP", Mono: true},
	{Key: "action", Label: "Action", Mono: true},
	{Key: "target", Label: "Target", Mono: true},
	{Key: "details", Label: "Details", Mono: true, Width: 140},
}

// auditTable is the audit page's TableTools table. An entry's expanded row
// shows its exact time, actor, IP and target (hidden on narrow screens), then each
// field of its details object and the details as they were stored (the cell
// is cut to one line).
func auditTable(rows []auditRow, raw []accounts.AuditRecord) dtTable {
	t := dtTable{Columns: auditColumns, Rows: make([]dtRow, 0, len(rows))}
	for i, r := range rows {
		detail := dtPairs{{"time", r.At.UTC().Format("2006-01-02 15:04:05") + " UTC"}, {"actor", r.Actor}, {"ip", r.IP}, {"target", r.Target}}
		detail = append(detail, detailPairs(raw[i].Details)...)
		if d := raw[i].Details; d != "" && d != "{}" {
			detail = append(detail, [2]string{"json", d})
		}
		t.Rows = append(t.Rows, dtRow{
			At: r.At.UTC().Format("2006-01-02 15:04"), Actor: r.Actor, IP: r.IP,
			Action: r.Action, Target: r.Target, Details: r.Details, Detail: detail,
		})
	}
	return t
}

// detailPairs lists the fields of a details object in its own order, strings
// as they are and anything else as compact JSON. Details that are not an
// object give none (the Details cell still shows them).
func detailPairs(details string) dtPairs {
	dec := json.NewDecoder(strings.NewReader(details))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var out dtPairs
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			var c bytes.Buffer
			if json.Compact(&c, v) != nil {
				return nil
			}
			s = c.String()
		}
		out = append(out, [2]string{key, s})
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
	Table                   dtTable
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
	d.Table = auditTable(d.Rows, list)
	d.Pager = newPager("/admin/audit", q, page, total, len(list))
	a.page(w, r, status, "admin/audit", "Audit log", d)
}
