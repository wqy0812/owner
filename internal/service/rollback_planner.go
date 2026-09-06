package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func (r *RollbackPlanner) bindBackupPlan(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, plan *lockedPlan) error {
	for index := range plan.Steps {
		step := &plan.Steps[index]
		switch step.Action {
		case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
			if err := r.bindInstallBackupStep(ctx, environmentID, runID, kind, capturedAt, step); err != nil {
				return err
			}
			for before := index - 1; before >= 0; before-- {
				previous := plan.Steps[before]
				if previous.Phase == "execute" && previous.ComponentID == step.ComponentID && previous.SourceNodeID == step.SourceNodeID && previous.Backup != nil && (previous.Action == domain.ActionInstall || previous.Action == domain.ActionUpgrade || previous.Action == domain.ActionConfigure) {
					step.Backup.Previous = &domain.EnvironmentComponentInstallation{NodeID: previous.SourceNodeID, EnvironmentID: environmentID, ComponentID: previous.ComponentID, ReleaseID: previous.ReleaseID, InstallRunID: previous.Backup.InstallRunID, BackupRef: previous.BackupRef, Backup: *previous.Backup, TestOnly: kind != domain.RunScenario, InstalledAt: capturedAt}
					break
				}
			}
		case domain.ActionRollback:
			if source := priorSameRunBackupStep(plan.Steps, index, step); source != nil {
				step.BackupRef = source.BackupRef
				metadata := *source.Backup
				step.Backup = &metadata
				bindBackupVariables(step, "restore", kind != domain.RunScenario && isFinalCleanupStep(plan.Steps, index))
				continue
			}
			installation, err := r.installationForStep(ctx, environmentID, *step)
			if receipt, receiptErr := r.pendingReceiptForStep(ctx, environmentID, *step); receiptErr == nil && receipt.Status != "verified" {
				sourceRun, sourceErr := r.store.GetRun(ctx, receipt.RunID)
				if sourceErr != nil {
					return sourceErr
				}
				if sourceRun.Status == domain.RunRunning || sourceRun.Status == domain.RunQueued {
					return fmt.Errorf("%w: source action is still active", domain.ErrConflict)
				}
				step.SourceNodeID = receipt.SourceNodeID
				step.BackupRef = receipt.BackupRef
				backup := receipt.Backup
				step.Backup = &backup
				bindBackupVariables(step, "restore", false)
				continue
			} else if receiptErr != nil && !errors.Is(receiptErr, domain.ErrNotFound) {
				return receiptErr
			}

			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: rollback refused: environment %s has no installed-component backup_ref for component %s", domain.ErrConflict, environmentID, step.ComponentID)
				}
				return err
			}
			expectedReleaseID := step.ReleaseID
			if step.FromReleaseID != "" {
				expectedReleaseID = step.FromReleaseID
			}
			if err := validateInstallationBackup(installation, environmentID, step.ComponentID, expectedReleaseID); err != nil {
				return err
			}
			if err := r.validateCurrentInstallEvidence(ctx, installation); err != nil {
				return err
			}
			step.BackupRef = installation.BackupRef
			metadata := installation.Backup
			step.Backup = &metadata
			bindBackupVariables(step, "restore", kind != domain.RunScenario && isFinalCleanupStep(plan.Steps, index))
		case domain.ActionUninstall:
			installation, err := r.installationForStep(ctx, environmentID, *step)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					return fmt.Errorf("%w: uninstall refused: environment %s has no installed-component record for component %s", domain.ErrConflict, environmentID, step.ComponentID)
				}
				return err
			}
			if err := validateInstallationBackup(installation, environmentID, step.ComponentID, step.ReleaseID); err != nil {
				return err
			}
			step.BackupRef = installation.BackupRef
			metadata := installation.Backup
			step.Backup = &metadata
			bindBackupVariables(step, "cleanup", isFinalCleanupStep(plan.Steps, index))
		}
	}
	return nil
}

