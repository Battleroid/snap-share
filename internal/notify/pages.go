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
	r.HandleFunc("POST /settings/notifications/default", s.saveDefault, web.SignedIn())
	r.HandleFunc("POST /settings/notifications/test", s.sendTest, web.SignedIn())
}

// Confirmations after a redirect (?saved=code). Fixed strings only.
var savedNotices = map[string]string{
	"ntfy":    "ntfy settings saved.",
	"default": "Saved.",
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
	Modes       []modeOption
}

type modeOption struct {
	Value Mode
	Label string
	Hint  string
}

var modeOptions = []modeOption{
	{ModeNone, "None", ""},
	{ModeFirst, "First open", ""},
	{ModeEvery, "Every open", "At most one message per item every 15 minutes."},
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
	d.Modes = modeOptions
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

func (s *Service) saveDefault(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	m := Mode(r.PostFormValue("event"))
	if !m.Valid() {
		s.render(w, r, http.StatusUnprocessableEntity, &pageData{Error: "Choose when to be notified."})
		return
	}
	if err := s.SetDefault(r.Context(), p.UserID, m); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, "/settings/notifications?saved=default")
}

// sendTest delivers a test message now and shows the result on the page.
func (s *Service) sendTest(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	ch := r.PostFormValue("channel")
	err := s.SendTest(r.Context(), p.UserID, ch)
	var de *deliveryError
	switch {
	case err == nil:
		name := "ntfy"
		if ch == ChannelEmail {
			name = "your email address"
		}
		s.render(w, r, http.StatusOK, &pageData{Notice: "Test sent to " + name + "."})
	case errors.Is(err, ErrTestLimit):
		s.render(w, r, http.StatusTooManyRequests, &pageData{Error: "Too many tests. Try again in a few minutes."})
	case errors.As(err, &de):
		s.render(w, r, http.StatusOK, &pageData{Error: "Test not delivered: " + de.msg + "."})
	default:
		s.serverError(w, r, err)
	}
}
