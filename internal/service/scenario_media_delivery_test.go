package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
)

type scenarioOrderedMediaRunner struct {
	*scenarioProtocolRunner
	events     *[]string
	failSource bool
}

func (r *scenarioOrderedMediaRunner) RunJob(ctx context.Context, request ansible.JobRequest) (ansible.JobResult, error) {
	result := ansible.JobResult{}
	if request.StagePreparationTimeout != 30*time.Minute {
		return result, errors.New("scenario media preparation timeout is not isolated from action timeout")
	}
	for _, stage := range request.Plan.Steps {
		raw := request.Plan.Metadata["steps"].([]any)
		label := ""
		for _, item := range raw {
			step := item.(map[string]any)
			if step["id"] == stage.ID {
				label = step["stage"].(string) + ":" + stage.Phase
			}
		}
		now := time.Now().UTC()
		stepResult := ansible.JobStepResult{StepID: stage.ID, Status: "running", StartedAt: now, FinishedAt: now, Hosts: map[string]ansible.HostRecap{"test": {OK: 1}}}
		if err := request.OnBoundary(ctx, "begin", stage, stepResult); err != nil {
			stepResult.Status = "failed"
			stepResult.Error = err.Error()
			result.Steps = append(result.Steps, stepResult)
			return result, err
		}
		*r.events = append(*r.events, label+":begin")
		if r.failSource && strings.HasPrefix(label, "source_verify:") {
			stepResult.Status = "failed"
			stepResult.Error = "source validation failed"
			result.Steps = append(result.Steps, stepResult)
			return result, errors.New(stepResult.Error)
		}
		stepResult.Status = "succeeded"
		if err := request.OnBoundary(ctx, "end", stage, stepResult); err != nil {
			return result, err
		}
		*r.events = append(*r.events, label+":end")
		result.Steps = append(result.Steps, stepResult)
	}
	return result, nil
}

type scenarioOrderedImageDelivery struct{ before func(string) error }

func (s scenarioOrderedImageDelivery) Probe(context.Context, ImageLocation, ImageDigest) error {
	return ErrDeliveryTargetMissing
}
func (s scenarioOrderedImageDelivery) Transfer(_ context.Context, _ ImageTransfer) error {
	return s.before("image")
}

type scenarioOrderedArtifactDelivery struct{ before func(string) error }

func (s scenarioOrderedArtifactDelivery) Probe(context.Context, ArtifactLocation, ArtifactIdentity) error {
	return ErrDeliveryTargetMissing
}
func (s scenarioOrderedArtifactDelivery) Transfer(_ context.Context, _ ArtifactTransfer) error {
	return s.before("artifact")
}

