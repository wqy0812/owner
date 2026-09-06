package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
)

type scenarioProtocolRunner struct {
	root      string
	failStage string
	observed  []string
}

func (r *scenarioProtocolRunner) Digest(path string) (string, string, error) {
	return (&ansible.Runner{AllowedRoot: r.root}).Digest(path)
}
func (r *scenarioProtocolRunner) DigestPlan(paths []string) (map[string]string, string, error) {
	return (&ansible.Runner{AllowedRoot: r.root}).DigestPlan(paths)
}
func (r *scenarioProtocolRunner) RunJob(ctx context.Context, request ansible.JobRequest) (ansible.JobResult, error) {
	result := ansible.JobResult{}
	for _, step := range request.Plan.Steps {
		r.observed = append(r.observed, step.ID)
		now := time.Now().UTC()
		item := ansible.JobStepResult{StepID: step.ID, Status: "running", StartedAt: now, FinishedAt: now, Hosts: map[string]ansible.HostRecap{"test": {OK: 1}}}
		if err := request.OnBoundary(ctx, "begin", step, item); err != nil {
			return result, err
		}
		if step.ID == r.failStage {
			item.Status = "failed"
			item.Error = "injected business failure"
			result.Steps = append(result.Steps, item)
			return result, errors.New(item.Error)
		}
		item.Status = "succeeded"
		if err := request.OnBoundary(ctx, "end", step, item); err != nil {
			return result, err
		}
		result.Steps = append(result.Steps, item)
	}
	return result, nil
}

