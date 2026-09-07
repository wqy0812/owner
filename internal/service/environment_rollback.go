package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"codex/platform-demo/internal/ansible"
	mediadelivery "codex/platform-demo/internal/delivery"
	"codex/platform-demo/internal/domain"
)

type EnvironmentRollbackRequest struct {
	Nodes                  []string `json:"nodes,omitempty"`
	ExpectedPlanDigest     string   `json:"expectedPlanDigest"`
	ConfirmEnvironmentName string   `json:"confirmEnvironmentName"`
}

type EnvironmentRollbackPlan struct {
	Nodes                 []string                    `json:"nodes"`
	EnvironmentID         string                      `json:"environmentId"`
	EnvironmentName       string                      `json:"environmentName"`
	EnvironmentRevisionID string                      `json:"environmentRevisionId"`
	Sources               []EnvironmentRollbackSource `json:"sources"`
	ComponentCount        int                         `json:"componentCount"`
	NodeCount             int                         `json:"nodeCount"`
	Destructive           bool                        `json:"destructive"`
	RequiresApproval      bool                        `json:"requiresApproval"`
	PlanDigest            string                      `json:"planDigest"`
	Steps                 []ComponentTestPlanStep     `json:"steps"`
	DeliveryRequirements  []mediadelivery.Requirement `json:"deliveryRequirements"`
}

type EnvironmentRollbackSource struct {
	RunID              string         `json:"runId"`
	Kind               domain.RunKind `json:"kind"`
	ScenarioRevisionID string         `json:"scenarioRevisionId,omitempty"`
	ComponentCount     int            `json:"componentCount"`
}

type preparedEnvironmentRollback struct {
	environment        domain.Environment
	sourceRuns         []domain.Run
	installationsByRun map[string][]domain.EnvironmentComponentInstallation
	steps              []lockedStep
}

func (p *ExecutionService) PreviewRollback(ctx context.Context, user domain.User, environmentID string, nodes ...string) (EnvironmentRollbackPlan, error) {
	prepared, err := p.planner.prepareEnvironmentRollback(ctx, user, environmentID, nodes...)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			err = actionableExistingError(err, "rollback.baseline_invalid", "当前安装清单、来源 Run 或备份基线无法生成安全回滚计划", "检查环境与来源 Run", "/environments?selected="+environmentID)
		}
		nodes := []string{}
		for _, step := range prepared.steps {
			nodes = append(nodes, step.ComponentID+"/"+step.SourceNodeID)
		}
		return EnvironmentRollbackPlan{Nodes: nodes}, err
	}
	if len(prepared.steps) == 0 {
		return EnvironmentRollbackPlan{
			EnvironmentID: prepared.environment.ID, EnvironmentName: prepared.environment.Name,
			EnvironmentRevisionID: prepared.environment.Revision.ID,
			Nodes:                 []string{}, Sources: []EnvironmentRollbackSource{},
			Steps: []ComponentTestPlanStep{}, DeliveryRequirements: []mediadelivery.Requirement{},
		}, nil
	}
	plan, digest, destructive, err := p.planner.prepareLockedPlan(ctx, prepared.environment, domain.RunEnvironmentRollback, "preview", prepared.sourceRuns[0].CreatedAt, prepared.steps)
	if err != nil {
		return EnvironmentRollbackPlan{}, err
	}
	return p.planner.environmentRollbackPlanDTO(ctx, prepared, plan, digest, destructive), nil
}

func (p *ExecutionService) StartRollback(ctx context.Context, user domain.User, environmentID string, input EnvironmentRollbackRequest) (domain.Run, error) {
	prepared, err := p.planner.prepareEnvironmentRollback(ctx, user, environmentID, input.Nodes...)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			err = actionableExistingError(err, "rollback.baseline_invalid", "当前安装清单、来源 Run 或备份基线无法生成安全回滚计划", "检查环境与来源 Run", "/environments?selected="+environmentID)
		}
		return domain.Run{}, err
	}
	if len(prepared.steps) == 0 {
		return domain.Run{}, fmt.Errorf("%w: 当前环境暂无可回滚组件，无需创建回滚 Run", domain.ErrConflict)
	}
	if strings.TrimSpace(input.ExpectedPlanDigest) == "" {
		return domain.Run{}, fmt.Errorf("%w: preview the cluster rollback plan before submitting", domain.ErrInvalid)
	}
	if input.ConfirmEnvironmentName != prepared.environment.Name {
		return domain.Run{}, fmt.Errorf("%w: confirmation must exactly match environment name %q", domain.ErrInvalid, prepared.environment.Name)
	}
	scenarioRevisionID := ""
	if len(prepared.sourceRuns) == 1 {
		scenarioRevisionID = prepared.sourceRuns[0].ScenarioRevisionID
	}
	run, err := p.createRun(ctx, user, prepared.environment, domain.RunEnvironmentRollback, "", scenarioRevisionID, domain.ActionRollback, prepared.steps, nil, input.ExpectedPlanDigest)
	if err != nil {
		return domain.Run{}, err
	}
	sourceRunIDs := make([]string, 0, len(prepared.sourceRuns))
	for _, sourceRun := range prepared.sourceRuns {
		sourceRunIDs = append(sourceRunIDs, sourceRun.ID)
	}
	p.audit.Record(ctx, user, "environment.cluster_rollback.requested", "environment", prepared.environment.ID, map[string]any{
		"runId": run.ID, "sourceRunIds": sourceRunIDs,
		"componentCount": countStepComponents(prepared.steps), "nodeCount": len(prepared.steps),
	})
	return run, nil
}

