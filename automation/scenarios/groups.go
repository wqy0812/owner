package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func (h *harness) runGroup1(ctx context.Context) error {
	installed, err := h.startScenarioTest(ctx)
	if err != nil {
		return err
	}
	h.record("1", "full scenario install and verify", installed)
	if err := requireStatus(installed, "succeeded"); err != nil {
		return err
	}
	if err := requireStepCount(installed, h.cfg.Fixture.ScenarioInstallStepCount); err != nil {
		return err
	}

	rolledBack, err := h.environmentRollback(ctx, "automated rollback after full scenario acceptance")
	if err != nil {
		return err
	}
	h.record("1", "full environment rollback", rolledBack)
	if rolledBack.Status == "succeeded" {
		if err := requireStepCount(rolledBack, h.cfg.Fixture.ScenarioRollbackStepCount); err != nil {
			return err
		}
	} else {
		retried, retryErr := h.environmentRollback(ctx, "automated bounded idempotent retry after environment rollback failure")
		if retryErr != nil {
			return fmt.Errorf("initial environment rollback %s failed (%s); retry: %w", rolledBack.ID, rolledBack.Error, retryErr)
		}
		h.record("1", "environment rollback retry", retried)
		if err := requireStatus(retried, "succeeded"); err != nil {
			return fmt.Errorf("initial environment rollback %s failed (%s); retry: %w", rolledBack.ID, rolledBack.Error, err)
		}
	}
	if err := h.requireCleanLifecycle(ctx); err != nil {
		return err
	}
	return h.verifyRollbackPostconditions(ctx)
}

func (h *harness) runGroup2(ctx context.Context) error {
	_, started, err := h.startComponent(ctx, h.cfg.Fixture.ControlledReleaseID, "install_verify", "direct", "automated controlled failure acceptance")
	if err != nil {
		return err
	}
	failed, err := h.waitRun(ctx, h.component, started.ID, "succeeded", "failed", "cancelled", "rejected")
	if err != nil {
		return err
	}
	h.record("2", "intentional first failure", failed)
	if err := requireStatus(failed, "failed"); err != nil {
		return err
	}

	retried, err := h.retryRun(ctx, failed)
	if err != nil {
		return err
	}
	h.record("2", "safe retry", retried)
	if err := requireStatus(retried, "succeeded"); err != nil {
		return err
	}
	if retried.RetryOfRunID != failed.ID || retried.RetryRootRunID != failed.ID || retried.RetryAttempt != 1 {
		return fmt.Errorf("retry provenance=%+v, source=%s", retried, failed.ID)
	}

	_, cancellable, err := h.startComponent(ctx, h.cfg.Fixture.ControlledReleaseID, "install_verify", "direct", "automated cancellation acceptance")
	if err != nil {
		return err
	}
	running, err := h.waitRun(ctx, h.component, cancellable.ID, "running", "succeeded", "failed", "cancelled")
	if err != nil {
		return err
	}
	if running.Status != "running" {
		return fmt.Errorf("cancellation Run reached %s before cancellation window", running.Status)
	}
	if err := h.component.data(ctx, http.MethodPost, "/api/v1/runs/"+running.ID+"/cancel", nil, nil); err != nil {
		return err
	}
	cancelled, err := h.waitRun(ctx, h.component, running.ID, "cancelled", "failed", "succeeded")
	if err != nil {
		return err
	}
	h.record("2", "cancellation", cancelled)
	if err := requireStatus(cancelled, "cancelled"); err != nil {
		return err
	}

	_, rollback, err := h.startComponent(ctx, h.cfg.Fixture.ControlledReleaseID, "rollback", "direct", "automated cleanup of controlled failure fixture")
	if err != nil {
		return err
	}
	rolledBack, err := h.waitRun(ctx, h.component, rollback.ID, "succeeded", "failed", "cancelled", "rejected")
	if err != nil {
		return err
	}
	h.record("2", "component rollback", rolledBack)
	if err := requireStatus(rolledBack, "succeeded"); err != nil {
		return err
	}
	return h.requireCleanLifecycle(ctx)
}

