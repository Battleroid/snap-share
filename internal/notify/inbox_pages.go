package notify

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

// registerInbox adds the inbox routes. All are browser-session only; the
// POSTs are covered by the cross-origin check like every form.
func (s *Service) registerInbox(r *web.Router) {
	r.HandleFunc("GET /notifications", s.inboxPage, web.SignedIn())
	r.HandleFunc("GET /notifications/panel", s.inboxPanel, web.SignedIn())
	r.HandleFunc("GET /notifications/stream", s.stream, web.SignedIn())
	r.HandleFunc("POST /notifications/read-all", s.readAll, web.SignedIn())
	r.HandleFunc("POST /notifications/{id}/read", s.readOne, web.SignedIn())
}

// entryView is an entry as a template shows it.
type entryView struct {
	Entry
	Time string // "2m"
	ISO  string
}

type inboxData struct {
	Entries []entryView
	Page    int
	Pages   int
}

func (s *Service) views(es []Entry) []entryView {
	now := s.now()
	out := make([]entryView, len(es))
	for i, e := range es {
		out[i] = entryView{Entry: e, Time: ago(e.At, now), ISO: e.At.UTC().Format(time.RFC3339)}
	}
	return out
}

func (s *Service) inboxError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("notify: inbox", "err", err)
	s.d.Errors.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
}

// inboxPage is the full list, newest first, inboxPageSize to a page.
func (s *Service) inboxPage(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	total, err := s.inboxTotal(r.Context(), p.UserID)
	if err != nil {
		s.inboxError(w, r, err)
		return
	}
	pages := max(1, (total+inboxPageSize-1)/inboxPageSize)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = min(max(page, 1), pages)
	es, err := s.Inbox(r.Context(), p.UserID, inboxPageSize, (page-1)*inboxPageSize)
	if err != nil {
		s.inboxError(w, r, err)
		return
	}
	s.d.Render.Page(w, r, http.StatusOK, "notify/inbox", web.Page{
		Title: "Notifications", Data: inboxData{Entries: s.views(es), Page: page, Pages: pages},
	})
}

// inboxPanel is the bell's list (htmx): the newest inboxPanelSize entries.
func (s *Service) inboxPanel(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	es, err := s.Inbox(r.Context(), p.UserID, inboxPanelSize, 0)
	if err != nil {
		s.inboxError(w, r, err)
		return
	}
	s.d.Render.Fragment(w, r, http.StatusOK, "notify/inbox", "inbox-panel", web.Page{Data: inboxData{Entries: s.views(es)}})
}

// readOne marks one of the user's notifications read. Anyone else's id is
// a 404, the same as one that does not exist.
func (s *Service) readOne(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.d.Errors.Error(w, r, http.StatusNotFound, "No such notification.")
		return
	}
	n, err := s.MarkRead(r.Context(), p.UserID, id)
	switch {
	case errors.Is(err, ErrNoEntry):
		s.d.Errors.Error(w, r, http.StatusNotFound, "No such notification.")
		return
	case err != nil:
		s.inboxError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, map[string]int{"unread": n})
}

// readAll marks every notification of the user read. A plain form post
// goes back to the list.
func (s *Service) readAll(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if err := s.MarkAllRead(r.Context(), p.UserID); err != nil {
		s.inboxError(w, r, err)
		return
	}
	if !web.IsAPI(r) {
		redirect(w, r, "/notifications")
		return
	}
	web.JSON(w, http.StatusOK, map[string]int{"unread": 0})
}
