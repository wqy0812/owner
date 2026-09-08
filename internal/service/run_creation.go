package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

type lockedRunPreparation struct {
	ID          string
	CapturedAt  time.Time
	Plan        domain.RunExecutionPlan
	Destructive bool
}

func (c *ExecutionService) createRun(ctx context.Context, user domain.User, environment domain.Environment, kind domain.RunKind, releaseID, revisionID string, action domain.ActionKind, steps []domain.RunPlanStep, resolvedParametersByNode map[string]map[string]resolvedParameter, expectedPlanDigest string, expectedScenarioDigest ...string) (domain.Run, error) {
	now := time.Now().UTC()
	runID := newID("run")
	plan, planDigest, destructive, err := c.planner.prepareLockedPlan(ctx, environment, kind, runID, now, steps)
	if err != nil {
		return domain.Run{}, err
	}
	if expectedPlanDigest != "" && expectedPlanDigest != planDigest {
		base := fmt.Errorf("%w: execution plan changed after preview; refresh the plan before submitting", domain.ErrConflict)
		return domain.Run{}, actionableExistingError(base, "execution.plan_changed", "环境版本、输入、Release 定义或可执行内容在预览后发生变化", "重新预览执行计划", c.planRefreshHref(ctx, kind, releaseID, revisionID, environment.ID))
	}
	prepared := lockedRunPreparation{ID: runID, CapturedAt: now, Plan: plan, Destructive: destructive}
	return c.creator.createRun(ctx, user, environment, kind, releaseID, revisionID, action, prepared, resolvedParametersByNode, expectedScenarioDigest...)
}

func (p *RunCreator) createScenarioRun(ctx context.Context, user domain.User, id string, input ScenarioExecutionRequest, kind domain.RunKind, runID string, at time.Time, x scenarioExecution, requestDigest string) (domain.Run, error) {
	mode := input.ExecutionMode
	snapshot := domain.SnapshotFromExecutionPlan(x.plan)

	snapshot.ScenarioExecution.Mode = mode
	snapshot.Subject.ScenarioID = x.prepared.revision.ScenarioID
	snapshot.Subject.ScenarioRevisionSpecDigest = scenarioRevisionSpecDigest(x.prepared.revision)
	snapshot.ScenarioExecution.TargetNodes = x.target
	snapshot.ScenarioExecution.SourceRevisionID = x.prepared.revision.SourceRevisionID
	snapshot.ScenarioExecution.Baseline.RunID = x.baseline.RunID
	snapshot.ScenarioExecution.Baseline.Generation = x.baseline.Generation
	snapshot.ScenarioExecution.Baseline.InstallationDigest = x.installationDigest
	snapshot.ScenarioExecution.Baseline.TestOnly = x.baseline.TestOnly
	if mode == domain.ScenarioExecutionBaselineVerify {
		snapshot.ScenarioExecution.RecoveredReceipts = x.recoveredReceipts
	}

	snapshot.Submission.PlanDigest = x.preview.PlanDigest
	snapshot.Submission.Key = input.IdempotencyKey
	snapshot.Submission.Digest = requestDigest
	jobIDs := []string{}
	for _, job := range x.prepared.revision.AcceptanceJobs {
		jobIDs = append(jobIDs, job.ID)
	}
	snapshot.ScenarioExecution.AcceptanceJobIDs = jobIDs
	snapshot.Inputs.ResolvedParametersByNode = provenanceSnapshot(x.prepared.provenance)
	snapshot.Inputs.CredentialRefs = domain.RedactCredentialRefs(x.prepared.environment.Revision.CredentialRefs, false)
	run := domain.Run{ID: runID, Kind: kind, Status: domain.RunQueued, RequestedBy: user.ID, EnvironmentID: input.EnvironmentID, EnvironmentRevisionID: x.prepared.environment.CurrentRevisionID, ScenarioRevisionID: id, Snapshot: snapshot, DeliveryResults: x.plan.DeliveryResults, ArtifactDigest: x.plan.TreeDigest, Destructive: x.preview.NeedsApproval, CreatedAt: at}
	var approval *domain.Approval
	if run.Destructive {
		run.Status = domain.RunAwaitingApproval
		approval = &domain.Approval{ID: newID("approval"), RunID: run.ID, Status: "pending", RequestedAt: at}
		run.Approval = approval
	}
	if err := p.store.CreateRun(ctx, run, approval); err != nil {
		if existing, e := p.store.GetScenarioSubmission(ctx, user.ID, input.IdempotencyKey, requestDigest); e == nil {
			return existing, nil
		}
		return domain.Run{}, err
	}
	p.audit.Record(ctx, user, "run.created", "run", run.ID, map[string]any{"executionMode": mode, "scenarioRevisionId": id, "planDigest": x.preview.PlanDigest})
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	if approval == nil {
		p.scheduler.schedule(run.EnvironmentID)
	}
	return run, nil
}

