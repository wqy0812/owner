package api

import (
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestGlobalParameterRoutesAreRemoved(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.session(seed.PlatformAdminID)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/environment-parameter-definitions"},
		{http.MethodPost, "/api/v1/environment-parameter-definitions"},
		{http.MethodPut, "/api/v1/environment-parameter-definitions/old/default"},
		{http.MethodDelete, "/api/v1/environment-parameter-definitions/old"},
	} {
		response := f.request(route.method, route.path, nil, admin)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: %d %s", route.path, response.Code, response.Body.String())
		}
	}
}

func TestConfigurationReferenceKindSurvivesImportAndContractAPI(t *testing.T) {
	f := newAPIFixture(t)
	f.platform.ConfigurePlaybookRoot(t.TempDir())
	owner := f.session(seed.ComponentOwnerRuntimeID)
	a, b := componentImportEntry("config-a"), componentImportEntry("config-b")
	for _, entry := range []map[string]any{a, b} {
		release := entry["release"].(map[string]any)
		release["parameters"] = []any{map[string]any{"name": "own", "description": "Own input", "type": "string", "visibility": "public", "modifiable": false, "valueProvider": "component_owner", "fixedValue": "value"}, map[string]any{"name": "reference", "description": "Other input", "type": "string", "visibility": "public", "modifiable": false, "valueProvider": "upstream_mapping", "required": true, "testValue": "test"}}
	}
	for _, pair := range []struct {
		entry  map[string]any
		source string
	}{{a, "config-b"}, {b, "config-a"}} {
		pair.entry["release"].(map[string]any)["dependencies"] = []any{map[string]any{"componentSlug": pair.source, "kind": "configuration", "parameterMappings": []any{map[string]any{"upstreamParameter": "own", "targetParameter": "reference"}}}}
	}
	entries := []any{a, b}
	preview := f.request(http.MethodPost, "/api/v1/component-imports/plan", map[string]any{"entries": entries}, owner)
	if preview.Code != http.StatusOK {
		t.Fatal(preview.Body.String())
	}
	digest := decodeEnvelope(t, preview)["data"].(map[string]any)["planDigest"]
	result := f.request(http.MethodPost, "/api/v1/component-imports", map[string]any{"entries": entries, "expectedPlanDigest": digest}, owner)
	if result.Code != http.StatusCreated {
		t.Fatal(result.Body.String())
	}
	ids := decodeEnvelope(t, result)["data"].(map[string]any)["createdDrafts"].(map[string]any)
	id := ids["config-a"].(string)
	r, err := f.database.GetComponentRelease(context.Background(), id)
	if err != nil || r.Dependencies[0].Kind != "configuration" {
		t.Fatalf("stored kind: %v %v", r.Dependencies, err)
	}
	dep := r.Dependencies[0]
	response := f.request(http.MethodPut, "/api/v1/component-releases/"+id+"/contract", map[string]any{"parameters": r.Parameters, "dependencies": []any{map[string]any{"kind": "configuration", "upstreamComponentId": dep.UpstreamComponentID, "upstreamReleaseId": dep.UpstreamReleaseID, "parameterMappings": dep.ParameterMappings}}}, owner)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	r, err = f.database.GetComponentRelease(context.Background(), id)
	if err != nil || r.Dependencies[0].Kind != "configuration" {
		t.Fatalf("saved kind: %v %v", r.Dependencies, err)
	}
	// Editing the source may create a field cycle through a configuration
	// reference in another release, even when this release has execution dependencies only.
	source, err := f.database.GetComponentRelease(context.Background(), ids["config-b"].(string))
	if err != nil {
		t.Fatal(err)
	}
	source.Parameters[0].FixedValue = nil
	source.Parameters[0].ValueProvider = domain.ParameterProviderUpstreamMapping
	source.Dependencies[0].Kind = ""
	source.Dependencies[0].ParameterMappings = append(source.Dependencies[0].ParameterMappings, domain.ParameterMapping{UpstreamParameter: "reference", TargetParameter: "own"})
	d := source.Dependencies[0]
	invalid := f.request(http.MethodPut, "/api/v1/component-releases/"+source.ID+"/contract", map[string]any{"parameters": source.Parameters, "dependencies": []any{map[string]any{"upstreamComponentId": d.UpstreamComponentID, "upstreamReleaseId": d.UpstreamReleaseID, "parameterMappings": d.ParameterMappings}}}, owner)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "cycle") {
		t.Fatalf("indirect field cycle accepted: %d %s", invalid.Code, invalid.Body.String())
	}

}
