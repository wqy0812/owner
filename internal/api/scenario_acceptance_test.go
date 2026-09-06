package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func TestScenarioAcceptanceAPIContractsAndWorkspaceUpload(t *testing.T) {
	f := newAPIFixture(t)
	owner := f.session(seed.ScenarioOwnerID)
	created := f.request(http.MethodPost, "/api/v1/scenarios", map[string]any{"name": "Acceptance API", "slug": "acceptance-api"}, owner)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", created.Code, created.Body)
	}
	scenario := decodeEnvelope(t, created)["data"].(map[string]any)
	id := scenario["currentRevisionId"].(string)
	base := "/api/v1/scenario-revisions/" + id + "/acceptance"
	read := func() map[string]any {
		t.Helper()
		response := f.request(http.MethodGet, base, nil, owner)
		if response.Code != http.StatusOK {
			t.Fatalf("read=%d %s", response.Code, response.Body)
		}
		return decodeEnvelope(t, response)["data"].(map[string]any)
	}
	definition := read()
	input := map[string]any{"expectedRevisionDigest": definition["revisionDigest"], "jobs": []any{map[string]any{"id": "api-check", "name": "API check", "purpose": "Verify business response", "hostGroup": "test_nodes", "timeoutSeconds": 60, "riskLevel": "low"}}, "parameters": []any{}, "values": map[string]any{}, "bindings": []any{}}
	saved := f.request(http.MethodPut, base, input, owner)
	if saved.Code != http.StatusOK {
		t.Fatalf("save=%d %s", saved.Code, saved.Body)
	}
	if stale := f.request(http.MethodPut, base, input, owner); stale.Code != http.StatusConflict {
		t.Fatalf("stale definition=%d %s", stale.Code, stale.Body)
	}
	definition = read()
	workspace := definition["workspace"].(map[string]any)
	path := "tasks/acceptance/api-check.yml"
	fileInput := map[string]any{"path": path, "content": "- ansible.builtin.assert:\n    that: true\n", "expectedRevisionDigest": definition["revisionDigest"], "expectedSha256": "", "expectedTreeSha256": workspace["treeSha256"]}
	file := f.request(http.MethodPut, base+"/workspace/file", fileInput, owner)
	if file.Code != http.StatusOK {
		t.Fatalf("file=%d %s", file.Code, file.Body)
	}
	if stale := f.request(http.MethodPut, base+"/workspace/file", fileInput, owner); stale.Code != http.StatusConflict {
		t.Fatalf("stale source=%d %s", stale.Code, stale.Body)
	}
	readFile := f.request(http.MethodGet, base+"/workspace/file?path="+url.QueryEscape(path), nil, owner)
	if readFile.Code != http.StatusOK || !strings.Contains(readFile.Body.String(), "ansible.builtin.assert") {
		t.Fatalf("source read=%d %s", readFile.Code, readFile.Body)
	}
	definition = read()
	workspace = definition["workspace"].(map[string]any)
	upload := f.multipartFileRequest(base+"/workspace/upload", map[string]string{"path": "files/fixture.bin", "expectedRevisionDigest": definition["revisionDigest"].(string), "expectedSha256": "", "expectedTreeSha256": workspace["treeSha256"].(string)}, "file", "fixture.bin", []byte{0, 1, 2, 3}, owner)
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload=%d %s", upload.Code, upload.Body)
	}
	uploaded := decodeEnvelope(t, upload)["data"].(map[string]any)
	definition = read()
	workspace = definition["workspace"].(map[string]any)
	query := url.Values{"path": {"files/fixture.bin"}, "expectedRevisionDigest": {definition["revisionDigest"].(string)}, "expectedSha256": {uploaded["sha256"].(string)}, "expectedTreeSha256": {workspace["treeSha256"].(string)}}
	deleted := f.request(http.MethodDelete, base+"/workspace/file?"+query.Encode(), nil, owner)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete=%d %s", deleted.Code, deleted.Body)
	}
	if missing := f.request(http.MethodGet, base+"/workspace/file?path=files/fixture.bin", nil, owner); missing.Code != http.StatusNotFound {
		t.Fatalf("deleted source=%d", missing.Code)
	}
}

func TestScenarioAcceptanceAPIRejectsNonOwnerAndActiveRunEdits(t *testing.T) {
	f := newAPIFixture(t)
	owner := f.session(seed.ScenarioOwnerID)
	ctx := context.Background()
	user, err := f.database.GetUser(ctx, seed.ScenarioOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := f.platform.Scenarios().Create(ctx, user, domain.Scenario{Name: "Acceptance lock", Slug: "acceptance-lock"})
	if err != nil {
		t.Fatal(err)
	}
	id := scenario.CurrentRevisionID
	base := "/api/v1/scenario-revisions/" + id + "/acceptance"
	response := f.request(http.MethodGet, base, nil, owner)
	definition := decodeEnvelope(t, response)["data"].(map[string]any)
	input := map[string]any{"expectedRevisionDigest": definition["revisionDigest"], "jobs": []any{}}
	if denied := f.request(http.MethodPut, base, input, f.session(seed.PlatformAdminID)); denied.Code != http.StatusForbidden {
		t.Fatalf("admin mutation=%d %s", denied.Code, denied.Body)
	}
	if _, err := f.database.DB().ExecContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,action_kind,destructive,input_snapshot_json,artifact_digest,error_text,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, "acceptance-active", "scenario_test", "awaiting_approval", user.ID, "environment-test", "environment-test-r1", id, "install", false, `{}`, "", "", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if blocked := f.request(http.MethodPut, base, input, owner); blocked.Code != http.StatusConflict {
		t.Fatalf("active mutation=%d %s", blocked.Code, blocked.Body)
	}
	response = f.request(http.MethodGet, base, nil, owner)
	definition = decodeEnvelope(t, response)["data"].(map[string]any)
	if definition["editable"] != false {
		t.Fatalf("active revision reported editable: %v", definition)
	}
}
