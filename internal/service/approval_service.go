package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

func (a *ApprovalService) CancelRun(ctx context.Context, user domain.User, runID string) (domain.Run, error) {
	p := a.platform
	run, err := p.store.GetRun(ctx, runID)
	if err != nil {
		return run, err
	}
	if user.ID != run.RequestedBy {
		environment, environmentErr := p.store.GetEnvironment(ctx, run.EnvironmentID, false)
		if environmentErr != nil {
			return run, environmentErr
		}
		if user.Role != domain.RoleEnvironmentOwner || environment.OwnerID != user.ID {
			return run, domain.ErrForbidden
		}
	}
	switch run.Status {
	case domain.RunQueued, domain.RunAwaitingApproval:
		if err := p.store.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{run.Status}, domain.RunCancelled, "cancelled by user", time.Now().UTC()); err != nil {
			return run, err
		}
		run.Status = domain.RunCancelled
		if run.Kind == domain.RunScenarioTest {
			_ = p.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
	case domain.RunRunning:
		p.mu.Lock()
		cancel := p.active[run.ID]
		p.mu.Unlock()
		if cancel == nil {
			return run, fmt.Errorf("%w: active process is not attached to this server", domain.ErrConflict)
		}
		cancel()
	default:
		base := fmt.Errorf("%w: run is already terminal", domain.ErrConflict)
		return run, actionableExistingError(base, "run.already_terminal", "该 Run 已进入终态，不能再次取消", "刷新运行详情", "/runs?selected="+run.ID)
	}
	p.audit(ctx, user, "run.cancel_requested", "run", run.ID, nil)
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	return run, nil
}

func (a *ApprovalService) DecideApproval(ctx context.Context, user domain.User, approvalID, decision, reason string, deliveryDecisions []DeliveryDecisionInput) (domain.Run, error) {
	p := a.platform
	if user.Role != domain.RoleEnvironmentOwner {
		base := fmt.Errorf("%w: only an environment owner may decide destructive runs", domain.ErrForbidden)
		return domain.Run{}, actionableExistingError(base, "permission.environment_owner_required", "只有目标环境的环境 Owner 可以审批危险作业", "返回我的工作", "/")
	}
	approval, err := p.store.GetApproval(ctx, approvalID)
	if err != nil {
		return domain.Run{}, err
	}
	run, err := p.store.GetRun(ctx, approval.RunID)
	if err != nil {
		return run, err
	}
	environment, err := p.store.GetEnvironment(ctx, run.EnvironmentID, false)
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
		plan, planErr = p.finalizeDeliveryPlan(ctx, plan, deliveryDecisions, user, now)
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
	if err := p.store.DecideApprovalWithSnapshot(ctx, approvalID, user.ID, decision, reason, snapshot, now); err != nil {
		return run, err
	}
	if decision == "approved" {
		run.Status = domain.RunQueued
		if snapshot != nil {
			run.InputSnapshot = snapshot
		}
		p.schedule(run.EnvironmentID)
	} else {
		run.Status = domain.RunRejected
		if run.Kind == domain.RunScenarioTest {
			_ = p.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
	}
	p.audit(ctx, user, "approval."+decision, "approval", approvalID, map[string]any{"runId": run.ID, "reason": reason})
	p.hub.Publish("approval.updated", map[string]any{"approvalId": approvalID, "runId": run.ID, "decision": decision})
	return p.store.GetRun(ctx, run.ID)
}

func (a *ApprovalService) BatchDecideApprovals(ctx context.Context, user domain.User, approvalIDs []string, decision, reason string) ([]domain.Run, error) {
	p := a.platform
	if user.Role != domain.RoleEnvironmentOwner {
		return nil, fmt.Errorf("%w: only an environment owner may decide destructive runs", domain.ErrForbidden)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("%w: batch approval reason is required", domain.ErrInvalid)
	}
	if decision == "approved" {
		for _, approvalID := range approvalIDs {
			approval, approvalErr := p.store.GetApproval(ctx, approvalID)
			if approvalErr != nil {
				return nil, approvalErr
			}
			run, runErr := p.store.GetRun(ctx, approval.RunID)
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
	runs, err := p.store.BatchDecideApprovals(ctx, approvalIDs, user.ID, decision, reason, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	environments := map[string]bool{}
	for _, run := range runs {
		if decision == "rejected" && run.Kind == domain.RunScenarioTest {
			_ = p.store.SetScenarioRevisionStatus(ctx, run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC())
		}
		p.audit(ctx, user, "approval.batch_"+decision, "run", run.ID, map[string]any{"reason": reason, "batchSize": len(runs)})
		p.hub.Publish("approval.updated", map[string]any{"runId": run.ID, "decision": decision, "batch": true})
		environments[run.EnvironmentID] = true
	}
	if decision == "approved" {
		for environmentID := range environments {
			p.schedule(environmentID)
		}
	}
	return runs, nil
}
