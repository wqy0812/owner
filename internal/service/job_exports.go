package service

import (
	"context"
	"fmt"
	"io"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
)

type ScenarioJobRequest struct {
	ExecutionMode      domain.ScenarioExecutionMode `json:"executionMode"`
	DeliveryDecisions  []DeliveryDecisionInput      `json:"deliveryDecisions"`
	EnvironmentID      string                       `json:"environmentId"`
	ExpectedPlanDigest string                       `json:"expectedPlanDigest"`
}

func (s *ExecutionService) scenarioJob(ctx context.Context, user domain.User, revisionID string, input ScenarioJobRequest, export bool) (ComponentTestPlan, *ansiblerunner.JobBundle, error) {
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	revision, err := s.store.GetScenarioRevision(ctx, revisionID)
	if err != nil {
		return ComponentTestPlan{}, nil, err
	}
	kind := domain.RunScenarioTest
	if revision.Status == domain.RevisionReleased || input.ExecutionMode == domain.ScenarioExecutionBaselineVerify {
		kind = domain.RunScenario
	}
	lifecycle, err := s.planner.prepareScenarioLifecycle(ctx, user, revisionID, ScenarioExecutionRequest{EnvironmentID: input.EnvironmentID, ExecutionMode: input.ExecutionMode}, kind, "preview", time.Time{})
	if err != nil {
		return ComponentTestPlan{}, nil, err
	}
	prepared, plan, digest, destructive := lifecycle.prepared, lifecycle.plan, lifecycle.preview.PlanDigest, lifecycle.preview.NeedsApproval
	if len(input.DeliveryDecisions) > 0 {
		plan, err = s.delivery.finalizeDeliveryPlan(ctx, plan, input.DeliveryDecisions, user, time.Time{})
		if err != nil {
			return ComponentTestPlan{}, nil, err
		}
		digest = digestValue(struct{ Lifecycle, Plan string }{lifecycle.preview.PlanDigest, componentTestPlanDigest(prepared.environment.CurrentRevisionID, plan)})
	}
	if export && len(plan.DeliveryRequirements) != len(plan.DeliveryDecisions) {
		return ComponentTestPlan{}, nil, fmt.Errorf("%w: choose the locked source or target for each media requirement and preview again before export", domain.ErrInvalid)
	}
	dto := s.planner.componentTestPlanDTO(ctx, prepared.environment, plan, digest, destructive)
	if !export {
		return dto, nil, nil
	}
	if input.ExpectedPlanDigest == "" || input.ExpectedPlanDigest != digest {
		return dto, nil, fmt.Errorf("%w: job plan changed; preview again", domain.ErrConflict)
	}
	// Export is a read-only compilation. It neither creates a Run nor marks a
	// scenario as testing. The standalone runner assigns its own execution ID.
	inventory, err := renderInventory(prepared.environment.Revision.Inventory)
	if err != nil {
		return dto, nil, err
	}
	builder := s.jobs
	job := jobPlanFromLocked(prepared.environment.ID, plan, inventory)
	if err := s.rollback.addRecoverySteps(ctx, &job, plan); err != nil {
		return dto, nil, err
	}
	bundle, err := builder.BuildJob(ctx, job)
	if err != nil {
		return dto, nil, err
	}
	if err := attachJobCompanions(bundle); err != nil {
		_ = bundle.Close()
		return dto, nil, err
	}
	return dto, bundle, nil
}
func (s *ExecutionService) PreviewScenarioJob(ctx context.Context, user domain.User, id string, input ScenarioJobRequest) (ComponentTestPlan, error) {
	plan, _, err := s.scenarioJob(ctx, user, id, input, false)
	return plan, err
}
func (s *ExecutionService) ExportScenarioJob(ctx context.Context, user domain.User, id string, input ScenarioJobRequest) (*ansiblerunner.JobBundle, error) {
	_, bundle, err := s.scenarioJob(ctx, user, id, input, true)
	return bundle, err
}
func (s *ExecutionService) DownloadRunJob(ctx context.Context, user domain.User, id string, output io.Writer) error {
	allowed, err := s.store.CanViewRun(ctx, user, id)
	if err != nil {
		return err
	}
	if !allowed {
		return domain.ErrForbidden
	}
	_, data, _, err := s.store.GetRunJob(ctx, id)
	if err != nil {
		return err
	}
	_, err = output.Write(data)
	return err
}
func (s *ExecutionService) RunJobSummary(ctx context.Context, id string) (string, *int) {
	digest, code, err := s.store.RunJobSummary(ctx, id)
	if err != nil {
		return "", nil
	}
	return digest, code
}
