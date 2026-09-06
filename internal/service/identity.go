package service

import (
	"context"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *IdentityService) ListUsers(ctx context.Context) ([]domain.User, error) {
	return s.store.ListUsers(ctx)
}

func (s *IdentityService) UserBySession(ctx context.Context, token string) (domain.User, error) {
	return s.store.UserBySession(ctx, token)
}

func (s *IdentityService) GetUser(ctx context.Context, id string) (domain.User, error) {
	return s.store.GetUser(ctx, id)
}

func (s *IdentityService) CreateSession(ctx context.Context, token, userID string, expires time.Time) error {
	return s.store.CreateSession(ctx, token, userID, expires)
}