func priorSameRunBackupStep(steps []lockedStep, rollbackIndex int, rollback *lockedStep) *lockedStep {
	expectedReleaseID := rollback.ReleaseID
	if rollback.FromReleaseID != "" {
		expectedReleaseID = rollback.FromReleaseID
	}
	for index := rollbackIndex - 1; index >= 0; index-- {
		candidate := &steps[index]
		if candidate.SourceNodeID != rollback.SourceNodeID || candidate.ComponentID != rollback.ComponentID || candidate.ReleaseID != expectedReleaseID || candidate.Backup == nil || candidate.BackupRef == "" {
			continue
		}
		switch candidate.Action {
		case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
			return candidate
		}
	}
	return nil
}

func (r *RollbackPlanner) bindInstallBackupStep(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, step *lockedStep) error {
	release, err := r.store.GetComponentRelease(ctx, step.ReleaseID)
	if err != nil {
		return err
	}
	if step.Variables == nil {
		step.Variables = map[string]any{}
	}
	for _, name := range []string{
		"clusterforge_backup_ref", "clusterforge_backup_marker", "clusterforge_backup_operation",
		"clusterforge_backup_cleanup_on_success", "clusterforge_backup_metadata",
		"clusterforge_replaced_backup_ref", "clusterforge_cleanup_replaced_backup_on_success",
	} {
		delete(step.Variables, name)
	}
	if receipt, receiptErr := r.store.LatestActionReceiptForNode(ctx, environmentID, step.ComponentID, "", step.SourceNodeID); receiptErr == nil && receipt.Status != "verified" {
		recovered, recoveryErr := r.store.IsActionReceiptRecovered(ctx, receipt)
		if recoveryErr != nil {
			return recoveryErr
		}
		if !recovered {
			return fmt.Errorf("%w: node has an unverified operation; use its safe retry or rollback without replacing the original baseline", domain.ErrConflict)
		}
	} else if receiptErr != nil && !errors.Is(receiptErr, domain.ErrNotFound) {
		return receiptErr
	}
	metadata := domain.BackupMetadata{
		NodeID: step.SourceNodeID, EnvironmentID: environmentID, ComponentID: step.ComponentID, ReleaseID: step.ReleaseID,
		ActionID: step.ActionID, InstallRunID: runID, CapturedAt: capturedAt,
		PlaybookSHA256: step.PlaybookDigest, DependencySnapshot: releaseDependencySnapshot(release),
	}
	step.BackupRef = path.Join("/var/lib/clusterforge/backups", safeBackupSegment(environmentID), safeBackupSegment(step.ComponentID), safeBackupSegment(step.ReleaseID), safeBackupSegment(runID), safeBackupSegment(step.SourceNodeID))
	step.Backup = &metadata
	if current, currentErr := r.store.GetEnvironmentComponentInstallationForNode(ctx, environmentID, step.ComponentID, step.SourceNodeID); currentErr == nil && current.BackupRef != step.BackupRef {
		step.Variables["clusterforge_replaced_backup_ref"] = current.BackupRef
		step.Variables["clusterforge_cleanup_replaced_backup_on_success"] = false
		metadata.Previous = &current
	} else if currentErr != nil && !errors.Is(currentErr, domain.ErrNotFound) {
		return currentErr
	}
	bindBackupVariables(step, "capture", kind != domain.RunScenario)
	return nil
}

func bindBackupVariables(step *lockedStep, operation string, cleanupOnSuccess bool) {
	cleanupOnSuccess = false // Recovery material survives until the postcondition is verified.
	metadata := step.Backup
	step.Variables["clusterforge_backup_ref"] = step.BackupRef
	step.Variables["clusterforge_backup_marker"] = path.Join(step.BackupRef, ".captured")
	step.Variables["clusterforge_backup_operation"] = operation
	step.Variables["clusterforge_backup_cleanup_on_success"] = cleanupOnSuccess && (operation == "restore" || operation == "cleanup")
	step.Variables["clusterforge_backup_metadata"] = map[string]any{
		"node_id":             metadata.NodeID,
		"environment_id":      metadata.EnvironmentID,
		"component_id":        metadata.ComponentID,
		"release_id":          metadata.ReleaseID,
		"action_id":           metadata.ActionID,
		"install_run_id":      metadata.InstallRunID,
		"captured_at":         metadata.CapturedAt.UTC().Format(time.RFC3339Nano),
		"playbook_sha256":     metadata.PlaybookSHA256,
		"dependency_snapshot": metadata.DependencySnapshot,
	}
}

