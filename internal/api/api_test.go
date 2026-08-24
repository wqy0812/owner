package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	ansiblerunner "codex/platform-demo/internal/ansible"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/fss"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
)

func TestEnvironmentMaintenanceHealthRevisionHistoryAndRestore(t *testing.T) {
	f := newAPIFixture(t)
	f.platform.ConfigureEnvironmentHealthDialer(func(_ context.Context, _, _ string) (net.Conn, error) {
		left, right := net.Pipe()
		_ = right.Close()
		return left, nil
	})
	owner := f.session(seed.EnvironmentOwnerID)
	alice := f.session(seed.ComponentOwnerRuntimeID)

	updated := f.request(http.MethodPut, "/api/v1/environments/environment-test/facts", map[string]any{
		"facts": map[string]any{"architecture": "amd64"}, "changeReason": "校正架构事实",
	}, owner)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "校正架构事实") {
		t.Fatalf("environment revision update status=%d body=%s", updated.Code, updated.Body.String())
	}

	listed := decodeEnvelope(t, f.request(http.MethodGet, "/api/v1/environments", nil, owner))["items"].([]any)
	var environment map[string]any
	for _, item := range listed {
		candidate := item.(map[string]any)
		if candidate["id"] == "environment-test" {
			environment = candidate
			break
		}
	}
	if environment == nil {
		t.Fatal("environment-test missing from environment list")
	}
	revisions := environment["revisions"].([]any)
	if len(revisions) != 2 || revisions[0].(map[string]any)["changeReason"] != "校正架构事实" {
		t.Fatalf("environment revisions=%#v", revisions)
	}
	sourceID := revisions[1].(map[string]any)["id"].(string)
	restored := f.request(http.MethodPost, "/api/v1/environments/environment-test/revisions/"+sourceID+"/restore", map[string]any{"changeReason": "恢复初始配置"}, owner)
	if restored.Code != http.StatusOK || !strings.Contains(restored.Body.String(), "恢复初始配置") {
		t.Fatalf("restore revision status=%d body=%s", restored.Code, restored.Body.String())
	}

	health := f.request(http.MethodPost, "/api/v1/environments/environment-test/health-checks", nil, owner)
	if health.Code != http.StatusOK || !strings.Contains(health.Body.String(), `"status":"healthy"`) {
		t.Fatalf("health check status=%d body=%s", health.Code, health.Body.String())
	}
	if denied := f.request(http.MethodPost, "/api/v1/environments/environment-test/health-checks", nil, alice); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner health check status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestComponentArtifactUploadAndDetach(t *testing.T) {
	f := newAPIFixture(t)
	root := t.TempDir()
	stationHandler, err := fss.New(root, []string{"127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	stationServer := httptest.NewServer(stationHandler)
	defer stationServer.Close()
	station := strings.TrimPrefix(stationServer.URL, "http://")

	dave := f.session(seed.EnvironmentOwnerID)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	updated := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"IMAGE_REGISTRY": "192.168.88.54:5000", "FILE_STATION": station}}, dave)
	if updated.Code != http.StatusOK {
		t.Fatalf("configure file station status=%d body=%s", updated.Code, updated.Body.String())
	}

	contents := []byte("runtime-media")
	digest := sha256.Sum256(contents)
	checksum := fmt.Sprintf("%x", digest[:])
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("environmentId", "environment-test")
	_ = writer.WriteField("alias", "runtime_media")
	_ = writer.WriteField("sha256", checksum)
	part, _ := writer.CreateFormFile("artifact", "runtime.tar.gz")
	_, _ = part.Write(contents)
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/artifacts/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(alice)
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), checksum) {
		t.Fatalf("artifact upload status=%d body=%s", response.Code, response.Body.String())
	}
	release, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil || len(release.Artifacts) != 1 || release.Artifacts[0].RelativePath != "components/component-test-runtime/v1.1.0/runtime.tar.gz" {
		t.Fatalf("release artifacts=%+v err=%v", release.Artifacts, err)
	}
	if stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(release.Artifacts[0].RelativePath))); err != nil || !bytes.Equal(stored, contents) {
		t.Fatalf("stored artifact=%q err=%v", stored, err)
	}
	targetRoot := t.TempDir()
	targetHandler, err := fss.New(targetRoot, []string{"127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	targetServer := httptest.NewServer(targetHandler)
	defer targetServer.Close()
	targetStation := strings.TrimPrefix(targetServer.URL, "http://")
	targetRevision := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"IMAGE_REGISTRY": "192.168.88.54:5000", "FILE_STATION": targetStation}}, dave)
	if targetRevision.Code != http.StatusOK {
		t.Fatalf("configure target file station status=%d body=%s", targetRevision.Code, targetRevision.Body.String())
	}
	preview := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-plan", map[string]any{"environmentId": "environment-test", "mode": "install_verify"}, alice)
	if preview.Code != http.StatusOK {
		t.Fatalf("artifact transfer preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	plan := decodeEnvelope(t, preview)["data"].(map[string]any)
	if plan["requiresApproval"] != true {
		t.Fatalf("artifact transfer plan bypassed approval: %#v", plan)
	}
	runResponse := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{"environmentId": "environment-test", "mode": "install_verify", "expectedPlanDigest": plan["planDigest"]}, alice)
	if runResponse.Code != http.StatusAccepted {
		t.Fatalf("artifact transfer run status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	runData := decodeEnvelope(t, runResponse)["data"].(map[string]any)
	if runData["status"] != string(domain.RunAwaitingApproval) || !strings.Contains(runData["approval"].(map[string]any)["riskReason"].(string), "平移") {
		t.Fatalf("artifact transfer approval=%#v", runData)
	}
	approvalID := runData["approval"].(map[string]any)["id"].(string)
	if approved := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/approve", nil, dave); approved.Code != http.StatusOK {
		t.Fatalf("approve artifact transfer status=%d body=%s", approved.Code, approved.Body.String())
	}
	waitForRun(t, f.database, runData["id"].(string), domain.RunSucceeded)
	if mirrored, err := os.ReadFile(filepath.Join(targetRoot, filepath.FromSlash(release.Artifacts[0].RelativePath))); err != nil || !bytes.Equal(mirrored, contents) {
		t.Fatalf("mirrored artifact=%q err=%v", mirrored, err)
	}

	secondPreview := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-plan", map[string]any{"environmentId": "environment-test", "mode": "install_verify"}, alice)
	if secondPreview.Code != http.StatusOK {
		t.Fatalf("reused artifact preview status=%d body=%s", secondPreview.Code, secondPreview.Body.String())
	}
	reusedPlan := decodeEnvelope(t, secondPreview)["data"].(map[string]any)
	if reusedPlan["requiresApproval"] != false {
		t.Fatalf("existing mirrored artifact requested approval again: %#v", reusedPlan)
	}

	detached := f.request(http.MethodDelete, "/api/v1/component-releases/release-test-runtime-1.1.0/artifacts/runtime_media", nil, alice)
	if detached.Code != http.StatusOK {
		t.Fatalf("artifact detach status=%d body=%s", detached.Code, detached.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(release.Artifacts[0].RelativePath))); err != nil {
		t.Fatalf("detach deleted physical media: %v", err)
	}
	imageHash := strings.Repeat("a", 64)
	imageBuild := domain.ComponentImageBuild{
		ID: "image-build-cross-registry", ReleaseID: release.ID, RequestedBy: seed.ComponentOwnerRuntimeID,
		Status: domain.ImageBuildSucceeded, DockerfileSHA256: strings.Repeat("b", 64), ImageTag: "v1.1.0",
		ImageRef:    "192.168.88.53:5000/components/component-test-runtime:v1.1.0",
		ImageDigest: "192.168.88.53:5000/components/component-test-runtime@sha256:" + imageHash,
		CreatedAt:   time.Now().UTC(), FinishedAt: func() *time.Time { value := time.Now().UTC(); return &value }(),
	}
	if err := f.database.CreateComponentImageBuild(context.Background(), imageBuild); err != nil {
		t.Fatal(err)
	}
	imagePreview := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-plan", map[string]any{"environmentId": "environment-test", "mode": "install_verify"}, alice)
	if imagePreview.Code != http.StatusOK || decodeEnvelope(t, imagePreview)["data"].(map[string]any)["requiresApproval"] != true {
		t.Fatalf("cross-registry image preview status=%d body=%s", imagePreview.Code, imagePreview.Body.String())
	}
	targetDigest := "192.168.88.54:5000/components/component-test-runtime@sha256:" + imageHash
	if err := f.database.RecordComponentImageMirror(context.Background(), "192.168.88.54:5000", imageBuild.ImageDigest, "192.168.88.54:5000/components/component-test-runtime:v1.1.0", targetDigest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	reusedImagePreview := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-plan", map[string]any{"environmentId": "environment-test", "mode": "install_verify"}, alice)
	if reusedImagePreview.Code != http.StatusOK || decodeEnvelope(t, reusedImagePreview)["data"].(map[string]any)["requiresApproval"] != false {
		t.Fatalf("mirrored image requested approval again status=%d body=%s", reusedImagePreview.Code, reusedImagePreview.Body.String())
	}
}

type fakeRunner struct {
	mu       sync.Mutex
	calls    []string
	requests []ansiblerunner.Request
}

func (f *fakeRunner) Digest(playbook string) (string, string, error) {
	return "playbook:" + playbook, "fixed-tree-digest", nil
}

func (f *fakeRunner) DigestPlan(playbooks []string) (map[string]string, string, error) {
	digests := make(map[string]string, len(playbooks))
	for _, playbook := range playbooks {
		digests[playbook] = "playbook:" + playbook
	}
	return digests, "fixed-tree-digest", nil
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
	seedAPITestFixtures(t, database)
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

func seedAPITestFixtures(t *testing.T, database *store.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)
	component := func(id, name, owner string) domain.Component {
		return domain.Component{
			ID: id, Slug: id, Name: name, Layer: domain.LayerRuntimeState, Category: domain.CategoryRuntime,
			Kind: domain.ComponentSoftware, Requiredness: domain.RequiredProfile,
			OwnerID: owner, CreatedAt: now, UpdatedAt: now,
		}
	}
	oldRelease := domain.ComponentRelease{
		ID: "release-test-runtime-1.0.0", ComponentID: "component-test-runtime", Version: "v1.0.0", Type: domain.ReleaseAtomic,
		Status: domain.ReleaseReleased, Verified: true, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now,
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{{Name: "expected_version", Description: "expected runtime version", Type: domain.ParameterTypeString, Required: true, DefaultValue: "1.0.0", Visibility: domain.ParameterInternal}},
		Actions: []domain.ActionDefinition{
			{ID: "action-test-runtime-install-1.0", ReleaseID: "release-test-runtime-1.0.0", Name: "install", Kind: domain.ActionInstall, Playbook: "tests/runtime/install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
			{ID: "action-test-runtime-verify-1.0", ReleaseID: "release-test-runtime-1.0.0", Name: "verify", Kind: domain.ActionVerify, Playbook: "tests/runtime/verify.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
		},
	}
	newRelease := domain.ComponentRelease{
		ID: "release-test-runtime-1.1.0", ComponentID: "component-test-runtime", Version: "v1.1.0", Type: domain.ReleaseAtomic,
		Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, CreatedAt: now.Add(time.Second),
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{{Name: "expected_version", Description: "expected runtime version", Type: domain.ParameterTypeString, Required: true, DefaultValue: "1.1.0", Visibility: domain.ParameterInternal}},
		Actions: []domain.ActionDefinition{
			{ID: "action-test-runtime-upgrade-1.1", ReleaseID: "release-test-runtime-1.1.0", Name: "upgrade", Kind: domain.ActionUpgrade, Playbook: "tests/runtime/upgrade.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, FromReleaseID: oldRelease.ID, ToReleaseID: "release-test-runtime-1.1.0"},
			{ID: "action-test-runtime-verify-1.1", ReleaseID: "release-test-runtime-1.1.0", Name: "verify", Kind: domain.ActionVerify, Playbook: "tests/runtime/verify.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
			{ID: "action-test-runtime-rollback-1.0", ReleaseID: "release-test-runtime-1.1.0", Name: "rollback", Kind: domain.ActionRollback, Playbook: "tests/runtime/rollback.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, FromReleaseID: "release-test-runtime-1.1.0", ToReleaseID: oldRelease.ID},
		},
	}
	if err := database.CreateComponent(ctx, component("component-test-runtime", "Test Runtime", seed.ComponentOwnerRuntimeID)); err != nil {
		t.Fatal(err)
	}
	for _, release := range []domain.ComponentRelease{oldRelease, newRelease} {
		if err := database.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
	}
	consumer := component("component-test-consumer", "Test Consumer", seed.ComponentOwnerK8sID)
	consumer.Layer, consumer.Category = domain.LayerPlatformExtension, domain.CategoryPlatform
	consumer.Kind = domain.ComponentSoftwareBundle
	if err := database.CreateComponent(ctx, consumer); err != nil {
		t.Fatal(err)
	}
	consumerRelease := domain.ComponentRelease{
		ID: "release-test-consumer-1.0.0", ComponentID: consumer.ID, Version: "v1.0.0", Type: domain.ReleaseBundle,
		Status: domain.ReleaseReleased, Verified: true, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now,
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{},
		Dependencies: []domain.ComponentDependency{{ID: "dependency-test-consumer-runtime", ReleaseID: "release-test-consumer-1.0.0", UpstreamComponentID: "component-test-runtime", UpstreamReleaseID: oldRelease.ID, Purpose: "runtime"}},
	}
	if err := database.CreateComponentRelease(ctx, consumerRelease); err != nil {
		t.Fatal(err)
	}
	testedAt, releasedAt := now, now
	scenario := domain.Scenario{ID: "scenario-test-runtime", Slug: "scenario-test-runtime", Name: "Test Runtime Lifecycle", OwnerID: seed.ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	revision1 := domain.ScenarioRevision{
		ID: "scenario-test-runtime-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionReleased,
		Graph:           domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime-install", Name: "Install runtime", ReleaseID: oldRelease.ID, Action: domain.ActionInstall, HostGroup: "test_nodes", Values: map[string]any{}}}, Edges: []domain.ScenarioEdge{}},
		ExecutionPolicy: map[string]any{}, CreatedAt: now, TestPassedAt: &testedAt, ReleasedAt: &releasedAt,
	}
	if err := database.CreateScenario(ctx, scenario, revision1); err != nil {
		t.Fatal(err)
	}
	revision2 := domain.ScenarioRevision{
		ID: "scenario-test-runtime-r2", ScenarioID: scenario.ID, Revision: 2, Status: domain.RevisionDraft,
		Graph:           domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime-upgrade", Name: "Upgrade runtime", ReleaseID: newRelease.ID, Action: domain.ActionUpgrade, HostGroup: "test_nodes", Values: map[string]any{}}}, Edges: []domain.ScenarioEdge{}},
		ExecutionPolicy: map[string]any{}, CreatedAt: now.Add(time.Second),
	}
	if err := database.CreateScenarioRevision(ctx, revision2); err != nil {
		t.Fatal(err)
	}
	inventory, _ := json.Marshal(map[string]any{"hosts": []any{map[string]any{"name": "localhost", "address": "127.0.0.1", "groups": []any{"test_nodes"}}}})
	environment := domain.Environment{ID: "environment-test", Name: "Test Environment", OwnerID: seed.EnvironmentOwnerID, CreatedAt: now, UpdatedAt: now}
	environmentRevision := domain.EnvironmentRevision{ID: "environment-test-r1", EnvironmentID: environment.ID, Revision: 1, Facts: map[string]any{}, Inventory: inventory, Variables: map[string]string{"IMAGE_REGISTRY": "192.168.88.54:5000"}, CredentialRefs: []domain.CredentialRef{}, MaxConcurrent: 1, CreatedAt: now}
	if err := database.CreateEnvironment(ctx, environment, environmentRevision); err != nil {
		t.Fatal(err)
	}
	// The fixture represents an environment currently running the Draft
	// candidate so rollback tests have an exact installation-scoped baseline.
	installedRun := domain.Run{
		ID: "run-installed-runtime-1.1", Kind: domain.RunComponentTest, Status: domain.RunSucceeded,
		RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: environment.ID, EnvironmentRevisionID: environmentRevision.ID,
		ComponentReleaseID: newRelease.ID, Action: domain.ActionUpgrade, InputSnapshot: map[string]any{},
		ArtifactDigest: "fixed-tree-digest", CreatedAt: now, StartedAt: &now, FinishedAt: &now,
	}
	if err := database.CreateRun(ctx, installedRun, nil); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertEnvironmentComponentInstallation(ctx, domain.EnvironmentComponentInstallation{
		EnvironmentID: environment.ID, ComponentID: newRelease.ComponentID, ReleaseID: newRelease.ID,
		InstallRunID: installedRun.ID,
		BackupRef:    "/var/lib/clusterforge/backups/environment-test/component-test-runtime/release-test-runtime-1.1.0/run-installed-runtime-1.1",
		Backup: domain.BackupMetadata{
			EnvironmentID: environment.ID, ComponentID: newRelease.ComponentID, ReleaseID: newRelease.ID,
			ActionID: "action-test-runtime-upgrade-1.1", InstallRunID: installedRun.ID, CapturedAt: now,
			PlaybookSHA256: "playbook:tests/runtime/upgrade.yml", DependencySnapshot: map[string]any{"dependencies": []any{}},
		},
		TestOnly: true, InstalledAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func componentRequest(name, slug string) map[string]any {
	return map[string]any{
		"name": name, "slug": slug, "layer": "runtime_state", "category": "runtime",
		"kind": "software", "requiredness": "profile_required",
	}
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

func (f *apiFixture) multipartRequest(path string, fields map[string]string, filename string, contents []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	return f.multipartFileRequest(path, fields, "dockerfile", filename, contents, cookie)
}

func (f *apiFixture) multipartFileRequest(path string, fields map[string]string, fieldName, filename string, contents []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			f.t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		f.t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		f.t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

func TestDraftPlaybookUploadAndOnlineEdit(t *testing.T) {
	f := newAPIFixture(t)
	root := t.TempDir()
	f.platform.ConfigurePlaybookRoot(root)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	bob := f.session(seed.ComponentOwnerK8sID)
	releasePath := "/api/v1/component-releases/release-test-runtime-1.1.0/playbook"

	uploaded := f.multipartFileRequest(releasePath, nil, "playbook", "install.yml", []byte("---\n- hosts: all\n  tasks: []\n"), alice)
	if uploaded.Code != http.StatusCreated {
		t.Fatalf("upload Playbook status=%d body=%s", uploaded.Code, uploaded.Body.String())
	}
	data := decodeEnvelope(t, uploaded)["data"].(map[string]any)
	managedPath, ok := data["path"].(string)
	if !ok || managedPath != "managed/component-test-runtime/release-test-runtime-1.1.0/install.yml" {
		t.Fatalf("managed Playbook path=%#v", data)
	}
	contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(managedPath)))
	if err != nil || !strings.Contains(string(contents), "hosts: all") {
		t.Fatalf("stored Playbook contents=%q err=%v", contents, err)
	}

	loaded := f.request(http.MethodGet, releasePath+"?path="+managedPath, nil, alice)
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), "hosts: all") {
		t.Fatalf("load Playbook status=%d body=%s", loaded.Code, loaded.Body.String())
	}
	if err := f.database.MarkReleaseVerified(context.Background(), "release-test-runtime-1.1.0", true); err != nil {
		t.Fatal(err)
	}
	edited := f.request(http.MethodPut, releasePath, map[string]any{"filename": "install.yml", "content": "---\n- hosts: workers\n  tasks: []\n"}, alice)
	if edited.Code != http.StatusOK || !strings.Contains(edited.Body.String(), "hosts: workers") {
		t.Fatalf("edit Playbook status=%d body=%s", edited.Code, edited.Body.String())
	}
	release, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil || release.Verified {
		t.Fatalf("Playbook edit did not invalidate verification: verified=%v err=%v", release.Verified, err)
	}
	if response := f.request(http.MethodPut, releasePath, map[string]any{"filename": "install.yml", "content": "---\n[]\n"}, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other owner edit Playbook status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPut, "/api/v1/component-releases/release-test-runtime-1.0.0/playbook", map[string]any{"filename": "install.yml", "content": "---\n[]\n"}, alice); response.Code != http.StatusConflict {
		t.Fatalf("released Playbook edit status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPut, releasePath, map[string]any{"filename": "notes.txt", "content": "not yaml"}, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid Playbook extension status=%d body=%s", response.Code, response.Body.String())
	}
	outside := filepath.Join(t.TempDir(), "outside.yml")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	escapePath := filepath.Join(filepath.Dir(filepath.Join(root, filepath.FromSlash(managedPath))), "escape.yml")
	if err := os.Symlink(outside, escapePath); err != nil {
		t.Fatal(err)
	}
	if response := f.request(http.MethodGet, releasePath+"?path=managed/component-test-runtime/release-test-runtime-1.1.0/escape.yml", nil, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("escaping Playbook symlink status=%d body=%s", response.Code, response.Body.String())
	}
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

	created := f.request(http.MethodPost, "/api/v1/components", componentRequest("Alice private", "alice-private"), alice)
	if created.Code != http.StatusCreated {
		t.Fatalf("owner create status=%d body=%s", created.Code, created.Body.String())
	}
	createdData := decodeEnvelope(t, created)["data"].(map[string]any)
	if createdData["layer"] != "runtime_state" || createdData["category"] != "runtime" || createdData["kind"] != "software" || createdData["requiredness"] != "profile_required" {
		t.Fatalf("component classification response=%#v", createdData)
	}
	componentID := createdData["id"].(string)
	if response := f.request(http.MethodPatch, "/api/v1/components/"+componentID, map[string]any{"name": "hijacked"}, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other component owner update status=%d", response.Code)
	}
	if response := f.request(http.MethodPost, "/api/v1/components", componentRequest("bad", "bad"), carol); response.Code != http.StatusForbidden {
		t.Fatalf("scenario owner component create status=%d", response.Code)
	}
	invalidClassification := componentRequest("Invalid", "invalid-classification")
	invalidClassification["category"] = "dns"
	if response := f.request(http.MethodPost, "/api/v1/components", invalidClassification, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid classification status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPut, "/api/v1/environments/environment-test/parameters", map[string]any{"parameters": map[string]any{"region": "cn"}}, alice); response.Code != http.StatusNotFound {
		t.Fatalf("removed environment parameter endpoint status=%d", response.Code)
	}
	if response := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"IMAGE_REGISTRY": "hijacked.invalid"}}, alice); response.Code != http.StatusForbidden {
		t.Fatalf("component owner environment variable update status=%d", response.Code)
	}
	variablesUpdated := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"IMAGE_REGISTRY": "192.168.88.54:5000/"}}, dave)
	if variablesUpdated.Code != http.StatusOK || !strings.Contains(variablesUpdated.Body.String(), `"IMAGE_REGISTRY":"192.168.88.54:5000"`) {
		t.Fatalf("environment variable update status=%d body=%s", variablesUpdated.Code, variablesUpdated.Body.String())
	}
	var variableAuditCount int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='environment.variables_updated' AND resource_id='environment-test'`).Scan(&variableAuditCount); err != nil || variableAuditCount != 1 {
		t.Fatalf("environment variable audit count=%d err=%v", variableAuditCount, err)
	}
	invalidVariable := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"registry_password": "secret"}}, dave)
	if invalidVariable.Code != http.StatusBadRequest || strings.Contains(invalidVariable.Body.String(), "secret") {
		t.Fatalf("invalid environment variable status=%d body=%s", invalidVariable.Code, invalidVariable.Body.String())
	}

	secret := "this-secret-must-not-be-persisted"
	rejected := f.request(http.MethodPut, "/api/v1/environments/environment-test/parameters", map[string]any{"parameters": map[string]any{"registryPassword": secret}}, dave)
	if rejected.Code != http.StatusNotFound || strings.Contains(rejected.Body.String(), secret) {
		t.Fatalf("removed parameter endpoint status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	updated := f.request(http.MethodPut, "/api/v1/environments/environment-test/credential-refs", map[string]any{"credentialRefs": []any{map[string]any{"name": "registry", "kind": "envVarRef", "reference": "TEST_REGISTRY_TOKEN"}}}, dave)
	if updated.Code != http.StatusOK {
		t.Fatalf("credential ref update status=%d body=%s", updated.Code, updated.Body.String())
	}
	visible := f.request(http.MethodGet, "/api/v1/environments", nil, alice)
	if visible.Code != http.StatusOK || strings.Contains(visible.Body.String(), "TEST_REGISTRY_TOKEN") || !strings.Contains(visible.Body.String(), "maskedReference") {
		t.Fatalf("credential redaction failed: %s", visible.Body.String())
	}
	var count int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE metadata_json LIKE ?`, "%"+secret+"%").Scan(&count); err != nil || count != 0 {
		t.Fatalf("secret found in audit: count=%d err=%v", count, err)
	}
}

func TestDraftDockerfileBuildPublishesForcedRegistryReference(t *testing.T) {
	f := newAPIFixture(t)
	binary := filepath.Join(t.TempDir(), "fake-docker")
	script := "#!/bin/sh\nif [ \"$1\" = image ]; then printf '%s\\n' '192.168.88.54:5000/components/component-test-runtime:v1.1.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; else printf '%s ok\\n' \"$1\"; fi\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	f.platform.ConfigureImageBuilder(t.TempDir(), binary)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	bob := f.session(seed.ComponentOwnerK8sID)
	missingEnvironment := f.multipartRequest(
		"/api/v1/component-releases/release-test-runtime-1.1.0/image-builds",
		map[string]string{"tag": "v1.1.0"}, "Dockerfile", []byte("FROM scratch\n"), alice,
	)
	if missingEnvironment.Code != http.StatusBadRequest {
		t.Fatalf("missing image-build environment status=%d body=%s", missingEnvironment.Code, missingEnvironment.Body.String())
	}

	created := f.multipartRequest(
		"/api/v1/component-releases/release-test-runtime-1.1.0/image-builds",
		map[string]string{"environmentId": "environment-test", "tag": "V1.1.0"},
		"Dockerfile",
		[]byte("FROM scratch\nLABEL purpose=api-test\n"),
		alice,
	)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create image build status=%d body=%s", created.Code, created.Body.String())
	}
	data := decodeEnvelope(t, created)["data"].(map[string]any)
	buildID := data["id"].(string)
	if data["imageRef"] != "192.168.88.54:5000/components/component-test-runtime:v1.1.0" {
		t.Fatalf("image reference was not forced to configured registry: %#v", data)
	}
	if data["environmentId"] != "environment-test" || data["environmentRevisionId"] == "" {
		t.Fatalf("image build did not lock the environment revision: %#v", data)
	}
	var auditCount int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='component.image_build_requested' AND metadata_json LIKE '%environmentRevisionId%'`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("image build environment audit count=%d err=%v", auditCount, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		build, err := f.database.GetComponentImageBuild(context.Background(), buildID)
		if err == nil && build.Status == domain.ImageBuildSucceeded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	visible := f.request(http.MethodGet, "/api/v1/image-builds/"+buildID, nil, alice)
	if visible.Code != http.StatusOK || !strings.Contains(visible.Body.String(), "@sha256:aaaaaaaa") || !strings.Contains(visible.Body.String(), "build ok") {
		t.Fatalf("completed build response status=%d body=%s", visible.Code, visible.Body.String())
	}
	if hidden := f.request(http.MethodGet, "/api/v1/image-builds/"+buildID, nil, bob); hidden.Code != http.StatusForbidden {
		t.Fatalf("other component owner can inspect draft build: status=%d", hidden.Code)
	}
	released := f.multipartRequest(
		"/api/v1/component-releases/release-test-runtime-1.0.0/image-builds",
		map[string]string{"environmentId": "environment-test", "tag": "v1.0.0"}, "Dockerfile", []byte("FROM scratch\n"), alice,
	)
	if released.Code != http.StatusConflict {
		t.Fatalf("released version image build status=%d body=%s", released.Code, released.Body.String())
	}
}

func TestComponentKindIsIndependentFromReleaseType(t *testing.T) {
	f := newAPIFixture(t)
	response := f.request(http.MethodGet, "/api/v1/components/component-kubernetes", nil, f.session(seed.ComponentOwnerK8sID))
	if response.Code != http.StatusOK {
		t.Fatalf("component response status=%d body=%s", response.Code, response.Body.String())
	}
	data := decodeEnvelope(t, response)["data"].(map[string]any)
	latest := data["latestRelease"].(map[string]any)
	if data["kind"] != "software_bundle" || latest["type"] != "bundle" {
		t.Fatalf("component kind and release type were conflated: %#v", data)
	}
}

func TestPublicListAndScenarioDTOContracts(t *testing.T) {
	f := newAPIFixture(t)
	carol := f.session(seed.ScenarioOwnerID)

	components := decodeEnvelope(t, f.request(http.MethodGet, "/api/v1/components", nil, carol))
	if _, ok := components["items"].([]any); !ok {
		t.Fatalf("components must use items envelope: %#v", components)
	}

	scenarios := decodeEnvelope(t, f.request(http.MethodGet, "/api/v1/scenarios", nil, carol))
	items, ok := scenarios["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("scenarios must use non-empty items envelope: %#v", scenarios)
	}
	scenario, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("scenario item type: %#v", items[0])
	}
	revision, ok := scenario["currentRevision"].(map[string]any)
	if !ok {
		t.Fatalf("currentRevision missing: %#v", scenario)
	}
	nodes, ok := revision["nodes"].([]any)
	if !ok {
		t.Fatalf("revision nodes must be an array: %#v", revision)
	}
	if len(nodes) > 0 {
		node, ok := nodes[0].(map[string]any)
		if !ok {
			t.Fatalf("scenario node type: %#v", nodes[0])
		}
		data, ok := node["data"].(map[string]any)
		if !ok || data["componentId"] == "" || data["releaseId"] == "" {
			t.Fatalf("scenario node public metadata missing: %#v", node)
		}
	}
}

func TestFirstVersionAPIRejectsOldRequestShapes(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)

	for name, definition := range map[string]map[string]any{
		"release state alias": {
			"version": "strict-state", "type": "atomic", "state": "draft", "parameters": []any{}, "actions": []any{},
		},
		"action type alias": {
			"version": "strict-action", "type": "atomic", "parameters": []any{},
			"actions": []any{map[string]any{"name": "install", "type": "install", "playbook": "tests/runtime/install.yml"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := f.request(http.MethodPost, "/api/v1/components/component-test-runtime/releases", definition, alice)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unknown field") {
				t.Fatalf("unknown first-version field status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	flatGraph := f.request(http.MethodPut, "/api/v1/scenario-revisions/scenario-test-runtime-r1/graph", map[string]any{
		"nodes": []any{map[string]any{"id": "flat", "releaseId": "release-test-runtime-1.0.0", "action": "install"}},
		"edges": []any{},
	}, alice)
	if flatGraph.Code != http.StatusBadRequest || !strings.Contains(flatGraph.Body.String(), "unknown field") {
		t.Fatalf("old flat graph status=%d body=%s", flatGraph.Code, flatGraph.Body.String())
	}
}

func TestReleaseImpactNotifiesDownstreamAndScenarioOwners(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	draftOnly := domain.Scenario{ID: "scenario-draft-only-impact", Slug: "draft-only-impact", Name: "Draft only impact", OwnerID: seed.ScenarioOwnerID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	draftRevision := domain.ScenarioRevision{
		ID: "scenario-draft-only-impact-r1", ScenarioID: draftOnly.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph:           domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "agent", Name: "Agent", ReleaseID: "release-test-runtime-1.0.0", Action: domain.ActionInstall, HostGroup: "test_nodes"}}, Edges: []domain.ScenarioEdge{}},
		ExecutionPolicy: map[string]any{}, CreatedAt: draftOnly.CreatedAt,
	}
	if err := f.database.CreateScenario(context.Background(), draftOnly, draftRevision); err != nil {
		t.Fatal(err)
	}
	response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/publish", nil, alice)
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
		expectedPath := "Test Consumer"
		if userID == seed.ScenarioOwnerID {
			expectedPath = "scenario-test-runtime"
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
	createdComponent := f.request(http.MethodPost, "/api/v1/components", componentRequest("Mutable", "mutable"), alice)
	componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
	definition := map[string]any{
		"version": "1.0.0", "type": "atomic", "parameters": []any{},
		"actions": []any{map[string]any{"name": "install", "kind": "install", "playbook": "tests/runtime/install.yml", "timeoutSeconds": 60, "requiredCredentials": []any{"ansible_ssh_pass"}}},
	}
	createdRelease := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", definition, alice)
	if createdRelease.Code != http.StatusCreated {
		t.Fatalf("create release status=%d body=%s", createdRelease.Code, createdRelease.Body.String())
	}
	releaseID := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["id"].(string)
	createdAction := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	if got, ok := createdAction["requiredCredentials"].([]any); !ok || len(got) != 1 || got[0] != "ansible_ssh_pass" {
		t.Fatalf("requiredCredentials API round trip=%#v", createdAction["requiredCredentials"])
	}
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
		RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
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

func TestDraftRequiredCredentialsDistinguishesOmittedFromExplicitEmpty(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	createdComponent := f.request(http.MethodPost, "/api/v1/components", componentRequest("Credential semantics", "credential-semantics"), alice)
	componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
	definition := map[string]any{
		"version": "1.0.0", "type": "atomic", "parameters": []any{},
		"actions": []any{map[string]any{
			"name": "install", "kind": "install", "playbook": "tests/runtime/install.yml", "timeoutSeconds": 60,
			"requiredCredentials": []any{"K8S_BOOTSTRAP_TOKEN"},
		}},
	}
	created := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", definition, alice)
	if created.Code != http.StatusCreated {
		t.Fatalf("create release status=%d body=%s", created.Code, created.Body.String())
	}
	releaseID := decodeEnvelope(t, created)["data"].(map[string]any)["id"].(string)
	action := definition["actions"].([]any)[0].(map[string]any)
	delete(action, "requiredCredentials")
	omitted := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID, definition, alice)
	if omitted.Code != http.StatusOK {
		t.Fatalf("omitted credential update status=%d body=%s", omitted.Code, omitted.Body.String())
	}
	omittedAction := decodeEnvelope(t, omitted)["data"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	if got := omittedAction["requiredCredentials"].([]any); len(got) != 1 || got[0] != "K8S_BOOTSTRAP_TOKEN" {
		t.Fatalf("omitted required credentials were not preserved: %#v", got)
	}
	action["requiredCredentials"] = []any{}
	cleared := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID, definition, alice)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear credential update status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	clearedAction := decodeEnvelope(t, cleared)["data"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	if got := clearedAction["requiredCredentials"].([]any); len(got) != 0 {
		t.Fatalf("explicit empty required credentials were not cleared: %#v", got)
	}
}

func TestComponentInstallBackupsAreScopedToEachRun(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	var refs []string
	for range 2 {
		response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
			"environmentId": "environment-test", "mode": "install_verify",
		}, alice)
		if response.Code != http.StatusAccepted {
			t.Fatalf("install test status=%d body=%s", response.Code, response.Body.String())
		}
		data := decodeEnvelope(t, response)["data"].(map[string]any)
		runID := data["id"].(string)
		backups := data["backups"].([]any)
		if len(backups) != 1 {
			t.Fatalf("run backup metadata=%#v", backups)
		}
		backup := backups[0].(map[string]any)
		if backup["installRunId"] != runID || backup["capturedAt"] == "" || backup["playbookSha256"] == "" {
			t.Fatalf("run-scoped backup metadata=%#v", backup)
		}
		refs = append(refs, backup["backupRef"].(string))
		waitForRun(t, f.database, runID, domain.RunSucceeded)
	}
	if refs[0] == refs[1] {
		t.Fatalf("separate install Runs reused backup_ref %q", refs[0])
	}
	installation, err := f.database.GetEnvironmentComponentInstallation(context.Background(), "environment-test", "component-test-runtime")
	if err != nil || installation.BackupRef != refs[1] {
		t.Fatalf("current installation backup=%+v err=%v", installation, err)
	}
}

func TestRollbackRefusesMismatchedInstallationMetadata(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	installation, err := f.database.GetEnvironmentComponentInstallation(context.Background(), "environment-test", "component-test-runtime")
	if err != nil {
		t.Fatal(err)
	}
	installation.Backup.ReleaseID = "release-test-runtime-1.0.0"
	if err := f.database.UpsertEnvironmentComponentInstallation(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
		"environmentId": "environment-test", "mode": "rollback",
	}, alice)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "backup release") {
		t.Fatalf("mismatched backup was not rejected: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRollbackRefusesCapturedPlaybookHashMismatch(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	installation, err := f.database.GetEnvironmentComponentInstallation(context.Background(), "environment-test", "component-test-runtime")
	if err != nil {
		t.Fatal(err)
	}
	installation.Backup.PlaybookSHA256 = "stale-playbook-sha256"
	if err := f.database.UpsertEnvironmentComponentInstallation(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
		"environmentId": "environment-test", "mode": "rollback",
	}, alice)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Playbook hash") {
		t.Fatalf("stale captured Playbook was not rejected: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUpdatingReleaseContractPreservesActionsAndRejectsDraftUpstream(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	createdComponent := f.request(http.MethodPost, "/api/v1/components", componentRequest("Contract Edit", "contract-edit"), alice)
	componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
	createdRelease := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", map[string]any{
		"version": "1.0.0", "type": "atomic", "riskLevel": "medium",
		"actions": []any{map[string]any{
			"name": "custom-install", "kind": "install", "playbook": "tests/runtime/install.yml",
			"limit": "runtime_nodes", "hostGroup": "test_nodes", "timeoutSeconds": 60, "riskLevel": "high",
		}},
	}, alice)
	if createdRelease.Code != http.StatusCreated {
		t.Fatalf("create release status=%d body=%s", createdRelease.Code, createdRelease.Body.String())
	}
	releaseID := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["id"].(string)

	updated := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID+"/contract", map[string]any{
		"parameters": []any{map[string]any{
			"name": "runtimeRoot", "description": "runtime install root", "type": "string", "visibility": "public",
		}},
		"dependencies": []any{map[string]any{
			"upstreamComponentId": "component-test-runtime", "upstreamReleaseId": "release-test-runtime-1.0.0", "purpose": "runtime",
		}},
	}, alice)
	if updated.Code != http.StatusOK {
		t.Fatalf("update contract status=%d body=%s", updated.Code, updated.Body.String())
	}
	release, err := f.database.GetComponentRelease(context.Background(), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Actions) != 1 || release.Actions[0].Name != "custom-install" || release.Actions[0].Limit != "runtime_nodes" || release.Actions[0].RiskLevel != domain.RiskHigh {
		t.Fatalf("contract edit changed action metadata: %+v", release.Actions)
	}
	if len(release.Dependencies) != 1 || release.Dependencies[0].UpstreamReleaseID != "release-test-runtime-1.0.0" {
		t.Fatalf("contract dependencies=%+v", release.Dependencies)
	}

	rejected := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID+"/contract", map[string]any{
		"parameters": []any{},
		"dependencies": []any{map[string]any{
			"upstreamComponentId": "component-test-runtime", "upstreamReleaseId": "release-test-runtime-1.1.0", "purpose": "draft must not be lockable",
		}},
	}, alice)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "must lock a released version") {
		t.Fatalf("draft upstream status=%d body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestCloneReleaseCanOverrideEnvironmentConstraints(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	createdComponent := f.request(http.MethodPost, "/api/v1/components", componentRequest("Clone Env", "clone-env"), alice)
	componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
	createdRelease := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", map[string]any{
		"version": "1.0.0", "type": "atomic",
		"environmentConstraints": map[string]any{"architecture": []any{"amd64"}, "operatingSystem": []any{"SUSE"}},
		"actions":                []any{map[string]any{"name": "install", "kind": "install", "playbook": "tests/runtime/install.yml", "timeoutSeconds": 60}},
	}, alice)
	if createdRelease.Code != http.StatusCreated {
		t.Fatalf("create release status=%d body=%s", createdRelease.Code, createdRelease.Body.String())
	}
	releaseID := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["id"].(string)
	if response := f.request(http.MethodPost, "/api/v1/component-releases/"+releaseID+"/publish", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", response.Code, response.Body.String())
	}
	cloned := f.request(http.MethodPost, "/api/v1/component-releases/"+releaseID+"/clone", map[string]any{
		"version": "1.1.0", "releaseNotes": "add arm64",
		"environmentConstraints": map[string]any{"architecture": []any{"amd64", "arm64"}, "operatingSystem": []any{"SUSE", "Kylin"}, "ipFamily": []any{"IPv4"}},
	}, alice)
	if cloned.Code != http.StatusCreated {
		t.Fatalf("clone status=%d body=%s", cloned.Code, cloned.Body.String())
	}
	constraints := decodeEnvelope(t, cloned)["data"].(map[string]any)["environmentConstraints"].(map[string]any)
	if !reflect.DeepEqual(constraints["architecture"], []any{"amd64", "arm64"}) {
		t.Fatalf("cloned architecture=%#v", constraints["architecture"])
	}
	if !reflect.DeepEqual(constraints["ipFamily"], []any{"IPv4"}) {
		t.Fatalf("cloned ipFamily=%#v", constraints["ipFamily"])
	}
	kept := f.request(http.MethodPost, "/api/v1/component-releases/"+releaseID+"/clone", map[string]any{
		"version": "1.1.1", "releaseNotes": "keep source constraints",
	}, alice)
	if kept.Code != http.StatusCreated {
		t.Fatalf("clone without constraints status=%d body=%s", kept.Code, kept.Body.String())
	}
	keptConstraints := decodeEnvelope(t, kept)["data"].(map[string]any)["environmentConstraints"].(map[string]any)
	if !reflect.DeepEqual(keptConstraints["architecture"], []any{"amd64"}) {
		t.Fatalf("source constraints were not kept: %#v", keptConstraints)
	}
}

func TestPublishRejectsCrossReleaseTransitionMismatch(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	componentResponse := f.request(http.MethodPost, "/api/v1/components", componentRequest("Transitions", "transitions"), alice)
	componentID := decodeEnvelope(t, componentResponse)["data"].(map[string]any)["id"].(string)
	oldResponse := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", map[string]any{
		"version": "1.0.0", "type": "atomic",
		"actions": []any{map[string]any{"name": "install", "kind": "install", "playbook": "tests/runtime/install-v1.0.yml", "timeoutSeconds": 60}},
	}, alice)
	oldID := decodeEnvelope(t, oldResponse)["data"].(map[string]any)["id"].(string)
	if response := f.request(http.MethodPost, "/api/v1/component-releases/"+oldID+"/publish", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("publish old release status=%d body=%s", response.Code, response.Body.String())
	}
	newResponse := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/releases", map[string]any{
		"version": "2.0.0", "type": "atomic",
		"actions": []any{map[string]any{
			"name": "upgrade", "kind": "upgrade", "playbook": "tests/runtime/upgrade-v1.1.yml", "timeoutSeconds": 60,
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
	blockedClone := f.request(http.MethodPost, "/api/v1/scenarios/scenario-test-runtime/revisions", nil, carol)
	if blockedClone.Code != http.StatusConflict {
		t.Fatalf("clone with active draft status=%d body=%s", blockedClone.Code, blockedClone.Body.String())
	}
	abandoned := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r2/abandon", nil, carol)
	if abandoned.Code != http.StatusOK {
		t.Fatalf("abandon draft status=%d body=%s", abandoned.Code, abandoned.Body.String())
	}
	abandonedScenario := decodeEnvelope(t, abandoned)["data"].(map[string]any)
	if abandonedScenario["currentRevisionId"] != "scenario-test-runtime-r1" {
		t.Fatalf("restored current revision=%#v", abandonedScenario["currentRevisionId"])
	}
	foundAbandoned := false
	for _, raw := range abandonedScenario["revisions"].([]any) {
		revision := raw.(map[string]any)
		if revision["id"] == "scenario-test-runtime-r2" && revision["state"] == "abandoned" {
			foundAbandoned = true
		}
	}
	if !foundAbandoned {
		t.Fatalf("abandoned revision missing from history: %#v", abandonedScenario["revisions"])
	}
	cloned := f.request(http.MethodPost, "/api/v1/scenarios/scenario-test-runtime/revisions", nil, carol)
	if cloned.Code != http.StatusCreated {
		t.Fatalf("clone revision status=%d body=%s", cloned.Code, cloned.Body.String())
	}
	if revision := decodeEnvelope(t, cloned)["data"].(map[string]any); revision["revision"] != float64(3) {
		t.Fatalf("cloned revision=%#v", revision)
	}
	if duplicate := f.request(http.MethodPost, "/api/v1/scenarios/scenario-test-runtime/revisions", nil, carol); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate active draft clone status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	oldGraphEdit := f.request(http.MethodPut, "/api/v1/scenario-revisions/scenario-test-runtime-r2/graph", map[string]any{
		"nodes": []any{map[string]any{"id": "old", "type": "component", "position": map[string]any{"x": 0, "y": 0}, "data": map[string]any{"label": "old", "releaseId": "release-test-runtime-1.0.0", "action": "install", "hostGroup": "test_nodes"}}},
		"edges": []any{},
	}, carol)
	if oldGraphEdit.Code != http.StatusConflict {
		t.Fatalf("non-current graph edit status=%d body=%s", oldGraphEdit.Code, oldGraphEdit.Body.String())
	}
	oldTest := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r2/test-runs", map[string]any{"environmentId": "environment-test"}, carol)
	if oldTest.Code != http.StatusConflict {
		t.Fatalf("non-current scenario test status=%d body=%s", oldTest.Code, oldTest.Body.String())
	}
	var auditCount int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='scenario_revision.abandoned' AND resource_id='scenario-test-runtime-r2'`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("abandon audit count=%d err=%v", auditCount, err)
	}
}

