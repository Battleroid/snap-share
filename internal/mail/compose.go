package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
)

//go:embed templates
var templateFS embed.FS

// Message kinds with templates in templates/<kind>.txt (defines "subject" and
// "text") and templates/<kind>.html (defines "body", wrapped in layout.html).
const (
	KindInvite      = "invite"
	KindVerify      = "verify"
	KindEmailChange = "email_change"
	KindReset       = "reset"
	KindTest        = "test"
)

// Data fills a template. Fields a kind does not use stay empty.
type Data struct {
	Site     string // site name
	URL      string // the link the message is about
	Expires  string // human duration, e.g. "48 hours"
	Username string // recipient's username, when they have one
	By       string // who caused the message (inviter, admin)
}

// Templates renders the embedded message templates.
type Templates struct {
	text map[string]*texttemplate.Template
	html map[string]*htmltemplate.Template
}

// LoadTemplates parses every embedded template.
func LoadTemplates() (*Templates, error) {
	t := &Templates{text: map[string]*texttemplate.Template{}, html: map[string]*htmltemplate.Template{}}
	for _, kind := range []string{KindInvite, KindVerify, KindEmailChange, KindReset, KindTest} {
		tt, err := texttemplate.ParseFS(templateFS, "templates/"+kind+".txt")
		if err != nil {
			return nil, fmt.Errorf("mail template %s: %w", kind, err)
		}
		ht, err := htmltemplate.ParseFS(templateFS, "templates/layout.html", "templates/"+kind+".html")
		if err != nil {
			return nil, fmt.Errorf("mail template %s: %w", kind, err)
		}
		t.text[kind], t.html[kind] = tt, ht
	}
	return t, nil
}

// Compose renders kind for one recipient.
func (t *Templates) Compose(kind, to string, d Data) (Message, error) {
	tt, ok := t.text[kind]
	if !ok {
		return Message{}, fmt.Errorf("mail: unknown kind %q", kind)
	}
	var subj, text, html bytes.Buffer
	if err := tt.ExecuteTemplate(&subj, "subject", d); err != nil {
		return Message{}, err
	}
	if err := tt.ExecuteTemplate(&text, "text", d); err != nil {
		return Message{}, err
	}
	if err := t.html[kind].ExecuteTemplate(&html, "layout", d); err != nil {
		return Message{}, err
	}
	return Message{
		To:      []string{to},
		Subject: strings.TrimSpace(subj.String()),
		Text:    strings.TrimLeft(text.String(), "\n"),
		HTML:    html.String(),
	}, nil
}
