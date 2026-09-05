package ansible

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStagePreparationHasItsOwnBoundedDeadline(t *testing.T) {
	for _, cancelPreparation := range []bool{false, true} {
		bundle := compileUnitJob(t)
		ctx, cancel := context.WithCancel(context.Background())
		controller := jobController{ctx: ctx, cancel: cancel, bundle: bundle, token: "token", active: -1}
		controller.request.StagePreparationTimeout = 30 * time.Minute
		controller.request.OnBoundary = func(context.Context, string, JobStep, JobStepResult) error {
			remaining := time.Until(time.Unix(0, controller.deadline.Load()))
			if remaining < 29*time.Minute || remaining > 30*time.Minute {
				t.Fatalf("preparation deadline=%v", remaining)
			}
			if cancelPreparation {
				cancel()
			}
			return nil
		}
		err := controller.handle(JobEvent{Kind: "begin", StepID: bundle.Manifest.Plan.Steps[0].ID, Token: "token"})
		if cancelPreparation {
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled preparation acknowledged: %v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			remaining := time.Until(time.Unix(0, controller.deadline.Load()))
			limit := time.Duration(bundle.Manifest.Plan.Steps[0].TimeoutSeconds) * time.Second
			if remaining > limit || remaining < limit-time.Second {
				t.Fatalf("action lost its independent timeout: %v", remaining)
			}
		}
		cancel()
	}
}
