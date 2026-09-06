package service

import (
	"context"
	"encoding/json"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func (s *ExecutionService) ListRunSummaries(ctx context.Context, user domain.User, options store.RunListOptions) (store.RunPage, error) {
	return s.store.ListRunSummaries(ctx, user, options)
}

func (s *ExecutionService) BatchApprovalCandidates(ctx context.Context, user domain.User) ([]store.RunSummary, error) {
	return s.store.BatchApprovalCandidates(ctx, user)
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
	return s.store.ReleaseRunSummaries(ctx, user, releaseID)
}

func (s *ExecutionService) ListRuns(ctx context.Context, user domain.User) ([]domain.Run, error) {
	return s.store.ListRuns(ctx, user)
}

func (s *ExecutionService) ListRunsForComponentRelease(ctx context.Context, user domain.User, releaseID string) ([]domain.Run, error) {
	release, err := s.store.GetComponentRelease(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	component, err := s.store.GetComponent(ctx, release.ComponentID, false)
	if err != nil {
		return nil, err
	}
	if err := requireOwner(user, domain.RoleComponentOwner, component.OwnerID); err != nil {
		return nil, err
	}
	return s.store.ListRunsForComponentRelease(ctx, releaseID)
}

func (s *ExecutionService) ListRunSteps(ctx context.Context, runID string) ([]domain.RunStep, error) {
	return s.store.ListRunSteps(ctx, runID)
}

func (s *ExecutionService) GetApprovalByRun(ctx context.Context, runID string) (domain.Approval, error) {
	return s.store.GetApprovalByRun(ctx, runID)
}

func (s *ExecutionService) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return s.store.GetRun(ctx, id)
}

func (s *ExecutionService) CanViewRun(ctx context.Context, user domain.User, id string) (bool, error) {
	return s.store.CanViewRun(ctx, user, id)
}

func (s *ExecutionService) GetEnvironment(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error) {
	return s.store.GetEnvironment(ctx, id, includeRevisions)
}

func (s *ExecutionService) GetUser(ctx context.Context, id string) (domain.User, error) {
	return s.store.GetUser(ctx, id)
}

func (s *ExecutionService) GetComponentRelease(ctx context.Context, id string) (domain.ComponentRelease, error) {
	return s.store.GetComponentRelease(ctx, id)
}

func (s *ExecutionService) GetComponent(ctx context.Context, id string, includeReleases bool) (domain.Component, error) {
	return s.store.GetComponent(ctx, id, includeReleases)
}

func (s *ExecutionService) GetScenarioRevision(ctx context.Context, id string) (domain.ScenarioRevision, error) {
	return s.store.GetScenarioRevision(ctx, id)
}

func (s *ExecutionService) GetScenario(ctx context.Context, id string, includeRevisions bool) (domain.Scenario, error) {
	return s.store.GetScenario(ctx, id, includeRevisions)
}

func (s *ExecutionService) ListRunLogTail(ctx context.Context, runID string, limit int) ([]domain.RunLog, error) {
	return s.store.ListRunLogTail(ctx, runID, limit)
}

func (s *ExecutionService) QueuedRunPosition(ctx context.Context, environmentID string, createdAt time.Time) (int, error) {
	return s.store.QueuedRunPosition(ctx, environmentID, createdAt)
}

func (s *ExecutionService) ListRunWaitingObservations(ctx context.Context, runID string) ([]json.RawMessage, error) {
	return s.store.ListRunWaitingObservations(ctx, runID)
}