func (p *PlanBuilder) prepareEnvironmentRollback(ctx context.Context, user domain.User, environmentID string, nodes ...string) (preparedEnvironmentRollback, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return preparedEnvironmentRollback{}, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return preparedEnvironmentRollback{}, err
	}
	if err := ensureEnvironmentActive(environment); err != nil {
		return preparedEnvironmentRollback{}, err
	}
	if environment.Revision == nil {
		return preparedEnvironmentRollback{}, fmt.Errorf("%w: environment has no current revision", domain.ErrConflict)
	}
	active, err := p.store.HasActiveEnvironmentRun(ctx, environmentID)
	if err != nil {
		return preparedEnvironmentRollback{}, err
	}
	if active {
		return preparedEnvironmentRollback{}, fmt.Errorf("%w: environment has an active run; finish or reject it before planning a cluster rollback", domain.ErrConflict)
	}
	installations, err := p.rollback.recoveryBaselines(ctx, environmentID)
	if err != nil {
		return preparedEnvironmentRollback{}, err
	}
	if len(installations) == 0 {
		// No recorded installation or pending recovery is a normal preview state.
		// StartRollback still rejects creating a Run without executable steps.
		return preparedEnvironmentRollback{environment: environment}, nil
	}
	installationsByRun := map[string][]domain.EnvironmentComponentInstallation{}
	installedByComponent := make(map[string]domain.EnvironmentComponentInstallation, len(installations))
	for _, installation := range installations {
		if err := validateInstallationBackup(installation, environmentID, installation.ComponentID, installation.ReleaseID); err != nil {
			return preparedEnvironmentRollback{}, err
		}
		if err := p.rollback.validateCurrentInstallEvidence(ctx, installation); err != nil {
			return preparedEnvironmentRollback{}, err
		}
		installedByComponent[installation.ComponentID+"/"+installation.NodeID] = installation
		installationsByRun[installation.InstallRunID] = append(installationsByRun[installation.InstallRunID], installation)
	}
	sourceRuns := make([]domain.Run, 0, len(installationsByRun))
	for sourceRunID, sourceInstallations := range installationsByRun {
		sourceRun, err := p.store.GetRun(ctx, sourceRunID)
		if err != nil {
			return preparedEnvironmentRollback{}, fmt.Errorf("%w: source installation Run %s no longer exists", domain.ErrConflict, sourceRunID)
		}
		if sourceRun.InputSnapshot["executionMode"] == "upgrade" {
			return preparedEnvironmentRollback{}, fmt.Errorf("%w: 环境包含场景升级历史，整集群清空回滚不可用；请按保留的恢复作业恢复来源版本并执行基线复核", domain.ErrConflict)
		}
		if sourceRun.EnvironmentID != environmentID || (sourceRun.Kind != domain.RunScenario && sourceRun.Kind != domain.RunScenarioTest && sourceRun.Kind != domain.RunComponentTest) {
			return preparedEnvironmentRollback{}, fmt.Errorf("%w: source Run %s is not a supported installation Run", domain.ErrConflict, sourceRunID)
		}
		if sourceRun.Status != domain.RunSucceeded && sourceRun.Status != domain.RunFailed && sourceRun.Status != domain.RunCancelled && sourceRun.Status != domain.RunInterrupted {
			return preparedEnvironmentRollback{}, fmt.Errorf("%w: source Run %s is not terminal", domain.ErrConflict, sourceRunID)
		}
		expectedTestOnly := sourceRun.Kind != domain.RunScenario
		for _, installation := range sourceInstallations {
			if installation.TestOnly != expectedTestOnly {
				return preparedEnvironmentRollback{}, fmt.Errorf("%w: installed-component test provenance does not match source Run %s", domain.ErrConflict, sourceRunID)
			}
		}
		sourceRuns = append(sourceRuns, sourceRun)
	}
	sort.Slice(sourceRuns, func(left, right int) bool {
		leftAt, rightAt := sourceRuns[left].CreatedAt, sourceRuns[right].CreatedAt
		if sourceRuns[left].FinishedAt != nil {
			leftAt = *sourceRuns[left].FinishedAt
		}
		if sourceRuns[right].FinishedAt != nil {
			rightAt = *sourceRuns[right].FinishedAt
		}
		if leftAt.Equal(rightAt) {
			return sourceRuns[left].ID > sourceRuns[right].ID
		}
		return leftAt.After(rightAt)
	})
	releases := map[string]domain.ComponentRelease{}
	covered := map[string]bool{}
	steps := make([]lockedStep, 0)
	for _, sourceRun := range sourceRuns {
		sourcePlan, err := mapToPlan(sourceRun.InputSnapshot)
		if err != nil {
			return preparedEnvironmentRollback{}, fmt.Errorf("%w: source Run %s has no usable locked plan", domain.ErrConflict, sourceRun.ID)
		}
		sourceRevision, err := p.store.GetEnvironmentRevision(ctx, sourceRun.EnvironmentRevisionID)
		if err != nil {
			return preparedEnvironmentRollback{}, fmt.Errorf("%w: source environment revision for Run %s no longer exists", domain.ErrConflict, sourceRun.ID)
		}
		for index := len(sourcePlan.Steps) - 1; index >= 0; index-- {
			sourceStep := sourcePlan.Steps[index]
			installation, installed := installedByComponent[sourceStep.ComponentID+"/"+sourceStep.SourceNodeID]
			if !installed || installation.InstallRunID != sourceRun.ID || sourceStep.ReleaseID != installation.ReleaseID || sourceStep.ActionID != installation.Backup.ActionID {
				continue
			}
			if sourceStep.Action != domain.ActionInstall && sourceStep.Action != domain.ActionConfigure && sourceStep.Action != domain.ActionUpgrade {
				continue
			}
			release, ok := releases[sourceStep.ReleaseID]
			if !ok {
				release, err = p.store.GetComponentRelease(ctx, sourceStep.ReleaseID)
				if err != nil {
					return preparedEnvironmentRollback{}, err
				}
				releases[sourceStep.ReleaseID] = release
			}
			rollback, ok := findAction(release, domain.ActionRollback)
			if !ok {
				return preparedEnvironmentRollback{}, fmt.Errorf("%w: release %s has no rollback action for clean-state recovery", domain.ErrConflict, release.Version)
			}

			component, err := p.store.GetComponent(ctx, release.ComponentID, false)
			if err != nil {
				return preparedEnvironmentRollback{}, err
			}
			rollback.HostGroup = sourceStep.Limit
			variables := rollbackVariablesFromInstall(sourceStep.Variables, sourceRevision, *environment.Revision, release)
			step, err := p.actions.lockAction(component, "clean-"+sourceRun.ID+"-"+sourceStep.NodeID, release, rollback, variables)
			if err != nil {
				return preparedEnvironmentRollback{}, err
			}
			step.SourceNodeID = sourceStep.SourceNodeID
			step.RollbackSourceActionID = sourceStep.ActionID
			step.BackupRef, step.Backup = installation.BackupRef, &installation.Backup
			steps = append(steps, step)
			covered[sourceStep.ComponentID+"/"+sourceStep.SourceNodeID] = true
		}
	}
	if len(covered) != len(installedByComponent) {
		missing := make([]string, 0)
		for componentID := range installedByComponent {
			if !covered[componentID] {
				missing = append(missing, componentID)
			}
		}
		sort.Strings(missing)
		return preparedEnvironmentRollback{}, fmt.Errorf("%w: source Run cannot reconstruct rollback nodes for %s", domain.ErrConflict, strings.Join(missing, ", "))
	}
	if len(steps) == 0 {
		return preparedEnvironmentRollback{}, fmt.Errorf("%w: no rollback steps could be generated", domain.ErrConflict)
	}
	if len(nodes) > 0 {
		jobSteps := jobPlanFromLocked(environmentID, lockedPlan{Steps: steps}, nil).Steps
		selected, err := ansible.SelectRollbackStages(jobSteps, nodes)
		if err != nil {
			return preparedEnvironmentRollback{}, fmt.Errorf("%w: %v", domain.ErrInvalid, err)
		}
		steps = steps[:len(selected)]
	}
	return preparedEnvironmentRollback{environment: environment, sourceRuns: sourceRuns, installationsByRun: installationsByRun, steps: steps}, nil
}