func scenarioExecutionFixture(t *testing.T) (*Platform, *store.Store, domain.User, domain.ScenarioRevision, domain.Environment, *scenarioProtocolRunner) {
	t.Helper()
	ctx := context.Background()
	p, db := readinessTestPlatform(t)
	t.Cleanup(p.Close)
	root := t.TempDir()
	p.catalog.workspace.root = root
	owner := domain.User{ID: "scenario-execution-owner", Name: "Scenario", Role: domain.RoleScenarioOwner, CreatedAt: time.Now().UTC()}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	release := domain.ComponentRelease{PlaybookWorkspaceRoot: "managed/component-1/scenario-line/scenario-release/", ID: "scenario-release", ComponentID: "component-1", Version: "1.0", LineID: "scenario-line", LineName: "Scenario", Status: domain.ReleaseReleased, ReleasedAt: &now, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, Actions: []domain.ActionDefinition{{ID: "scenario-install", Kind: domain.ActionInstall, Name: "Install", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}}}
	if err := db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, root, release.ID)
	sc, err := p.scenarios.Create(ctx, owner, domain.Scenario{Name: "Lifecycle", Slug: "lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := p.scenarios.SaveGraph(ctx, owner, sc.CurrentRevisionID, domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime", Name: "Runtime", ReleaseID: release.ID, Action: domain.ActionInstall, HostGroup: "test_nodes", ParameterValues: map[string]any{}}}, Edges: []domain.ScenarioEdge{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.scenarios.SaveAcceptance(ctx, owner, rev.ID, ScenarioAcceptanceInput{ExpectedRevisionDigest: domain.ScenarioRevisionSpecDigest(rev), Jobs: []domain.ScenarioAcceptanceJob{{ID: "business", Name: "Business", Purpose: "Verify application", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.scenarios.SaveAcceptanceFile(ctx, owner, rev.ID, "tasks/acceptance/business.yml", []byte(testutil.Playbook), acceptanceExpectation(t, p, owner, rev.ID, "tasks/acceptance/business.yml"))
	if err != nil {
		t.Fatal(err)
	}
	rev, err = db.GetScenarioRevision(ctx, rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	env := domain.Environment{ID: "scenario-env", Name: "Scenario env", OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	envRev := domain.EnvironmentRevision{ID: "scenario-env-r1", EnvironmentID: env.ID, Revision: 1, Facts: completeServiceTestFacts(), Inventory: []byte(`{"groups":{"test_nodes":["node"]},"hosts":[{"name":"node","address":"127.0.0.1","port":22,"user":"test","groups":["test_nodes"]}]}`), CreatedAt: now}
	if err := db.CreateEnvironment(ctx, env, envRev); err != nil {
		t.Fatal(err)
	}
	env, err = db.GetEnvironment(ctx, env.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	runner := &scenarioProtocolRunner{root: root}
	setTestRunner(t, p, runner)
	return p, db, owner, rev, env, runner
}

func TestScenarioExecutionAcceptanceControlsSuccessAndPartialState(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "business_failure"}[fail], func(t *testing.T) {
			p, db, owner, rev, env, runner := scenarioExecutionFixture(t)
			ctx := context.Background()
			if fail {
				runner.failStage = "acceptance-business"
			}
			input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionInstall, IdempotencyKey: "submit"}
			preview, err := p.execution.PreviewScenarioExecution(ctx, owner, rev.ID, input, domain.RunScenarioTest)
			if err != nil || !preview.Ready {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			if len(preview.Steps) != 5 || preview.Steps[3].Stage != "target_verify" || preview.Steps[4].SourceType != "scenario_acceptance" {
				t.Fatalf("incomplete plan=%+v", preview.Steps)
			}
			input.ExpectedPlanDigest = preview.PlanDigest
			// Keep the worker dormant so the test can inspect the durable queue and
			// execute exactly one protocol without polling arbitrary timing.
			p.scheduler.mu.Lock()
			p.scheduler.workers[env.ID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
			p.scheduler.mu.Unlock()
			run, err := p.execution.StartScenarioExecution(ctx, owner, rev.ID, input, domain.RunScenarioTest)
			if err != nil {
				t.Fatal(err)
			}
			repeated, err := p.execution.StartScenarioExecution(ctx, owner, rev.ID, input, domain.RunScenarioTest)
			if err != nil || repeated.ID != run.ID {
				t.Fatalf("idempotency %s %v", repeated.ID, err)
			}
			if err := db.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{domain.RunQueued}, domain.RunRunning, "", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			run.Status = domain.RunRunning
			p.executor.executeRun(run)
			finished, err := db.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := db.GetScenarioInstallation(ctx, env.ID, rev.ScenarioID)
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if finished.Status != domain.RunFailed || baseline.State != "partial" {
					t.Fatalf("failure run=%s baseline=%+v", finished.Status, baseline)
				}
				if _, err := db.ScenarioTestEvidence(ctx, rev.ID); err == nil {
					t.Fatal("failed acceptance produced evidence")
				}
			} else {
				if finished.Status != domain.RunSucceeded || baseline.State != "test" || !baseline.TestOnly {
					t.Fatalf("success run=%s error=%s baseline=%+v", finished.Status, finished.Error, baseline)
				}
				evidence, err := db.ScenarioTestEvidence(ctx, rev.ID)
				if err != nil || evidence["install"] != run.ID {
					t.Fatalf("evidence=%v err=%v", evidence, err)
				}
			}
		})
	}
}

func TestScenarioOperationOrderingRejectsAmbiguityAndCycles(t *testing.T) {
	nodes := map[string]bool{"old": true, "new": true, "unchanged": true}
	actions := map[string]lockedStep{"old": {}, "new": {}}
	if _, err := uniqueScenarioOperationOrder(nodes, map[string]map[string]bool{}, actions); err == nil {
		t.Fatal("ambiguous ordering accepted")
	}
	order, err := uniqueScenarioOperationOrder(nodes, map[string]map[string]bool{"new": {"unchanged": true}, "unchanged": {"old": true}}, actions)
	if err != nil || len(order) != 3 || order[0] != "new" || order[2] != "old" {
		t.Fatalf("order=%v err=%v", order, err)
	}
	if _, err := uniqueScenarioOperationOrder(nodes, map[string]map[string]bool{"new": {"old": true}, "old": {"new": true}}, actions); err == nil {
		t.Fatal("cycle accepted")
	}
}

func runScenarioProtocol(t *testing.T, p *Platform, owner domain.User, revisionID, environmentID string, mode domain.ScenarioExecutionMode, kind domain.RunKind) domain.Run {
	t.Helper()
	ctx := context.Background()
	input := ScenarioExecutionRequest{EnvironmentID: environmentID, ExecutionMode: mode, IdempotencyKey: newID("submission")}
	preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revisionID, input, kind)
	if err != nil || !preview.Ready {
		t.Fatalf("%s preview=%+v err=%v", mode, preview, err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	p.scheduler.mu.Lock()
	p.scheduler.workers[environmentID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
	p.scheduler.mu.Unlock()
	run, err := p.execution.StartScenarioExecution(ctx, owner, revisionID, input, kind)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.RunQueued {
		t.Fatalf("expected non-destructive test job, got %s", run.Status)
	}
	if err = testDatabase(p).UpdateRunStatus(ctx, run.ID, []domain.RunStatus{domain.RunQueued}, domain.RunRunning, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	run.Status = domain.RunRunning
	p.executor.executeRun(run)
	run, err = testDatabase(p).GetRun(ctx, run.ID)
	if err != nil || run.Status != domain.RunSucceeded {
		t.Fatalf("%s run status=%s error=%s err=%v", mode, run.Status, run.Error, err)
	}
	return run
}

func copyScenarioTestEnvironment(t *testing.T, p *Platform, source domain.Environment, id string) domain.Environment {
	t.Helper()
	ctx := context.Background()
	environment := source
	environment.ID = id
	environment.Name = id
	environment.CurrentRevisionID = ""
	revision := *source.Revision
	revision.ID = id + "-r1"
	revision.EnvironmentID = id
	if err := testDatabase(p).CreateEnvironment(ctx, environment, revision); err != nil {
		t.Fatal(err)
	}
	environment, err := testDatabase(p).GetEnvironment(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	return environment
}

func TestScenarioLifecycleRequiresBothTestsAndKeepsForkIndependent(t *testing.T) {
	p, db, owner, first, env, runner := scenarioExecutionFixture(t)
	ctx := context.Background()
	runScenarioProtocol(t, p, owner, first.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	first, err := p.releases.PublishScenario(ctx, owner, first.ID)
	if err != nil {
		t.Fatalf("initial publication: %v", err)
	}
	formalEnv := copyScenarioTestEnvironment(t, p, env, "formal-env")
	formal := runScenarioProtocol(t, p, owner, first.ID, formalEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenario)
	cloneInput := ScenarioCloneRequest{SourceRevisionID: first.ID, SourceRunID: formal.ID}
	clonePlan, err := p.scenarios.PreviewClone(ctx, owner, first.ScenarioID, cloneInput)
	if err != nil {
		t.Fatal(err)
	}
	cloneInput.ExpectedPlanDigest = clonePlan.PlanDigest
	next, err := p.scenarios.CloneRevision(ctx, owner, first.ScenarioID, cloneInput)
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.GetComponentRelease(ctx, first.Graph.Nodes[0].ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	release := old
	release.ID = "scenario-release-v2"
	release.Version = "2.0"
	release.ParentReleaseID = old.ID
	release.Actions = []domain.ActionDefinition{{ID: "v2-install", Kind: domain.ActionInstall, Name: "Install", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}, {ID: "v2-upgrade", Kind: domain.ActionUpgrade, Name: "Upgrade", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, FromReleaseID: old.ID, ToReleaseID: release.ID}}
	release.PlaybookFiles = nil
	release.PlaybookWorkspaceRoot = ""
	release.PlaybookTreeSHA256 = ""
	if err := db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, runner.root, release.ID)
	graph := next.Graph
	graph.Nodes = append([]domain.ScenarioNode(nil), graph.Nodes...)
	graph.Nodes[0].ReleaseID = release.ID
	next, err = p.scenarios.SaveGraph(ctx, owner, next.ID, graph)
	if err != nil {
		t.Fatal(err)
	}
	upgrade := runScenarioProtocol(t, p, owner, next.ID, formalEnv.ID, domain.ScenarioExecutionUpgrade, domain.RunScenarioTest)
	if _, err = p.releases.PublishScenario(ctx, owner, next.ID); err == nil {
		t.Fatal("published without installation test")
	}
	baseline, err := db.GetScenarioInstallation(ctx, formalEnv.ID, next.ScenarioID)
	if err != nil || !baseline.TestOnly || baseline.RunID != upgrade.ID {
		t.Fatalf("upgrade test identity=%+v err=%v", baseline, err)
	}
	cleanEnv := copyScenarioTestEnvironment(t, p, env, "install-v2-env")
	runScenarioProtocol(t, p, owner, next.ID, cleanEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	next, err = p.releases.PublishScenario(ctx, owner, next.ID)
	if err != nil {
		t.Fatalf("dual-test publication: %v", err)
	}
	input := ScenarioForkRequest{SourceRevisionID: next.ID, Name: "Independent branch", Slug: "independent-branch"}
	forkPlan, err := p.scenarios.PreviewFork(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedPlanDigest = forkPlan.PlanDigest
	fork, err := p.scenarios.Fork(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	draft := fork.Revisions[0]
	if fork.ID == next.ScenarioID || fork.ForkedFromRevisionID != next.ID || draft.SourceRevisionID != "" || draft.SourceRunID != "" || draft.Status != domain.RevisionDraft || draft.Revision != 1 {
		t.Fatalf("invalid fork=%+v", fork)
	}
	if _, err := db.ScenarioTestEvidence(ctx, draft.ID); err == nil {
		t.Fatal("fork inherited test evidence")
	}
	if _, err := db.GetScenarioInstallation(ctx, formalEnv.ID, fork.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("fork inherited baseline: %v", err)
	}
}