func TestReleasedScenarioRetainsDeprecatedLockedComponent(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	carol := f.session(seed.ScenarioOwnerID)
	if response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.0.0/deprecate", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("deprecate release status=%d body=%s", response.Code, response.Body.String())
	}
	runResponse := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r1/runs", map[string]any{"environmentId": "environment-test"}, carol)
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
	dave := f.session(seed.EnvironmentOwnerID)
	if response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/publish", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("publish rollback source status=%d body=%s", response.Code, response.Body.String())
	}
	now := time.Now().UTC()
	scenario := domain.Scenario{ID: "scenario-rollback-target", Slug: "rollback-target", Name: "Rollback target", OwnerID: seed.ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	revision := domain.ScenarioRevision{
		ID: "scenario-rollback-target-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{
			ID: "rollback", Name: "Rollback", ReleaseID: "release-test-runtime-1.1.0", Action: domain.ActionRollback,
			HostGroup: "test_nodes", Values: map[string]any{}, RunInputs: []string{"agent_root"},
		}}, Edges: []domain.ScenarioEdge{}}, ExecutionPolicy: map[string]any{}, CreatedAt: now,
	}
	if err := f.database.CreateScenario(context.Background(), scenario, revision); err != nil {
		t.Fatal(err)
	}
	runResponse := f.request(http.MethodPost, "/api/v1/scenario-revisions/"+revision.ID+"/test-runs", map[string]any{"environmentId": "environment-test"}, carol)
	if runResponse.Code != http.StatusAccepted {
		t.Fatalf("rollback test status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	runID := decodeEnvelope(t, runResponse)["data"].(map[string]any)["id"].(string)
	runData := decodeEnvelope(t, runResponse)["data"].(map[string]any)
	if runData["status"] != string(domain.RunAwaitingApproval) || runData["destructive"] != true {
		t.Fatalf("rollback run bypassed approval: %#v", runData)
	}
	approvalID := runData["approval"].(map[string]any)["id"].(string)
	if response := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/approve", nil, dave); response.Code != http.StatusOK {
		t.Fatalf("approve rollback status=%d body=%s", response.Code, response.Body.String())
	}
	waitForRun(t, f.database, runID, domain.RunSucceeded)
	f.runner.mu.Lock()
	requests := append([]ansiblerunner.Request(nil), f.runner.requests...)
	f.runner.mu.Unlock()
	if len(requests) != 2 || requests[0].Playbook != "tests/runtime/rollback.yml" || requests[1].Playbook != "tests/runtime/verify.yml" || requests[1].Variables["expected_version"] != "1.0.0" {
		t.Fatalf("rollback execution requests=%+v", requests)
	}
}

