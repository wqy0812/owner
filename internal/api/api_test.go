package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
)

type fakeRunner struct {
	mu       sync.Mutex
	calls    []string
	requests []ansiblerunner.Request
}

func (f *fakeRunner) Digest(playbook string) (string, string, error) {
	return "playbook:" + playbook, "fixed-tree-digest", nil
}

func (f *fakeRunner) Run(_ context.Context, request ansiblerunner.Request) (ansiblerunner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, request.Playbook)
	f.requests = append(f.requests, request)
	f.mu.Unlock()
	if request.LogSink != nil {
		request.LogSink(ansiblerunner.LogEvent{Time: time.Now().UTC(), Phase: ansiblerunner.PhaseExecute, Stream: ansiblerunner.StreamStdout, Line: "fake execution succeeded"})
	}
	return ansiblerunner.Result{
		TreeSHA256: "fixed-tree-digest", Successful: true,
		Phases: []ansiblerunner.PhaseResult{{Phase: ansiblerunner.PhaseExecute, ExitCode: 0, Successful: true}},
		Recap:  map[string]ansiblerunner.HostRecap{"localhost": {OK: 1}},
	}, nil
}

type apiFixture struct {
	t        *testing.T
	database *store.Store
	platform *service.Platform
	handler  http.Handler
	runner   *fakeRunner
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	database, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := (seed.Seeder{Store: database, Now: func() time.Time { return time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC) }}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	platform := service.NewPlatform(database, runner, service.NewEventHub())
	if err := platform.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture := &apiFixture{t: t, database: database, platform: platform, runner: runner}
	fixture.handler = NewHandler(platform, http.NotFoundHandler())
	t.Cleanup(func() {
		platform.Close()
		_ = database.Close()
	})
	return fixture
}

