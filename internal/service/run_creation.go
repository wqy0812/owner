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
	Plan        lockedPlan
	Destructive bool
}

func (c *ExecutionService) createRun(ctx context.Context, user domain.User, environment domain.Environment, kind domain.RunKind, releaseID, revisionID string, action domain.ActionKind, steps []lockedStep, resolvedParametersByNode map[string]map[string]resolvedParameter, expectedPlanDigest string, expectedScenarioDigest ...string) (domain.Run, error) {
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
	snapshot := structToMap(x.plan)
	snapshot["scenarioContractVersion"] = 2
	snapshot["executionMode"] = string(mode)
	snapshot["scenarioId"] = x.prepared.revision.ScenarioID
	snapshot["scenarioRevisionSpecDigest"] = scenarioRevisionSpecDigest(x.prepared.revision)
	snapshot["targetNodes"] = x.target
	snapshot["sourceRevisionId"] = x.prepared.revision.SourceRevisionID
	snapshot["baselineRunId"] = x.baseline.RunID
	snapshot["baselineGeneration"] = x.baseline.Generation
	snapshot["baselineInstallationDigest"] = x.installationDigest
	snapshot["baselineTestOnly"] = x.baseline.TestOnly
	if mode == domain.ScenarioExecutionBaselineVerify {
		snapshot["recoveredReceipts"] = x.recoveredReceipts
	}
	if x.historical != nil {
		snapshot["historicalBaselineRunId"] = x.historical.Run.ID
		snapshot["historicalBaselineTestOnly"] = x.historical.TestOnly
	}
	snapshot["environmentRevisionId"] = x.prepared.environment.CurrentRevisionID
	snapshot["planDigest"] = x.preview.PlanDigest
	snapshot["submissionKey"] = input.IdempotencyKey
	snapshot["submissionDigest"] = requestDigest
	jobIDs := []string{}
	for _, job := range x.prepared.revision.AcceptanceJobs {
		jobIDs = append(jobIDs, job.ID)
	}
	snapshot["acceptanceJobIds"] = jobIDs
	snapshot["resolvedParametersByNode"] = provenanceSnapshot(x.prepared.provenance)
	snapshot["credentialRefs"] = domain.RedactCredentialRefs(x.prepared.environment.Revision.CredentialRefs, false)
	run := domain.Run{ID: runID, Kind: kind, Status: domain.RunQueued, RequestedBy: user.ID, EnvironmentID: input.EnvironmentID, EnvironmentRevisionID: x.prepared.environment.CurrentRevisionID, ScenarioRevisionID: id, InputSnapshot: snapshot, ArtifactDigest: x.plan.TreeDigest, Destructive: x.preview.NeedsApproval, CreatedAt: at}
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

func (p *RunCreator) createRetryRun(ctx context.Context, user domain.User, source domain.Run, locked lockedPlan, preview RunRetryPlan, runID string, now time.Time) (domain.Run, error) {
	retryAttempt, err := p.store.NextRetryAttempt(ctx, preview.RetryRootRunID)
	if err != nil {
		return domain.Run{}, err
	}
	snapshot := structToMap(locked)
	for key, value := range source.InputSnapshot {
		if key != "steps" && key != "artifactTransfers" && key != "imageTransfers" && key != "treeDigest" && key != "installationBaseline" && key != "installationBaselineDigest" {
			snapshot[key] = value
		}
	}
	snapshot["retryRecoveryStateDigest"] = preview.RecoveryStateDigest
	run := domain.Run{ID: runID, Kind: source.Kind, Status: domain.RunQueued, RequestedBy: user.ID, EnvironmentID: source.EnvironmentID, EnvironmentRevisionID: source.EnvironmentRevisionID, ComponentReleaseID: source.ComponentReleaseID, ScenarioRevisionID: source.ScenarioRevisionID, Action: source.Action, Destructive: preview.RequiresApproval, InputSnapshot: snapshot, ArtifactDigest: source.ArtifactDigest, RetryOfRunID: source.ID, RetryRootRunID: preview.RetryRootRunID, RetryAttempt: retryAttempt, RetryStartStep: source.RetryStartStep + preview.StartStep, CreatedAt: now}
	var approval *domain.Approval
	if preview.RequiresApproval {
		run.Status = domain.RunAwaitingApproval
		approval = &domain.Approval{ID: newID("approval"), RunID: run.ID, Status: "pending", RequestedAt: now}
		run.Approval = approval
	}
	if source.InputSnapshot["scenarioContractVersion"] != nil {
		baseline, err := p.store.GetScenarioInstallation(ctx, source.EnvironmentID, fmt.Sprint(source.InputSnapshot["scenarioId"]))
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return run, err
		}
		run.InputSnapshot["retryBaselineGeneration"] = baseline.Generation
		run.InputSnapshot["retryBaselineMutatingRunId"] = baseline.MutatingRunID
		run.InputSnapshot["submissionKey"] = "retry-" + run.ID
		run.InputSnapshot["submissionDigest"] = preview.PlanDigest
	} else if source.Kind == domain.RunScenarioTest {
		if err := p.store.SetScenarioRevisionStatus(ctx, source.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTesting, now); err != nil {
			return run, err
		}
	}
	if err := p.store.CreateRun(ctx, run, approval); err != nil {
		if source.Kind == domain.RunScenarioTest && source.InputSnapshot["scenarioContractVersion"] == nil {
			_ = p.store.SetScenarioRevisionStatus(ctx, source.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, now)
		}
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
