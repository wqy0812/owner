package scenarios

import (
	"context"
	"testing"
	"time"
)

// TestRealScenarioAutomation runs the three real-environment acceptance groups
// through HTTP APIs. It is skipped unless the operator supplies every explicit
// opt-in guard documented in README.md.
func TestRealScenarioAutomation(t *testing.T) {
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Skip("set CLUSTERFORGE_REAL_E2E=1 and the documented confirmation variables to run real scenarios")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	h, err := newHarness(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer h.cleanup(t)

	if cfg.PreflightOnly {
		if err := h.preflight(ctx); err != nil {
			t.Fatal(err)
		}
		t.Logf("preflight passed for %s (%s)", cfg.Fixture.EnvironmentName, cfg.Fixture.EnvironmentID)
		return
	}

	if err := h.acquireLock(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.preflight(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.armCleanup(); err != nil {
		t.Fatal(err)
	}

	if !t.Run("1_full_scenario_and_environment_rollback", func(t *testing.T) {
		if err := h.runGroup1(ctx); err != nil {
			t.Fatal(err)
		}
	}) {
		return
	}
	if !t.Run("2_failure_retry_and_cancel", func(t *testing.T) {
		if err := h.runGroup2(ctx); err != nil {
			t.Fatal(err)
		}
	}) {
		return
	}
	if !t.Run("3_artifact_and_image_delivery", func(t *testing.T) {
		if err := h.resetDeliveryTargets(ctx); err != nil {
			t.Fatal(err)
		}
		if err := h.runGroup3(ctx); err != nil {
			t.Fatal(err)
		}
	}) {
		return
	}
	if err := h.requireCleanLifecycle(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("all real scenarios passed; report: %s/summary.json", cfg.OutputDir)
}
