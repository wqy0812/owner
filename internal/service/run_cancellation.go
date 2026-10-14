package service

import (
	"context"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

func (a *RunCancellationService) CancelRun(ctx context.Context, user domain.User, runID string) (domain.Run, error) {
	run, err := a.store.GetRun(ctx, runID)
	if err != nil {
		return run, err
	}
	if user.ID != run.RequestedBy {
		environment, environmentErr := a.store.GetEnvironment(ctx, run.EnvironmentID, false)
		if environmentErr != nil {
			return run, environmentErr
		}
		if user.Role != domain.RoleEnvironmentOwner || environment.OwnerID != user.ID {
			return run, domain.ErrForbidden
		}
	}
	switch run.Status {
	case domain.RunQueued:
		if err := a.store.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{run.Status}, domain.RunCancelled, "cancelled by user", time.Now().UTC()); err != nil {
			return run, err
		}
		run.Status = domain.RunCancelled
		if run.Kind == domain.RunScenarioTest {
			_ = a.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
	case domain.RunRunning:
		if !a.control.cancelRun(run.ID) {
			return run, fmt.Errorf("%w: active process is not attached to this server", domain.ErrConflict)
		}

	default:
		base := fmt.Errorf("%w: run is already terminal", domain.ErrConflict)
		return run, actionableExistingError(base, "run.already_terminal", "该 Run 已进入终态，不能再次取消", "刷新运行详情", "/runs?selected="+run.ID)
	}
	a.audit.Record(ctx, user, "run.cancel_requested", "run", run.ID, nil)
	a.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	return run, nil
}

func (s *ExecutionService) Cancel(ctx context.Context, user domain.User, id string) (domain.Run, error) {
	return s.cancellation.CancelRun(ctx, user, id)
}
