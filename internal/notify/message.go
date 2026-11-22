package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"
	"unicode"

	"github.com/Battleroid/snap-share/internal/stats"
)

// Message kinds.
const (
	KindOpen          = "open"         // an item was opened (first or every open)
	KindOpenSummary   = "open_summary" // opens held back by coalescing
	KindSecretReveal  = "secret_revealed"
	KindSecretBurned  = "secret_burned" // destroyed after failed passphrase attempts
	KindTest          = "test"
	maxFieldRunes     = 120
	maxTitleRunes     = 160
	maxDetailRunes    = 400
	coalesceWindow    = 15 * time.Minute
	defaultNtfyServer = "https://ntfy.sh"
)

// Message is one notification, rendered for every channel. It is stored in
// the notify.send job payload, so it never holds a token, a secret's link
// or key material.
type Message struct {
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`            // "photo.jpg opened"
	Detail   string   `json:"detail,omitempty"` // "Firefox · macOS · 203.0.113.4 · 14:03 UTC"
	Click    string   `json:"click,omitempty"`  // the item's link, or the owner's dashboard
	Priority int      `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// Line is the one-line form: title and detail joined with " · ".
func (m Message) Line() string { return joinDot(m.Title, m.Detail) }

// visitor is who caused an event, as shown in a message.
type visitor struct {
	Client, IP string
	At         time.Time
}

func visitorOf(e stats.Event) visitor {
	client := e.UASummary
	if client == "" {
		client = e.Bot
	}
	return visitor{Client: client, IP: e.IP, At: e.At}
}

// detail is "Firefox · macOS · 203.0.113.4 · 14:03 UTC"; parts that are
// unknown are left out.
func (v visitor) detail(now time.Time) string {
	return joinDot(oneLine(v.Client, maxFieldRunes), oneLine(v.IP, 64), clock(v.At, now))
}

// clock formats t as "14:03 UTC", with the date when t is not today (UTC).
func clock(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t, now = t.UTC(), now.UTC()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04 UTC")
	}
	return t.Format("2006-01-02 15:04 UTC")
}

// openMessage is "photo.jpg opened · Firefox · macOS · 203.0.113.4 · 14:03 UTC".
func openMessage(name, link string, v visitor, now time.Time) Message {
	return Message{
		Kind: KindOpen, Title: oneLine(name, maxFieldRunes) + " opened", Detail: v.detail(now),
		Click: link, Tags: []string{"snap", "open"},
	}
}

// summaryMessage reports opens held back by coalescing:
// "photo.jpg opened 3 times · since 13:48 UTC · last Firefox · macOS · 203.0.113.4 · 14:03 UTC".
func summaryMessage(name, link string, n int64, since time.Time, last *visitor, now time.Time) Message {
	title := oneLine(name, maxFieldRunes) + " opened"
	if n == 1 {
		title += " again"
	} else {
		title += fmt.Sprintf(" %d more times", n)
	}
	detail := ""
	if !since.IsZero() {
		detail = "since " + clock(since, now)
	}
	if last != nil {
		detail = joinDot(detail, "last "+last.detail(now))
	}
	return Message{Kind: KindOpenSummary, Title: title, Detail: detail, Click: link, Tags: []string{"snap", "open"}}
}

// secretName is `Secret "prod db"` or "Secret".
func secretName(note string) string {
	if note = oneLine(note, maxFieldRunes); note != "" {
		return fmt.Sprintf("Secret %q", note)
	}
	return "Secret"
}

// revealMessage never names the secret's link or id: only the owner's
// note, who opened it and the dashboard URL.
func revealMessage(note string, viewsLeft, maxViews int, v visitor, dashboard string, now time.Time) Message {
	left := fmt.Sprintf("%d of %d views left", viewsLeft, maxViews)
	return Message{
		Kind: KindSecretReveal, Title: secretName(note) + " opened", Detail: joinDot(v.detail(now), left),
		Click: dashboard, Tags: []string{"snap", "reveal"},
	}
}

func burnMessage(note string, v visitor, dashboard string, now time.Time) Message {
	d := v.detail(now)
	if d != "" {
		d = "last attempt " + d
	}
	return Message{
		Kind: KindSecretBurned, Title: secretName(note) + " destroyed after failed passphrase attempts", Detail: d,
		Click: dashboard, Priority: 4, Tags: []string{"snap", "burn"},
	}
}

func testMessage(site, settingsURL string, now time.Time) Message {
	return Message{
		Kind: KindTest, Title: "Test from " + oneLine(site, 64), Detail: "Notifications work · " + clock(now, now),
		Click: settingsURL, Tags: []string{"snap", "test"},
	}
}

// emailParts renders the message as an email: the one-line form as the
// subject, plain text and a simple HTML version.
func (m Message) emailParts(site, settingsURL string) (subject, text, html string, err error) {
	subject = oneLine(m.Line(), 200)
	var b strings.Builder
	b.WriteString(m.Line())
	b.WriteString("\n")
	if m.Click != "" {
		b.WriteString("\n" + m.Click + "\n")
	}
	b.WriteString("\n--\nNotification settings: " + settingsURL + "\n")
	var h bytes.Buffer
	err = emailHTML.Execute(&h, map[string]string{
		"Site": site, "Title": m.Title, "Detail": m.Detail, "Click": m.Click, "Settings": settingsURL,
	})
	return subject, b.String(), h.String(), err
}

var emailHTML = template.Must(template.New("email").Parse(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Site}}</title></head>
<body style="margin:0;padding:24px;background:#f7f3ea;color:#17150f;font:16px/24px Georgia,'Times New Roman',serif">
<div style="max-width:520px;margin:0 auto;padding:24px;background:#ffffff;border:1px solid #e4ddcc;border-radius:12px">
<p style="margin:0 0 16px;font:600 19px/26px Georgia,'Times New Roman',serif">{{.Site}}</p>
<p style="margin:0">{{.Title}}</p>
{{with .Detail}}<p style="margin:8px 0 0;font:14px/20px monospace;color:#5c574b">{{.}}</p>{{end}}
{{with .Click}}<p style="margin:16px 0 0;font-size:14px;line-height:20px;word-break:break-all"><a href="{{.}}" style="color:#2f5bd3">{{.}}</a></p>{{end}}
<p style="margin:16px 0 0;color:#5c574b;font-size:14px;line-height:20px"><a href="{{.Settings}}" style="color:#5c574b">Notification settings</a></p>
</div>
</body>
</html>`))

// oneLine replaces control, format and line-separator characters with
// spaces, collapses runs of spaces and cuts s to limit runes. Visitor
// User-Agents and file names end up in mail subjects and HTTP headers.
func oneLine(s string, limit int) string {
	var b strings.Builder
	n, space := 0, false
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) || unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if n >= limit {
			break
		}
		if space {
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func joinDot(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}
