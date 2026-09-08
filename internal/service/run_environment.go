package service

import (
	"codex/platform-demo/internal/domain"
	"context"
	"fmt"
)

func (b *PlanBuilder) verifyRunEnvironment(ctx context.Context, run domain.Run, plan domain.RunExecutionPlan) error {
	if run.Kind == domain.RunEnvironmentRollback {
		return nil // The rollback planner validates its frozen installation boundary.
	}
	allFrozen := len(plan.Steps) > 0
	for _, step := range plan.Steps {
		if !step.SourceParametersFrozen && step.SourceType != "scenario_acceptance" {
			allFrozen = false
		}
	}
	if allFrozen {
		return nil // Source verification uses its frozen environment inputs.
	}
	env, err := b.store.GetEnvironment(ctx, run.EnvironmentID, true)
	if err != nil {
		return err
	}
	revision, err := b.store.GetEnvironmentRevision(ctx, run.EnvironmentRevisionID)
	if err != nil {
		return err
	}
	if env.CurrentRevisionID != revision.ID {
		return fmt.Errorf("%w: environment configuration changed after the job was locked", domain.ErrConflict)
	}
	return nil
}
