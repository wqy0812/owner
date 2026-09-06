package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

// Exercise the public preview/submit contract using the same asynchronous job
// protocol as component tests. No evidence or source relationship is fabricated.
func TestScenarioLifecycleAPIInstallPublishForkDeriveAndUpgrade(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	owner, envOwner := f.session(seed.ScenarioOwnerID), f.session(seed.EnvironmentOwnerID)
	data := func(method, path string, body any, cookie *http.Cookie, want int) map[string]any {
		t.Helper()
		response := f.request(method, path, body, cookie)
		if response.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
		}
		return decodeEnvelope(t, response)["data"].(map[string]any)
	}
	cleanEnvironment := func(id string) {
		t.Helper()
		template, err := f.database.GetEnvironmentRevision(ctx, "environment-test-r1")
		if err != nil {
			t.Fatal(err)
		}
		template.ID, template.EnvironmentID, template.Revision = id+"-r1", id, 1
		template.CreatedAt = time.Now().UTC()
		if err := f.database.CreateEnvironment(ctx, domain.Environment{ID: id, Name: id, OwnerID: seed.EnvironmentOwnerID, CreatedAt: template.CreatedAt, UpdatedAt: template.CreatedAt}, template); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"lifecycle-test-first", "lifecycle-upgrade-test", "lifecycle-test-next", "lifecycle-formal"} {
		cleanEnvironment(id)
	}
	created := data(http.MethodPost, "/api/v1/scenarios", map[string]any{"name": "Business lifecycle", "slug": "business-lifecycle"}, owner, http.StatusCreated)
	scenarioID, revisionID := created["id"].(string), created["currentRevisionId"].(string)
	acceptanceURL := "/api/v1/scenario-revisions/" + revisionID + "/acceptance"
	definition := data(http.MethodGet, acceptanceURL, nil, owner, http.StatusOK)
	graph := map[string]any{"nodes": []any{map[string]any{"id": "runtime", "type": "component", "position": map[string]any{"x": 0, "y": 0}, "data": map[string]any{"label": "Runtime", "releaseId": "release-test-runtime-1.0.0", "action": "install", "parameterValues": map[string]any{}, "dependencySources": map[string]any{}}}}, "edges": []any{}}
	data(http.MethodPut, "/api/v1/scenario-revisions/"+revisionID+"/graph", map[string]any{"expectedDigest": definition["revisionDigest"], "graph": graph, "environmentConstraints": map[string]any{}}, owner, http.StatusOK)
	definition = data(http.MethodGet, acceptanceURL, nil, owner, http.StatusOK)
	definition = data(http.MethodPut, acceptanceURL, map[string]any{"expectedRevisionDigest": definition["revisionDigest"], "jobs": []any{map[string]any{"id": "business", "name": "Business API", "purpose": "Verify complete service", "hostGroup": "test_nodes", "timeoutSeconds": 60, "riskLevel": "low"}}, "parameters": []any{}, "values": map[string]any{}, "bindings": []any{}}, owner, http.StatusOK)
	workspace := definition["workspace"].(map[string]any)
	data(http.MethodPut, acceptanceURL+"/workspace/file", map[string]any{"path": "tasks/acceptance/business.yml", "content": "- ansible.builtin.assert:\n    that: true\n", "expectedRevisionDigest": definition["revisionDigest"], "expectedSha256": "", "expectedTreeSha256": workspace["treeSha256"]}, owner, http.StatusOK)

	sequence := 0
	run := func(revision, environment, mode string, testOnly bool) (string, map[string]any) {
		t.Helper()
		sequence++
		input := map[string]any{"environmentId": environment, "executionMode": mode, "testOnly": testOnly, "idempotencyKey": fmt.Sprintf("lifecycle-%d", sequence)}
		preview := data(http.MethodPost, "/api/v1/scenario-revisions/"+revision+"/execution-plan", input, owner, http.StatusOK)
		if preview["ready"] != true {
			t.Fatalf("lifecycle plan is blocked: %#v", preview)
		}
		input["expectedPlanDigest"] = preview["planDigest"]
		endpoint := "runs"
		if testOnly {
			endpoint = "test-runs"
		}
		started := data(http.MethodPost, "/api/v1/scenario-revisions/"+revision+"/"+endpoint, input, owner, http.StatusAccepted)
		id := started["id"].(string)
		if started["status"] == string(domain.RunAwaitingApproval) {
			approval := started["approval"].(map[string]any)
			data(http.MethodPost, "/api/v1/approvals/"+approval["id"].(string)+"/approve", map[string]any{"reason": "Approve lifecycle fixture"}, envOwner, http.StatusOK)
		}
		waitForRun(t, f.database, id, domain.RunSucceeded)
		stored, err := f.database.GetRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, step := range stored.Steps {
			if step.NodeID == "acceptance:business" {
				// Acceptance is a stage in the Run's single Ansible process.
				// Its persisted recap and timestamps prove stage completion;
				// only the complete job has a process exit code.
				found = step.Status == domain.RunSucceeded && step.ExitCode == nil && step.Summary != "" &&
					step.StartedAt != nil && step.FinishedAt != nil && !step.FinishedAt.Before(*step.StartedAt)
			}
		}
		if !found {
			t.Fatalf("successful Run has no persisted successful acceptance: %+v", stored.Steps)
		}
		return id, preview
	}
	firstTest, _ := run(revisionID, "lifecycle-test-first", "install", true)
	data(http.MethodPost, "/api/v1/scenario-revisions/"+revisionID+"/publish", nil, owner, http.StatusOK)
	// A successful test permits publication but cannot qualify a version source.
	if blocked := f.request(http.MethodPost, "/api/v1/scenarios/"+scenarioID+"/revision-clone-plan", map[string]any{"sourceRevisionId": revisionID, "sourceRunId": firstTest}, owner); blocked.Code != http.StatusConflict {
		t.Fatalf("test-only source qualified: %d %s", blocked.Code, blocked.Body)
	}

	branchOwner := domain.User{ID: "lifecycle-branch-owner", Name: "Branch Owner", Role: domain.RoleScenarioOwner, CreatedAt: time.Now().UTC()}
	if err := f.database.UpsertUser(ctx, branchOwner); err != nil {
		t.Fatal(err)
	}
	branchCookie := f.session(branchOwner.ID)
	forkInput := map[string]any{"sourceRevisionId": revisionID, "name": "Business branch", "slug": "business-branch"}
	forkPreview := data(http.MethodPost, "/api/v1/scenarios/fork-plan", forkInput, branchCookie, http.StatusOK)
	forkInput["expectedPlanDigest"] = forkPreview["planDigest"]
	fork := data(http.MethodPost, "/api/v1/scenarios/forks", forkInput, branchCookie, http.StatusCreated)
	if fork["ownerId"] != branchOwner.ID || fork["forkedFromRevisionId"] != revisionID || fork["id"] == scenarioID {
		t.Fatalf("incorrect branch identity: %#v", fork)
	}
	forkRevision, err := f.database.GetScenarioRevision(ctx, fork["currentRevisionId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if forkRevision.Revision != 1 || forkRevision.Status != domain.RevisionDraft || forkRevision.SourceRevisionID != "" || forkRevision.SourceRunID != "" || forkRevision.TestPassedAt != nil {
		t.Fatalf("branch inherited lifecycle evidence: %+v", forkRevision)
	}

	formalSource, _ := run(revisionID, "lifecycle-upgrade-test", "install", false)
	run(revisionID, "lifecycle-formal", "install", false)
	cloneInput := map[string]any{"sourceRevisionId": revisionID, "sourceRunId": formalSource}
	clonePreview := data(http.MethodPost, "/api/v1/scenarios/"+scenarioID+"/revision-clone-plan", cloneInput, owner, http.StatusOK)
	if clonePreview["sourceRunId"] != formalSource {
		t.Fatalf("clone lost formal source Run: %#v", clonePreview)
	}
	cloneInput["expectedPlanDigest"] = clonePreview["planDigest"]
	next := data(http.MethodPost, "/api/v1/scenarios/"+scenarioID+"/revisions", cloneInput, owner, http.StatusCreated)
	nextID := next["id"].(string)
	if duplicate := f.request(http.MethodPost, "/api/v1/scenarios/"+scenarioID+"/revisions", cloneInput, owner); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate active draft=%d %s", duplicate.Code, duplicate.Body)
	}
	if next["sourceRevisionId"] != revisionID || next["sourceRunId"] != formalSource {
		t.Fatalf("derived source identity=%#v", next)
	}
	run(nextID, "lifecycle-test-next", "install", true)
	if missingUpgrade := f.request(http.MethodPost, "/api/v1/scenario-revisions/"+nextID+"/publish", nil, owner); missingUpgrade.Code != http.StatusConflict {
		t.Fatalf("derived version published without upgrade evidence: %d %s", missingUpgrade.Code, missingUpgrade.Body)
	}
	_, upgradePreview := run(nextID, "lifecycle-upgrade-test", "upgrade", true)
	for _, raw := range upgradePreview["steps"].([]any) {
		if raw.(map[string]any)["action"] == "install" {
			t.Fatalf("unchanged component reinstalled during upgrade: %#v", upgradePreview)
		}
	}
	data(http.MethodPost, "/api/v1/scenario-revisions/"+nextID+"/publish", nil, owner, http.StatusOK)
	formalUpgrade, _ := run(nextID, "lifecycle-formal", "upgrade", false)
	installed, err := f.database.GetScenarioInstallation(ctx, "lifecycle-formal", scenarioID)
	if err != nil {
		t.Fatal(err)
	}
	if installed.RevisionID != nextID || installed.RunID != formalUpgrade || installed.State != "complete" || installed.TestOnly {
		t.Fatalf("formal upgrade baseline=%+v", installed)
	}
	tested, err := f.database.GetScenarioInstallation(ctx, "lifecycle-upgrade-test", scenarioID)
	if err != nil {
		t.Fatal(err)
	}
	if tested.State != "test" || !tested.TestOnly {
		t.Fatalf("upgrade test became formal: %+v", tested)
	}
	// A formal upgrade is itself a valid source for the next direct version.
	third := data(http.MethodPost, "/api/v1/scenarios/"+scenarioID+"/revision-clone-plan", map[string]any{"sourceRevisionId": nextID, "sourceRunId": formalUpgrade}, owner, http.StatusOK)
	if third["sourceRunId"] != formalUpgrade {
		t.Fatalf("formal upgrade cannot be a source: %#v", third)
	}
}

