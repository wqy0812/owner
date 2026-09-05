package service

import (
	"context"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

type RunRetryPlan struct {
	SourceRunID           string                  `json:"sourceRunId"`
	RetryRootRunID        string                  `json:"retryRootRunId"`
	EnvironmentRevisionID string                  `json:"environmentRevisionId"`
	StartStep             int                     `json:"startStep"`
	SkippedSteps          int                     `json:"skippedSteps"`
	RemainingSteps        []ComponentTestPlanStep `json:"remainingSteps"`
	RequiresApproval      bool                    `json:"requiresApproval"`
	PlanDigest            string                  `json:"planDigest"`
}

type RunRetryRequest struct {
	ExpectedPlanDigest string `json:"expectedPlanDigest"`
}

func retryStartIndex(run domain.Run, plan lockedPlan) int {
	succeeded := map[string]bool{}
	for _, step := range run.Steps {
		if step.Status == domain.RunSucceeded {
			succeeded[step.NodeID] = true
		}
	}
	for index, step := range plan.Steps {
		if !succeeded[step.NodeID] {
			return index
		}
	}
	return len(plan.Steps)
}

func (p *Platform) PreviewRunRetry(ctx context.Context, user domain.User, sourceRunID string) (RunRetryPlan, error) {
	source, err := p.store.GetRun(ctx, sourceRunID)
	if err != nil {
		return RunRetryPlan{}, err
	}
	if source.RequestedBy != user.ID {
		return RunRetryPlan{}, domain.ErrForbidden
	}
	if source.Status != domain.RunFailed && source.Status != domain.RunInterrupted {
		return RunRetryPlan{}, fmt.Errorf("%w: only failed or interrupted runs can be retried", domain.ErrConflict)
	}
	if source.Kind != domain.RunComponentTest && source.Kind != domain.RunScenarioTest && source.Kind != domain.RunScenario {
		return RunRetryPlan{}, fmt.Errorf("%w: this run kind does not support safe retry", domain.ErrInvalid)
	}
	root := source.RetryRootRunID
	if root == "" {
		root = source.ID
	}
	active, err := p.store.HasActiveRetry(ctx, root)
	if err != nil {
		return RunRetryPlan{}, err
	}
	if active {
		return RunRetryPlan{}, fmt.Errorf("%w: source run already has an active retry", domain.ErrConflict)
	}
	environment, err := p.store.GetEnvironment(ctx, source.EnvironmentID, false)
	if err != nil {
		return RunRetryPlan{}, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return RunRetryPlan{}, err
	}
	if environment.CurrentRevisionID != source.EnvironmentRevisionID {
		return RunRetryPlan{}, fmt.Errorf("%w: environment revision changed; run a full preview", domain.ErrConflict)
	}
	locked, err := mapToPlan(source.InputSnapshot)
	if err != nil {
		return RunRetryPlan{}, fmt.Errorf("%w: historical run has no retryable locked plan", domain.ErrConflict)
	}
	start := retryStartIndex(source, locked)
	if start >= len(locked.Steps) {
		return RunRetryPlan{}, fmt.Errorf("%w: no incomplete step remains", domain.ErrConflict)
	}
	if !locked.Steps[start].RetrySafe {
		return RunRetryPlan{}, fmt.Errorf("%w: first incomplete action is not declared retry-safe", domain.ErrConflict)
	}
	for _, step := range locked.Steps[start:] {
		release, getErr := p.store.GetComponentRelease(ctx, step.ReleaseID)
		if getErr != nil {
			return RunRetryPlan{}, getErr
		}
		if componentReleaseSpecDigest(release) != step.ReleaseSpecDigest {
			return RunRetryPlan{}, fmt.Errorf("%w: component release definition changed", domain.ErrConflict)
		}
	}
	if source.ScenarioRevisionID != "" {
		revision, getErr := p.store.GetScenarioRevision(ctx, source.ScenarioRevisionID)
		if getErr != nil {
			return RunRetryPlan{}, getErr
		}
		lockedDigest, _ := source.InputSnapshot["scenarioRevisionSpecDigest"].(string)
		if lockedDigest == "" || scenarioRevisionSpecDigest(revision) != lockedDigest {
			return RunRetryPlan{}, fmt.Errorf("%w: scenario revision definition changed", domain.ErrConflict)
		}
	}
	if digester, ok := p.runner.(digestRunner); ok {
		for _, step := range locked.Steps[start:] {
			playbookDigest, treeDigest, digestErr := digester.Digest(step.Playbook)
			if digestErr != nil {
				return RunRetryPlan{}, digestErr
			}
			if playbookDigest != step.PlaybookDigest || treeDigest != step.WorkspaceDigest {
				return RunRetryPlan{}, fmt.Errorf("%w: executable content changed", domain.ErrConflict)
			}
		}
	} else {
		return RunRetryPlan{}, fmt.Errorf("%w: runner cannot verify executable fingerprints", domain.ErrConflict)
	}
	result := RunRetryPlan{SourceRunID: source.ID, RetryRootRunID: root, EnvironmentRevisionID: source.EnvironmentRevisionID, StartStep: start, SkippedSteps: start}
	for index, step := range locked.Steps[start:] {
		result.RemainingSteps = append(result.RemainingSteps, ComponentTestPlanStep{Order: start + index + 1, ComponentID: step.ComponentID, ComponentName: step.ComponentName, ReleaseID: step.ReleaseID, ReleaseVersion: step.ReleaseVersion, Action: step.Action, Playbook: step.Playbook, Limit: step.Limit, NeedsApproval: step.NeedsApproval})
		result.RequiresApproval = result.RequiresApproval || step.NeedsApproval
	}
	if len(locked.ArtifactTransfers) > 0 || len(locked.ImageTransfers) > 0 {
		result.RequiresApproval = true
	}
	result.PlanDigest = digestValue(struct {
		SourceID, Status, EnvironmentRevisionID, ArtifactDigest string
		Start                                                   int
		Steps                                                   []lockedStep
		DeliveryRequirements                                    []DeliveryRequirement
		DeliveryDecisions                                       []DeliveryDecision
		ArtifactTransfers                                       []lockedArtifactTransfer
		ImageTransfers                                          []lockedImageTransfer
	}{source.ID, string(source.Status), source.EnvironmentRevisionID, source.ArtifactDigest, start, locked.Steps[start:], locked.DeliveryRequirements, locked.DeliveryDecisions, locked.ArtifactTransfers, locked.ImageTransfers})
	return result, nil
}

func (p *Platform) RetryRun(ctx context.Context, user domain.User, sourceRunID string, input RunRetryRequest) (domain.Run, error) {
	preview, err := p.PreviewRunRetry(ctx, user, sourceRunID)
	if err != nil {
		return domain.Run{}, err
	}
	if input.ExpectedPlanDigest == "" || input.ExpectedPlanDigest != preview.PlanDigest {
		return domain.Run{}, fmt.Errorf("%w: retry plan changed; preview again", domain.ErrConflict)
	}
	source, err := p.store.GetRun(ctx, sourceRunID)
	if err != nil {
		return domain.Run{}, err
	}
	locked, err := mapToPlan(source.InputSnapshot)
	if err != nil {
		return domain.Run{}, err
	}
	locked.Steps = append([]lockedStep(nil), locked.Steps[preview.StartStep:]...)
	now := time.Now().UTC()
	runID := newID("run")
	if err := p.rebindRetryBackupPlan(ctx, source.EnvironmentID, runID, source.Kind, now, &locked); err != nil {
		return domain.Run{}, err
	}
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
	run := domain.Run{ID: runID, Kind: source.Kind, Status: domain.RunQueued, RequestedBy: user.ID, EnvironmentID: source.EnvironmentID, EnvironmentRevisionID: source.EnvironmentRevisionID, ComponentReleaseID: source.ComponentReleaseID, ScenarioRevisionID: source.ScenarioRevisionID, Action: source.Action, Destructive: preview.RequiresApproval, InputSnapshot: snapshot, ArtifactDigest: source.ArtifactDigest, RetryOfRunID: source.ID, RetryRootRunID: preview.RetryRootRunID, RetryAttempt: retryAttempt, RetryStartStep: source.RetryStartStep + preview.StartStep, CreatedAt: now}
	var approval *domain.Approval
	if preview.RequiresApproval {
		run.Status = domain.RunAwaitingApproval
		approval = &domain.Approval{ID: newID("approval"), RunID: run.ID, Status: "pending", RequestedAt: now}
		run.Approval = approval
	}
	if source.Kind == domain.RunScenarioTest {
		if err := p.store.SetScenarioRevisionStatus(ctx, source.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTesting, now); err != nil {
			return run, err
		}
	}
	if err := p.store.CreateRun(ctx, run, approval); err != nil {
		if source.Kind == domain.RunScenarioTest {
			_ = p.store.SetScenarioRevisionStatus(ctx, source.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, now)
		}
		return run, err
	}
	p.audit(ctx, user, "run.retry_created", "run", run.ID, map[string]any{"sourceRunId": source.ID, "retryRootRunId": run.RetryRootRunID, "retryAttempt": run.RetryAttempt, "retryStartStep": run.RetryStartStep, "planDigest": preview.PlanDigest})
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": run.Status})
	if run.Status == domain.RunQueued {
		p.schedule(run.EnvironmentID)
	}
	return run, nil
}