func rollbackVariablesFromInstall(values map[string]any, sourceRevision, currentRevision domain.EnvironmentRevision, release domain.ComponentRelease) map[string]any {
	output := cloneMap(values)
	for name := range sourceRevision.Variables {
		delete(output, name)
	}
	for name := range currentRevision.Variables {
		delete(output, name)
	}
	for _, name := range []string{
		"clusterforge_backup_ref", "clusterforge_backup_marker", "clusterforge_backup_operation",
		"clusterforge_backup_cleanup_on_success", "clusterforge_backup_metadata",
		"clusterforge_replaced_backup_ref", "clusterforge_cleanup_replaced_backup_on_success",
		"component_image_ref", "component_image_digest",
	} {
		delete(output, name)
	}
	for _, artifact := range release.Artifacts {
		delete(output, artifact.Alias+"_path")
		delete(output, artifact.Alias+"_url")
		delete(output, artifact.Alias+"_sha256")
	}
	for _, image := range release.Images {
		delete(output, image.LogicalName+"_image_ref")
		delete(output, image.LogicalName+"_image_digest")
	}
	return output
}

func (p *PlanBuilder) environmentRollbackPlanDTO(ctx context.Context, prepared preparedEnvironmentRollback, plan lockedPlan, digest string, destructive bool) EnvironmentRollbackPlan {
	base := p.componentTestPlanDTO(ctx, prepared.environment, plan, digest, destructive)
	return EnvironmentRollbackPlan{
		EnvironmentID: prepared.environment.ID, EnvironmentName: prepared.environment.Name,
		EnvironmentRevisionID: prepared.environment.CurrentRevisionID,
		Sources:               environmentRollbackSources(prepared),
		ComponentCount:        countStepComponents(prepared.steps), NodeCount: len(prepared.steps),
		Destructive: true, RequiresApproval: true, PlanDigest: base.PlanDigest, Steps: base.Steps,
		DeliveryRequirements: base.DeliveryRequirements,
	}
}

