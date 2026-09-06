package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

func (a *ApprovalService) CancelRun(ctx context.Context, user domain.User, runID string) (domain.Run, error) {
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
	case domain.RunQueued, domain.RunAwaitingApproval:
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

func (a *ApprovalService) DecideApproval(ctx context.Context, user domain.User, approvalID, decision, reason string, deliveryDecisions []DeliveryDecisionInput) (domain.Run, error) {
	if user.Role != domain.RoleEnvironmentOwner {
		base := fmt.Errorf("%w: only an environment owner may decide destructive runs", domain.ErrForbidden)
		return domain.Run{}, actionableExistingError(base, "permission.environment_owner_required", "只有目标环境的环境 Owner 可以审批危险作业", "返回我的工作", "/")
	}
	approval, err := a.store.GetApproval(ctx, approvalID)
	if err != nil {
		return domain.Run{}, err
	}
	run, err := a.store.GetRun(ctx, approval.RunID)
	if err != nil {
		return run, err
	}
	environment, err := a.store.GetEnvironment(ctx, run.EnvironmentID, false)
	if err != nil {
		return run, err
	}
	if environment.OwnerID != user.ID {
		base := fmt.Errorf("%w: approval belongs to another owner's environment", domain.ErrForbidden)
		return run, actionableExistingError(base, "permission.environment_owner_required", "该审批属于另一位环境 Owner 管理的环境", "查看运行详情", "/runs?selected="+run.ID)
	}
	if approval.Status != "pending" {
		if approval.Status == decision && run.Status != domain.RunAwaitingApproval {
			// Treat an identical retry as success. This makes the endpoint safe for
			// double clicks and network retries without duplicating audit events or
			// attempting to enqueue the run a second time.
			return run, nil
		}
		base := fmt.Errorf("%w: approval was already %s", domain.ErrConflict, approval.Status)
		return run, actionableExistingError(base, "approval.already_decided", "审批状态已由另一请求消费，当前页面数据已过期", "刷新运行详情", "/runs?selected="+run.ID)
	}
	now := time.Now().UTC()
	var snapshot map[string]any
	if decision == "approved" {
		plan, planErr := mapToPlan(run.InputSnapshot)
		if planErr != nil {
			return run, planErr
		}
		plan, planErr = a.delivery.finalizeDeliveryPlan(ctx, plan, deliveryDecisions, user, now)
		if planErr != nil {
			return run, planErr
		}
		snapshot = cloneMap(run.InputSnapshot)
		for key, value := range structToMap(plan) {
			snapshot[key] = value
		}
	} else if len(deliveryDecisions) > 0 {
		return run, fmt.Errorf("%w: rejected approvals must not include delivery decisions", domain.ErrInvalid)
	}
	if err := a.store.DecideApprovalWithSnapshot(ctx, approvalID, user.ID, decision, reason, snapshot, now); err != nil {
		return run, err
	}
	if decision == "approved" {
		run.Status = domain.RunQueued
		if snapshot != nil {
			run.InputSnapshot = snapshot
		}
		a.scheduler.schedule(run.EnvironmentID)
	} else {
		run.Status = domain.RunRejected
		if run.Kind == domain.RunScenarioTest {
			_ = a.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
	}
	a.audit.Record(ctx, user, "approval."+decision, "approval", approvalID, map[string]any{"runId": run.ID, "reason": reason})
	a.hub.Publish("approval.updated", map[string]any{"approvalId": approvalID, "runId": run.ID, "decision": decision})
	return a.store.GetRun(ctx, run.ID)
}

func (a *ApprovalService) BatchDecideApprovals(ctx context.Context, user domain.User, approvalIDs []string, decision, reason string) ([]domain.Run, error) {
	if user.Role != domain.RoleEnvironmentOwner {
		return nil, fmt.Errorf("%w: only an environment owner may decide destructive runs", domain.ErrForbidden)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("%w: batch approval reason is required", domain.ErrInvalid)
	}
	if decision == "approved" {
		for _, approvalID := range approvalIDs {
			approval, approvalErr := a.store.GetApproval(ctx, approvalID)
			if approvalErr != nil {
				return nil, approvalErr
			}
			run, runErr := a.store.GetRun(ctx, approval.RunID)
			if runErr != nil {
				return nil, runErr
			}
			plan, planErr := mapToPlan(run.InputSnapshot)
			if planErr != nil {
				return nil, planErr
			}
			if len(plan.DeliveryRequirements) > 0 {
				return nil, fmt.Errorf("%w: runs with delivery requirements cannot be batch approved", domain.ErrConflict)
			}
		}
	}
	runs, err := a.store.BatchDecideApprovals(ctx, approvalIDs, user.ID, decision, reason, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	environments := map[string]bool{}
	for _, run := range runs {
		if decision == "rejected" && run.Kind == domain.RunScenarioTest {
			_ = a.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
		a.audit.Record(ctx, user, "approval.batch_"+decision, "run", run.ID, map[string]any{"reason": reason, "batchSize": len(runs)})
		a.hub.Publish("approval.updated", map[string]any{"runId": run.ID, "decision": decision, "batch": true})
		environments[run.EnvironmentID] = true
	}
	if decision == "approved" {
		for environmentID := range environments {
			a.scheduler.schedule(environmentID)
		}
	}
	return runs, nil
}

func (s *ExecutionService) Cancel(ctx context.Context, user domain.User, id string) (domain.Run, error) {
	return s.approvals.CancelRun(ctx, user, id)
}

func (s *ExecutionService) DecideApproval(ctx context.Context, user domain.User, id, decision, reason string, deliveryDecisions []DeliveryDecisionInput) (domain.Run, error) {
	return s.approvals.DecideApproval(ctx, user, id, decision, reason, deliveryDecisions)
}

func (s *ExecutionService) BatchDecideApprovals(ctx context.Context, user domain.User, ids []string, decision, reason string) ([]domain.Run, error) {
	return s.approvals.BatchDecideApprovals(ctx, user, ids, decision, reason)
}
