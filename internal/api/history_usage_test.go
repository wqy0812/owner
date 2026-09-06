package api

import (
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUsageAndRetentionHTTPAuthorization(t *testing.T) {
	f := newAPIFixture(t)
	owner := f.session(seed.ComponentOwnerRuntimeID)
	other := f.session(seed.ComponentOwnerK8sID)
	admin := f.session(seed.PlatformAdminID)
	for _, check := range []struct {
		cookie *http.Cookie
		status int
	}{{nil, 401}, {owner, 200}, {other, 403}, {admin, 200}} {
		response := f.request(http.MethodGet, "/api/v1/components/component-test-runtime/usage", nil, check.cookie)
		if response.Code != check.status {
			t.Fatalf("usage status=%d want=%d %s", response.Code, check.status, response.Body.String())
		}
		if response.Code == 200 && (strings.Contains(response.Body.String(), "inputSnapshot") || strings.Contains(response.Body.String(), "playbook") || strings.Contains(response.Body.String(), "parameterValues")) {
			t.Fatal("private payload exposed", response.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/run-retention", "/api/v1/components/component-test-runtime/usage?releaseId=foreign", "/api/v1/components/component-test-runtime/usage?includeHistory=nonsense"} {
		response := f.request(http.MethodGet, path, nil, owner)
		if response.Code < 400 {
			t.Fatal("invalid query or role accepted", path, response.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/runs/archive", "/api/v1/runs/cleanup-preview", "/api/v1/runs/cleanup"} {
		if response := f.request(http.MethodPost, path, map[string]any{"runIds": []string{"missing"}}, owner); response.Code != 403 {
			t.Fatal(path, response.Code, response.Body.String())
		}
	}
	response := f.request(http.MethodPut, "/api/v1/run-retention", map[string]any{"autoArchive": false, "autoCleanup": false, "archiveDays": 0, "cleanupDays": 90}, admin)
	if response.Code != 400 {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestCleanedRunLinkPreservesOriginalPermission(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-91 * 24 * time.Hour)
	env, err := f.database.GetEnvironment(ctx, "environment-test", true)
	if err != nil {
		t.Fatal(err)
	}
	run := domain.Run{ID: "cleaned-http", Kind: domain.RunComponentTest, Status: domain.RunFailed, ComponentReleaseID: "release-test-runtime-1.0.0", EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, RequestedBy: seed.ComponentOwnerRuntimeID, Action: domain.ActionInstall, CreatedAt: now, FinishedAt: &now, InputSnapshot: map[string]any{}}
	if err = f.database.CreateRun(ctx, run, nil); err != nil {
		t.Fatal(err)
	}
	response := f.request(http.MethodPost, "/api/v1/runs/cleanup", map[string]any{"runIds": []string{run.ID}}, f.session(seed.PlatformAdminID))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, check := range []struct {
		id     string
		status int
	}{{seed.ComponentOwnerRuntimeID, 410}, {seed.PlatformAdminID, 410}, {seed.EnvironmentOwnerID, 410}, {seed.ComponentOwnerK8sID, 403}} {
		response = f.request(http.MethodGet, "/api/v1/runs/"+run.ID, nil, f.session(check.id))
		if response.Code != check.status {
			t.Fatal(check.id, response.Code, response.Body.String())
		}
		if response.Code == 410 && !strings.Contains(response.Body.String(), "run.cleaned") {
			t.Fatal(response.Body.String())
		}
		if response.Code == 403 && strings.Contains(response.Body.String(), "run.cleaned") {
			t.Fatal("history existence leaked")
		}
	}
}

func TestScenarioLabelsCannotBypassGraphWriteValidation(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	revision, err := f.database.GetScenarioRevision(ctx, "scenario-test-runtime-r2")
	if err != nil {
		t.Fatal(err)
	}
	// This fixture's component restricts amd64, independently of the browser.
	if _, err = f.database.DB().Exec(`UPDATE component_releases SET candidate=1,environment_constraints_json='{"architecture":["amd64"]}' WHERE id='release-test-runtime-1.1.0'`); err != nil {
		t.Fatal(err)
	}
	owner := f.session(seed.ScenarioOwnerID)
	graph := graphInput{Edges: revision.Graph.Edges}
	for _, n := range revision.Graph.Nodes {
		flow := flowNodeInput{ID: n.ID, Type: "component", Position: n.Position}
		flow.Data.Label = n.Name
		flow.Data.ReleaseID = n.ReleaseID
		flow.Data.Action = n.Action
		flow.Data.ParameterValues = n.ParameterValues
		flow.Data.DependencySources = n.DependencySources
		graph.Nodes = append(graph.Nodes, flow)
	}
	body := map[string]any{"expectedDigest": domain.ScenarioRevisionSpecDigest(revision), "graph": graph, "environmentConstraints": map[string]any{"architecture": []string{"arm64"}}}
	response := f.request(http.MethodPut, "/api/v1/scenario-revisions/"+revision.ID+"/graph", body, owner)
	if response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	unchanged, err := f.database.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if domain.ScenarioRevisionSpecDigest(unchanged) != domain.ScenarioRevisionSpecDigest(revision) {
		t.Fatal("invalid graph partially saved")
	}
	body["environmentConstraints"] = map[string]any{}
	response = f.request(http.MethodPut, "/api/v1/scenario-revisions/"+revision.ID+"/graph", body, owner)
	if response.Code != 200 {
		t.Fatal("incomplete draft should save", response.Code, response.Body.String())
	}
	response = f.request(http.MethodPost, "/api/v1/scenario-revisions/"+revision.ID+"/test-runs", map[string]any{"environmentId": "environment-test"}, owner)
	if response.Code < 400 {
		t.Fatal("incomplete labels executed", response.Body.String())
	}
}
