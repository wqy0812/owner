package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"codex/platform-demo/internal/seed"
)

func (f apiFixture) releaseGeneration(id string) int64 {
	f.t.Helper()
	release, err := f.database.GetComponentRelease(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return release.PublicationGeneration
}

func TestReleaseFullWriteRequiresExpectedGeneration(t *testing.T) {
	f := newAPIFixture(t)
	owner := f.session(seed.ComponentOwnerRuntimeID)
	for _, suffix := range []string{"", "/contract"} {
		response := f.request(http.MethodPut, "/api/v1/component-releases/release-test-runtime-1.1.0"+suffix, map[string]any{}, owner)
		if response.Code != http.StatusConflict {
			t.Fatalf("missing generation: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestHostFoundationSampleImportsAsTwoIndependentDrafts(t *testing.T) {
	f := newAPIFixture(t)
	contents, err := os.ReadFile("../../examples/components/host-foundation-example.json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []any
	if err = json.Unmarshal(contents, &entries); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"entries": entries}
	owner := f.session(seed.ComponentOwnerRuntimeID)
	preview := f.request(http.MethodPost, "/api/v1/component-imports/plan", input, owner)
	if preview.Code != http.StatusOK {
		t.Fatalf("sample preview: %d %s", preview.Code, preview.Body.String())
	}
	input["expectedPlanDigest"] = decodeEnvelope(t, preview)["data"].(map[string]any)["planDigest"]
	saved := f.request(http.MethodPost, "/api/v1/component-imports", input, owner)
	if saved.Code != http.StatusCreated {
		t.Fatalf("sample import: %d %s", saved.Code, saved.Body.String())
	}
}