func releaseDependencySnapshot(release domain.ComponentRelease) map[string]any {
	dependencies := make([]map[string]any, 0, len(release.Dependencies))
	for _, dependency := range release.Dependencies {
		entry := map[string]any{
			"component_id":       dependency.UpstreamComponentID,
			"release_id":         dependency.UpstreamReleaseID,
			"parameter_mappings": dependency.ParameterMappings,
		}
		if dependency.Kind != "" {
			entry["kind"] = dependency.Kind
		}
		dependencies = append(dependencies, entry)
	}
	return map[string]any{"dependencies": dependencies}
}

func validateInstallationBackup(installation domain.EnvironmentComponentInstallation, environmentID, componentID, releaseID string) error {
	metadata := installation.Backup
	if installation.BackupRef == "" || installation.InstallRunID == "" || metadata.EnvironmentID == "" || metadata.ComponentID == "" || metadata.ReleaseID == "" || metadata.InstallRunID == "" || metadata.PlaybookSHA256 == "" || metadata.CapturedAt.IsZero() {
		return fmt.Errorf("%w: rollback refused: installed-component backup metadata is incomplete", domain.ErrConflict)
	}
	if installation.EnvironmentID != environmentID || metadata.EnvironmentID != environmentID {
		return fmt.Errorf("%w: rollback refused: backup environment does not match %s", domain.ErrConflict, environmentID)
	}
	if installation.ComponentID != componentID || metadata.ComponentID != componentID {
		return fmt.Errorf("%w: rollback refused: backup component does not match %s", domain.ErrConflict, componentID)
	}
	if installation.ReleaseID != releaseID || metadata.ReleaseID != releaseID {
		return fmt.Errorf("%w: rollback refused: backup release %s does not match %s", domain.ErrConflict, metadata.ReleaseID, releaseID)
	}
	if installation.InstallRunID != metadata.InstallRunID {
		return fmt.Errorf("%w: rollback refused: backup install Run metadata does not match the installed-component record", domain.ErrConflict)
	}
	return nil
}

func (r *RollbackPlanner) validateCurrentInstallEvidence(ctx context.Context, installation domain.EnvironmentComponentInstallation) error {
	release, err := r.store.GetComponentRelease(ctx, installation.ReleaseID)
	if err != nil {
		return fmt.Errorf("%w: rollback refused: installed release no longer exists", domain.ErrConflict)
	}
	digester := r.inspector
	matched, err := currentReleaseContainsCapturedInstallPlaybook(release, installation.Backup.ActionID, installation.Backup.PlaybookSHA256, digester)
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("%w: rollback refused: captured Playbook hash does not match the current release definition", domain.ErrConflict)
	}
	return nil
}

func currentReleaseContainsCapturedInstallPlaybook(release domain.ComponentRelease, actionID, playbookSHA256 string, digester digestRunner) (bool, error) {
	candidates := make([]domain.ActionDefinition, 0, len(release.Actions))
	for _, action := range release.Actions {
		if action.ID == actionID {
			candidates = append(candidates, action)
			break
		}
	}
	if len(candidates) == 0 {
		return false, fmt.Errorf("%w: rollback refused: captured install action no longer matches release %s", domain.ErrConflict, release.ID)
	}
	for _, action := range candidates {
		currentDigest, _, err := digester.Digest(action.Playbook)
		if err != nil {
			return false, fmt.Errorf("%w: rollback refused: cannot verify captured Playbook: %v", domain.ErrConflict, err)
		}
		if currentDigest == playbookSHA256 {
			return true, nil
		}
	}
	return false, nil
}

func safeBackupSegment(value string) string {
	if value != "" {
		valid := true
		for _, char := range value {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '.' && char != '_' && char != '-' {
				valid = false
				break
			}
		}
		if valid && value != "." && value != ".." {
			return value
		}
	}
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("id-%x", digest[:12])
}

