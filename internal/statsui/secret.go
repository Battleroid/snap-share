package statsui

import (
	"errors"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
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
	Day    string // UTC date, the Timeline's day heading
	Clock  string // UTC time of day, to the second
	Label  string // "revealed", "wrong passphrase", ...
	Step   string // "1 of 2" for reveals and failed attempts
	Status string // badge status
	Icon   string // the mark's kit icon
	IP     string
	Client string
	Viewer string // the viewer chip's id ("v1")
	VLabel string // "Visitor 2", or the preview bot's name
	VHue   int    // 1-6, the viewer's marker color
}

// viewer is one distinct visitor of a secret, for the Viewers strip. The
// visitor key itself never leaves the server: the page sees the chip's
// ordinal, its label and a hue derived from the key.
type viewer struct {
	ID       string
	Label    string
	Hue      int
	IP       string
	Client   string
	Opened   int
	Revealed int
	Failed   int
	Total    int
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
	Viewers     []viewer // from every event, not just this page
	EndNote     string   // on the last page: older events are gone
}

// timeline labels a secret's events (newest first, as stored and shown),
// counting from the oldest: reveals up to the view limit, wrong
// passphrases up to the attempt limit.
// viewerKey is what tells visitors of a secret apart: a preview bot by its
// name, a person by their visitor key (a hash of IP and User-Agent), or,
// for events recorded without one, by IP and client.
func viewerKey(e stats.Event) string {
	switch {
	case e.Bot != "":
		return "bot:" + e.Bot
	case e.VisitorKey != "":
		return "key:" + e.VisitorKey
	}
	return "ipua:" + e.IP + "|" + e.UASummary
}

// hueOf is a stable 1-6 from a viewer key, as tag pills hash their names.
func hueOf(key string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32()%6) + 1
}

// timeline labels a secret's events (newest first, as stored and shown),
// counting from the oldest: reveals up to the view limit, wrong
// passphrases up to the attempt limit. It also groups the events by viewer:
// "Visitor N" in order of first appearance, or the preview bot's name.
func timeline(sm secrets.Summary, evs []stats.Event) ([]timelineRow, []viewer, int, int) {
	rows := make([]timelineRow, len(evs))
	reveals, fails, people := 0, 0, 0
	chips := map[string]*viewer{}
	usedHue := map[int]bool{}
	var order []*viewer
	for i := len(evs) - 1; i >= 0; i-- { // evs are newest first
		e := evs[i]
		at := e.At.UTC()
		row := timelineRow{Day: at.Format(time.DateOnly), Clock: at.Format(time.TimeOnly), IP: e.IP, Client: e.UASummary}
		if row.Client == "" {
			row.Client = e.Bot
		}
		k := viewerKey(e)
		v := chips[k]
		if v == nil {
			v = &viewer{ID: "v" + strconv.Itoa(len(order)+1), Hue: hueOf(k), Label: e.Bot}
			// A hue another viewer has is skipped, while there are hues left.
			for len(usedHue) < 6 && usedHue[v.Hue] {
				v.Hue = v.Hue%6 + 1
			}
			usedHue[v.Hue] = true
			if e.Bot == "" {
				people++
				v.Label = "Visitor " + strconv.Itoa(people)
			}
			chips[k] = v
			order = append(order, v)
		}
		v.Total++
		v.IP, v.Client = e.IP, row.Client // the newest event's
		row.Viewer, row.VLabel, row.VHue = v.ID, v.Label, v.Hue
		switch e.Kind {
		case stats.KindLinkOpened:
			v.Opened++
			row.Label, row.Status, row.Icon = "link opened", "info", "link"
		case stats.KindRevealed:
			reveals++
			v.Revealed++
			row.Label, row.Status, row.Icon, row.Step = "revealed", "ok", "unlock", strconv.Itoa(reveals)+" of "+strconv.Itoa(sm.MaxViews)
		case stats.KindFailed:
			fails++
			v.Failed++
			row.Label, row.Status, row.Icon = "wrong passphrase", "warn", "status-warn"
			if sm.MaxFailures > 0 {
				row.Step = strconv.Itoa(fails) + " of " + strconv.Itoa(sm.MaxFailures)
			}
		case stats.KindBurned:
			row.Label, row.Status, row.Icon = "burned", "down", "status-down"
			if sm.BurnReason != "" && sm.BurnReason != "viewed" {
				row.Step = sm.BurnReason
			}
		default:
			row.Label, row.Status, row.Icon = string(e.Kind), "", "clock"
		}
		rows[i] = row
	}
	viewers := make([]viewer, len(order))
	for i, v := range order {
		viewers[i] = *v
	}
	return rows, viewers, reveals, fails
}

// endNote is the quiet line under the oldest events: why there is nothing
// older. retention is the retention period in days, 0 when unknown.
func endNote(sm secrets.Summary, now time.Time, retention, total int) string {
	var parts []string
	if retention > 0 && sm.Created.Before(now.AddDate(0, 0, -retention)) {
		parts = append(parts, "Events older than "+strconv.Itoa(retention)+" "+plural(int64(retention), "day", "days")+" are removed.")
	}
	if total >= maxTimeline {
		parts = append(parts, "Only the latest "+strconv.Itoa(maxTimeline)+" events are shown.")
	}
	return strings.Join(parts, " ")
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
	rows, viewers, reveals, fails := timeline(sm, evs)
	v.Reveals, v.Failures, v.Total, v.Viewers = reveals, fails, len(rows), viewers
	v.Pages = max(1, (v.Total+timelinePageSize-1)/timelinePageSize)
	v.Page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	v.Page = min(max(v.Page, 1), v.Pages)
	from := (v.Page - 1) * timelinePageSize
	v.Rows = rows[from:min(from+timelinePageSize, v.Total)]
	if v.Total > 0 {
		v.First, v.Last = from+1, from+len(v.Rows)
	}
	v.PageHref = SecretTimelinePath(sm.ID) + "?page="
	if v.Page == v.Pages {
		retention := 0
		if s.d.RetentionDays != nil {
			retention = s.d.RetentionDays()
		}
		v.EndNote = endNote(sm, s.now(), retention, v.Total)
	}
	v.Notify = s.d.Notify != nil && s.d.Notify.Enabled(r.Context(), p.UserID)
	// htmx gets only what it swaps: the dashboard's timeline dialog, or the
	// body of this page (its pager).
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		block := "secret-timeline"
		if r.Header.Get("HX-Target") == "secret-timeline-dialog" {
			block = "secret-timeline-dialog"
		}
		s.d.Render.Fragment(w, r, http.StatusOK, "stats/secret", block, web.Page{Data: v})
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
