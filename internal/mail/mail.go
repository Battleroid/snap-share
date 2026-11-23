// Package mail sends email. The SMTP implementation is added by the accounts
// package; this file defines the interface and test/dev senders.
package mail

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Message is one email. Text is required; HTML is optional.
type Message struct {
	To      []string
	ReplyTo string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ErrDisabled is returned when no mail transport is configured.
var ErrDisabled = errors.New("mail is not configured")

// Disabled refuses to send.
type Disabled struct{}

// Send implements Sender.
func (Disabled) Send(context.Context, Message) error { return ErrDisabled }

// Fake records messages (tests).
type Fake struct {
	mu   sync.Mutex
	sent []Message
	fail error
}

// Send implements Sender.
func (f *Fake) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, m)
	return nil
}

// FailWith makes every later Send return err without recording the
// message (nil: send again).
func (f *Fake) FailWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = err
}

// Sent returns a copy of the recorded messages.
func (f *Fake) Sent() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.sent...)
}

// Log notes that a message was not sent, and nothing about it: bodies carry
// tokens, and subjects can name visitors, items and secrets' notes.
type Log struct{ L *slog.Logger }

// Send implements Sender.
func (l Log) Send(_ context.Context, m Message) error {
	l.L.Info("mail not sent (no SMTP configured)", "recipients", len(m.To))
	return ErrDisabled
}
