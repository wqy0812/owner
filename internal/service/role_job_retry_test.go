package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/testutil"
)

func TestScenarioRoleJobSafeContinuation(t *testing.T) { scenarioRoleContinuation(t, "") }

func TestScenarioRoleJobContinuationRealAnsible(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	scenarioRoleContinuation(t, binary)
}

func scenarioRoleContinuation(t *testing.T, binary string) {
	for _, phase := range []string{"pre", "execute", "post", "acceptance"} {
		t.Run(phase, func(t *testing.T) {
			p, db, owner, revision, env, runner := scenarioExecutionFixture(t)
			ctx := context.Background()
			allow := filepath.Join(t.TempDir(), "allow-stage")
			calls := filepath.Join(t.TempDir(), "install-calls")
			if binary != "" {
				release, err := db.GetComponentRelease(ctx, revision.Graph.Nodes[0].ReleaseID)
				if err != nil {
					t.Fatal(err)
				}
				install, _ := findAction(release, domain.ActionInstall)
				probe := fmt.Sprintf("- stat:\n    path: %s\n  register: cf_local_stage_probe\n- assert:\n    that: cf_local_stage_probe.stat.exists\n", allow)
				for _, action := range release.Actions {
					content := ""
					if action.Kind == domain.ActionInstall {
						content = fmt.Sprintf("- shell: echo install >> %s\n", calls)
					}
					if (phase == "pre" && action.ID == install.PreCheckActionID) || (phase == "post" && action.ID == install.PostCheckActionID) || (phase == "execute" && action.Kind == domain.ActionInstall) {
						content += probe
					}
					if content != "" {
						if err := os.WriteFile(filepath.Join(runner.root, action.Playbook), []byte(content), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				testutil.Workspaces(t, db, runner.root, release.ID)
				if phase == "acceptance" {
					if _, err := p.scenarios.SaveAcceptanceFile(ctx, owner, revision.ID, "tasks/acceptance/business.yml", []byte(probe), acceptanceExpectation(t, p, owner, revision.ID, "tasks/acceptance/business.yml")); err != nil {
						t.Fatal(err)
					}
					revision, err = db.GetScenarioRevision(ctx, revision.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
				setTestRunner(t, p, &ansible.Runner{AllowedRoot: runner.root, Binary: binary})
			}
			input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionInstall, IdempotencyKey: "safe-retry"}
			preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
			if err != nil || !preview.Ready {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			for _, step := range preview.Steps {
				if step.Phase == phase {
					runner.failStage = step.ID
					break
				}
			}
			if runner.failStage == "" {
				t.Fatal("missing selected phase")
			}
			p.scheduler.mu.Lock()
			p.scheduler.workers[env.ID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
			p.scheduler.mu.Unlock()
			input.ExpectedPlanDigest = preview.PlanDigest
			source, err := p.execution.StartScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
			if err != nil {
				t.Fatal(err)
			}
			execute := func(run domain.Run) domain.Run {
				t.Helper()
				if err := db.UpdateRunStatus(ctx, run.ID, []domain.RunStatus{domain.RunQueued}, domain.RunRunning, "", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				run.Status = domain.RunRunning
				p.executor.executeRun(run)
				run, err := db.GetRun(ctx, run.ID)
				if err != nil {
					t.Fatal(err)
				}
				if binary != "" && run.Status == domain.RunFailed {
					logs, _ := db.ListRunLogs(ctx, run.ID, 0, 3000)
					for _, item := range logs {
						if item.Stream == "stderr" || strings.Contains(item.Message, "fatal:") || strings.Contains(item.Message, "ERROR") || (item.Stream == "event" && strings.Contains(item.Message, `"status":"failed"`)) {
							t.Logf("%s", item.Message)
						}
					}
				}
				return run
			}
			source = execute(source)
			if source.Status != domain.RunFailed {
				t.Fatalf("source=%s %s", source.Status, source.Error)
			}
			snapshot := source.InputSnapshot
			original, err := mapToPlan(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			retry, err := p.execution.PreviewRetry(ctx, owner, source.ID)
			if phase == "execute" {
				if err == nil {
					t.Fatal("unverified mutating body retry allowed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if binary != "" {
				if err := os.WriteFile(allow, []byte("ready"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner.failStage = ""
			runner.observed = nil
			next, err := p.execution.Retry(ctx, owner, source.ID, RunRetryRequest{ExpectedPlanDigest: retry.PlanDigest})
			if err != nil {
				t.Fatal(err)
			}
			next = execute(next)
			if binary != "" {
				data, err := os.ReadFile(calls)
				if err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(string(data)) != "install" {
					t.Fatalf("install was repeated: %s", data)
				}
			}
			if next.Status != domain.RunSucceeded {
				t.Fatalf("retry=%s %s", next.Status, next.Error)
			}
			if binary != "" {
				var archive bytes.Buffer
				if err := p.execution.DownloadVerifiedRunJob(ctx, owner, next.ID, &archive); err != nil {
					t.Fatalf("verified full-job export: %v", err)
				}
				full, err := ansible.ReadJobArchive(bytes.NewReader(archive.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				defer full.Close()
				if len(full.Manifest.Plan.Steps) != len(original.Steps) || full.Manifest.Verification == nil || len(full.Manifest.Verification.RunIDs) != 2 {
					t.Fatal("export contains only the retry fragment or lacks the full evidence chain")
				}
				if _, exists := full.Manifest.Files["clusterforge-job"]; exists {
					t.Fatal("native delivery still requires a Go launcher")
				}
				if err := p.execution.DownloadVerifiedRunJob(ctx, owner, source.ID, &bytes.Buffer{}); err == nil {
					t.Fatal("failed Run was exported as a verified delivery")
				}
			}
			continuation, err := mapToPlan(next.InputSnapshot)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(continuation.ParentSteps[0].Backup, original.ParentSteps[0].Backup) {
				t.Fatal("original backup was replaced")
			}
			if phase != "pre" {
				for _, step := range continuation.Steps {
					if step.Phase == "execute" {
						t.Fatal("successful mutation was repeated")
					}
				}
			}
			for _, step := range next.Steps {
				if step.ExitCode != nil {
					t.Fatal("stage has a fabricated process exit code")
				}
			}
			unchanged, _ := db.GetRun(ctx, source.ID)
			if !reflect.DeepEqual(unchanged.InputSnapshot, snapshot) {
				t.Fatal("source history was modified")
			}
			state, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
			if err != nil || state.State != "test" || state.RunID != next.ID {
				t.Fatalf("final baseline=%+v %v", state, err)
			}
			if _, err := p.releases.PublishScenario(ctx, owner, revision.ID); err != nil {
				t.Fatalf("continuation evidence could not publish: %v", err)
			}
			if _, err := p.execution.PreviewRetry(ctx, owner, source.ID); err == nil {
				t.Fatal("old source branch was replayed")
			}
		})
	}
}
