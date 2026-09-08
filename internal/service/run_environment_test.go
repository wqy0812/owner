package service

import (
	"context"
	"errors"
	"testing"

	"codex/platform-demo/internal/domain"
)

type runEnvironmentFixture struct {
	plannerStore
	current string
}

func (s runEnvironmentFixture) GetEnvironment(context.Context, string, bool) (domain.Environment, error) {
	return domain.Environment{CurrentRevisionID: s.current}, nil
}

func (s runEnvironmentFixture) GetEnvironmentRevision(_ context.Context, id string) (domain.EnvironmentRevision, error) {
	return domain.EnvironmentRevision{ID: id}, nil
}

func TestRunEnvironmentStillRejectsQueuedConfigurationDrift(t *testing.T) {
	run := domain.Run{Kind: domain.RunScenario, EnvironmentID: "environment", EnvironmentRevisionID: "locked"}
	for _, tc := range []struct {
		name, current string
		steps         []domain.RunPlanStep
		conflict      bool
	}{
		{"unchanged", "locked", []domain.RunPlanStep{{ID: "install"}}, false},
		{"changed", "new-revision", []domain.RunPlanStep{{ID: "install"}}, true},
		{"mixed frozen and new actions", "new-revision", []domain.RunPlanStep{{SourceParametersFrozen: true}, {ID: "install"}}, true},
		{"frozen source verification", "new-revision", []domain.RunPlanStep{{SourceParametersFrozen: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder := &PlanBuilder{store: runEnvironmentFixture{current: tc.current}}
			err := builder.verifyRunEnvironment(context.Background(), run, domain.RunExecutionPlan{Steps: tc.steps})
			if errors.Is(err, domain.ErrConflict) != tc.conflict || (!tc.conflict && err != nil) {
				t.Fatalf("configuration drift guard: %v", err)
			}
		})
	}
}
