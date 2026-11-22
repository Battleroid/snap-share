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