func (p *RollbackPlanner) installationForStep(ctx context.Context, environmentID string, step lockedStep) (domain.EnvironmentComponentInstallation, error) {
	if step.BackupRef != "" {
		all, err := p.store.ListEnvironmentComponentInstallations(ctx, environmentID)
		if err != nil {
			return domain.EnvironmentComponentInstallation{}, err
		}
		for _, item := range all {
			if item.ComponentID == step.ComponentID && item.BackupRef == step.BackupRef {
				return item, nil
			}
		}
	}
	item, err := p.store.GetEnvironmentComponentInstallationForNode(ctx, environmentID, step.ComponentID, step.SourceNodeID)
	if err == nil || !errors.Is(err, domain.ErrNotFound) {
		return item, err
	}
	all, err := p.store.ListEnvironmentComponentInstallations(ctx, environmentID)
	if err != nil {
		return item, err
	}
	found := 0
	for _, candidate := range all {
		if candidate.ComponentID == step.ComponentID {
			item = candidate
			found++
		}
	}
	if found == 1 {
		return item, nil
	}
	if found > 1 {
		return item, fmt.Errorf("%w: multiple installed nodes require an exact backup selection", domain.ErrConflict)
	}
	return item, domain.ErrNotFound
}

func (p *RollbackPlanner) pendingReceiptForStep(ctx context.Context, environmentID string, step lockedStep) (store.ActionExecutionReceipt, error) {
	receipts, err := p.store.LatestEnvironmentActionReceipts(ctx, environmentID)
	if err != nil {
		return store.ActionExecutionReceipt{}, err
	}
	matches := []store.ActionExecutionReceipt{}
	for _, r := range receipts {
		if r.ComponentID != step.ComponentID || r.Status == "verified" {
			continue
		}
		if step.BackupRef != "" && r.BackupRef != step.BackupRef {
			continue
		}
		if r.SourceNodeID == step.SourceNodeID || (step.Backup != nil && r.SourceNodeID == step.Backup.NodeID) {
			return r, nil
		}
		matches = append(matches, r)
	}
	if len(matches) == 1 && strings.HasPrefix(step.SourceNodeID, "component-") {
		return matches[0], nil
	}
	if len(matches) > 0 {
		return store.ActionExecutionReceipt{}, fmt.Errorf("%w: select the exact component instance for pending recovery", domain.ErrConflict)
	}
	return store.ActionExecutionReceipt{}, domain.ErrNotFound
}

// Recovery baselines include operations which changed targets but have not passed their post-check.
func (p *RollbackPlanner) recoveryBaselines(ctx context.Context, environmentID string) ([]domain.EnvironmentComponentInstallation, error) {
	installations, err := p.store.ListEnvironmentComponentInstallations(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	receipts, err := p.store.LatestEnvironmentActionReceipts(ctx, environmentID)
	if err != nil {
		return nil, err
	}
	byNode := map[string]domain.EnvironmentComponentInstallation{}
	for _, item := range installations {
		byNode[item.ComponentID+"/"+item.NodeID] = item
	}
	for _, receipt := range receipts {
		if receipt.Status == "verified" {
			continue
		}
		if receipt.BackupRef == "" {
			return nil, fmt.Errorf("%w: pending operation has no recovery baseline", domain.ErrConflict)
		}
		source, err := p.store.GetRun(ctx, receipt.Backup.InstallRunID)
		if err != nil {
			return nil, err
		}
		byNode[receipt.ComponentID+"/"+receipt.SourceNodeID] = domain.EnvironmentComponentInstallation{EnvironmentID: environmentID, ComponentID: receipt.ComponentID, ReleaseID: receipt.Backup.ReleaseID, NodeID: receipt.SourceNodeID, InstallRunID: receipt.Backup.InstallRunID, BackupRef: receipt.BackupRef, Backup: receipt.Backup, TestOnly: source.Kind != domain.RunScenario, InstalledAt: receipt.StartedAt}
	}
	out := make([]domain.EnvironmentComponentInstallation, 0, len(byNode))
	for _, item := range byNode {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ComponentID+"/"+out[i].NodeID < out[j].ComponentID+"/"+out[j].NodeID
	})
	return out, nil
}