func TestDraftComponentRollbackTestLocksRollbackAndTargetVerify(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	dave := f.session(seed.EnvironmentOwnerID)

	invalid := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
		"environmentId": "environment-test", "mode": "unknown",
	}, alice)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid component test mode status=%d body=%s", invalid.Code, invalid.Body.String())
	}

	response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
		"environmentId": "environment-test", "mode": "rollback",
	}, alice)
	if response.Code != http.StatusAccepted {
		t.Fatalf("draft rollback test status=%d body=%s", response.Code, response.Body.String())
	}
	runData := decodeEnvelope(t, response)["data"].(map[string]any)
	if runData["action"] != string(domain.ActionRollback) || runData["status"] != string(domain.RunAwaitingApproval) || runData["destructive"] != true {
		t.Fatalf("draft rollback test bypassed rollback approval: %#v", runData)
	}
	runID := runData["id"].(string)
	lockedRun, err := f.database.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	steps := lockedRun.InputSnapshot["steps"].([]any)
	if len(steps) != 2 || steps[0].(map[string]any)["playbook"] != "tests/runtime/rollback.yml" || steps[1].(map[string]any)["playbook"] != "tests/runtime/verify.yml" || steps[1].(map[string]any)["releaseId"] != "release-test-runtime-1.0.0" {
		t.Fatalf("draft rollback locked steps=%#v", steps)
	}
	approvalID := runData["approval"].(map[string]any)["id"].(string)
	if approved := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/approve", nil, dave); approved.Code != http.StatusOK {
		t.Fatalf("approve draft rollback status=%d body=%s", approved.Code, approved.Body.String())
	}
	waitForRun(t, f.database, runID, domain.RunSucceeded)
	if _, err := f.database.GetEnvironmentComponentInstallation(context.Background(), "environment-test", "component-test-runtime"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("successful test rollback retained its test backup record: %v", err)
	}
	release, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if release.Verified {
		t.Fatal("rollback-only component test incorrectly marked install verification as passed")
	}
}

