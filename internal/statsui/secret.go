package statsui

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
	"github.com/Battleroid/snap-share/internal/web"
)

// maxTimeline bounds the events shown for one secret; timelinePageSize
// is one page of them.
const (
	maxTimeline      = 500
	timelinePageSize = 20
)

type timelineRow struct {
	When   string // UTC, to the second
	Label  string // "revealed", "wrong passphrase", ...
	Step   string // "1 of 2" for reveals and failed attempts
	Status string // badge status
	IP     string
	Client string
}

type secretView struct {
	Secret      secrets.Summary
	Name        string
	StateStatus string
	Rows        []timelineRow
	Reveals     int
	Failures    int
	Notify      bool // a channel is on: show the switch
	Action      string
	Total       int // events, all pages
	First, Last int // 1-based range of Rows in Total
	Page, Pages int
	PageHref    string
}

// timeline labels a secret's events (newest first, as stored and shown),
// counting from the oldest: reveals up to the view limit, wrong
// passphrases up to the attempt limit.
func timeline(sm secrets.Summary, evs []stats.Event) ([]timelineRow, int, int) {
	rows := make([]timelineRow, len(evs))
	reveals, fails := 0, 0
	for i := len(evs) - 1; i >= 0; i-- { // evs are newest first
		e := evs[i]
		row := timelineRow{When: e.At.UTC().Format(time.DateTime), IP: e.IP, Client: e.UASummary}
		if row.Client == "" {
			row.Client = e.Bot
		}
		switch e.Kind {
		case stats.KindLinkOpened:
			row.Label, row.Status = "link opened", "info"
		case stats.KindRevealed:
			reveals++
			row.Label, row.Status, row.Step = "revealed", "ok", strconv.Itoa(reveals)+" of "+strconv.Itoa(sm.MaxViews)
		case stats.KindFailed:
			fails++
			row.Label, row.Status = "wrong passphrase", "warn"
			if sm.MaxFailures > 0 {
				row.Step = strconv.Itoa(fails) + " of " + strconv.Itoa(sm.MaxFailures)
			}
		case stats.KindBurned:
			row.Label, row.Status = "burned", "down"
			if sm.BurnReason != "" && sm.BurnReason != "viewed" {
				row.Step = sm.BurnReason
			}
		default:
			row.Label, row.Status = string(e.Kind), "unknown"
		}
		rows[i] = row
	}
	return rows, reveals, fails
}

func stateStatus(state string) string {
	switch state {
	case "ready":
		return "ok"
	case "uploading":
		return "paused"
	case "expired":
		return "unknown"
	}
	return "down"
}

// secretPage is GET /dashboard/secrets/{id}/timeline: the owner's full
// timeline of one secret, with time, IP and client for every event.
func (s *Service) secretPage(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if !p.Can(auth.ScopeSecretsRead) {
		s.d.Errors.Error(w, r, http.StatusForbidden, "This needs the secrets:read permission.")
		return
	}
	sm, err := s.d.Secrets.OwnerSecret(r.Context(), p.UserID, r.PathValue("id"))
	if errors.Is(err, secrets.ErrNotFound) {
		s.d.Errors.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	evs, err := stats.LatestSecretEvents(r.Context(), s.d.DB.R, sm.ID, maxTimeline)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v := secretView{Secret: sm, Name: sm.Note, StateStatus: stateStatus(sm.State), Action: "/dashboard/secrets/" + sm.ID + "/notify"}
	rows, reveals, fails := timeline(sm, evs)
	v.Reveals, v.Failures, v.Total = reveals, fails, len(rows)
	v.Pages = max(1, (v.Total+timelinePageSize-1)/timelinePageSize)
	v.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	v.Page = min(max(v.Page, 1), v.Pages)
	from := (v.Page - 1) * timelinePageSize
	v.Rows = rows[from:min(from+timelinePageSize, v.Total)]
	if v.Total > 0 {
		v.First, v.Last = from+1, from+len(v.Rows)
	}
	v.PageHref = SecretTimelinePath(sm.ID) + "?page="
	v.Notify = s.d.Notify != nil && s.d.Notify.Enabled(r.Context(), p.UserID)
	// htmx (the dashboard's timeline dialog and its pager) gets the body only.
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		s.d.Render.Fragment(w, r, http.StatusOK, "stats/secret", "secret-timeline", web.Page{Data: v})
		return
	}
	s.d.Render.Page(w, r, http.StatusOK, "stats/secret", web.Page{
		Title: "Secret timeline", Nav: "secrets", NoIndex: true, NoReferrer: true, Scripts: []string{"js/stats.js"}, Data: v,
	})
}

// secretNotify turns reveal notifications for one secret on or off
// (enabled=1|0): the secret's own notify flag.
func (s *Service) secretNotify(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if !p.Can(auth.ScopeSecretsWrite) {
		s.d.Errors.Error(w, r, http.StatusForbidden, "This needs the secrets:write permission.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	on := r.PostFormValue("enabled") == "1"
	id := r.PathValue("id")
	err := s.d.Secrets.SetNotify(r.Context(), p.UserID, id, on)
	if errors.Is(err, secrets.ErrNotFound) {
		s.d.Errors.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if web.IsAPI(r) {
		web.JSON(w, http.StatusOK, map[string]bool{"notify": on})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, SecretTimelinePath(id), http.StatusSeeOther) // #nosec G710 -- a local path; id was validated by SetNotify
}
