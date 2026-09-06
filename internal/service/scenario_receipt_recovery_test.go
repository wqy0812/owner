package service

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
)

func TestScenarioFailedUpgradeRequiresRecoveryVerificationBeforeNewUpgrade(t *testing.T) {
	for _, phase := range []string{"execute", "post"} {
		t.Run(phase, func(t *testing.T) {
			p, db, owner, first, testEnv, runner := scenarioExecutionFixture(t)
			ctx := context.Background()
			runScenarioProtocol(t, p, owner, first.ID, testEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
			first, err := p.releases.PublishScenario(ctx, owner, first.ID)
			if err != nil {
				t.Fatal(err)
			}
			formalEnv := copyScenarioTestEnvironment(t, p, testEnv, "receipt-recovery-formal")
			sourceRun := runScenarioProtocol(t, p, owner, first.ID, formalEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenario)
			original, err := db.GetEnvironmentComponentInstallationForNode(ctx, formalEnv.ID, "component-1", "runtime")
			if err != nil {
				t.Fatal(err)
			}
			clone := ScenarioCloneRequest{SourceRevisionID: first.ID, SourceRunID: sourceRun.ID}
			clonePlan, err := p.scenarios.PreviewClone(ctx, owner, first.ScenarioID, clone)
			if err != nil {
				t.Fatal(err)
			}
			clone.ExpectedPlanDigest = clonePlan.PlanDigest
			next, err := p.scenarios.CloneRevision(ctx, owner, first.ScenarioID, clone)
			if err != nil {
				t.Fatal(err)
			}
			release, err := db.GetComponentRelease(ctx, original.ReleaseID)
			if err != nil {
				t.Fatal(err)
			}
			release.ID, release.Version, release.ParentReleaseID = "evolved-v2", "2.0", original.ReleaseID
			release.PlaybookWorkspaceRoot, release.PlaybookTreeSHA256 = "", ""
			release.PlaybookFiles = nil
			release.Actions = []domain.ActionDefinition{{ID: "evolved-install", Name: "Install", Kind: domain.ActionInstall, HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}, {ID: "evolved-upgrade", Name: "Upgrade", Kind: domain.ActionUpgrade, HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, FromReleaseID: original.ReleaseID, ToReleaseID: release.ID}}
			if err := db.CreateComponentRelease(ctx, release); err != nil {
				t.Fatal(err)
			}
			testutil.Workspaces(t, db, runner.root, release.ID)
			next.Graph.Nodes[0].ReleaseID = release.ID
			next, err = p.scenarios.SaveGraph(ctx, owner, next.ID, next.Graph)
			if err != nil {
				t.Fatal(err)
			}
			input := ScenarioExecutionRequest{EnvironmentID: formalEnv.ID, ExecutionMode: domain.ScenarioExecutionUpgrade, IdempotencyKey: "failed-upgrade"}
			preview, err := p.execution.PreviewScenarioExecution(ctx, owner, next.ID, input, domain.RunScenarioTest)
			if err != nil || !preview.Ready {
				t.Fatalf("upgrade preview=%+v %v", preview, err)
			}
			for _, step := range preview.Steps {
				if step.Stage == "change" && step.Phase == phase {
					runner.failStage = step.ID
				}
			}
			if runner.failStage == "" {
				t.Fatalf("missing upgrade %s stage", phase)
			}
			input.ExpectedPlanDigest = preview.PlanDigest
			p.scheduler.mu.Lock()
			p.scheduler.workers[formalEnv.ID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
			p.scheduler.mu.Unlock()
			failed, err := p.execution.StartScenarioExecution(ctx, owner, next.ID, input, domain.RunScenarioTest)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.UpdateRunStatus(ctx, failed.ID, []domain.RunStatus{domain.RunQueued}, domain.RunRunning, "", time.Now().UTC()); err != nil {
				t.Fatalf("start status=%s approval=%+v transition=%v", failed.Status, failed.Approval, err)
			}
			failed.Status = domain.RunRunning
			p.executor.executeRun(failed)
			failed, err = db.GetRun(ctx, failed.ID)
			if err != nil || failed.Status != domain.RunFailed {
				t.Fatalf("failed upgrade=%+v %v", failed, err)
			}
			_, retryErr := p.execution.PreviewRetry(ctx, owner, failed.ID)
			if phase == "post" && retryErr != nil {
				t.Fatalf("completed upgrade body cannot continue its postcheck: %v", retryErr)
			}
			if phase == "execute" && retryErr == nil {
				t.Fatal("unsafe upgrade body retry accepted")
			}
			failedSnapshot, _ := json.Marshal(failed.InputSnapshot)
			receipt, err := db.LatestActionReceiptForNode(ctx, formalEnv.ID, original.ComponentID, "", original.NodeID)
			if err != nil {
				t.Fatal(err)
			}
			expectedStatus := "started"
			if phase == "post" {
				expectedStatus = "main_succeeded"
			}
			if receipt.Status != expectedStatus {
				t.Fatalf("failed receipt=%+v", receipt)
			}
			retryInput := input
			retryInput.IdempotencyKey = "after-failure"
			retryInput.ExpectedPlanDigest = ""
			blocked, err := p.execution.PreviewScenarioExecution(ctx, owner, next.ID, retryInput, domain.RunScenarioTest)
			if err != nil || blocked.Ready {
				t.Fatalf("partially changed environment accepted another upgrade: %+v %v", blocked, err)
			}
			// Simulate discovering target-version files after the failed command. The
			// source verification cannot assert a complete source baseline in that state.
			changed := original
			changed.ReleaseID = release.ID
			changed.InstallRunID = failed.ID
			changed.BackupRef = receipt.BackupRef
			if err := db.UpsertEnvironmentComponentInstallation(ctx, changed); err != nil {
				t.Fatal(err)
			}
			verifyInput := ScenarioExecutionRequest{EnvironmentID: formalEnv.ID, ExecutionMode: domain.ScenarioExecutionBaselineVerify, IdempotencyKey: "unrestored-baseline"}
			blocked, err = p.execution.PreviewScenarioExecution(ctx, owner, first.ID, verifyInput, domain.RunScenario)
			if err != nil || blocked.Ready {
				t.Fatalf("target installation counted as restored source: %+v %v", blocked, err)
			}
			// External recovery restores the exact original installation snapshot; it
			// does not rewrite the failed Run or its unfinished action receipt.
			if err := db.UpsertEnvironmentComponentInstallation(ctx, original); err != nil {
				t.Fatal(err)
			}
			runner.failStage = ""
			verified := runScenarioProtocol(t, p, owner, first.ID, formalEnv.ID, domain.ScenarioExecutionBaselineVerify, domain.RunScenario)
			baseline, err := db.GetScenarioInstallation(ctx, formalEnv.ID, first.ScenarioID)
			if err != nil || baseline.State != "complete" || baseline.TestOnly || baseline.RunID != sourceRun.ID {
				t.Fatalf("recovered baseline=%+v %v", baseline, err)
			}
			if verified.InputSnapshot["executionMode"] != "baseline_verify" {
				t.Fatalf("not baseline verification: %+v", verified)
			}
			recovered, err := db.IsActionReceiptRecovered(ctx, receipt)
			if err != nil || !recovered {
				t.Fatalf("exact receipt not covered: %v %v", recovered, err)
			}
			preserved, err := db.LatestActionReceiptForNode(ctx, formalEnv.ID, original.ComponentID, "", original.NodeID)
			if err != nil || !reflect.DeepEqual(receipt, preserved) {
				t.Fatalf("verification rewrote history: before=%+v after=%+v err=%v", receipt, preserved, err)
			}
			upgraded := runScenarioProtocol(t, p, owner, next.ID, formalEnv.ID, domain.ScenarioExecutionUpgrade, domain.RunScenarioTest)
			if upgraded.ID == failed.ID {
				t.Fatal("new upgrade reused failed Run")
			}
			old, err := db.GetRun(ctx, failed.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldSnapshot, _ := json.Marshal(old.InputSnapshot)
			if old.Status != domain.RunFailed || string(oldSnapshot) != string(failedSnapshot) {
				t.Fatal("new upgrade changed old failed Run")
			}
			var historicalStatus string
			if err := db.DB().QueryRow(`SELECT status FROM action_execution_receipts WHERE run_id=? AND step_id=?`, receipt.RunID, receipt.StepID).Scan(&historicalStatus); err != nil || historicalStatus != expectedStatus {
				t.Fatalf("old receipt fabricated verification: %s %v", historicalStatus, err)
			}
			installed, err := db.GetEnvironmentComponentInstallationForNode(ctx, formalEnv.ID, original.ComponentID, original.NodeID)
			if err != nil || installed.ReleaseID != release.ID || installed.Backup.Previous == nil || installed.Backup.Previous.BackupRef != original.BackupRef {
				t.Fatalf("recovery backup lineage lost: %+v %v", installed, err)
			}
			if !strings.Contains(string(oldSnapshot), original.BackupRef) {
				t.Fatal("failed upgrade no longer references its source recovery backup")
			}
		})
	}
}

func TestScenarioBaselineCannotRecoverUnverifiedNewNodeByCheckingOnlyOldNodes(t *testing.T) {
	p, db, owner, revision, env, runner := scenarioExecutionFixture(t)
	ctx := context.Background()
	runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	added := scenarioPlannerRelease(t, p, "introduced-service", "introduced-component", "", domain.ActionInstall)
	now := time.Now().UTC()
	failed := domain.Run{ID: "failed-added-component", Kind: domain.RunScenarioTest, Status: domain.RunFailed, RequestedBy: owner.ID, EnvironmentID: env.ID, EnvironmentRevisionID: env.Revision.ID, ScenarioRevisionID: revision.ID, InputSnapshot: map[string]any{}, CreatedAt: now, FinishedAt: &now}
	if err := db.CreateRun(ctx, failed, nil); err != nil {
		t.Fatal(err)
	}
	receipt := store.ActionExecutionReceipt{RunID: failed.ID, StepID: "introduced-install", EnvironmentID: env.ID, ComponentID: added.ComponentID, ReleaseID: added.ID, ActionID: added.ID + "-install", SourceNodeID: "introduced", Status: "started", BackupRef: "/backups/introduced", StartedAt: now, UpdatedAt: now}
	if err := db.RecordActionExecution(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	baseline.State = "partial"
	baseline.MutatingRunID = failed.ID
	if err := db.SaveScenarioInstallation(ctx, baseline, baseline.Generation); err != nil {
		t.Fatal(err)
	}
	before := len(runner.observed)
	input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionBaselineVerify, IdempotencyKey: "unverified-new-node"}
	preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenario)
	if err != nil || preview.Ready {
		t.Fatalf("unchecked new node accepted: %+v %v", preview, err)
	}
	details, _ := json.Marshal(preview)
	if !strings.Contains(string(details), "introduced") {
		t.Fatalf("blocked plan does not identify unresolved new node: %s", details)
	}
	if len(runner.observed) != before {
		t.Fatal("baseline preview invoked runner")
	}
	if recovered, err := db.IsActionReceiptRecovered(ctx, receipt); err != nil || recovered {
		t.Fatalf("unverified introduced component recovered: %v %v", recovered, err)
	}
}
