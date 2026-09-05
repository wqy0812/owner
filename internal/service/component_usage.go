package service

import (
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"context"
)

func (s *CatalogService) Usage(ctx context.Context, user domain.User, id, releaseID string, history bool) (store.ComponentUsage, error) {
	return s.platform.store.ComponentUsage(ctx, user, id, releaseID, history)
}