func (f *apiFixture) session(userID string) *http.Cookie {
	f.t.Helper()
	response := f.request(http.MethodPost, "/api/v1/session/switch", map[string]any{"userId": userID}, nil)
	if response.Code != http.StatusOK {
		f.t.Fatalf("switch %s: status=%d body=%s", userID, response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		f.t.Fatalf("unsafe demo session cookie: %#v", cookies)
	}
	return cookies[0]
}

func (f *apiFixture) request(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	var input *bytes.Reader
	if body == nil {
		input = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		input = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, input)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func decodeEnvelope(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var output map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return output
}

func TestRBACOwnerIsolationAndCredentialRedaction(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	bob := f.session(seed.ComponentOwnerK8sID)
	carol := f.session(seed.ScenarioOwnerID)
	dave := f.session(seed.EnvironmentOwnerID)

	created := f.request(http.MethodPost, "/api/v1/components", map[string]any{"name": "Alice private", "slug": "alice-private"}, alice)
	if created.Code != http.StatusCreated {
		t.Fatalf("owner create status=%d body=%s", created.Code, created.Body.String())
	}
	createdData := decodeEnvelope(t, created)["data"].(map[string]any)
	componentID := createdData["id"].(string)
	if response := f.request(http.MethodPatch, "/api/v1/components/"+componentID, map[string]any{"name": "hijacked"}, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other component owner update status=%d", response.Code)
	}
	if response := f.request(http.MethodPost, "/api/v1/components", map[string]any{"name": "bad", "slug": "bad"}, carol); response.Code != http.StatusForbidden {
		t.Fatalf("scenario owner component create status=%d", response.Code)
	}
	if response := f.request(http.MethodPut, "/api/v1/environments/environment-local/parameters", map[string]any{"parameters": map[string]any{"region": "cn"}}, alice); response.Code != http.StatusForbidden {
		t.Fatalf("component owner environment update status=%d", response.Code)
	}

	secret := "this-secret-must-not-be-persisted"
	rejected := f.request(http.MethodPut, "/api/v1/environments/environment-local/parameters", map[string]any{"parameters": map[string]any{"registryPassword": secret}}, dave)
	if rejected.Code != http.StatusBadRequest || strings.Contains(rejected.Body.String(), secret) {
		t.Fatalf("secret parameter response status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	updated := f.request(http.MethodPut, "/api/v1/environments/environment-local/credential-refs", map[string]any{"credentialRefs": []any{map[string]any{"name": "registry", "type": "envVarRef", "reference": "DEMO_REGISTRY_TOKEN"}}}, dave)
	if updated.Code != http.StatusOK {
		t.Fatalf("credential ref update status=%d body=%s", updated.Code, updated.Body.String())
	}
	visible := f.request(http.MethodGet, "/api/v1/environments", nil, alice)
	if visible.Code != http.StatusOK || strings.Contains(visible.Body.String(), "DEMO_REGISTRY_TOKEN") || !strings.Contains(visible.Body.String(), "maskedReference") {
		t.Fatalf("credential redaction failed: %s", visible.Body.String())
	}
	var count int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE metadata_json LIKE ?`, "%"+secret+"%").Scan(&count); err != nil || count != 0 {
		t.Fatalf("secret found in audit: count=%d err=%v", count, err)
	}
}

func TestReleaseImpactNotifiesDownstreamAndScenarioOwners(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	draftOnly := domain.Scenario{ID: "scenario-draft-only-impact", Slug: "draft-only-impact", Name: "Draft only impact", OwnerID: seed.ScenarioOwnerID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	draftRevision := domain.ScenarioRevision{
		ID: "scenario-draft-only-impact-r1", ScenarioID: draftOnly.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph:           domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "agent", Name: "Agent", ReleaseID: "release-demo-agent-1.0.0", Action: domain.ActionInstall, HostGroup: "demo_nodes"}}, Edges: []domain.ScenarioEdge{}},
		ExecutionPolicy: map[string]any{}, CreatedAt: draftOnly.CreatedAt,
	}
	if err := f.database.CreateScenario(context.Background(), draftOnly, draftRevision); err != nil {
		t.Fatal(err)
	}
	response := f.request(http.MethodPost, "/api/v1/component-releases/release-demo-agent-1.1.0/publish", nil, alice)
	if response.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", response.Code, response.Body.String())
	}
	published := decodeEnvelope(t, response)["data"].(map[string]any)
	if published["status"] != string(domain.ReleaseReleased) || published["verified"] != false {
		t.Fatalf("unexpected unverified publish: %#v", published)
	}
	for _, userID := range []string{seed.ComponentOwnerK8sID, seed.ScenarioOwnerID} {
		cookie := f.session(userID)
		notifications := f.request(http.MethodGet, "/api/v1/notifications", nil, cookie)
		expectedPath := "Demo Policy Bundle"
		if userID == seed.ScenarioOwnerID {
			expectedPath = "scenario-demo-agent"
		}
		if notifications.Code != http.StatusOK || !strings.Contains(notifications.Body.String(), expectedPath) || !strings.Contains(notifications.Body.String(), "v1.1.0") {
			t.Fatalf("notification for %s: %s", userID, notifications.Body.String())
		}
		if userID == seed.ScenarioOwnerID && !strings.Contains(notifications.Body.String(), draftOnly.ID) {
			t.Fatalf("draft-only scenario did not receive impact routing: %s", notifications.Body.String())
		}
	}
}

func TestEditingDraftReleaseInvalidatesVerification(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	createdComponent := f.request(http.MethodPost, "/api/v1/components", map[string]any{"name": "Mutable", "slug": "mutable"}, alice)
	componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
	definition := map[string]any{
		"version": "1.0.0", "type": "atomic", "parameterSchema": map[string]any{},
		"actions": []any{map[string]any{"name": "install", "kind": "install", "playbook": "demo-node-agent/install.yml", "timeoutSeconds": 60}},
	}
	createdRelease := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", definition, alice)
	if createdRelease.Code != http.StatusCreated {
		t.Fatalf("create release status=%d body=%s", createdRelease.Code, createdRelease.Body.String())
	}
	releaseID := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["id"].(string)
	if err := f.database.MarkReleaseVerified(context.Background(), releaseID, true); err != nil {
		t.Fatal(err)
	}
	definition["releaseNotes"] = "changed after test"
	updated := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID, definition, alice)
	if updated.Code != http.StatusOK {
		t.Fatalf("update release status=%d body=%s", updated.Code, updated.Body.String())
	}
	if verified := decodeEnvelope(t, updated)["data"].(map[string]any)["verified"]; verified != false {
		t.Fatalf("draft update retained stale verification: %v", verified)
	}
	release, _ := f.database.GetComponentRelease(context.Background(), releaseID)
	queued := domain.Run{
		ID: "active-edit-guard", Kind: domain.RunComponentTest, Status: domain.RunQueued,
		RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: "environment-local", EnvironmentRevisionID: "environment-local-r1",
		ComponentReleaseID: releaseID, InputSnapshot: map[string]any{}, CreatedAt: time.Now().UTC(),
	}
	if err := f.database.CreateRun(context.Background(), queued, nil); err != nil {
		t.Fatal(err)
	}
	owner, _ := f.database.GetUser(context.Background(), seed.ComponentOwnerRuntimeID)
	if _, err := f.platform.UpdateRelease(context.Background(), owner, releaseID, release); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("draft edit during active component test=%v, want conflict", err)
	}
}

func TestPublishRejectsCrossReleaseTransitionMismatch(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	componentResponse := f.request(http.MethodPost, "/api/v1/components", map[string]any{"name": "Transitions", "slug": "transitions"}, alice)
	componentID := decodeEnvelope(t, componentResponse)["data"].(map[string]any)["id"].(string)
	oldResponse := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", map[string]any{
		"version": "1.0.0", "type": "atomic",
		"actions": []any{map[string]any{"name": "install", "kind": "install", "playbook": "demo-node-agent/install-v1.0.yml", "timeoutSeconds": 60}},
	}, alice)
	oldID := decodeEnvelope(t, oldResponse)["data"].(map[string]any)["id"].(string)
	if response := f.request(http.MethodPost, "/api/v1/component-releases/"+oldID+"/publish", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("publish old release status=%d body=%s", response.Code, response.Body.String())
	}
	newResponse := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", map[string]any{
		"version": "2.0.0", "type": "atomic",
		"actions": []any{map[string]any{
			"name": "upgrade", "kind": "upgrade", "playbook": "demo-node-agent/upgrade-v1.1.yml", "timeoutSeconds": 60,
			"fromReleaseId": oldID, "toReleaseId": "another-release",
		}},
	}, alice)
	newID := decodeEnvelope(t, newResponse)["data"].(map[string]any)["id"].(string)
	badPublish := f.request(http.MethodPost, "/api/v1/component-releases/"+newID+"/publish", nil, alice)
	if badPublish.Code != http.StatusBadRequest {
		t.Fatalf("mismatched transition publish status=%d body=%s", badPublish.Code, badPublish.Body.String())
	}
}

func TestOnlyCurrentScenarioRevisionCanBeMutatedOrTested(t *testing.T) {
	f := newAPIFixture(t)
	carol := f.session(seed.ScenarioOwnerID)
	cloned := f.request(http.MethodPost, "/api/v1/scenarios/scenario-demo-agent/revisions", nil, carol)
	if cloned.Code != http.StatusCreated {
		t.Fatalf("clone revision status=%d body=%s", cloned.Code, cloned.Body.String())
	}
	oldGraphEdit := f.request(http.MethodPut, "/api/v1/scenario-revisions/scenario-demo-agent-r2/graph", map[string]any{
		"nodes": []any{map[string]any{"id": "old", "releaseId": "release-demo-agent-1.0.0", "action": "install", "hostGroup": "demo_nodes"}},
		"edges": []any{},
	}, carol)
	if oldGraphEdit.Code != http.StatusConflict {
		t.Fatalf("non-current graph edit status=%d body=%s", oldGraphEdit.Code, oldGraphEdit.Body.String())
	}
	oldTest := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-demo-agent-r2/test-runs", map[string]any{"environmentId": "environment-local"}, carol)
	if oldTest.Code != http.StatusConflict {
		t.Fatalf("non-current scenario test status=%d body=%s", oldTest.Code, oldTest.Body.String())
	}
}

func TestReleasedScenarioRetainsDeprecatedLockedComponent(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	carol := f.session(seed.ScenarioOwnerID)
	if response := f.request(http.MethodPost, "/api/v1/component-releases/release-demo-agent-1.0.0/deprecate", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("deprecate release status=%d body=%s", response.Code, response.Body.String())
	}
	runResponse := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-demo-agent-r1/runs", map[string]any{"environmentId": "environment-local"}, carol)
	if runResponse.Code != http.StatusAccepted {
		t.Fatalf("run released scenario with deprecated lock status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	runID := decodeEnvelope(t, runResponse)["data"].(map[string]any)["id"].(string)
	waitForRun(t, f.database, runID, domain.RunSucceeded)
}

func TestRollbackVerifiesTargetReleaseDefaults(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	carol := f.session(seed.ScenarioOwnerID)
	if response := f.request(http.MethodPost, "/api/v1/component-releases/release-demo-agent-1.1.0/publish", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("publish rollback source status=%d body=%s", response.Code, response.Body.String())
	}
	now := time.Now().UTC()
	scenario := domain.Scenario{ID: "scenario-rollback-target", Slug: "rollback-target", Name: "Rollback target", OwnerID: seed.ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	revision := domain.ScenarioRevision{
		ID: "scenario-rollback-target-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{
			ID: "rollback", Name: "Rollback", ReleaseID: "release-demo-agent-1.1.0", Action: domain.ActionRollback,
			HostGroup: "demo_nodes", Values: map[string]any{}, Bindings: map[string]string{}, RunInputs: []string{"agent_root"},
		}}, Edges: []domain.ScenarioEdge{}}, ExecutionPolicy: map[string]any{}, CreatedAt: now,
	}
	if err := f.database.CreateScenario(context.Background(), scenario, revision); err != nil {
		t.Fatal(err)
	}
	runResponse := f.request(http.MethodPost, "/api/v1/scenario-revisions/"+revision.ID+"/test-runs", map[string]any{"environmentId": "environment-local"}, carol)
	if runResponse.Code != http.StatusAccepted {
		t.Fatalf("rollback test status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	runID := decodeEnvelope(t, runResponse)["data"].(map[string]any)["id"].(string)
	waitForRun(t, f.database, runID, domain.RunSucceeded)
	f.runner.mu.Lock()
	requests := append([]ansiblerunner.Request(nil), f.runner.requests...)
	f.runner.mu.Unlock()
	if len(requests) != 2 || requests[0].Playbook != "demo-node-agent/rollback-v1.0.yml" || requests[1].Playbook != "demo-node-agent/verify.yml" || requests[1].Variables["expected_version"] != "1.0.0" {
		t.Fatalf("rollback execution requests=%+v", requests)
	}
}

func TestScenarioTestGateAndDestructiveApproval(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	carol := f.session(seed.ScenarioOwnerID)
	dave := f.session(seed.EnvironmentOwnerID)

	if response := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-demo-agent-r2/publish", nil, carol); response.Code != http.StatusConflict {
		t.Fatalf("untested scenario publish status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPost, "/api/v1/component-releases/release-demo-agent-1.1.0/publish", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("component publish: %s", response.Body.String())
	}
	testRun := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-demo-agent-r2/test-runs", map[string]any{"environmentId": "environment-local"}, carol)
	if testRun.Code != http.StatusAccepted {
		t.Fatalf("scenario test status=%d body=%s", testRun.Code, testRun.Body.String())
	}
	runID := decodeEnvelope(t, testRun)["data"].(map[string]any)["id"].(string)
	if response := f.request(http.MethodGet, "/api/v1/runs/"+runID, nil, alice); response.Code != http.StatusOK {
		t.Fatalf("component owner cannot view scenario run that locks its release: status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodGet, "/api/v1/runs/"+runID, nil, f.session(seed.ComponentOwnerK8sID)); response.Code != http.StatusForbidden {
		t.Fatalf("unrelated component owner scenario run status=%d", response.Code)
	}
	if response := f.request(http.MethodGet, "/api/v1/runs", nil, alice); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), runID) {
		t.Fatalf("component owner related run list status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodGet, "/api/v1/runs", nil, f.session(seed.ComponentOwnerK8sID)); response.Code != http.StatusOK || strings.Contains(response.Body.String(), runID) {
		t.Fatalf("unrelated component owner run list status=%d body=%s", response.Code, response.Body.String())
	}
	waitForRun(t, f.database, runID, domain.RunSucceeded)
	if response := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-demo-agent-r2/publish", nil, carol); response.Code != http.StatusOK {
		t.Fatalf("tested scenario publish status=%d body=%s", response.Code, response.Body.String())
	}

	destructive := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-openfuyao-r1/test-runs", map[string]any{"environmentId": "environment-openfuyao-template"}, carol)
	if destructive.Code != http.StatusAccepted {
		t.Fatalf("destructive request status=%d body=%s", destructive.Code, destructive.Body.String())
	}
	runData := decodeEnvelope(t, destructive)["data"].(map[string]any)
	if runData["status"] != string(domain.RunAwaitingApproval) || runData["destructive"] != true {
		t.Fatalf("destructive run bypassed approval: %#v", runData)
	}
	approval := runData["approval"].(map[string]any)
	approvalID := approval["id"].(string)
	if response := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/approve", nil, alice); response.Code != http.StatusForbidden {
		t.Fatalf("component owner approval status=%d", response.Code)
	}
	if response := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/reject", map[string]any{"reason": "missing private artifacts"}, dave); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), string(domain.RunRejected)) {
		t.Fatalf("environment rejection status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSeededKubernetes1175ScenarioRequiresApprovalBeforeRunner(t *testing.T) {
	f := newAPIFixture(t)
	carol := f.session(seed.ScenarioOwnerID)

	scenario := f.request(http.MethodGet, "/api/v1/scenarios/scenario-k8s-1.17.5", nil, carol)
	if scenario.Code != http.StatusOK {
		t.Fatalf("get seeded Kubernetes 1.17.5 scenario status=%d body=%s", scenario.Code, scenario.Body.String())
	}
	scenarioData := decodeEnvelope(t, scenario)["data"].(map[string]any)
	if scenarioData["currentRevisionId"] != "scenario-k8s-1.17.5-r1" {
		t.Fatalf("unexpected seeded Kubernetes 1.17.5 revision: %#v", scenarioData)
	}
	currentRevision, ok := scenarioData["currentRevision"].(map[string]any)
	if !ok || len(currentRevision["nodes"].([]any)) != 4 || len(currentRevision["edges"].([]any)) != 3 {
		t.Fatalf("unexpected Kubernetes 1.17.5 DAG: %#v", currentRevision)
	}

	f.runner.mu.Lock()
	beforeCalls, beforeRequests := len(f.runner.calls), len(f.runner.requests)
	f.runner.mu.Unlock()
	runResponse := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-k8s-1.17.5-r1/test-runs", map[string]any{
		"environmentId": "environment-k8s-1.17.5-template",
	}, carol)
	if runResponse.Code != http.StatusAccepted {
		t.Fatalf("Kubernetes 1.17.5 test run status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	runData := decodeEnvelope(t, runResponse)["data"].(map[string]any)
	if runData["status"] != string(domain.RunAwaitingApproval) || runData["destructive"] != true {
		t.Fatalf("Kubernetes 1.17.5 run bypassed destructive approval: %#v", runData)
	}
	if runData["scenarioRevisionId"] != "scenario-k8s-1.17.5-r1" || runData["environmentRevisionId"] != "environment-k8s-1.17.5-template-r1" {
		t.Fatalf("Kubernetes 1.17.5 run did not lock seeded revisions: %#v", runData)
	}
	storedRun, err := f.database.GetRun(context.Background(), runData["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	lockedSteps, ok := storedRun.InputSnapshot["steps"].([]any)
	if !ok || len(lockedSteps) != 4 {
		t.Fatalf("Kubernetes 1.17.5 locked plan=%#v", storedRun.InputSnapshot)
	}
	wantPlaybooks := []string{
		"k8s-1.17.5-cluster/cert_1175.yml",
		"k8s-1.17.5-cluster/etcd_serverless.yml",
		"k8s-1.17.5-cluster/master_1175.yml",
		"k8s-1.17.5-cluster/node_1175.yml",
	}
	wantLimits := []string{"k8s_cert_controller", "k8setcd", "k8smaster", "k8snode"}
	for index, rawStep := range lockedSteps {
		step, ok := rawStep.(map[string]any)
		if !ok || step["playbook"] != wantPlaybooks[index] || step["limit"] != wantLimits[index] {
			t.Fatalf("locked step %d=%#v", index, rawStep)
		}
		tags, ok := step["tags"].([]any)
		if !ok || len(tags) != 1 || tags[0] != "install" {
			t.Fatalf("locked step %d has unsafe tags: %#v", index, step["tags"])
		}
		variables, _ := step["variables"].(map[string]any)
		if _, leaked := variables["K8S_ENCRYPTION_KEY"]; leaked {
			t.Fatalf("runtime encryption key leaked into locked step %d", index)
		}
	}
	snapshotJSON, err := json.Marshal(storedRun.InputSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(snapshotJSON), "NEWPLATFORM_K8S1175_ENCRYPTION_KEY") {
		t.Fatalf("credential environment-variable reference leaked into run snapshot: %s", snapshotJSON)
	}

	f.runner.mu.Lock()
	afterCalls, afterRequests := len(f.runner.calls), len(f.runner.requests)
	f.runner.mu.Unlock()
	if afterCalls != beforeCalls || afterRequests != beforeRequests {
		t.Fatalf("runner invoked before Kubernetes 1.17.5 approval: calls %d -> %d, requests %d -> %d", beforeCalls, afterCalls, beforeRequests, afterRequests)
	}
}

func waitForRun(t *testing.T, database *store.Store, runID string, status domain.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := database.GetRun(context.Background(), runID)
		if err == nil && run.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, err := database.GetRun(context.Background(), runID)
	t.Fatalf("run %s status=%s err=%v, want %s", runID, run.Status, err, status)
}
