package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"codex/platform-demo/internal/domain"
)

func (r *LifecycleRecorder) recordSuccessfulLifecycleStep(ctx context.Context, run domain.Run, step lockedStep, installedAt time.Time) error {
	p := r.platform
	switch step.Action {
	case domain.ActionInstall, domain.ActionConfigure, domain.ActionUpgrade:
		if step.Backup == nil || step.BackupRef == "" {
			return fmt.Errorf("%w: successful install action is missing its locked backup metadata", domain.ErrConflict)
		}
		return p.store.UpsertEnvironmentComponentInstallation(ctx, domain.EnvironmentComponentInstallation{
			EnvironmentID: run.EnvironmentID, ComponentID: step.ComponentID, ReleaseID: step.ReleaseID,
			InstallRunID: run.ID, BackupRef: step.BackupRef, Backup: *step.Backup,
			TestOnly: run.Kind != domain.RunScenario, InstalledAt: installedAt,
		})
	case domain.ActionRollback, domain.ActionUninstall:
		if step.Backup == nil {
			return fmt.Errorf("%w: successful rollback action is missing its locked backup metadata", domain.ErrConflict)
		}
		if hasLaterCleanupStep(run.InputSnapshot, step) {
			return nil
		}
		err := p.store.DeleteEnvironmentComponentInstallation(ctx, run.EnvironmentID, step.ComponentID, step.Backup.InstallRunID)
		if errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("%w: rollback backup_ref was replaced while the Run was active", domain.ErrConflict)
		}
		return err
	default:
		return nil
	}
}

func (r *LifecycleRecorder) finishRun(run domain.Run, status domain.RunStatus, cause error) {
	p := r.platform
	errText := ""
	if cause != nil {
		errText = Redact(cause.Error()).(string)
	}
	if err := p.store.UpdateRunStatus(context.Background(), run.ID, []domain.RunStatus{domain.RunRunning}, status, errText, time.Now().UTC()); err != nil {
		log.Printf("finishRun %s: update status to %s: %v", run.ID, status, err)
	}
	if run.Kind == domain.RunComponentTest && run.Action != domain.ActionRollback && status == domain.RunSucceeded {
		lockedDigest, _ := run.InputSnapshot["componentReleaseSpecDigest"].(string)
		current, currentErr := p.store.GetComponentRelease(context.Background(), run.ComponentReleaseID)
		if currentErr == nil && lockedDigest != "" && componentReleaseSpecDigest(current) == lockedDigest {
			p.hub.Publish("release.readiness_updated", map[string]any{"releaseId": run.ComponentReleaseID, "runId": run.ID})
		}
	}
	if run.Kind == domain.RunScenarioTest {
		if status != domain.RunSucceeded {
			if err := p.store.SetScenarioRevisionStatus(context.Background(), run.ScenarioRevisionID, []domain.RevisionStatus{domain.RevisionTesting}, domain.RevisionDraft, time.Now().UTC()); err != nil {
				log.Printf("finishRun %s: reset scenario revision to draft: %v", run.ID, err)
			}
		}
	}
	actor, _ := p.store.GetUser(context.Background(), run.RequestedBy)
	p.audit(context.Background(), actor, "run.finished", "run", run.ID, map[string]any{"status": status, "error": errText})
	p.hub.Publish("run.updated", map[string]any{"runId": run.ID, "status": status})
}

func componentReleaseSpecDigest(release domain.ComponentRelease) string {
	return domain.ComponentReleaseSpecDigest(release)
}

// ComponentReleaseSpecDigest exposes the canonical immutable-content digest to
// integration tooling that records externally produced validation evidence.
func ComponentReleaseSpecDigest(release domain.ComponentRelease) string {
	return componentReleaseSpecDigest(release)
}

func scenarioRevisionSpecDigest(revision domain.ScenarioRevision) string {
	return domain.ScenarioRevisionSpecDigest(revision)
}
