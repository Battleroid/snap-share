package notify

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

// Register adds the notification settings and inbox routes. All are
// browser-session only; the POSTs are covered by the cross-origin check like
// every form.
func (s *Service) Register(r *web.Router) {
	s.registerInbox(r)
	r.HandleFunc("GET /settings/notifications", s.page, web.SignedIn())
	r.HandleFunc("POST /settings/notifications/channel", s.saveChannel, web.SignedIn())
	r.HandleFunc("POST /settings/notifications/ntfy", s.saveNtfy, web.SignedIn())
	r.HandleFunc("POST /settings/notifications/events", s.saveEvents, web.SignedIn())
	r.HandleFunc("POST /settings/notifications/test", s.sendTest, web.SignedIn())
}

// Confirmations after a redirect (?saved=code). Fixed strings only.
var savedNotices = map[string]string{
	"ntfy":   "ntfy settings saved.",
	"events": "Saved.",
}

type pageData struct {
	Prefs       Prefs
	Email       string // the account's address
	EmailOK     bool   // mail is set up and the address is verified
	EmailReason string // why email can't be used
	NtfyOK      bool   // a topic is set
	PublicNtfy  bool   // ntfy.sh without a token: anyone who guesses the topic reads it
	EmailStatus Status
	NtfyStatus  Status
	Server      string
	Topic       string
	Errors      FieldErrors
	Notice      string
	Error       string
	Columns     []gridColumn
	Rows        []gridRow
}

// gridColumn is a channel of the events grid.
type gridColumn struct {
	Index  int // its column in the table, for the kit's "All events" boxes
	Name   string
	Label  string
	On     bool // the channel is on, so its boxes can be changed
	All    bool // every event is ticked
	Test   bool // has a test button
	TestOK bool // the test can be sent
}

type gridRow struct {
	EventInfo
	Cells []gridCell
}

type gridCell struct {
	Value   string // "event:channel"
	Channel string
	Aria    string
	Checked bool
	On      bool
}

// channelLabels are the grid's column headers.
var channelLabels = map[string]string{ChannelInbox: "bell", ChannelEmail: "email", ChannelNtfy: "ntfy"}

// grid builds the events table from the user's prefs.
func (d *pageData) grid() {
	on := map[string]bool{ChannelInbox: true, ChannelEmail: d.Prefs.EmailEnabled, ChannelNtfy: d.Prefs.NtfyEnabled}
	d.Columns = nil
	for i, ch := range AllChannels {
		c := gridColumn{Index: i + 1, Name: ch, Label: channelLabels[ch], On: on[ch], All: true, Test: ch != ChannelInbox}
		c.TestOK = (ch == ChannelEmail && d.EmailOK) || (ch == ChannelNtfy && d.NtfyOK)
		for _, e := range Events {
			c.All = c.All && d.Prefs.Events.On(e.Name, ch)
		}
		d.Columns = append(d.Columns, c)
	}
	d.Rows = nil
	for _, e := range Events {
		row := gridRow{EventInfo: e}
		for _, ch := range AllChannels {
			row.Cells = append(row.Cells, gridCell{Value: e.Name + ":" + ch, Channel: ch, Aria: e.Label + " by " + channelLabels[ch],
				Checked: d.Prefs.Events.On(e.Name, ch), On: on[ch]})
		}
		d.Rows = append(d.Rows, row)
	}
}