func TestDraftRollbackPlanPreviewStrategiesAndDigest(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	now := time.Now().UTC()
	for _, target := range []domain.ComponentRelease{
		{
			ID: "release-test-runtime-0.9.0", ComponentID: "component-test-runtime", Version: "v0.9.0", Type: domain.ReleaseAtomic,
			Status: domain.ReleaseDeprecated, Verified: true, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now,
			EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{{Name: "expected_version", Type: domain.ParameterTypeString, Required: true, DefaultValue: "0.9.0", Visibility: domain.ParameterInternal}},
			Actions: []domain.ActionDefinition{{ID: "action-test-runtime-verify-0.9", ReleaseID: "release-test-runtime-0.9.0", Name: "verify", Kind: domain.ActionVerify, Playbook: "tests/runtime/verify.yml", HostGroup: "test_nodes", TimeoutSeconds: 60}},
		},
		{
			ID: "release-test-runtime-bad-host", ComponentID: "component-test-runtime", Version: "v0.8.0", Type: domain.ReleaseAtomic,
			Status: domain.ReleaseReleased, Verified: true, RiskLevel: domain.RiskLow, CreatedAt: now.Add(-time.Second), ReleasedAt: &now,
			EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{{Name: "expected_version", Type: domain.ParameterTypeString, Required: true, DefaultValue: "0.8.0", Visibility: domain.ParameterInternal}},
			Actions: []domain.ActionDefinition{{ID: "action-test-runtime-verify-bad-host", ReleaseID: "release-test-runtime-bad-host", Name: "verify", Kind: domain.ActionVerify, Playbook: "tests/runtime/verify.yml", HostGroup: "k8smaster", TimeoutSeconds: 60}},
		},
		{
			ID: "release-test-runtime-no-verify", ComponentID: "component-test-runtime", Version: "v0.7.0", Type: domain.ReleaseAtomic,
			Status: domain.ReleaseReleased, Verified: true, RiskLevel: domain.RiskLow, CreatedAt: now.Add(-2 * time.Second), ReleasedAt: &now,
			EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, Actions: []domain.ActionDefinition{},
		},
	} {
		if err := f.database.CreateComponentRelease(context.Background(), target); err != nil {
			t.Fatal(err)
		}
	}

	var before int
	if err := f.database.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM runs`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	preview := func(verification map[string]any) *httptest.ResponseRecorder {
		return f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-plan", map[string]any{
			"environmentId": "environment-test", "mode": "rollback", "rollbackVerification": verification,
		}, alice)
	}

	badHost := preview(map[string]any{"kind": "target_release", "releaseId": "release-test-runtime-bad-host"})
	if badHost.Code != http.StatusBadRequest || !strings.Contains(badHost.Body.String(), "k8smaster") {
		t.Fatalf("bad target host group preview status=%d body=%s", badHost.Code, badHost.Body.String())
	}

	rollbackOnly := preview(map[string]any{"kind": "rollback_only"})
	if rollbackOnly.Code != http.StatusOK {
		t.Fatalf("rollback-only preview status=%d body=%s", rollbackOnly.Code, rollbackOnly.Body.String())
	}
	rollbackOnlyData := decodeEnvelope(t, rollbackOnly)["data"].(map[string]any)
	rollbackOnlySteps := rollbackOnlyData["steps"].([]any)
	if rollbackOnlyData["requiresApproval"] != true || len(rollbackOnlySteps) != 1 || rollbackOnlySteps[0].(map[string]any)["releaseVersion"] != "v1.1.0" || rollbackOnlySteps[0].(map[string]any)["backupInstallRunId"] != "run-installed-runtime-1.1" {
		t.Fatalf("rollback-only preview=%#v", rollbackOnlyData)
	}

	alternate := preview(map[string]any{"kind": "target_release", "releaseId": "release-test-runtime-0.9.0"})
	if alternate.Code != http.StatusOK {
		t.Fatalf("alternate target preview status=%d body=%s", alternate.Code, alternate.Body.String())
	}
	alternateData := decodeEnvelope(t, alternate)["data"].(map[string]any)
	alternateSteps := alternateData["steps"].([]any)
	if len(alternateSteps) != 2 {
		t.Fatalf("alternate target steps=%#v", alternateSteps)
	}
	rollbackStep, verifyStep := alternateSteps[0].(map[string]any), alternateSteps[1].(map[string]any)
	if rollbackStep["fromReleaseId"] != "release-test-runtime-1.1.0" || rollbackStep["toReleaseId"] != "release-test-runtime-1.0.0" || verifyStep["releaseId"] != "release-test-runtime-0.9.0" || verifyStep["releaseVersion"] != "v0.9.0" {
		t.Fatalf("alternate target steps=%#v", alternateSteps)
	}

	for name, verification := range map[string]map[string]any{
		"draft target":          {"kind": "target_release", "releaseId": "release-test-runtime-1.1.0"},
		"cross component":       {"kind": "target_release", "releaseId": "release-test-consumer-1.0.0"},
		"missing verify":        {"kind": "target_release", "releaseId": "release-test-runtime-no-verify"},
		"rollback only with id": {"kind": "rollback_only", "releaseId": "release-test-runtime-1.0.0"},
	} {
		t.Run(name, func(t *testing.T) {
			response := preview(verification)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid target status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	var afterPreview int
	if err := f.database.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM runs`).Scan(&afterPreview); err != nil {
		t.Fatal(err)
	}
	if afterPreview != before {
		t.Fatalf("plan preview persisted a Run: before=%d after=%d", before, afterPreview)
	}

	installation, err := f.database.GetEnvironmentComponentInstallation(context.Background(), "environment-test", "component-test-runtime")
	if err != nil {
		t.Fatal(err)
	}
	installation.BackupRef += "-changed-after-preview"
	if err := f.database.UpsertEnvironmentComponentInstallation(context.Background(), installation); err != nil {
		t.Fatal(err)
	}
	stale := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
		"environmentId": "environment-test", "mode": "rollback",
		"rollbackVerification": map[string]any{"kind": "rollback_only"}, "expectedPlanDigest": rollbackOnlyData["planDigest"],
	}, alice)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "execution plan changed") {
		t.Fatalf("stale plan status=%d body=%s", stale.Code, stale.Body.String())
	}
	var afterStale int
	if err := f.database.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM runs`).Scan(&afterStale); err != nil {
		t.Fatal(err)
	}
	if afterStale != before {
		t.Fatalf("stale plan persisted a Run: before=%d after=%d", before, afterStale)
	}
}

func TestScenarioTestGateAndDestructiveApproval(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	carol := f.session(seed.ScenarioOwnerID)
	dave := f.session(seed.EnvironmentOwnerID)

	if response := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r2/publish", nil, carol); response.Code != http.StatusConflict {
		t.Fatalf("untested scenario publish status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/publish", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("component publish: %s", response.Body.String())
	}
	testRun := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r2/test-runs", map[string]any{"environmentId": "environment-test"}, carol)
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
	if response := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r2/publish", nil, carol); response.Code != http.StatusOK {
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
	if response := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/reject", map[string]any{"reason": "retry"}, dave); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), string(domain.RunRejected)) {
		t.Fatalf("idempotent rejection status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/approve", nil, dave); response.Code != http.StatusConflict {
		t.Fatalf("conflicting approval retry status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAPIWorkflowResponsesAreNotCacheable(t *testing.T) {
	f := newAPIFixture(t)
	response := f.request(http.MethodGet, "/api/v1/runs", nil, f.session(seed.EnvironmentOwnerID))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("runs cache control status=%d header=%q", response.Code, response.Header().Get("Cache-Control"))
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
	if !ok || len(currentRevision["nodes"].([]any)) != 21 || len(currentRevision["edges"].([]any)) != 50 {
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
	if !ok || len(lockedSteps) != 42 {
		t.Fatalf("Kubernetes 1.17.5 locked plan=%#v", storedRun.InputSnapshot)
	}
	limitsByPlaybook := map[string]map[string]bool{}
	approvalLocked := false
	for index, rawStep := range lockedSteps {
		step, ok := rawStep.(map[string]any)
		playbook, _ := step["playbook"].(string)
		limit, _ := step["limit"].(string)
		if !ok || !strings.HasPrefix(playbook, "k8s-1.17.5-cluster/components/") || limit == "" {
			t.Fatalf("locked step %d=%#v", index, rawStep)
		}
		if strings.Contains(strings.ToLower(playbook), "recovery") || strings.Contains(strings.ToLower(playbook), "housekeeping") || strings.Contains(strings.ToLower(playbook), "uninstall") {
			t.Fatalf("locked step %d references a forbidden lifecycle path: %s", index, playbook)
		}
		if limitsByPlaybook[playbook] == nil {
			limitsByPlaybook[playbook] = map[string]bool{}
		}
		limitsByPlaybook[playbook][limit] = true
		variables, _ := step["variables"].(map[string]any)
		if _, leaked := variables["K8S_ENCRYPTION_KEY"]; leaked {
			t.Fatalf("runtime encryption key leaked into locked step %d", index)
		}
		if needsApproval, _ := step["needsApproval"].(bool); needsApproval {
			approvalLocked = true
		}
	}
	if !approvalLocked {
		t.Fatal("Kubernetes 1.17.5 plan did not lock destructive approval metadata")
	}
	for _, playbook := range []string{
		"k8s-1.17.5-cluster/components/docker.yml",
		"k8s-1.17.5-cluster/components/kubernetes-distribution.yml",
		"k8s-1.17.5-cluster/components/flannel.yml",
		"k8s-1.17.5-cluster/components/kubelet.yml",
		"k8s-1.17.5-cluster/components/kube-proxy.yml",
	} {
		if !limitsByPlaybook[playbook]["k8smaster"] || !limitsByPlaybook[playbook]["k8snode"] {
			t.Fatalf("release playbook %s was not independently locked for both host groups: %#v", playbook, limitsByPlaybook[playbook])
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
