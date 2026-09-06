package service

import (
	"context"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func (s *CatalogService) Usage(ctx context.Context, user domain.User, id, releaseID string, history bool) (store.ComponentUsage, error) {
	return s.store.ComponentUsage(ctx, user, id, releaseID, history)
}
