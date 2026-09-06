package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func (r *LifecycleRecorder) markMutation(ctx context.Context, run domain.Run) error {
	return r.store.MarkScenarioMutation(ctx, run)
}

// beginStep returns the persisted step even when its receipt cannot be saved,
// so the executor can still attribute the failed stage to that same step.
func (r *LifecycleRecorder) beginStep(ctx context.Context, run domain.Run, locked lockedStep, result ansiblerunner.JobStepResult) (domain.RunStep, error) {
	if locked.Phase == "execute" || locked.MayMutate {
		if err := r.markMutation(ctx, run); err != nil {
			return domain.RunStep{}, err
		}
	}
	step := domain.RunStep{ID: newID("step"), RunID: run.ID, NodeID: locked.NodeID, Name: locked.Name, Status: domain.RunRunning, StartedAt: &result.StartedAt}
	if err := r.store.CreateRunStep(ctx, step); err != nil {
		return domain.RunStep{}, err
	}
	if locked.Phase == "execute" {
		if err := r.recordBoundaryReceipt(ctx, run, locked, "execute", "begin", result.StartedAt); err != nil {
			return step, err
		}
	}
	r.hub.Publish("run.updated", map[string]any{"runId": run.ID, "stepId": step.ID})
	return step, nil
}

func (r *LifecycleRecorder) completeStep(ctx context.Context, run domain.Run, locked lockedStep, parents []lockedStep, step domain.RunStep, result ansiblerunner.JobStepResult) (domain.RunStep, error) {
	step.Status, step.FinishedAt, step.Summary = domain.RunSucceeded, &result.FinishedAt, recapSummary(result.Hosts)
	if locked.Phase == "execute" {
		if err := r.recordBoundaryReceipt(ctx, run, locked, "execute", "end", result.StartedAt); err != nil {
			return step, err
		}
	}
	if locked.Phase == "post" {
		main := parentExecutionStep(parents, locked)
		if main == nil {
			return step, fmt.Errorf("post-check has no locked parent action")
		}
		if err := r.recordSuccessfulLifecycleStep(ctx, run, *main, result.FinishedAt); err != nil {
			return step, err
		}
		if err := r.recordBoundaryReceipt(ctx, run, *main, "post", "end", result.StartedAt); err != nil {
			return step, err
		}
	}
	if err := r.store.UpdateRunStep(ctx, step); err != nil {
		return step, err
	}
	r.hub.Publish("run.updated", map[string]any{"runId": run.ID, "stepId": step.ID})
	return step, nil
}

func (r *LifecycleRecorder) recordOutput(runID, stepID string, event ansiblerunner.LogEvent) error {
	id, err := r.store.AppendRunLog(context.Background(), domain.RunLog{RunID: runID, StepID: stepID, Stream: string(event.Stream), Message: event.Line, CreatedAt: event.Time})
	if err != nil {
		return err
	}
	r.hub.Publish("run.log", map[string]any{"runId": runID, "logId": id})
	return nil
}

func (r *LifecycleRecorder) recordEvent(ctx context.Context, runID, stepID string, event ansiblerunner.JobEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	id, err := r.store.AppendRunLog(ctx, domain.RunLog{RunID: runID, StepID: stepID, Stream: "event", Message: string(data), CreatedAt: time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("persist execution event: %w", err)
	}
	r.hub.Publish("run.log", map[string]any{"runId": runID, "logId": id})
	return nil
}

func (r *LifecycleRecorder) recordFailedSteps(stages []ansiblerunner.JobStepResult, active map[string]domain.RunStep) {
	for _, stage := range stages {
		if stage.Status == "succeeded" {
			continue
		}
		step, ok := active[stage.StepID]
		if !ok {
			continue
		}
		step.Status, step.Summary, step.FinishedAt = domain.RunFailed, stage.Error, &stage.FinishedAt
		if stage.Status == "cancelled" {
			step.Status = domain.RunCancelled
		}
		if err := r.store.UpdateRunStep(context.Background(), step); err != nil {
			log.Printf("record failed stage: %v", err)
		}
	}
}

func (p *LifecycleRecorder) recordActionReceipt(ctx context.Context, run domain.Run, step lockedStep, status string, started time.Time) error {
	if step.Backup == nil {
		return fmt.Errorf("execution is missing its recovery baseline")
	}
	return p.store.RecordActionExecution(ctx, store.ActionExecutionReceipt{RunID: run.ID, StepID: step.ID, EnvironmentID: run.EnvironmentID, ComponentID: step.ComponentID, ReleaseID: step.ReleaseID, ActionID: step.ActionID, SourceNodeID: step.SourceNodeID, Status: status, BackupRef: step.BackupRef, Backup: *step.Backup, StartedAt: started, UpdatedAt: time.Now().UTC()})
}

func (r *LifecycleRecorder) recordBoundaryReceipt(ctx context.Context, run domain.Run, step lockedStep, phase, boundary string, started time.Time) error {
	status, err := ansiblerunner.ActionBoundaryStatus(phase, boundary)
	if err != nil {
		return err
	}
	return r.recordActionReceipt(ctx, run, step, status, started)
}
