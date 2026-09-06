package ansible

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	if len(source.Manifest.GeneratedEntries) > 0 {
		clean, err := os.MkdirTemp("", "cf-source-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(clean)
		if err := copyRegularTree(source.Path, clean); err != nil {
			return nil, err
		}
		for generated, entry := range source.Manifest.GeneratedEntries {
			parts := strings.Split(generated, "/")
			if len(parts) != 4 || parts[0] != "roles" || parts[2] != "tasks" || parts[3] != roleEntrypoint(entry) || !strings.Contains(entry, "/") {
				return nil, fmt.Errorf("invalid generated role entry")
			}
			if err := os.Remove(filepath.Join(clean, generated)); err != nil {
				return nil, err
			}
		}
		runner.AllowedRoot = clean
	}
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
	return recoveryStages(map[string]any{"operation": "retry", "plan": plan, "results": results})
}

// ContinuationSteps refreshes acknowledged upstream checks without repeating
// successful mutations. The native job uses this same recovery implementation.
func ContinuationSteps(plan JobPlan, start int) ([]JobStep, error) {
	return recoveryStages(map[string]any{"operation": "continuation", "plan": plan, "start": start})
}
