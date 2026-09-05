package ansible

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// RebuildJob compiles a new single job from an immutable exported role tree.
// It is used for standalone runs, retries and explicitly selected rollback.
func (r *Runner) RebuildJob(ctx context.Context, source *JobBundle, plan JobPlan) (*JobBundle, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Role == "" || step.TasksFrom == "" {
			return nil, fmt.Errorf("unsealed role reference")
		}
		step.Playbook = filepath.ToSlash(filepath.Join("roles", step.Role, "tasks", step.TasksFrom))
	}
	// Recovery roles are already included in the same snapshots. Seal their
	// portable paths exactly as the executed roles.
	for i := range plan.Recovery {
		step := &plan.Recovery[i]
		step.Playbook = filepath.ToSlash(filepath.Join("roles", step.Role, "tasks", step.TasksFrom))
	}
	runner := *r
	runner.AllowedRoot = source.Path
	return runner.BuildJob(ctx, plan)
}

func CloneJobPlan(plan JobPlan) JobPlan {
	data, _ := json.Marshal(plan)
	var result JobPlan
	_ = json.Unmarshal(data, &result)
	return result
}

// RetryStages preserves successful mutations, refreshes completed upstream
// postconditions, and restarts a failed executable only from its precheck.
func RetryStages(plan JobPlan, results []JobStepResult) ([]JobStep, error) {
	completed := map[string]bool{}
	for _, result := range results {
		completed[result.StepID] = result.Status == "succeeded"
	}
	start := len(plan.Steps)
	for i, step := range plan.Steps {
		if !completed[step.ID] {
			start = i
			break
		}
	}
	if start == len(plan.Steps) {
		return nil, fmt.Errorf("job has no incomplete stages")
	}
	step := plan.Steps[start]
	if step.Phase == "execute" {
		if !step.RetrySafe {
			return nil, fmt.Errorf("failed action is not declared safe to retry; inspect or roll back")
		}
		if start == 0 || plan.Steps[start-1].Phase != "pre" {
			return nil, fmt.Errorf("retry action has no precheck")
		}
		start--
	}
	return ContinuationSteps(plan, start), nil
}

// ContinuationSteps refreshes only completed dependency instances. A preceding
// action on the same instance is not an upstream dependency: its postcondition
// may intentionally have been changed by the action being resumed.
func ContinuationSteps(plan JobPlan, start int) []JobStep {
	pending := map[string]bool{}
	key := func(s JobStep) string { return s.ComponentID + "/" + s.NodeID }
	for _, step := range plan.Steps[start:] {
		pending[key(step)] = true
	}
	latest := map[string]int{}
	for i, step := range plan.Steps[:start] {
		if step.Phase == "post" || step.Phase == "check" {
			latest[key(step)] = i
		}
	}
	stages := []JobStep{}
	for i, done := range plan.Steps[:start] {
		if last, ok := latest[key(done)]; ok && last == i && !pending[key(done)] {
			if !strings.HasPrefix(done.ID, "refresh-") {
				done.ID = "refresh-" + done.ID
			}
			done.Phase = "check"
			stages = append(stages, done)
		}
	}
	return append(stages, plan.Steps[start:]...)
}