func (f *apiFixture) cleanScenarioEnvironment(id string) {
	f.t.Helper()
	ctx := context.Background()
	template, err := f.database.GetEnvironmentRevision(ctx, "environment-test-r1")
	if err != nil {
		f.t.Fatal(err)
	}
	template.ID, template.EnvironmentID, template.Revision = id+"-r1", id, 1
	template.CreatedAt = time.Now().UTC()
	if err := f.database.CreateEnvironment(ctx, domain.Environment{ID: id, Name: id, OwnerID: seed.EnvironmentOwnerID, CreatedAt: template.CreatedAt, UpdatedAt: template.CreatedAt}, template); err != nil {
		f.t.Fatal(err)
	}
}

func (f *apiFixture) authorScenarioAcceptance(revisionID string, risk domain.RiskLevel) {
	f.t.Helper()
	owner := f.session(seed.ScenarioOwnerID)
	base := "/api/v1/scenario-revisions/" + revisionID + "/acceptance"
	read := f.request(http.MethodGet, base, nil, owner)
	if read.Code != http.StatusOK {
		f.t.Fatalf("read acceptance=%d %s", read.Code, read.Body)
	}
	definition := decodeEnvelope(f.t, read)["data"].(map[string]any)
	saved := f.request(http.MethodPut, base, map[string]any{"expectedRevisionDigest": definition["revisionDigest"], "jobs": []any{map[string]any{"id": "business", "name": "Business verification", "purpose": "Verify business behavior", "hostGroup": "test_nodes", "timeoutSeconds": 60, "riskLevel": risk, "mayMutate": risk != domain.RiskLow}}, "parameters": []any{}, "values": map[string]any{}, "bindings": []any{}}, owner)
	if saved.Code != http.StatusOK {
		f.t.Fatalf("save acceptance=%d %s", saved.Code, saved.Body)
	}
	definition = decodeEnvelope(f.t, saved)["data"].(map[string]any)
	workspace := definition["workspace"].(map[string]any)
	file := f.request(http.MethodPut, base+"/workspace/file", map[string]any{"path": "tasks/acceptance/business.yml", "content": "- ansible.builtin.assert:\n    that: true\n", "expectedRevisionDigest": definition["revisionDigest"], "expectedSha256": "", "expectedTreeSha256": workspace["treeSha256"]}, owner)
	if file.Code != http.StatusOK {
		f.t.Fatalf("save acceptance file=%d %s", file.Code, file.Body)
	}
}

func (f *apiFixture) submitScenarioInstallTest(revisionID, environmentID, key string) *httptest.ResponseRecorder {
	f.t.Helper()
	owner := f.session(seed.ScenarioOwnerID)
	input := map[string]any{"environmentId": environmentID, "executionMode": "install", "testOnly": true, "idempotencyKey": key}
	preview := f.request(http.MethodPost, "/api/v1/scenario-revisions/"+revisionID+"/execution-plan", input, owner)
	if preview.Code != http.StatusOK {
		f.t.Fatalf("scenario preview=%d %s", preview.Code, preview.Body)
	}
	data := decodeEnvelope(f.t, preview)["data"].(map[string]any)
	if data["ready"] != true {
		f.t.Fatalf("scenario blocked: %#v", data)
	}
	input["expectedPlanDigest"] = data["planDigest"]
	return f.request(http.MethodPost, "/api/v1/scenario-revisions/"+revisionID+"/test-runs", input, owner)
}