func (h *harness) runGroup3(ctx context.Context) error {
	if err := h.rejectDigestMismatches(ctx); err != nil {
		return err
	}
	directPlan, directRun, err := h.startComponent(ctx, h.cfg.Fixture.DeliveryReleaseID, "install_verify", "direct", "automated direct-source delivery acceptance")
	if err != nil {
		return err
	}
	if len(directPlan.DeliveryRequirements) != 2 {
		return fmt.Errorf("direct plan requirements=%d, want 2", len(directPlan.DeliveryRequirements))
	}
	if err := h.validateDeliveryRequirements(directPlan.DeliveryRequirements); err != nil {
		return err
	}
	directRun, err = h.waitRun(ctx, h.component, directRun.ID, "succeeded", "failed", "cancelled", "rejected")
	if err != nil {
		return err
	}
	h.record("3", "direct Artifact/Image sources", directRun)
	if err := requireStatus(directRun, "succeeded"); err != nil {
		return err
	}
	if err := requireDelivery(directRun, "direct", 2); err != nil {
		return err
	}

	transferPlan, transferRun, err := h.startComponent(ctx, h.cfg.Fixture.DeliveryReleaseID, "install_verify", "transfer", "automated Artifact/Image transfer acceptance")
	if err != nil {
		return err
	}
	if len(transferPlan.DeliveryRequirements) != 2 {
		return fmt.Errorf("transfer plan requirements=%d, want 2", len(transferPlan.DeliveryRequirements))
	}
	if err := h.validateDeliveryRequirements(transferPlan.DeliveryRequirements); err != nil {
		return err
	}
	transferRun, err = h.waitRun(ctx, h.component, transferRun.ID, "succeeded", "failed", "cancelled", "rejected")
	if err != nil {
		return err
	}
	h.record("3", "transfer Artifact/Image to environment", transferRun)
	if err := requireStatus(transferRun, "succeeded"); err != nil {
		return err
	}
	if err := requireDelivery(transferRun, "transferred", 2); err != nil {
		return err
	}

	reusePlan, reuseRun, err := h.startComponent(ctx, h.cfg.Fixture.DeliveryReleaseID, "install_verify", "direct", "automated delivery target reuse acceptance")
	if err != nil {
		return err
	}
	if len(reusePlan.DeliveryRequirements) != 0 {
		return fmt.Errorf("reuse plan requirements=%d, want 0", len(reusePlan.DeliveryRequirements))
	}
	reuseRun, err = h.waitRun(ctx, h.component, reuseRun.ID, "succeeded", "failed", "cancelled", "rejected")
	if err != nil {
		return err
	}
	h.record("3", "reuse environment targets", reuseRun)
	if err := requireStatus(reuseRun, "succeeded"); err != nil {
		return err
	}

	_, rollback, err := h.startComponent(ctx, h.cfg.Fixture.DeliveryReleaseID, "rollback", "direct", "automated cleanup of delivery fixture")
	if err != nil {
		return err
	}
	rolledBack, err := h.waitRun(ctx, h.component, rollback.ID, "succeeded", "failed", "cancelled", "rejected")
	if err != nil {
		return err
	}
	h.record("3", "component rollback", rolledBack)
	if err := requireStatus(rolledBack, "succeeded"); err != nil {
		return err
	}
	if err := h.requireCleanLifecycle(ctx); err != nil {
		return err
	}
	if err := verifyHTTPArtifact(ctx, h.cfg.Fixture.ArtifactTargetURL, h.cfg.Fixture.ArtifactSHA256, http.StatusOK); err != nil {
		return fmt.Errorf("verify transferred artifact target: %w", err)
	}
	if status, observed, err := h.registryManifest(ctx, http.MethodHead); err != nil {
		return err
	} else if status != http.StatusOK || observed != h.cfg.Fixture.ImageDigest {
		return fmt.Errorf("transferred image status=%d digest=%s", status, observed)
	}
	return nil
}