func scenarioMediaUpgradeFixture(t *testing.T) (*Platform, *store.Store, domain.User, domain.ScenarioRevision, domain.Environment, *scenarioProtocolRunner) {
	t.Helper()
	p, db, owner, first, env, runner := scenarioExecutionFixture(t)
	ctx := context.Background()
	runScenarioProtocol(t, p, owner, first.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	first, err := p.releases.PublishScenario(ctx, owner, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	formalEnv := copyScenarioTestEnvironment(t, p, env, "media-formal")
	formal := runScenarioProtocol(t, p, owner, first.ID, formalEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenario)
	input := ScenarioCloneRequest{SourceRevisionID: first.ID, SourceRunID: formal.ID}
	preview, err := p.scenarios.PreviewClone(ctx, owner, first.ScenarioID, input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	next, err := p.scenarios.CloneRevision(ctx, owner, first.ScenarioID, input)
	if err != nil {
		t.Fatal(err)
	}
	release, err := db.GetComponentRelease(ctx, first.Graph.Nodes[0].ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	release.ParentReleaseID = release.ID
	release.ID, release.Version = "media-target", "2.0"
	release.PlaybookWorkspaceRoot, release.PlaybookTreeSHA256 = "", ""
	release.PlaybookFiles = nil
	release.Actions = []domain.ActionDefinition{{ID: "media-install", Kind: domain.ActionInstall, Name: "Install", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}, {ID: "media-upgrade", Kind: domain.ActionUpgrade, Name: "Upgrade", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, FromReleaseID: release.ParentReleaseID, ToReleaseID: release.ID}}
	if err := db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, runner.root, release.ID)
	next.Graph.Nodes[0].ReleaseID = release.ID
	next, err = p.scenarios.SaveGraph(ctx, owner, next.ID, next.Graph)
	if err != nil {
		t.Fatal(err)
	}
	return p, db, owner, next, formalEnv, runner
}

// This fixture injects the result of approved delivery decisions into a queued
// snapshot so the test isolates executor ordering from registry/FSS discovery.
func queueScenarioWithMedia(t *testing.T, p *Platform, owner domain.User, revisionID, environmentID string, mode domain.ScenarioExecutionMode, kind domain.RunKind) domain.Run {
	t.Helper()
	ctx := context.Background()
	input := ScenarioExecutionRequest{EnvironmentID: environmentID, ExecutionMode: mode, IdempotencyKey: newID("media-test")}
	preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revisionID, input, kind)
	if err != nil || !preview.Ready {
		t.Fatalf("preview=%+v %v", preview, err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	p.scheduler.mu.Lock()
	p.scheduler.workers[environmentID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
	p.scheduler.mu.Unlock()
	run, err := p.execution.StartScenarioExecution(ctx, owner, revisionID, input, kind)
	if err != nil {
		t.Fatal(err)
	}
	run.InputSnapshot["imageTransfers"] = []lockedImageTransfer{{RequirementID: "image", SourceRegistry: "source", TargetRegistry: "target", SourceDigest: "source/image@sha256:abc", TargetRef: "target/image:version", TargetDigest: "target/image@sha256:abc"}}
	run.InputSnapshot["artifactTransfers"] = []lockedArtifactTransfer{{RequirementID: "artifact", Alias: "installer", SourceURL: "http://source/package", TargetStation: "target", RelativePath: "package.tar", SHA256: "abc", SizeBytes: 1}}
	run.InputSnapshot["deliveryResults"] = []DeliveryResult{{RequirementID: "image", Mode: "transfer", Status: "pending"}, {RequirementID: "artifact", Mode: "transfer", Status: "pending"}}
	encoded, err := json.Marshal(run.InputSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testDatabase(p).DB().Exec(`UPDATE runs SET input_snapshot_json=? WHERE id=?`, string(encoded), run.ID); err != nil {
		t.Fatal(err)
	}
	run, err = testDatabase(p).GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestScenarioUpgradeDeliveryFollowsSourceVerificationAndPersistsPartial(t *testing.T) {
	for _, failure := range []string{"", "source", "image", "artifact"} {
		t.Run(map[string]string{"": "success", "source": "source_failure", "image": "image_failure", "artifact": "artifact_failure"}[failure], func(t *testing.T) {
			p, db, owner, revision, env, protocol := scenarioMediaUpgradeFixture(t)
			ctx := context.Background()
			events := []string{}
			runner := &scenarioOrderedMediaRunner{scenarioProtocolRunner: protocol, events: &events, failSource: failure == "source"}
			setTestRunner(t, p, runner)
			run := queueScenarioWithMedia(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionUpgrade, domain.RunScenarioTest)
			before := func(kind string) error {
				baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
				if err != nil || baseline.State != "partial" || baseline.MutatingRunID != run.ID {
					return errors.New("delivery started before durable mutation marker")
				}
				if len(events) == 0 || events[0] != "source_verify:check:begin" {
					return errors.New("media delivered before source validation began")
				}
				foundSource := false
				for _, event := range events {
					if event == "source_verify:check:end" {
						foundSource = true
					}
					if strings.HasPrefix(event, "change:") {
						return errors.New("component change started before delivery")
					}
				}
				if !foundSource {
					return errors.New("media delivered before source validation completed")
				}
				events = append(events, "delivery:"+kind)
				if failure == kind {
					return errors.New("injected " + kind + " transfer failure")
				}
				return nil
			}
			p.delivery.imageDelivery = scenarioOrderedImageDelivery{before: before}
			p.delivery.artifactDelivery = scenarioOrderedArtifactDelivery{before: before}
			if err := db.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{domain.RunQueued}, domain.RunRunning, "", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			run.Status = domain.RunRunning
			p.executor.executeRun(run)
			finished, err := db.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, wantState := domain.RunSucceeded, "test"
			if failure != "" {
				wantStatus, wantState = domain.RunFailed, "partial"
			}
			if failure == "source" {
				wantState = "unverified"
			}
			if finished.Status != wantStatus || baseline.State != wantState {
				t.Fatalf("run=%s error=%s baseline=%+v events=%v", finished.Status, finished.Error, baseline, events)
			}
			image, artifact, change := 0, 0, 0
			for _, event := range events {
				if event == "delivery:image" {
					image++
				}
				if event == "delivery:artifact" {
					artifact++
				}
				if strings.HasPrefix(event, "change:") {
					change++
				}
			}
			if failure == "source" && (image != 0 || artifact != 0) || failure == "image" && (image != 1 || artifact != 0) || failure == "artifact" && (image != 1 || artifact != 1) || failure == "" && (image != 1 || artifact != 1 || change == 0) {
				t.Fatalf("unexpected delivery order/count: %v", events)
			}
			if failure != "" && change != 0 {
				t.Fatalf("component changed after failure: %v", events)
			}
		})
	}
}

func TestScenarioBaselineVerificationNeverTransfersMedia(t *testing.T) {
	p, db, owner, revision, env, protocol := scenarioExecutionFixture(t)
	ctx := context.Background()
	runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	run := queueScenarioWithMedia(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionBaselineVerify, domain.RunScenario)
	events := []string{}
	setTestRunner(t, p, &scenarioOrderedMediaRunner{scenarioProtocolRunner: protocol, events: &events})
	before := func(kind string) error { events = append(events, "delivery:"+kind); return nil }
	p.delivery.imageDelivery = scenarioOrderedImageDelivery{before: before}
	p.delivery.artifactDelivery = scenarioOrderedArtifactDelivery{before: before}
	if err := db.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{domain.RunQueued}, domain.RunRunning, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run.Status = domain.RunRunning
	p.executor.executeRun(run)
	finished, err := db.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != domain.RunFailed || !strings.Contains(finished.Error, "基线复核不能执行新的媒体交付") || len(events) != 0 {
		t.Fatalf("baseline delivery=%s error=%s events=%v", finished.Status, finished.Error, events)
	}
	baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
	if err != nil || baseline.State == "partial" || !baseline.TestOnly {
		t.Fatalf("blocked media changed baseline identity: %+v %v", baseline, err)
	}
}
