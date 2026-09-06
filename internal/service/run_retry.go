package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/ansible"
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
	RecoveryStateDigest   string                  `json:"recoveryStateDigest"`
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

func (p *ExecutionService) PreviewRetry(ctx context.Context, user domain.User, sourceRunID string) (RunRetryPlan, error) {
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
	if source.Kind != domain.RunComponentTest && source.Kind != domain.RunScenarioTest && source.Kind != domain.RunScenario && source.Kind != domain.RunEnvironmentRollback {
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
	nextAttempt, err := p.store.NextRetryAttempt(ctx, root)
	if err != nil {
		return RunRetryPlan{}, err
	}
	if source.RetryAttempt != nextAttempt-1 {
		return RunRetryPlan{}, fmt.Errorf("%w: a newer attempt exists; continue from that Run", domain.ErrConflict)
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
	if err := p.validateRetryRecoveryIdentity(ctx, source, locked, root); err != nil {
		return RunRetryPlan{}, err
	}
	start := retryStartIndex(source, locked)
	if start >= len(locked.Steps) {
		return RunRetryPlan{}, fmt.Errorf("%w: no incomplete step remains", domain.ErrConflict)
	}
	if !locked.Steps[start].RetrySafe {
		return RunRetryPlan{}, fmt.Errorf("%w: first incomplete action is not declared retry-safe", domain.ErrConflict)
	}
	for _, step := range locked.Steps {
		if step.SourceType == "scenario_acceptance" {
			if err := p.workspaceVerifier.verifyScenarioAcceptanceStep(ctx, &step); err != nil {
				return RunRetryPlan{}, err
			}
			continue
		}
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
	for _, step := range locked.Steps {
		playbookDigest, treeDigest, digestErr := p.inspector.Digest(step.Playbook)
		if digestErr != nil {
			return RunRetryPlan{}, digestErr
		}
		if playbookDigest != step.PlaybookDigest || treeDigest != step.WorkspaceDigest {
			return RunRetryPlan{}, fmt.Errorf("%w: executable content changed", domain.ErrConflict)
		}
	}

	if locked.Steps[start].Phase == "execute" && start > 0 && locked.Steps[start-1].Phase == "pre" {
		start--
	}
	result := RunRetryPlan{SourceRunID: source.ID, RetryRootRunID: root, EnvironmentRevisionID: source.EnvironmentRevisionID, StartStep: start, SkippedSteps: start}
	result.RecoveryStateDigest, err = p.retryRecoveryStateDigest(ctx, source.EnvironmentID)
	if err != nil {
		return RunRetryPlan{}, err
	}
	remaining, err := retryLockedSteps(locked, start)
	if err != nil {
		return RunRetryPlan{}, err
	}
	for index, step := range remaining {
		result.RemainingSteps = append(result.RemainingSteps, ComponentTestPlanStep{Order: start + index + 1, ComponentID: step.ComponentID, ComponentName: step.ComponentName, ReleaseID: step.ReleaseID, ReleaseVersion: step.ReleaseVersion, Phase: step.Phase, ParentActionID: step.ParentActionID, ActionID: step.ActionID, NodeID: step.SourceNodeID, Action: step.Action, Playbook: step.Playbook, Limit: step.Limit, NeedsApproval: step.NeedsApproval})
		result.RequiresApproval = result.RequiresApproval || step.NeedsApproval
	}
	if len(locked.ArtifactTransfers) > 0 || len(locked.ImageTransfers) > 0 {
		result.RequiresApproval = true
	}
	result.PlanDigest = digestValue(struct {
		SourceID, Status, EnvironmentRevisionID, ArtifactDigest, RecoveryStateDigest string
		Start                                                                        int
		Steps                                                                        []lockedStep
		DeliveryRequirements                                                         []DeliveryRequirement
		DeliveryDecisions                                                            []DeliveryDecision
		ArtifactTransfers                                                            []lockedArtifactTransfer
		ImageTransfers                                                               []lockedImageTransfer
	}{source.ID, string(source.Status), source.EnvironmentRevisionID, source.ArtifactDigest, result.RecoveryStateDigest, start, remaining, locked.DeliveryRequirements, locked.DeliveryDecisions, locked.ArtifactTransfers, locked.ImageTransfers})
	return result, nil
}

func (p *ExecutionService) Retry(ctx context.Context, user domain.User, sourceRunID string, input RunRetryRequest) (domain.Run, error) {
	preview, err := p.PreviewRetry(ctx, user, sourceRunID)
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
	locked.Steps, err = retryLockedSteps(locked, preview.StartStep)
	if err != nil {
		return domain.Run{}, err
	}
	if source.Kind == domain.RunEnvironmentRollback {
		locked.InstallationBaseline = installationBaselineFromSteps(locked.Steps)
		locked.InstallationBaselineDigest = installationBaselineDigest(locked.InstallationBaseline)
	}
	now := time.Now().UTC()
	runID := newID("run")
	if err := p.workspaceVerifier.verifyLockedWorkspaceDigests(ctx, locked.Steps); err != nil {
		return domain.Run{}, err
	}
	return p.creator.createRetryRun(ctx, user, source, locked, preview, runID, now)
}

func retryLockedSteps(plan lockedPlan, start int) ([]lockedStep, error) {
	selected, err := ansible.ContinuationSteps(jobPlanFromLocked("", plan, nil), start)
	if err != nil {
		return nil, err
	}
	steps := make([]lockedStep, 0, len(selected))
	for _, item := range selected {
		source := lockedStepByID(plan.Steps, item.ID)
		if source == nil {
			source = lockedStepByID(plan.Steps, strings.TrimPrefix(item.ID, "refresh-"))
		}
		if source == nil {
			continue
		}
		step := *source
		if step.ID != item.ID {
			step.NodeID = "refresh-" + step.NodeID
		}
		step.ID, step.Phase = item.ID, item.Phase
		steps = append(steps, step)
	}
	return steps, nil
}

func (p *ExecutionService) validateRetryRecoveryIdentity(ctx context.Context, source domain.Run, plan lockedPlan, root string) error {
	rootRun, err := p.store.GetRun(ctx, root)
	if err != nil {
		return err
	}
	receipts, err := p.store.LatestEnvironmentActionReceipts(ctx, source.EnvironmentID)
	if err != nil {
		return err
	}
	for _, receipt := range receipts {
		relevant := false
		identityMatches := false
		priorMatches := false
		for _, step := range plan.ParentSteps {
			if step.ComponentID != receipt.ComponentID || step.SourceNodeID != receipt.SourceNodeID {
				continue
			}
			relevant = true
			if step.Backup != nil && receipt.BackupRef == step.BackupRef && digestValue(receipt.Backup) == digestValue(*step.Backup) {
				identityMatches = true
			}
			if step.Backup != nil && step.Backup.Previous != nil {
				prior := step.Backup.Previous
				priorMatches = priorMatches || (prior.BackupRef == receipt.BackupRef && digestValue(prior.Backup) == digestValue(receipt.Backup))
			}
		}
		if !relevant {
			continue
		}
		receiptRun, err := p.store.GetRun(ctx, receipt.RunID)
		if err != nil {
			return err
		}
		if (receiptRun.ID == root || receiptRun.RetryRootRunID == root) && identityMatches {
			continue
		}
		// A failed precheck has not touched its baseline. An older verified
		// installation is valid only if it is the exact locked restore source.
		if receipt.Status == "verified" && receipt.UpdatedAt.Before(rootRun.CreatedAt) && (priorMatches || identityMatches) {
			continue
		}
		return fmt.Errorf("%w: recovery baseline or component state changed outside this retry chain", domain.ErrConflict)
	}
	return nil
}

func (p *ExecutionService) retryRecoveryStateDigest(ctx context.Context, environmentID string) (string, error) {
	return p.store.RetryRecoveryStateDigest(ctx, environmentID)
}