func (h *harness) validateDeliveryRequirements(requirements []deliveryRequirement) error {
	fixture := h.cfg.Fixture
	byID := make(map[string]deliveryRequirement, len(requirements))
	for _, requirement := range requirements {
		byID[requirement.ID] = requirement
	}
	artifact := byID["artifact:"+fixture.DeliveryReleaseID+":"+fixture.DeliveryArtifactAlias]
	if artifact.Kind != "artifact" || artifact.Identity != "sha256:"+fixture.ArtifactSHA256 || artifact.Source != fixture.ArtifactSourceURL || artifact.Target != fixture.ArtifactTargetURL || artifact.TargetPresent {
		return fmt.Errorf("delivery Artifact requirement drifted: %+v", artifact)
	}
	image := byID["image:"+fixture.DeliveryReleaseID+":"+fixture.DeliveryImageLogicalName]
	if image.Kind != "image" || image.Identity != fixture.ImageDigest || image.Source != immutableConfiguredImageRef(fixture.ImageSourceRef, fixture.ImageDigest) || image.TargetPresent {
		return fmt.Errorf("delivery Image requirement drifted: %+v", image)
	}
	if !strings.HasPrefix(image.Target, strings.TrimSuffix(fixture.ImageRegistryEndpoint, "/")+"/"+fixture.ImageTargetRepository+"@") || !strings.HasSuffix(image.Target, fixture.ImageDigest) {
		return fmt.Errorf("delivery Image target drifted: %s", image.Target)
	}
	return nil
}

func (h *harness) rejectDigestMismatches(ctx context.Context) error {
	fixture := h.cfg.Fixture
	status, body, err := h.component.request(ctx, http.MethodPost, "/api/v1/component-releases/"+fixture.DeliveryReleaseID+"/artifacts/register", map[string]any{
		"alias": "automation_mismatch", "filename": "automation-mismatch.bin", "sourceUrl": fixture.ArtifactSourceURL, "sha256": strings.Repeat("0", 64),
	})
	if err != nil {
		return err
	}
	if status != http.StatusConflict {
		if status >= 200 && status < 300 {
			_ = h.component.data(ctx, http.MethodDelete, "/api/v1/component-releases/"+fixture.DeliveryReleaseID+"/artifacts/automation_mismatch", nil, nil)
		}
		return fmt.Errorf("Artifact digest mismatch status=%d body=%s, want 409", status, truncate(body))
	}
	status, body, err = h.component.request(ctx, http.MethodPost, "/api/v1/component-releases/"+fixture.DeliveryReleaseID+"/images/register", map[string]any{
		"logicalName": "automation_mismatch", "sourceRef": fixture.ImageSourceRef, "digest": "sha256:" + strings.Repeat("0", 64),
	})
	if err != nil {
		return err
	}
	if status != http.StatusConflict {
		if status >= 200 && status < 300 {
			_ = h.component.data(ctx, http.MethodDelete, "/api/v1/component-releases/"+fixture.DeliveryReleaseID+"/images/automation_mismatch", nil, nil)
		}
		return fmt.Errorf("Image digest mismatch status=%d body=%s, want 409", status, truncate(body))
	}
	var component componentDTO
	if err := h.component.data(ctx, http.MethodGet, "/api/v1/components/"+fixture.DeliveryComponentID, nil, &component); err != nil {
		return err
	}
	for _, release := range component.Releases {
		if release.ID != fixture.DeliveryReleaseID {
			continue
		}
		for _, artifact := range release.Artifacts {
			if artifact.Alias == "automation_mismatch" {
				return fmt.Errorf("Artifact digest mismatch persisted alias")
			}
		}
		for _, image := range release.Images {
			if image.LogicalName == "automation_mismatch" {
				return fmt.Errorf("Image digest mismatch persisted logical name")
			}
		}
		return nil
	}
	return fmt.Errorf("delivery release disappeared while checking digest rejection")
}

func immutableConfiguredImageRef(sourceRef, digest string) string {
	repository := sourceRef
	if index := strings.LastIndex(repository, "@"); index >= 0 {
		repository = repository[:index]
	} else if colon := strings.LastIndex(repository, ":"); colon > strings.LastIndex(repository, "/") {
		repository = repository[:colon]
	}
	return repository + "@" + digest
}
