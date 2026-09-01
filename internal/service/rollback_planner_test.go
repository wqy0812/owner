package service

import (
	"context"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestRetryBackupRebindsRollbackAfterRemainingCaptureSteps(t *testing.T) {
	ctx := context.Background()
	platform, database := readinessTestPlatform(t)
	now := time.Now().UTC()
	parent := domain.ComponentRelease{
		ID: "release-retry-parent", ComponentID: "component-1", LineID: "line-retry", LineName: "Retry",
		Version: "1.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: now, ReleasedAt: &now,
	}
	target := domain.ComponentRelease{
		ID: "release-retry-target", ComponentID: "component-1", LineID: parent.LineID, LineName: parent.LineName,
		ParentReleaseID: parent.ID, TemplateSourceReleaseID: parent.ID,
		Version: "2.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityCompatible,
		RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: now.Add(time.Second),
	}
	if err := database.CreateComponentRelease(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateComponentRelease(ctx, target); err != nil {
		t.Fatal(err)
	}
	oldCapturedAt := now.Add(-time.Minute)
	oldBackup := &domain.BackupMetadata{EnvironmentID: "environment-1", ComponentID: "component-1", ReleaseID: target.ID, ActionID: "upgrade", InstallRunID: "run-source", CapturedAt: oldCapturedAt, PlaybookSHA256: "upgrade-digest"}
	plan := lockedPlan{Steps: []lockedStep{
		{ComponentID: "component-1", ReleaseID: target.ID, ActionID: "upgrade", Action: domain.ActionUpgrade, PlaybookDigest: "upgrade-digest", Variables: map[string]any{}},
		{ComponentID: "component-1", ReleaseID: target.ID, ActionID: "rollback", Action: domain.ActionRollback, FromReleaseID: target.ID, ToReleaseID: parent.ID, PlaybookDigest: "rollback-digest", BackupRef: "/old/ref", Backup: oldBackup, Variables: map[string]any{}},
	}}
	if err := platform.rebindRetryBackupPlan(ctx, "environment-1", "run-retry", domain.RunComponentTest, now, &plan); err != nil {
		t.Fatal(err)
	}
	upgrade, rollback := plan.Steps[0], plan.Steps[1]
	if upgrade.Backup == nil || rollback.Backup == nil || rollback.BackupRef != upgrade.BackupRef || rollback.Backup.InstallRunID != "run-retry" {
		t.Fatalf("retry backup mismatch upgrade=%+v rollback=%+v", upgrade.Backup, rollback.Backup)
	}
	metadata, ok := rollback.Variables["clusterforge_backup_metadata"].(map[string]any)
	if !ok || metadata["install_run_id"] != "run-retry" || rollback.Variables["clusterforge_backup_operation"] != "restore" {
		t.Fatalf("retry rollback variables=%#v", rollback.Variables)
	}
}

func TestRetryBackupPreservesInstalledBaselineWithoutRemainingCapture(t *testing.T) {
	now := time.Now().UTC()
	backup := &domain.BackupMetadata{EnvironmentID: "environment-1", ComponentID: "component-1", ReleaseID: "release-target", ActionID: "upgrade", InstallRunID: "run-source", CapturedAt: now.Add(-time.Minute), PlaybookSHA256: "upgrade-digest"}
	plan := lockedPlan{Steps: []lockedStep{
		{ComponentID: "component-1", ReleaseID: "release-target", Action: domain.ActionVerify, Variables: map[string]any{}},
		{ComponentID: "component-1", ReleaseID: "release-target", Action: domain.ActionRollback, FromReleaseID: "release-target", ToReleaseID: "release-parent", BackupRef: "/source/ref", Backup: backup, Variables: map[string]any{}},
	}}
	planner := &RollbackPlanner{}
	if err := planner.rebindRetryBackupPlan(context.Background(), "environment-1", "run-retry", domain.RunComponentTest, now, &plan); err != nil {
		t.Fatal(err)
	}
	rollback := plan.Steps[1]
	if rollback.BackupRef != "/source/ref" || rollback.Backup == nil || rollback.Backup.InstallRunID != "run-source" {
		t.Fatalf("source backup was replaced: ref=%s metadata=%+v", rollback.BackupRef, rollback.Backup)
	}
}

func TestUpgradeAndRollbackRemainNonRetrySafe(t *testing.T) {
	platform, _ := readinessTestPlatform(t)
	component := domain.Component{ID: "component-1", Name: "component"}
	release := domain.ComponentRelease{ID: "release-1", Version: "1.0.0"}
	for _, kind := range []domain.ActionKind{domain.ActionUpgrade, domain.ActionRollback} {
		step, err := platform.lockAction(component, "node", release, domain.ActionDefinition{ID: "action", Kind: kind, Playbook: "action.yml", Idempotent: true}, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if step.RetrySafe {
			t.Fatalf("%s unexpectedly became retry-safe", kind)
		}
	}
}
