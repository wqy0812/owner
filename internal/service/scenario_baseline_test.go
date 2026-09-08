package service

import (
	"codex/platform-demo/internal/testutil/runfixture"
	"context"
	"errors"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestScenarioBaselineVerificationDoesNotInferMissingBaselineFromRuns(t *testing.T) {
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	installed := runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	if _, err := db.DB().ExecContext(ctx, "DELETE FROM scenario_installations WHERE environment_id=? AND scenario_id=?", env.ID, revision.ScenarioID); err != nil {
		t.Fatal(err)
	}
	input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionBaselineVerify}
	preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenario)
	if err == nil && preview.Ready {
		t.Fatal("verification inferred a missing baseline from successful historical Runs")
	}
	if _, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("verification backfilled a baseline: %v", err)
	}
	if run, err := db.GetRun(ctx, installed.ID); err != nil || run.Status != domain.RunSucceeded {
		t.Fatalf("verification changed source evidence: run=%+v err=%v", run, err)
	}
}

func TestScenarioBaselineVerificationPreservesTestIdentityAndEvidence(t *testing.T) {
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	installed := runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	baseline.State = "unverified"
	baseline.UpdatedAt = time.Now().UTC()
	if err = db.SaveScenarioInstallation(ctx, baseline, baseline.Generation); err != nil {
		t.Fatal(err)
	}
	verified := runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionBaselineVerify, domain.RunScenario)
	baseline, err = db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
	if err != nil || baseline.State != "test" || !baseline.TestOnly || baseline.RunID != installed.ID {
		t.Fatalf("verification promoted test identity: %+v err=%v", baseline, err)
	}
	evidence, err := db.ScenarioTestEvidence(ctx, revision.ID)
	if err != nil || evidence["install"] != installed.ID {
		t.Fatalf("verification replaced test evidence: %+v err=%v", evidence, err)
	}
	if _, err = db.SuccessfulScenarioSourceRun(ctx, revision, verified.ID); err == nil {
		t.Fatal("baseline verification became formal source evidence")
	}
	for _, step := range runfixture.StepMaps(verified.Snapshot.Plan.Steps) {
		locked := step.(map[string]any)
		if locked["phase"] == "execute" || locked["stage"] == "source_verify" {
			t.Fatalf("baseline verification executes component changes: %+v", locked)
		}
	}
}

func TestScenarioSubmissionRechecksBaselineAndRejectsHostMigration(t *testing.T) {
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionBaselineVerify, IdempotencyKey: "baseline-drift"}
	preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenario)
	if err != nil || !preview.Ready {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	baseline.State = "unverified"
	baseline.UpdatedAt = time.Now().UTC()
	if err = db.SaveScenarioInstallation(ctx, baseline, baseline.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = p.execution.StartScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenario); err == nil {
		t.Fatal("submitted a drifted preview")
	}
	if sameScenarioHostInventory(env.Revision.Inventory, []byte(`{"hosts":[{"name":"node","address":"192.0.2.10","port":22,"user":"test","groups":["test_nodes"]}]}`)) {
		t.Fatal("host migration considered equivalent")
	}
	if sameScenarioHostInventory(env.Revision.Inventory, []byte(`{"hosts":[]}`)) {
		t.Fatal("scaling considered equivalent")
	}
}