func (p *RunCreator) createRetryRun(ctx context.Context, user domain.User, source domain.Run, locked domain.RunExecutionPlan, preview RunRetryPlan, runID string, now time.Time) (domain.Run, error) {
	retryAttempt, err := p.store.NextRetryAttempt(ctx, preview.RetryRootRunID)
	if err != nil {
		return domain.Run{}, err
	}
	frozen, err := source.Snapshot.Clone()
	if err != nil {
		return domain.Run{}, err
	}
	refreshed := domain.SnapshotFromExecutionPlan(locked)
	snapshot := domain.RunSnapshot{Contract: domain.RunSnapshotContract, Subject: frozen.Subject, Inputs: frozen.Inputs, ScenarioExecution: frozen.ScenarioExecution, Plan: refreshed.Plan, Delivery: refreshed.Delivery, Recovery: refreshed.Recovery, Retry: domain.RunRetryContext{RecoveryStateDigest: preview.RecoveryStateDigest}}
	results := retryDeliveryResults(locked.DeliveryDecisions)
	run := domain.Run{ID: runID, Kind: source.Kind, Status: domain.RunQueued, RequestedBy: user.ID, EnvironmentID: source.EnvironmentID, EnvironmentRevisionID: source.EnvironmentRevisionID, ComponentReleaseID: source.ComponentReleaseID, ScenarioRevisionID: source.ScenarioRevisionID, Action: source.Action, Destructive: preview.RequiresApproval, Snapshot: snapshot, DeliveryResults: results, ArtifactDigest: source.ArtifactDigest, RetryOfRunID: source.ID, RetryRootRunID: preview.RetryRootRunID, RetryAttempt: retryAttempt, RetryStartStep: source.RetryStartStep + preview.StartStep, CreatedAt: now}
	var approval *domain.Approval
	if preview.RequiresApproval {
		run.Status = domain.RunAwaitingApproval
		approval = &domain.Approval{ID: newID("approval"), RunID: run.ID, Status: "pending", RequestedAt: now}
		run.Approval = approval
	}
	if source.Kind == domain.RunScenario || source.Kind == domain.RunScenarioTest {
		baseline, err := p.store.GetScenarioInstallation(ctx, source.EnvironmentID, source.Snapshot.Subject.ScenarioID)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return run, err
		}
		run.Snapshot.Retry.BaselineGeneration = baseline.Generation
		run.Snapshot.Retry.BaselineMutatingRunID = baseline.MutatingRunID
		run.Snapshot.Submission.Key = "retry-" + run.ID
		run.Snapshot.Submission.Digest = preview.PlanDigest
		run.Snapshot.Submission.PlanDigest = preview.PlanDigest
	}
	if err := p.store.CreateRun(ctx, run, approval); err != nil {
		return run, err
	}
	p.audit.Record(ctx, user, "run.retry_created", "run", run.ID, map[string]any{"sourceRunId": source.ID, "retryRootRunId": run.RetryRootRunID, "retryAttempt": run.RetryAttempt, "retryStartStep": run.RetryStartStep, "planDigest": preview.PlanDigest})
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	if run.Status == domain.RunQueued {
		p.scheduler.schedule(run.EnvironmentID)
	}
	return run, nil
}

func (p *ExecutionService) PreviewComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (ComponentTestPlan, error) {
	prepared, err := p.planner.prepareComponentTest(ctx, user, releaseID, input)
	if err != nil {
		return ComponentTestPlan{}, err
	}
	plan, digest, destructive, err := p.planner.prepareLockedPlan(ctx, prepared.environment, domain.RunComponentTest, "preview", time.Time{}, prepared.steps)
	if err != nil {
		return ComponentTestPlan{}, err
	}
	return p.planner.componentTestPlanDTO(ctx, prepared.environment, plan, digest, destructive), nil
}

func (p *ExecutionService) StartComponentTest(ctx context.Context, user domain.User, releaseID string, input ComponentTestRequest) (domain.Run, error) {
	prepared, err := p.planner.prepareComponentTest(ctx, user, releaseID, input)
	if err != nil {
		return domain.Run{}, err
	}
	return p.createRun(ctx, user, prepared.environment, domain.RunComponentTest, prepared.release.ID, "", prepared.action, prepared.steps, prepared.provenance, input.ExpectedPlanDigest)
}

func (p *ExecutionService) StartScenarioTest(ctx context.Context, user domain.User, revisionID, environmentID string) (domain.Run, error) {
	return p.startScenario(ctx, user, revisionID, environmentID, domain.RunScenarioTest)
}

func (p *ExecutionService) StartScenarioRun(ctx context.Context, user domain.User, revisionID, environmentID string) (domain.Run, error) {
	return p.startScenario(ctx, user, revisionID, environmentID, domain.RunScenario)
}

func (p *ExecutionService) startScenario(ctx context.Context, user domain.User, revisionID, environmentID string, kind domain.RunKind) (domain.Run, error) {
	input := ScenarioExecutionRequest{EnvironmentID: environmentID, ExecutionMode: domain.ScenarioExecutionInstall, IdempotencyKey: newID("scenario-submit")}
	preview, err := p.PreviewScenarioExecution(ctx, user, revisionID, input, kind)
	if err != nil {
		return domain.Run{}, err
	}
	if !preview.Ready {
		return domain.Run{}, &domain.ValidationError{Message: "scenario execution blocked", Details: preview.Issues}
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	return p.StartScenarioExecution(ctx, user, revisionID, input, kind)
}

// A new attempt starts without observations from an earlier Run. Approved
// choices remain in the frozen delivery plan and are verified before execution.
func retryDeliveryResults(decisions []domain.RunDeliveryDecision) []domain.RunDeliveryResult {
	out := make([]domain.RunDeliveryResult, 0, len(decisions))
	for _, d := range decisions {
		out = append(out, domain.RunDeliveryResult{RequirementID: d.RequirementID, Mode: d.Mode, Status: "pending"})
	}
	return out
}
