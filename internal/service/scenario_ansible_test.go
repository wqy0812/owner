package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

// This integration test uses only local Ansible connections and temporary
// resources. Component fixtures are synthetic; no cluster is provisioned.
func TestScenarioBusinessAcceptanceRealAnsible(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("set CLUSTERFORGE_JOB_TEST_ANSIBLE for isolated local execution")
	}
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("cleanup_failure_%t", failure), func(t *testing.T) {
			p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
			ctx := context.Background()
			resource := filepath.Join(t.TempDir(), "temporary-business-resource")
			first := revision.AcceptanceJobs[0]
			first.MayMutate = true
			second := first
			second.ID, second.Name, second.MayMutate = "after-cleanup", "After cleanup", false
			if _, err := p.scenarios.SaveAcceptance(ctx, owner, revision.ID, ScenarioAcceptanceInput{ExpectedRevisionDigest: domain.ScenarioRevisionSpecDigest(revision), Jobs: []domain.ScenarioAcceptanceJob{first, second}}); err != nil {
				t.Fatal(err)
			}
			cleanup := "- name: Clean successful business resource\n  file:\n    path: " + resource + "\n    state: absent\n"
			if failure {
				cleanup = "- name: Reject incomplete cleanup\n  fail:\n    msg: deliberate cleanup failure\n"
			}
			content := "- name: Create temporary business resource\n  copy:\n    dest: " + resource + "\n    content: scenario acceptance\n" + cleanup
			if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, "tasks/acceptance/business.yml", []byte(content), acceptanceExpectation(t, p, owner, revision.ID, "tasks/acceptance/business.yml")); err != nil {
				t.Fatal(err)
			}
			after := "- name: Check successful cleanup\n  stat:\n    path: " + resource + "\n  register: cf_local_resource\n- assert:\n    that: not cf_local_resource.stat.exists\n"
			if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, "tasks/acceptance/after-cleanup.yml", []byte(after), acceptanceExpectation(t, p, owner, revision.ID, "tasks/acceptance/after-cleanup.yml")); err != nil {
				t.Fatal(err)
			}
			setTestRunner(t, p, &ansible.Runner{AllowedRoot: p.catalog.workspace.root, Binary: binary})
			input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionInstall, IdempotencyKey: "real-local-acceptance"}
			preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
			if err != nil || !preview.Ready {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			input.ExpectedPlanDigest = preview.PlanDigest
			p.scheduler.mu.Lock()
			p.scheduler.workers[env.ID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
			p.scheduler.mu.Unlock()
			run, err := p.execution.StartScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
			if err != nil {
				t.Fatal(err)
			}
			if err = db.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{domain.RunQueued}, domain.RunRunning, "", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			run.Status = domain.RunRunning
			p.executor.executeRun(run)
			run, err = db.GetRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if failure {
				if run.Status != domain.RunFailed {
					t.Fatalf("cleanup failure status=%s error=%s", run.Status, run.Error)
				}
				if _, err = os.Stat(resource); err != nil {
					t.Fatalf("failed state was not preserved: %v; run error %s", err, run.Error)
				}
				for _, step := range run.Steps {
					if step.NodeID == "acceptance:after-cleanup" {
						t.Fatal("later business job executed after failure")
					}
				}
				baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
				if err != nil || baseline.State != "partial" {
					t.Fatalf("baseline=%+v err=%v", baseline, err)
				}
			} else {
				if run.Status != domain.RunSucceeded {
					t.Fatalf("local Ansible status=%s error=%s", run.Status, run.Error)
				}
				if _, err = os.Stat(resource); !os.IsNotExist(err) {
					t.Fatalf("resource not cleaned: %v", err)
				}
			}
		})
	}
}
