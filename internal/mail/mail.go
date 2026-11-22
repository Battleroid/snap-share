// Package mail sends email. The SMTP implementation is added by the accounts
// track; this file defines the interface and test/dev senders.
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
}

// Send implements Sender.
func (f *Fake) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

// Sent returns a copy of the recorded messages.
func (f *Fake) Sent() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.sent...)
}

// Log writes subjects and recipients (never bodies, which carry tokens) to the logger.
type Log struct{ L *slog.Logger }

// Send implements Sender.
func (l Log) Send(_ context.Context, m Message) error {
	l.L.Info("mail not sent (no SMTP configured)", "to", len(m.To), "subject", m.Subject)
	return ErrDisabled
}
