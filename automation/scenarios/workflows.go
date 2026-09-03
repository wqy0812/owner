package scenarios

import (
	"context"
	"fmt"
	"net/http"
)

func (h *harness) startScenarioTest(ctx context.Context) (runDTO, error) {
	var run runDTO
	if err := h.scenario.data(ctx, http.MethodPost, "/api/v1/scenario-revisions/"+h.cfg.Fixture.ScenarioRevisionID+"/test-runs", map[string]any{
		"environmentId": h.cfg.Fixture.EnvironmentID,
	}, &run); err != nil {
		return run, err
	}
	if err := h.trackRun(run, h.scenario); err != nil {
		return run, err
	}
	if err := h.approveIfNeeded(ctx, run, "direct", "automated real-environment full scenario acceptance"); err != nil {
		return run, err
	}
	return h.waitRun(ctx, h.scenario, run.ID, "succeeded", "failed", "cancelled", "rejected")
}

func (h *harness) previewComponent(ctx context.Context, releaseID, mode string) (componentPlan, map[string]any, error) {
	input := map[string]any{"environmentId": h.cfg.Fixture.EnvironmentID, "mode": mode}
	if mode == "rollback" {
		input["rollbackVerification"] = map[string]any{"kind": "rollback_only"}
	}
	var plan componentPlan
	err := h.component.data(ctx, http.MethodPost, "/api/v1/component-releases/"+releaseID+"/test-plan", input, &plan)
	return plan, input, err
}

func (h *harness) startComponent(ctx context.Context, releaseID, mode, deliveryMode, reason string) (componentPlan, runDTO, error) {
	plan, input, err := h.previewComponent(ctx, releaseID, mode)
	if err != nil {
		return plan, runDTO{}, err
	}
	if releaseID == h.cfg.Fixture.DeliveryReleaseID && mode == "install_verify" && len(plan.DeliveryRequirements) > 0 {
		if err := h.validateDeliveryRequirements(plan.DeliveryRequirements); err != nil {
			return plan, runDTO{}, err
		}
	}
	input["expectedPlanDigest"] = plan.PlanDigest
	var run runDTO
	if err := h.component.data(ctx, http.MethodPost, "/api/v1/component-releases/"+releaseID+"/test-runs", input, &run); err != nil {
		return plan, run, err
	}
	if err := h.trackRun(run, h.component); err != nil {
		return plan, run, err
	}
	if err := h.approveIfNeeded(ctx, run, deliveryMode, reason); err != nil {
		return plan, run, err
	}
	return plan, run, nil
}

func (h *harness) retryRun(ctx context.Context, source runDTO) (runDTO, error) {
	var plan struct {
		PlanDigest string `json:"planDigest"`
	}
	if err := h.component.data(ctx, http.MethodPost, "/api/v1/runs/"+source.ID+"/retry-plan", nil, &plan); err != nil {
		return runDTO{}, err
	}
	var retried runDTO
	if err := h.component.data(ctx, http.MethodPost, "/api/v1/runs/"+source.ID+"/retry-runs", map[string]string{"expectedPlanDigest": plan.PlanDigest}, &retried); err != nil {
		return retried, err
	}
	if err := h.trackRun(retried, h.component); err != nil {
		return retried, err
	}
	if err := h.approveIfNeeded(ctx, retried, "direct", "automated safe retry of controlled failure"); err != nil {
		return retried, err
	}
	return h.waitRun(ctx, h.component, retried.ID, "succeeded", "failed", "cancelled", "rejected")
}

func (h *harness) environmentRollback(ctx context.Context, reason string) (runDTO, error) {
	if !h.destructiveCleanupAllowed() {
		return runDTO{}, fmt.Errorf("automatic environment rollback requires an owned lock, a successful clean preflight, and a Run created by this test")
	}
	if err := h.verifyLockOwnership(ctx); err != nil {
		return runDTO{}, err
	}
	var plan rollbackPlan
	path := "/api/v1/environments/" + h.cfg.Fixture.EnvironmentID
	if err := h.environment.data(ctx, http.MethodPost, path+"/cluster-rollback-plan", nil, &plan); err != nil {
		return runDTO{}, err
	}
	if err := h.validateRollbackSources(plan.Sources); err != nil {
		return runDTO{}, err
	}
	var run runDTO
	if err := h.environment.data(ctx, http.MethodPost, path+"/cluster-rollback-runs", map[string]string{
		"expectedPlanDigest": plan.PlanDigest, "confirmEnvironmentName": h.cfg.Fixture.EnvironmentName,
	}, &run); err != nil {
		return run, err
	}
	if err := h.trackRun(run, h.environment); err != nil {
		return run, err
	}
	if err := h.approveIfNeeded(ctx, run, "direct", reason); err != nil {
		return run, err
	}
	return h.waitRun(ctx, h.environment, run.ID, "succeeded", "failed", "cancelled", "rejected")
}

func requireStatus(run runDTO, expected string) error {
	if run.Status != expected {
		return fmt.Errorf("run %s status=%s error=%q, want %s", run.ID, run.Status, run.Error, expected)
	}
	return nil
}

func requireStepCount(run runDTO, expected int) error {
	if len(run.Steps) != expected {
		return fmt.Errorf("run %s has %d steps, want %d", run.ID, len(run.Steps), expected)
	}
	for _, step := range run.Steps {
		if step.Status != "succeeded" {
			return fmt.Errorf("run %s step %s status=%s", run.ID, step.Name, step.Status)
		}
	}
	return nil
}

func requireDelivery(run runDTO, expected string, count int) error {
	if len(run.DeliveryResults) != count {
		return fmt.Errorf("run %s delivery result count=%d, want %d", run.ID, len(run.DeliveryResults), count)
	}
	expectedMode := expected
	if expected == "transferred" {
		expectedMode = "transfer"
	}
	for _, result := range run.DeliveryResults {
		if result.Status != expected || result.Mode != expectedMode {
			return fmt.Errorf("run %s delivery %s mode=%s status=%s, want mode=%s status=%s", run.ID, result.RequirementID, result.Mode, result.Status, expectedMode, expected)
		}
	}
	return nil
}
