package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/config"
)

func TestCompose(t *testing.T) {
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	d := Data{Site: "snap", URL: "https://snap.test/reset#t=abc", Expires: "1 hour", Username: "ann", By: "root"}
	for _, kind := range []string{KindInvite, KindVerify, KindEmailChange, KindReset, KindTest} {
		m, err := tpl.Compose(kind, "ann@example.com", d)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if m.Subject == "" || strings.Contains(m.Subject, "\n") || m.Text == "" || !strings.Contains(m.HTML, "<html") {
			t.Errorf("%s: incomplete message %+v", kind, m)
		}
		if kind != KindTest && (!strings.Contains(m.Text, d.URL) || !strings.Contains(m.HTML, `href="https://snap.test/reset#t=abc"`)) {
			t.Errorf("%s: link missing", kind)
		}
		if len(m.To) != 1 || m.To[0] != "ann@example.com" {
			t.Errorf("%s: to %v", kind, m.To)
		}
	}
	// The shell is the kit's alert email: table layout with inline styles, a dark scheme, and the
	// kit's accent on the button.
	m, _ := tpl.Compose(KindReset, "ann@example.com", d)
	for _, want := range []string{"prefers-color-scheme: dark", `role="presentation"`, "background:#1f3fd6", "Reset password"} {
		if !strings.Contains(m.HTML, want) {
			t.Errorf("reset mail lacks %q", want)
		}
	}
	// HTML bodies escape data.
	m, _ = tpl.Compose(KindInvite, "x@example.com", Data{Site: "<b>site</b>", URL: "https://snap.test/invite#t=x", Expires: "7 days"})
	if strings.Contains(m.HTML, "<b>site</b>") {
		t.Error("site name not escaped in HTML")
	}
}

// fakeSMTP accepts one plain-text SMTP session and records the DATA section.
func fakeSMTP(t *testing.T) (port int, got chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got = make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		r := bufio.NewReader(c)
		say := func(s string) { _, _ = c.Write([]byte(s + "\r\n")) }
		say("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					got <- data.String()
					say("250 queued")
					continue
				}
				data.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250-fake")
				say("250 8BITMIME")
			case strings.HasPrefix(cmd, "DATA"):
				inData = true
				say("354 go ahead")
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				return
			default: // MAIL, RCPT, NOOP, RSET
				say("250 ok")
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestSMTPNoTLS(t *testing.T) {
	port, got := fakeSMTP(t)
	s := NewSMTP(config.SMTP{Host: "127.0.0.1", Port: port, TLS: "none", From: "snap <snap@example.com>"})
	err := s.Send(context.Background(), Message{To: []string{"ann@example.com"}, Subject: "Hello", Text: "plain body", HTML: "<p>html body</p>"})
	if err != nil {
		t.Fatal(err)
	}
	data := <-got
	for _, want := range []string{"Subject: Hello", "To: <ann@example.com>", "plain body", "html body", "multipart/alternative"} {
		if !strings.Contains(data, want) {
			t.Errorf("message lacks %q:\n%s", want, data)
		}
	}
}

func TestSMTPStartTLSRequired(t *testing.T) {
	// The fake server offers no STARTTLS, so starttls mode must refuse to send in the clear.
	port, _ := fakeSMTP(t)
	s := NewSMTP(config.SMTP{Host: "127.0.0.1", Port: port, TLS: "starttls", From: "snap@example.com"})
	if err := s.Send(context.Background(), Message{To: []string{"a@example.com"}, Subject: "x", Text: "secret body"}); err == nil {
		t.Fatal("sent without TLS")
	} else if strings.Contains(err.Error(), "secret body") {
		t.Fatal("error leaks the body")
	}
}
