package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"time"

	"codex/platform-demo/internal/domain"
)

func (r *RollbackPlanner) bindBackupPlan(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, plan *lockedPlan) error {
	p := r.platform
	for index := range plan.Steps {
		step := &plan.Steps[index]
		switch step.Action {
		case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
			if err := p.bindInstallBackupStep(ctx, environmentID, runID, kind, capturedAt, step); err != nil {
				return err
			}
		case domain.ActionRollback:
			if source := priorSameRunBackupStep(plan.Steps, index, step); source != nil {
				step.BackupRef = source.BackupRef
				metadata := *source.Backup
				step.Backup = &metadata
				bindBackupVariables(step, "restore", kind != domain.RunScenario && isFinalCleanupStep(plan.Steps, index))
				continue
			}
			installation, err := p.store.GetEnvironmentComponentInstallation(ctx, environmentID, step.ComponentID)
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
			if err := p.validateCurrentInstallEvidence(ctx, installation); err != nil {
				return err
			}
			step.BackupRef = installation.BackupRef
			metadata := installation.Backup
			step.Backup = &metadata
			bindBackupVariables(step, "restore", kind != domain.RunScenario && isFinalCleanupStep(plan.Steps, index))
		case domain.ActionUninstall:
			installation, err := p.store.GetEnvironmentComponentInstallation(ctx, environmentID, step.ComponentID)
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
		if candidate.ComponentID != rollback.ComponentID || candidate.ReleaseID != expectedReleaseID || candidate.Backup == nil || candidate.BackupRef == "" {
			continue
		}
		switch candidate.Action {
		case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
			return candidate
		}
	}
	return nil
}

func (r *RollbackPlanner) rebindRetryBackupPlan(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, plan *lockedPlan) error {
	p := r.platform
	// Rebind every remaining capture step first. A later Rollback must see the
	// complete retry-local capture set rather than the source Run's metadata.
	for index := range plan.Steps {
		switch plan.Steps[index].Action {
		case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
			if err := p.bindInstallBackupStep(ctx, environmentID, runID, kind, capturedAt, &plan.Steps[index]); err != nil {
				return err
			}
		}
	}
	for index := range plan.Steps {
		step := &plan.Steps[index]
		if step.Action != domain.ActionRollback {
			continue
		}
		if source := priorSameRunBackupStep(plan.Steps, index, step); source != nil {
			step.BackupRef = source.BackupRef
			metadata := *source.Backup
			step.Backup = &metadata
		} else if step.Backup == nil || step.BackupRef == "" {
			return fmt.Errorf("%w: retry rollback step is missing its locked backup metadata", domain.ErrConflict)
		}
		if step.Variables == nil {
			step.Variables = map[string]any{}
		}
		bindBackupVariables(step, "restore", kind != domain.RunScenario && isFinalCleanupStep(plan.Steps, index))
	}
	return nil
}

func (r *RollbackPlanner) bindInstallBackupStep(ctx context.Context, environmentID, runID string, kind domain.RunKind, capturedAt time.Time, step *lockedStep) error {
	p := r.platform
	release, err := p.store.GetComponentRelease(ctx, step.ReleaseID)
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
	metadata := domain.BackupMetadata{
		EnvironmentID: environmentID, ComponentID: step.ComponentID, ReleaseID: step.ReleaseID,
		ActionID: step.ActionID, InstallRunID: runID, CapturedAt: capturedAt,
		PlaybookSHA256: step.PlaybookDigest, DependencySnapshot: releaseDependencySnapshot(release),
	}
	step.BackupRef = path.Join("/var/lib/clusterforge/backups", safeBackupSegment(environmentID), safeBackupSegment(step.ComponentID), safeBackupSegment(step.ReleaseID), safeBackupSegment(runID))
	step.Backup = &metadata
	if current, currentErr := p.store.GetEnvironmentComponentInstallation(ctx, environmentID, step.ComponentID); currentErr == nil && current.BackupRef != step.BackupRef {
		step.Variables["clusterforge_replaced_backup_ref"] = current.BackupRef
		step.Variables["clusterforge_cleanup_replaced_backup_on_success"] = true
	} else if currentErr != nil && !errors.Is(currentErr, domain.ErrNotFound) {
		return currentErr
	}
	bindBackupVariables(step, "capture", kind != domain.RunScenario)
	return nil
}

func bindBackupVariables(step *lockedStep, operation string, cleanupOnSuccess bool) {
	metadata := step.Backup
	step.Variables["clusterforge_backup_ref"] = step.BackupRef
	step.Variables["clusterforge_backup_marker"] = path.Join(step.BackupRef, ".captured")
	step.Variables["clusterforge_backup_operation"] = operation
	step.Variables["clusterforge_backup_cleanup_on_success"] = cleanupOnSuccess && (operation == "restore" || operation == "cleanup")
	step.Variables["clusterforge_backup_metadata"] = map[string]any{
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
		dependencies = append(dependencies, map[string]any{
			"component_id":       dependency.UpstreamComponentID,
			"release_id":         dependency.UpstreamReleaseID,
			"parameter_mappings": dependency.ParameterMappings,
		})
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
	p := r.platform
	release, err := p.store.GetComponentRelease(ctx, installation.ReleaseID)
	if err != nil {
		return fmt.Errorf("%w: rollback refused: installed release no longer exists", domain.ErrConflict)
	}
	digester, ok := p.runner.(digestRunner)
	if !ok {
		return fmt.Errorf("%w: rollback refused: runner cannot verify the captured Playbook hash", domain.ErrConflict)
	}
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
	// Saving a Draft replaces Action rows, so a metadata-only correction can
	// rotate the Action ID even though the captured install Playbook is still
	// byte-for-byte identical. Preserve the digest guard while allowing that
	// auditable edit to remain rollback-compatible.
	if len(candidates) == 0 {
		for _, action := range release.Actions {
			if action.Kind == domain.ActionInstall || action.Kind == domain.ActionConfigure || action.Kind == domain.ActionUpgrade {
				candidates = append(candidates, action)
			}
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
