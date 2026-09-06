package service

import (
	"context"
	"encoding/json"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func (s *ExecutionService) RunActivity(ctx context.Context, user domain.User, id string, afterID *int64, limit int) (store.RunActivity, error) {
	visible, err := s.store.CanViewRun(ctx, user, id)
	if err != nil {
		return store.RunActivity{}, err
	}
	if !visible {
		return store.RunActivity{}, domain.ErrForbidden
	}
	out, err := s.store.ReadRunActivity(ctx, id, afterID, limit)
	if err != nil {
		return out, err
	}
	for i, item := range out.Logs {
		out.Logs[i] = cleanRunLog(item)
	}
	for i, item := range out.WaitingObservations {
		out.WaitingObservations[i] = json.RawMessage(cleanRunLog(domain.RunLog{Stream: "event", Message: string(item)}).Message)
	}
	return out, nil
}