func (s *Service) load(r *http.Request, d *pageData) error {
	p := auth.FromContext(r.Context())
	prefs, err := s.Prefs(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	d.Prefs = prefs
	if d.Server == "" && d.Topic == "" && d.Errors == nil {
		d.Server, d.Topic = prefs.NtfyServer, prefs.NtfyTopic
	}
	var email sql.NullString
	var verified sql.NullInt64
	err = s.d.DB.R.QueryRowContext(r.Context(), `SELECT email, email_verified_at FROM users WHERE id = ?`, p.UserID).Scan(&email, &verified)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	d.Email = email.String
	switch {
	case !s.mailEnabled():
		d.EmailReason = "Email is not set up on this server."
	case d.Email == "" || !verified.Valid:
		d.EmailReason = "Verify your email address first."
	default:
		d.EmailOK = true
	}
	d.NtfyOK = prefs.NtfyTopic != ""
	d.PublicNtfy = prefs.NtfyServer == defaultNtfyServer && !prefs.NtfyTokenSet
	st, err := s.Statuses(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	d.EmailStatus, d.NtfyStatus = st[ChannelEmail], st[ChannelNtfy]
	d.grid()
	return nil
}

func (s *Service) render(w http.ResponseWriter, r *http.Request, status int, d *pageData) {
	if err := s.load(r, d); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.d.Render.Page(w, r, status, "notify/settings", web.Page{
		Title: "Notifications", Nav: "settings", Scripts: []string{"js/notify.js"}, Data: d,
	})
}

func (s *Service) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("notify: settings", "err", err)
	s.d.Errors.Error(w, r, http.StatusInternalServerError, "Something went wrong.")
}

func (s *Service) page(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, &pageData{Notice: savedNotices[r.URL.Query().Get("saved")]})
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// saveChannel is the Switch endpoint: channel=email|ntfy, enabled=1|0. It
// answers JSON for fetch and redirects plain form posts.
func (s *Service) saveChannel(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	ch := r.PostFormValue("channel")
	on := r.PostFormValue("enabled") == "1"
	prefs, err := s.Prefs(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	switch ch {
	case ChannelEmail:
		if on {
			d := &pageData{}
			if err := s.load(r, d); err != nil {
				s.serverError(w, r, err)
				return
			}
			if !d.EmailOK {
				s.d.Errors.Error(w, r, http.StatusConflict, d.EmailReason)
				return
			}
		}
	case ChannelNtfy:
		if on && prefs.NtfyTopic == "" {
			s.d.Errors.Error(w, r, http.StatusConflict, "Set an ntfy topic first.")
			return
		}
	default:
		s.d.Errors.Error(w, r, http.StatusBadRequest, "Unknown channel.")
		return
	}
	if err := s.SetChannel(r.Context(), p.UserID, ch, on); err != nil {
		s.serverError(w, r, err)
		return
	}
	if !web.IsAPI(r) {
		redirect(w, r, "/settings/notifications")
		return
	}
	prefs, err = s.Prefs(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	web.JSON(w, http.StatusOK, map[string]bool{"email": prefs.EmailEnabled, "ntfy": prefs.NtfyEnabled})
}

// saveNtfy saves the server, topic and token. An empty token field keeps
// the stored token; "remove token" clears it. The token is never sent
// back to the browser.
func (s *Service) saveNtfy(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	in := NtfyInput{Server: r.PostFormValue("server"), Topic: r.PostFormValue("topic")}
	if tok := strings.TrimSpace(r.PostFormValue("token")); tok != "" {
		in.Token = &tok
	} else if r.PostFormValue("remove_token") == "1" {
		empty := ""
		in.Token = &empty
	}
	err := s.SaveNtfy(r.Context(), p.UserID, in)
	var fe FieldErrors
	if errors.As(err, &fe) {
		s.render(w, r, http.StatusUnprocessableEntity, &pageData{Server: in.Server, Topic: in.Topic, Errors: fe})
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/settings/notifications?saved=ntfy")
}

// saveEvents saves the events grid. "on" lists the ticked boxes as
// event:channel; "channel" lists the columns being saved (those whose
// channel is on), so a disabled column, whose boxes the browser does not
// send, keeps its rows. It answers JSON for fetch and redirects plain form
// posts.
func (s *Service) saveEvents(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		s.d.Errors.Error(w, r, http.StatusBadRequest, "Bad request.")
		return
	}
	err := s.SetEvents(r.Context(), p.UserID, r.PostForm["channel"], r.PostForm["on"])
	switch {
	case errors.Is(err, ErrUnknownEvent):
		if web.IsAPI(r) {
			s.d.Errors.Error(w, r, http.StatusUnprocessableEntity, "Unknown event or channel.")
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, &pageData{Error: "Unknown event or channel."})
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	if web.IsAPI(r) {
		web.JSON(w, http.StatusOK, map[string]bool{"saved": true})
		return
	}
	redirect(w, r, "/settings/notifications?saved=events")
}

// sendTest delivers a test message now and shows the result: JSON for the
// grid's Test buttons (fetch), the page for a plain form post.
func (s *Service) sendTest(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	ch := r.PostFormValue("channel")
	err := s.SendTest(r.Context(), p.UserID, ch)
	var de *deliveryError
	api := web.IsAPI(r)
	switch {
	case err == nil:
		name := "ntfy"
		if ch == ChannelEmail {
			name = "your email address"
		}
		if api {
			web.JSON(w, http.StatusOK, map[string]string{"message": "Test sent to " + name + "."})
			return
		}
		s.render(w, r, http.StatusOK, &pageData{Notice: "Test sent to " + name + "."})
	case errors.Is(err, ErrTestLimit):
		if api {
			s.d.Errors.Error(w, r, http.StatusTooManyRequests, "Too many tests. Try again in a few minutes.")
			return
		}
		s.render(w, r, http.StatusTooManyRequests, &pageData{Error: "Too many tests. Try again in a few minutes."})
	case errors.As(err, &de):
		if api {
			s.d.Errors.Error(w, r, http.StatusBadGateway, "Test not delivered: "+de.msg+".")
			return
		}
		s.render(w, r, http.StatusOK, &pageData{Error: "Test not delivered: " + de.msg + "."})
	default:
		s.serverError(w, r, err)
	}
}
