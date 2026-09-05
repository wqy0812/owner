package service

import (
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"context"
)

func (s *ExecutionService) ListRunSummaries(ctx context.Context, user domain.User, options store.RunListOptions) (store.RunPage, error) {
	return s.platform.store.ListRunSummaries(ctx, user, options)
}

func (s *ExecutionService) BatchApprovalCandidates(ctx context.Context, user domain.User) ([]store.RunSummary, error) {
	return s.platform.store.BatchApprovalCandidates(ctx, user)
}

func (s *ExecutionService) ReleaseRunSummaries(ctx context.Context, user domain.User, releaseID string) ([]store.RunSummary, error) {
	release, err := s.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	component, err := s.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return nil, err
	}
	if err = requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return nil, err
	}
	return s.platform.store.ReleaseRunSummaries(ctx, user, releaseID)
}
