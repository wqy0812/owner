package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

type scenarioCancellationRunner struct {
	*scenarioProtocolRunner
	cancelAt string
	onCancel func() error
}

func (r *scenarioCancellationRunner) RunJob(ctx context.Context, request ansible.JobRequest) (ansible.JobResult, error) {
	result := ansible.JobResult{}
	for _, stage := range request.Plan.Steps {
		r.observed = append(r.observed, stage.ID)
		now := time.Now().UTC()
		item := ansible.JobStepResult{StepID: stage.ID, Status: "running", StartedAt: now, FinishedAt: now, Hosts: map[string]ansible.HostRecap{"test": {OK: 1}}}
		if err := request.OnBoundary(ctx, "begin", stage, item); err != nil {
			return result, err
		}
		if stage.ID == r.cancelAt {
			if err := r.onCancel(); err != nil {
				return result, err
			}
			result.Canceled = true
			item.Status = "cancelled"
			item.Error = "cancelled by owner"
			result.Steps = append(result.Steps, item)
			return result, ctx.Err()
		}
		item.Status = "succeeded"
		if err := request.OnBoundary(ctx, "end", stage, item); err != nil {
			return result, err
		}
		result.Steps = append(result.Steps, item)
	}
	return result, nil
}

func TestScenarioCancellationPersistsMutationBeforeStopping(t *testing.T) {
	for _, mutating := range []bool{false, true} {
		t.Run(map[bool]string{false: "precheck", true: "main_action"}[mutating], func(t *testing.T) {
			p, db, owner, revision, env, protocol := scenarioExecutionFixture(t)
			ctx := context.Background()
			input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionInstall, IdempotencyKey: "cancel-boundary"}
			preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
			if err != nil || !preview.Ready {
				t.Fatalf("preview=%+v %v", preview, err)
			}
			index := 0
			if mutating {
				index = 1
			}
			runner := &scenarioCancellationRunner{scenarioProtocolRunner: protocol, cancelAt: preview.Steps[index].ID}
			setTestRunner(t, p, runner)
			input.ExpectedPlanDigest = preview.PlanDigest
			p.scheduler.mu.Lock()
			p.scheduler.workers[env.ID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
			p.scheduler.mu.Unlock()
			run, err := p.execution.StartScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
			if err != nil {
				t.Fatal(err)
			}
			runner.onCancel = func() error {
				baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
				if mutating && (err != nil || baseline.State != "partial" || baseline.MutatingRunID != run.ID) {
					return errors.New("mutation was not durable before runner cancellation")
				}
				if !mutating && !errors.Is(err, domain.ErrNotFound) {
					return errors.New("read-only precheck marked a clean environment as changed")
				}
				_, err = p.approvals.CancelRun(ctx, owner, run.ID)
				return err
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
			if finished.Status != domain.RunCancelled || len(protocol.observed) != index+1 {
				t.Fatalf("cancellation continued: status=%s error=%s observed=%v", finished.Status, finished.Error, protocol.observed)
			}
			if len(finished.Steps) != index+1 || finished.Steps[index].Status != domain.RunCancelled {
				t.Fatalf("cancelled step was lost: %+v", finished.Steps)
			}
			if _, err := db.ScenarioTestEvidence(ctx, revision.ID); err == nil {
				t.Fatal("cancellation created evidence")
			}
			if mutating {
				baseline, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID)
				if err != nil || baseline.State != "partial" || baseline.MutatingRunID != run.ID {
					t.Fatalf("cancelled partial state=%+v %v", baseline, err)
				}
				if _, err := p.execution.Retry(ctx, owner, run.ID, RunRetryRequest{}); err == nil {
					t.Fatal("partially changed scenario entered generic retry")
				}
			}
		})
	}
}

func TestScenarioConcurrentIdempotentSubmissionCreatesOneRun(t *testing.T) {
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionInstall, IdempotencyKey: "concurrent-submission"}
	preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
	if err != nil || !preview.Ready {
		t.Fatalf("preview=%+v %v", preview, err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	p.scheduler.mu.Lock()
	p.scheduler.workers[env.ID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
	p.scheduler.mu.Unlock()
	const count = 12
	type outcome struct {
		run domain.Run
		err error
	}
	results := make(chan outcome, count)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			run, err := p.execution.StartScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
			results <- outcome{run, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	id := ""
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if id == "" {
			id = result.run.ID
		}
		if result.run.ID != id {
			t.Fatalf("duplicate idempotent run %s != %s", result.run.ID, id)
		}
	}
	var runs, submissions int
	if err := db.DB().QueryRow(`SELECT count(*) FROM runs WHERE scenario_revision_id=?`, revision.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRow(`SELECT count(*) FROM scenario_execution_submissions WHERE user_id=? AND key=?`, owner.ID, input.IdempotencyKey).Scan(&submissions); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || submissions != 1 {
		t.Fatalf("duplicate persistence: runs=%d submissions=%d", runs, submissions)
	}
	changed := input
	changed.ExecutionMode = domain.ScenarioExecutionUpgrade
	if _, err := p.execution.StartScenarioExecution(ctx, owner, revision.ID, changed, domain.RunScenarioTest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("idempotency key reused for another payload: %v", err)
	}
}

type scenarioBuildCancellationRunner struct {
	*scenarioProtocolRunner
	onBuild func() error
}

func (r *scenarioBuildCancellationRunner) BuildJob(ctx context.Context, _ ansible.JobPlan) (*ansible.JobBundle, error) {
	if err := r.onBuild(); err != nil {
		return nil, err
	}
	return nil, ctx.Err()
}
func (r *scenarioBuildCancellationRunner) RunBundle(context.Context, *ansible.JobBundle, ansible.JobRequest) (ansible.JobResult, error) {
	return ansible.JobResult{}, errors.New("cancelled bundle must not execute")
}
func TestScenarioCancellationDuringBundleBuildKeepsCleanEnvironment(t *testing.T) {
	p, db, owner, revision, env, protocol := scenarioExecutionFixture(t)
	ctx := context.Background()
	runner := &scenarioBuildCancellationRunner{scenarioProtocolRunner: protocol}
	setTestRunner(t, p, runner)
	input := ScenarioExecutionRequest{EnvironmentID: env.ID, ExecutionMode: domain.ScenarioExecutionInstall, IdempotencyKey: "cancel-build"}
	preview, err := p.execution.PreviewScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
	if err != nil || !preview.Ready {
		t.Fatalf("preview=%+v %v", preview, err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	p.scheduler.mu.Lock()
	p.scheduler.workers[env.ID] = environmentWorkerState{token: 99, heartbeat: time.Now().UTC()}
	p.scheduler.mu.Unlock()
	run, err := p.execution.StartScenarioExecution(ctx, owner, revision.ID, input, domain.RunScenarioTest)
	if err != nil {
		t.Fatal(err)
	}
	invoked := false
	runner.onBuild = func() error {
		invoked = true
		_, err := p.approvals.CancelRun(ctx, owner, run.ID)
		return err
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
	if !invoked || finished.Status != domain.RunCancelled || len(finished.Steps) != 0 {
		t.Fatalf("bundle cancellation=%+v invoked=%v", finished, invoked)
	}
	if _, err := db.GetScenarioInstallation(ctx, env.ID, revision.ScenarioID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cancelled build created baseline: %v", err)
	}
}
