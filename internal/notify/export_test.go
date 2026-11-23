package notify

import (
	"context"
	"encoding/json"
)

// Job kinds and handlers, for tests that drive delivery step by step.
const (
	JobOpen = jobOpen
	JobSend = jobSend
)

func (s *Service) HandleOpen(ctx context.Context, payload json.RawMessage) error {
	return s.handleOpen(ctx, payload)
}

func (s *Service) HandleSend(ctx context.Context, payload json.RawMessage) error {
	return s.handleSend(ctx, payload)
}

// AddInbox stores entries as the open and secret hooks do.
func (s *Service) AddInbox(ctx context.Context, entries ...Entry) error {
	return s.addInbox(ctx, entries...)
}

// SetStreamLimits changes the stream bounds (zero fields: the defaults).
func (s *Service) SetStreamLimits(l StreamLimits) {
	l = l.withDefaults()
	s.limits.Store(&l)
}

// Streams is the number of open notification streams.
func (s *Service) Streams() int { return s.streams.open() }
