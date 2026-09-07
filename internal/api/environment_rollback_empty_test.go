package api

import (
	"net/http"
	"strings"
	"testing"

	"codex/platform-demo/internal/seed"
)

func TestEnvironmentRollbackEmptyPreviewDoesNotCreateRun(t *testing.T) {
	f := newAPIFixture(t)
	owner := f.session(seed.EnvironmentOwnerID)
	nonOwner := f.session(seed.ComponentOwnerRuntimeID)
	created := f.request(http.MethodPost, "/api/v1/environments", map[string]any{
		"name": "Empty Environment", "facts": completeTestEnvironmentFacts(),
	}, owner)
	if created.Code != http.StatusCreated {
		t.Fatalf("create environment status=%d body=%s", created.Code, created.Body.String())
	}
	environment := decodeEnvelope(t, created)["data"].(map[string]any)
	baseURL := "/api/v1/environments/" + environment["id"].(string)
	if denied := f.request(http.MethodPost, baseURL+"/cluster-rollback-plan", nil, nonOwner); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner preview status=%d body=%s", denied.Code, denied.Body.String())
	}
	preview := f.request(http.MethodPost, baseURL+"/cluster-rollback-plan", nil, owner)
	if preview.Code != http.StatusOK {
		t.Fatalf("empty preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	plan := decodeEnvelope(t, preview)["data"].(map[string]any)
	if plan["environmentId"] != environment["id"] || plan["environmentName"] != environment["name"] || plan["environmentRevisionId"] != environment["currentRevisionId"] {
		t.Fatalf("empty preview lost environment identity: %#v", plan)
	}
	for _, key := range []string{"nodes", "sources", "steps", "deliveryRequirements"} {
		if values, ok := plan[key].([]any); !ok || len(values) != 0 {
			t.Fatalf("empty preview %s must be an empty array, got %#v", key, plan[key])
		}
	}
	if plan["componentCount"] != float64(0) || plan["nodeCount"] != float64(0) || plan["planDigest"] != "" || plan["destructive"] != false || plan["requiresApproval"] != false {
		t.Fatalf("empty preview contains executable plan metadata: %#v", plan)
	}
	// Neither an empty preview digest nor a stale previously valid digest may
	// create a no-op rollback Run after its recovery baselines are gone.
	for _, digest := range []string{"", "stale-plan-digest"} {
		submitted := f.request(http.MethodPost, baseURL+"/cluster-rollback-runs", map[string]any{
			"expectedPlanDigest": digest, "confirmEnvironmentName": environment["name"],
		}, owner)
		if submitted.Code != http.StatusConflict || !strings.Contains(submitted.Body.String(), "暂无可回滚组件") {
			t.Fatalf("empty rollback submission status=%d body=%s", submitted.Code, submitted.Body.String())
		}
	}
	lifecycle := decodeEnvelope(t, f.request(http.MethodGet, baseURL+"/lifecycle", nil, owner))["data"].(map[string]any)
	if lifecycle["runCount"] != float64(0) || lifecycle["activeRunCount"] != float64(0) {
		t.Fatalf("empty rollback created a Run: %#v", lifecycle)
	}
	// The seed installation has no current executable lock. A broken baseline
	// must remain an actionable conflict, rather than becoming an empty state.
	invalid := f.request(http.MethodPost, "/api/v1/environments/environment-test/cluster-rollback-plan", nil, owner)
	if invalid.Code != http.StatusConflict || !strings.Contains(invalid.Body.String(), "rollback.baseline_invalid") {
		t.Fatalf("invalid baseline preview status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