func environmentRollbackSources(prepared preparedEnvironmentRollback) []EnvironmentRollbackSource {
	sources := make([]EnvironmentRollbackSource, 0, len(prepared.sourceRuns))
	for _, run := range prepared.sourceRuns {
		sources = append(sources, EnvironmentRollbackSource{
			RunID: run.ID, Kind: run.Kind, ScenarioRevisionID: run.ScenarioRevisionID,
			ComponentCount: len(prepared.installationsByRun[run.ID]),
		})
	}
	return sources
}

func countStepComponents(steps []lockedStep) int {
	components := map[string]struct{}{}
	for _, step := range steps {
		components[step.ComponentID] = struct{}{}
	}
	return len(components)
}

func installationBaselineFromSteps(steps []lockedStep) []lockedInstallationBaseline {
	byComponent := map[string]lockedInstallationBaseline{}
	for _, step := range steps {
		if step.Action != domain.ActionRollback && step.Action != domain.ActionUninstall || step.Backup == nil {
			continue
		}
		byComponent[step.ComponentID+"/"+step.Backup.NodeID] = lockedInstallationBaseline{
			NodeID:      step.Backup.NodeID,
			ComponentID: step.ComponentID, ReleaseID: step.Backup.ReleaseID,
			InstallRunID: step.Backup.InstallRunID, BackupRef: step.BackupRef,
			PlaybookSHA256: step.Backup.PlaybookSHA256,
		}
	}
	baseline := make([]lockedInstallationBaseline, 0, len(byComponent))
	for _, item := range byComponent {
		baseline = append(baseline, item)
	}
	sort.Slice(baseline, func(i, j int) bool {
		return baseline[i].ComponentID+"/"+baseline[i].NodeID < baseline[j].ComponentID+"/"+baseline[j].NodeID
	})
	return baseline
}

func installationBaselineDigest(baseline []lockedInstallationBaseline) string {
	encoded, _ := json.Marshal(baseline)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:])
}

func isFinalCleanupStep(steps []lockedStep, index int) bool {
	for later := index + 1; later < len(steps); later++ {
		if steps[later].ComponentID == steps[index].ComponentID && (steps[later].Action == domain.ActionRollback || steps[later].Action == domain.ActionUninstall) {
			return false
		}
	}
	return true
}

func (r *RollbackPlanner) validateLockedRollbackPlan(ctx context.Context, run domain.Run, plan lockedPlan) error {
	if run.Kind == domain.RunEnvironmentRollback && run.RetryOfRunID == "" {
		expected := installationBaselineFromSteps(plan.Steps)
		if plan.InstallationBaselineDigest == "" || plan.InstallationBaselineDigest != installationBaselineDigest(expected) || len(plan.InstallationBaseline) != len(expected) {
			return fmt.Errorf("%w: locked environment rollback baseline is incomplete or inconsistent", domain.ErrConflict)
		}
		for index := range expected {
			if plan.InstallationBaseline[index] != expected[index] {
				return fmt.Errorf("%w: locked environment rollback baseline changed; preview again", domain.ErrConflict)
			}
		}
		current, err := r.recoveryBaselines(ctx, run.EnvironmentID)
		if err != nil {
			return err
		}
		if plan.RecoveryEnvironmentDigest == "" || digestValue(current) != plan.RecoveryEnvironmentDigest {
			return fmt.Errorf("%w: recovery state changed after preview; preview again", domain.ErrConflict)
		}
		selected := map[string]bool{}
		for _, entry := range expected {
			selected[entry.ComponentID+"/"+entry.NodeID] = true
		}
		selectedCurrent := current[:0]
		for _, entry := range current {
			if selected[entry.ComponentID+"/"+entry.NodeID] {
				selectedCurrent = append(selectedCurrent, entry)
			}
		}
		current = selectedCurrent
		if len(current) != len(expected) {
			return fmt.Errorf("%w: installed-component set changed after approval; preview again", domain.ErrConflict)
		}
		actual := make([]lockedInstallationBaseline, 0, len(current))
		for _, installation := range current {
			actual = append(actual, lockedInstallationBaseline{
				NodeID: installation.NodeID, ComponentID: installation.ComponentID, ReleaseID: installation.ReleaseID,
				InstallRunID: installation.InstallRunID, BackupRef: installation.BackupRef,
				PlaybookSHA256: installation.Backup.PlaybookSHA256,
			})
		}
		sort.Slice(actual, func(i, j int) bool {
			return actual[i].ComponentID+"/"+actual[i].NodeID < actual[j].ComponentID+"/"+actual[j].NodeID
		})
		for index := range actual {
			if actual[index] != expected[index] {
				return fmt.Errorf("%w: installed-component baseline changed after approval; preview again", domain.ErrConflict)
			}
		}
	}
	checked := map[string]struct{}{}
	for index := range plan.Steps {
		step := plan.Steps[index]
		if step.Action != domain.ActionRollback && step.Action != domain.ActionUninstall {
			continue
		}
		if _, ok := checked[step.ComponentID+"/"+step.BackupRef]; ok {
			continue
		}
		checked[step.ComponentID+"/"+step.BackupRef] = struct{}{}
		if step.Backup == nil || step.BackupRef == "" {
			return fmt.Errorf("%w: locked rollback step is missing backup metadata", domain.ErrConflict)
		}
		if source := priorSameRunBackupStep(plan.Steps, index, &step); source != nil {
			if source.Backup == nil || source.BackupRef != step.BackupRef || source.Backup.InstallRunID != step.Backup.InstallRunID || source.Backup.PlaybookSHA256 != step.Backup.PlaybookSHA256 {
				return fmt.Errorf("%w: same-run rollback backup is inconsistent with its source action", domain.ErrConflict)
			}
			continue
		}
		if receipt, receiptErr := r.store.LatestActionReceiptForNode(ctx, run.EnvironmentID, step.ComponentID, "", step.Backup.NodeID); receiptErr == nil && receipt.BackupRef == step.BackupRef && digestValue(receipt.Backup) == digestValue(*step.Backup) && receipt.Status != "verified" {
			continue
		} else if receiptErr != nil && !errors.Is(receiptErr, domain.ErrNotFound) {
			return receiptErr
		}
		installation, err := r.installationForStep(ctx, run.EnvironmentID, step)
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: rollback baseline disappeared before execution", domain.ErrConflict)
		}
		if err != nil {
			return err
		}
		expectedReleaseID := step.ReleaseID
		if step.FromReleaseID != "" {
			expectedReleaseID = step.FromReleaseID
		}
		if err := validateInstallationBackup(installation, run.EnvironmentID, step.ComponentID, expectedReleaseID); err != nil {
			return err
		}
		if installation.BackupRef != step.BackupRef || installation.InstallRunID != step.Backup.InstallRunID || installation.Backup.PlaybookSHA256 != step.Backup.PlaybookSHA256 {
			return fmt.Errorf("%w: rollback baseline changed after approval; preview again", domain.ErrConflict)
		}
		if err := r.validateCurrentInstallEvidence(ctx, installation); err != nil {
			return err
		}
	}
	return nil
}
