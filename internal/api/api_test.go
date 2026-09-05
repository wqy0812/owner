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
	"net/url"
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
	"codex/platform-demo/internal/testutil"
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
		"facts": completeTestEnvironmentFacts(), "changeReason": "校正环境事实",
	}, owner)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "校正环境事实") {
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
	if len(revisions) != 2 || revisions[0].(map[string]any)["changeReason"] != "校正环境事实" {
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
	connectivity := f.request(http.MethodPost, "/api/v1/environments/environment-test/connectivity-checks", nil, owner)
	if connectivity.Code != http.StatusOK || !strings.Contains(connectivity.Body.String(), `"tcpCheck"`) || !strings.Contains(connectivity.Body.String(), `"sshCheck"`) || !strings.Contains(connectivity.Body.String(), `"local_connection"`) {
		t.Fatalf("connectivity check status=%d body=%s", connectivity.Code, connectivity.Body.String())
	}
	if denied := f.request(http.MethodPost, "/api/v1/environments/environment-test/connectivity-checks", nil, alice); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner connectivity check status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestReleaseReviewPreviewRBACContentAndDigestGuard(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	component := domain.Component{
		ID: "component-review-api", Slug: "review-api", Name: "Review API", Layer: domain.LayerRuntimeState,
		OwnerID: seed.ComponentOwnerRuntimeID, CreatedAt: now, UpdatedAt: now,
	}
	if err := f.database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	f.configurePlaybookRoot(root)
	release := domain.ComponentRelease{
		ID: "release-review-api", ComponentID: component.ID, LineName: "baseline", Version: "1.0.0-p1",
		Compatibility: domain.CompatibilityNotApplicable, Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now,
	}
	for _, kind := range []domain.ActionKind{domain.ActionInstall, domain.ActionVerify, domain.ActionRollback} {
		content := []byte("---\n- hosts: all\n  # " + string(kind) + "\n  tasks: []\n")
		digest := sha256.Sum256(content)
		path := "managed/review-api/release-review-api/" + string(kind) + ".yml"
		release.Actions = append(release.Actions, domain.ActionDefinition{
			ID: "action-review-api-" + string(kind), ReleaseID: release.ID, Name: string(kind), Kind: kind,
			Playbook: path, PlaybookSHA256: fmt.Sprintf("%x", digest), HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow,
		})
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	release, err := f.database.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.database.SubmitComponentReleaseReview(ctx, release.ID, service.ComponentReleaseSpecDigest(release), release.PublicationGeneration, now); err != nil {
		t.Fatal(err)
	}
	owner := f.session(seed.ComponentOwnerRuntimeID)
	admin := f.session(seed.PlatformAdminID)
	if denied := f.request(http.MethodGet, "/api/v1/component-releases/"+release.ID+"/review-preview", nil, owner); denied.Code != http.StatusForbidden {
		t.Fatalf("owner preview status=%d body=%s", denied.Code, denied.Body.String())
	}
	response := f.request(http.MethodGet, "/api/v1/component-releases/"+release.ID+"/review-preview", nil, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("admin preview status=%d body=%s", response.Code, response.Body.String())
	}
	preview := decodeEnvelope(t, response)["data"].(map[string]any)
	playbooks := preview["playbooks"].([]any)
	previewDigest, _ := preview["previewDigest"].(string)
	if preview["componentName"] != component.Name || len(playbooks) != 3 || previewDigest == "" || !strings.Contains(playbooks[0].(map[string]any)["content"].(string), "hosts: all") {
		t.Fatalf("preview=%#v", preview)
	}
	missing := f.request(http.MethodPost, "/api/v1/component-releases/"+release.ID+"/review-decision", map[string]any{"decision": "approve"}, admin)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing digest status=%d body=%s", missing.Code, missing.Body.String())
	}
	stale := f.request(http.MethodPost, "/api/v1/component-releases/"+release.ID+"/review-decision", map[string]any{"decision": "approve", "expectedPreviewDigest": "stale"}, admin)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale digest status=%d body=%s", stale.Code, stale.Body.String())
	}
	blankReject := f.request(http.MethodPost, "/api/v1/component-releases/"+release.ID+"/review-decision", map[string]any{"decision": "reject", "expectedPreviewDigest": previewDigest}, admin)
	if blankReject.Code != http.StatusBadRequest {
		t.Fatalf("blank rejection status=%d body=%s", blankReject.Code, blankReject.Body.String())
	}
	approved := f.request(http.MethodPost, "/api/v1/component-releases/"+release.ID+"/review-decision", map[string]any{"decision": "approve", "comment": "preview checked", "expectedPreviewDigest": previewDigest}, admin)
	if approved.Code != http.StatusOK || !strings.Contains(approved.Body.String(), `"status":"approved"`) {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	if noLongerPending := f.request(http.MethodGet, "/api/v1/component-releases/"+release.ID+"/review-preview", nil, admin); noLongerPending.Code != http.StatusConflict {
		t.Fatalf("approved preview status=%d body=%s", noLongerPending.Code, noLongerPending.Body.String())
	}
}

func TestCatalogRepositoryEndpointsRequireEnvironmentOwner(t *testing.T) {
	f := newAPIFixture(t)
	owner := f.session(seed.EnvironmentOwnerID)
	nonOwner := f.session(seed.ComponentOwnerRuntimeID)
	if denied := f.request(http.MethodGet, "/api/v1/catalog-repository", nil, nonOwner); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner Catalog repository status=%d body=%s", denied.Code, denied.Body.String())
	}
	if denied := f.request(http.MethodPost, "/api/v1/catalog-repository/backups", nil, nonOwner); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner Catalog backup status=%d body=%s", denied.Code, denied.Body.String())
	}
	disabled := f.request(http.MethodGet, "/api/v1/catalog-repository", nil, owner)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disabled Catalog repository status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	status := decodeEnvelope(t, disabled)["data"].(map[string]any)
	if status["enabled"] != false || status["configured"] != false || status["reasonCode"] != catalogBackupDisabledCode {
		t.Fatalf("disabled Catalog repository body=%#v", status)
	}
	create := f.request(http.MethodPost, "/api/v1/catalog-repository/create", map[string]any{"path": "catalog.git"}, owner)
	if create.Code != http.StatusConflict {
		t.Fatalf("disabled Catalog repository create status=%d body=%s", create.Code, create.Body.String())
	}
	createError := decodeEnvelope(t, create)["error"].(map[string]any)
	if createError["code"] != catalogBackupDisabledCode {
		t.Fatalf("disabled Catalog repository create error=%#v", createError)
	}
	backupNow := f.request(http.MethodPost, "/api/v1/catalog-repository/backups", nil, owner)
	if backupNow.Code != http.StatusConflict {
		t.Fatalf("disabled Catalog backup status=%d body=%s", backupNow.Code, backupNow.Body.String())
	}
	backupError := decodeEnvelope(t, backupNow)["error"].(map[string]any)
	if backupError["code"] != catalogBackupDisabledCode {
		t.Fatalf("disabled Catalog backup error=%#v", backupError)
	}
}

func TestCodedConflictErrorPreservesAPIErrorCodeAndDetails(t *testing.T) {
	response := httptest.NewRecorder()
	writeError(response, &domain.CodedError{Code: "target_catalog_not_empty", Message: "target database already contains components or scenarios", Cause: domain.ErrConflict, Details: map[string]any{"componentCount": 1, "scenarioCount": 0}})
	if response.Code != http.StatusConflict {
		t.Fatalf("coded error status=%d body=%s", response.Code, response.Body.String())
	}
	body := decodeEnvelope(t, response)["error"].(map[string]any)
	if body["code"] != "target_catalog_not_empty" || body["details"].(map[string]any)["componentCount"] != float64(1) {
		t.Fatalf("coded error body=%#v", body)
	}
}

func TestCatalogRepositoryErrorsDistinguishInvalidPathsFromOperationalFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid path", err: fmt.Errorf("%w: absolute repository paths must start with /", domain.ErrInvalid), wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "existing target", err: fmt.Errorf("%w: repository path already exists", domain.ErrConflict), wantStatus: http.StatusConflict, wantCode: "conflict"},
		{name: "git failure", err: errors.New("git checkout --orphan catalog: exit status 1"), wantStatus: http.StatusConflict, wantCode: "conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeError(response, catalogRepositoryError(test.err, domain.ErrConflict))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			body := decodeEnvelope(t, response)["error"].(map[string]any)
			if body["code"] != test.wantCode {
				t.Fatalf("error=%#v", body)
			}
			if test.name == "git failure" && strings.HasPrefix(body["message"].(string), "invalid") {
				t.Fatalf("operational failure was presented as invalid input: %#v", body)
			}
		})
	}
}

func TestEnvironmentLifecycleDeleteArchiveAndRestore(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	owner := f.session(seed.EnvironmentOwnerID)
	nonOwner := f.session(seed.ComponentOwnerRuntimeID)

	createEnvironment := func(name string) (string, string) {
		response := f.request(http.MethodPost, "/api/v1/environments", map[string]any{"name": name, "facts": completeTestEnvironmentFacts()}, owner)
		if response.Code != http.StatusCreated {
			t.Fatalf("create environment status=%d body=%s", response.Code, response.Body.String())
		}
		data := decodeEnvelope(t, response)["data"].(map[string]any)
		return data["id"].(string), data["currentRevisionId"].(string)
	}

	deletableID, _ := createEnvironment("Disposable Environment")
	if denied := f.request(http.MethodGet, "/api/v1/environments/"+deletableID+"/lifecycle", nil, nonOwner); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner lifecycle status=%d body=%s", denied.Code, denied.Body.String())
	}
	lifecycle := f.request(http.MethodGet, "/api/v1/environments/"+deletableID+"/lifecycle", nil, owner)
	if lifecycle.Code != http.StatusOK {
		t.Fatalf("unused environment lifecycle status=%d body=%s", lifecycle.Code, lifecycle.Body.String())
	}
	lifecycleData := decodeEnvelope(t, lifecycle)["data"].(map[string]any)
	if lifecycleData["canDelete"] != true || lifecycleData["revisionCount"] != float64(1) {
		t.Fatalf("unused environment lifecycle=%#v", lifecycleData)
	}
	if denied := f.request(http.MethodDelete, "/api/v1/environments/"+deletableID, nil, nonOwner); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner delete status=%d body=%s", denied.Code, denied.Body.String())
	}
	if deleted := f.request(http.MethodDelete, "/api/v1/environments/"+deletableID, nil, owner); deleted.Code != http.StatusOK {
		t.Fatalf("delete unused environment status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, err := f.database.GetEnvironment(ctx, deletableID, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted environment still exists: %v", err)
	}

	historyID, historyRevisionID := createEnvironment("Historical Environment")
	finished := time.Now().UTC()
	if err := f.database.CreateRun(ctx, domain.Run{
		ID: "run-retains-environment", Kind: domain.RunComponentTest, Status: domain.RunFailed,
		RequestedBy: seed.EnvironmentOwnerID, EnvironmentID: historyID, EnvironmentRevisionID: historyRevisionID,
		InputSnapshot: map[string]any{}, CreatedAt: finished.Add(-time.Minute), FinishedAt: &finished,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if blocked := f.request(http.MethodDelete, "/api/v1/environments/"+historyID, nil, owner); blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "运行记录") {
		t.Fatalf("delete historical environment status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	archived := f.request(http.MethodPost, "/api/v1/environments/"+historyID+"/archive", nil, owner)
	if archived.Code != http.StatusOK || !strings.Contains(archived.Body.String(), "archivedAt") {
		t.Fatalf("archive historical environment status=%d body=%s", archived.Code, archived.Body.String())
	}
	if activeList := f.request(http.MethodGet, "/api/v1/environments", nil, owner); strings.Contains(activeList.Body.String(), historyID) {
		t.Fatalf("archived environment remained selectable: %s", activeList.Body.String())
	}
	if ownerArchiveList := f.request(http.MethodGet, "/api/v1/environments?includeArchived=true", nil, owner); !strings.Contains(ownerArchiveList.Body.String(), historyID) {
		t.Fatalf("owner cannot inspect archived environment: %s", ownerArchiveList.Body.String())
	}
	if otherArchiveList := f.request(http.MethodGet, "/api/v1/environments?includeArchived=true", nil, nonOwner); strings.Contains(otherArchiveList.Body.String(), historyID) {
		t.Fatalf("archived environment leaked into task selection: %s", otherArchiveList.Body.String())
	}
	if update := f.request(http.MethodPut, "/api/v1/environments/"+historyID+"/facts", map[string]any{"facts": map[string]any{}, "changeReason": "不应允许"}, owner); update.Code != http.StatusConflict || !strings.Contains(update.Body.String(), "已归档") {
		t.Fatalf("archived environment update status=%d body=%s", update.Code, update.Body.String())
	}
	if err := f.database.CreateRun(ctx, domain.Run{
		ID: "run-rejected-for-archived-environment", Kind: domain.RunComponentTest, Status: domain.RunQueued,
		RequestedBy: seed.EnvironmentOwnerID, EnvironmentID: historyID, EnvironmentRevisionID: historyRevisionID,
		InputSnapshot: map[string]any{}, CreatedAt: time.Now().UTC(),
	}, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("archived environment database fence error=%v", err)
	}
	if err := f.database.SaveEnvironmentHealthCheck(ctx, domain.EnvironmentHealthCheck{
		ID: "health-rejected-for-archived-environment", EnvironmentID: historyID, EnvironmentRevisionID: historyRevisionID,
		Status: "healthy", Results: []domain.EnvironmentEndpointCheck{}, CheckedAt: time.Now().UTC(),
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("archived environment health-check fence error=%v", err)
	}
	if err := f.database.SaveEnvironmentSSHCheck(ctx, domain.EnvironmentSSHCheck{
		ID: "ssh-rejected-for-archived-environment", EnvironmentID: historyID, EnvironmentRevisionID: historyRevisionID,
		Status: "healthy", Results: []domain.EnvironmentSSHHostCheck{}, CheckedAt: time.Now().UTC(),
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("archived environment SSH-check fence error=%v", err)
	}
	restored := f.request(http.MethodPost, "/api/v1/environments/"+historyID+"/unarchive", nil, owner)
	if restored.Code != http.StatusOK || decodeEnvelope(t, restored)["data"].(map[string]any)["archivedAt"] != nil {
		t.Fatalf("unarchive environment status=%d body=%s", restored.Code, restored.Body.String())
	}
	if activeList := f.request(http.MethodGet, "/api/v1/environments", nil, owner); !strings.Contains(activeList.Body.String(), historyID) {
		t.Fatalf("restored environment is not selectable: %s", activeList.Body.String())
	}

	buildID, buildRevisionID := createEnvironment("Building Environment")
	if err := f.database.CreateComponentImageBuild(ctx, domain.ComponentImageBuild{
		ID: "image-build-retains-environment", ReleaseID: "release-test-runtime-1.1.0",
		EnvironmentID: buildID, EnvironmentRevisionID: buildRevisionID, RequestedBy: seed.ComponentOwnerRuntimeID,
		Status: domain.ImageBuildQueued, DockerfileSHA256: strings.Repeat("a", 64), ImageTag: "test", ImageRef: "registry.example/test:latest", CreatedAt: finished,
	}); err != nil {
		t.Fatal(err)
	}
	buildLifecycle := f.request(http.MethodGet, "/api/v1/environments/"+buildID+"/lifecycle", nil, owner)
	buildLifecycleData := decodeEnvelope(t, buildLifecycle)["data"].(map[string]any)
	if buildLifecycleData["activeImageBuildCount"] != float64(1) || buildLifecycleData["canArchive"] != false {
		t.Fatalf("active image build lifecycle=%#v", buildLifecycleData)
	}
	if blocked := f.request(http.MethodPost, "/api/v1/environments/"+buildID+"/archive", nil, owner); blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "镜像构建") {
		t.Fatalf("archive building environment status=%d body=%s", blocked.Code, blocked.Body.String())
	}

	installedID, installedRevisionID := createEnvironment("Installed Environment")
	installRunID := "run-retains-installed-environment"
	if err := f.database.CreateRun(ctx, domain.Run{
		ID: installRunID, Kind: domain.RunComponentTest, Status: domain.RunSucceeded,
		RequestedBy: seed.EnvironmentOwnerID, EnvironmentID: installedID, EnvironmentRevisionID: installedRevisionID,
		ComponentReleaseID: "release-test-runtime-1.1.0", InputSnapshot: map[string]any{}, CreatedAt: finished, FinishedAt: &finished,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.database.UpsertEnvironmentComponentInstallation(ctx, domain.EnvironmentComponentInstallation{
		EnvironmentID: installedID, ComponentID: "component-test-runtime", ReleaseID: "release-test-runtime-1.1.0", InstallRunID: installRunID,
		BackupRef:   "/var/lib/clusterforge/backups/" + installedID + "/component-test-runtime/" + installRunID,
		Backup:      domain.BackupMetadata{EnvironmentID: installedID, ComponentID: "component-test-runtime", ReleaseID: "release-test-runtime-1.1.0", InstallRunID: installRunID, CapturedAt: finished, DependencySnapshot: map[string]any{}},
		InstalledAt: finished,
	}); err != nil {
		t.Fatal(err)
	}
	if blocked := f.request(http.MethodPost, "/api/v1/environments/"+installedID+"/archive", nil, owner); blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "回滚至干净状态") {
		t.Fatalf("archive installed environment status=%d body=%s", blocked.Code, blocked.Body.String())
	}

	var deletedAudits, archivedAudits, restoredAudits int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='environment.deleted' AND resource_id=?`, deletableID).Scan(&deletedAudits); err != nil || deletedAudits != 1 {
		t.Fatalf("environment delete audits=%d err=%v", deletedAudits, err)
	}
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='environment.archived' AND resource_id=?`, historyID).Scan(&archivedAudits); err != nil || archivedAudits != 1 {
		t.Fatalf("environment archive audits=%d err=%v", archivedAudits, err)
	}
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='environment.unarchived' AND resource_id=?`, historyID).Scan(&restoredAudits); err != nil || restoredAudits != 1 {
		t.Fatalf("environment unarchive audits=%d err=%v", restoredAudits, err)
	}
}

func TestEnvironmentExportImportAndCredentialReferenceModes(t *testing.T) {
	f := newAPIFixture(t)
	owner := f.session(seed.EnvironmentOwnerID)
	updated := f.request(http.MethodPut, "/api/v1/environments/environment-test/credential-refs", map[string]any{
		"credentialRefs": []any{map[string]any{"name": "SSH_KEY", "kind": "envVarRef", "reference": "CLUSTERFORGE_TEST_SSH_KEY"}},
		"changeReason":   "配置导出测试凭据引用",
	}, owner)
	if updated.Code != http.StatusOK {
		t.Fatalf("update credential refs status=%d body=%s", updated.Code, updated.Body.String())
	}
	current := decodeEnvelope(t, updated)["data"].(map[string]any)["currentRevision"].(map[string]any)
	revisionID := current["id"].(string)
	safe := f.request(http.MethodPost, "/api/v1/environments/environment-test/revisions/"+revisionID+"/export", map[string]any{"includeCredentialReferences": false}, owner)
	if safe.Code != http.StatusOK || !strings.Contains(safe.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("safe export status=%d body=%s", safe.Code, safe.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(safe.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	refs := document["snapshot"].(map[string]any)["credentialRefs"].([]any)
	if _, exposed := refs[0].(map[string]any)["reference"]; exposed {
		t.Fatalf("safe export exposed reference: %#v", refs[0])
	}
	sensitive := f.request(http.MethodPost, "/api/v1/environments/environment-test/revisions/"+revisionID+"/export", map[string]any{"includeCredentialReferences": true}, owner)
	if sensitive.Code != http.StatusOK || !strings.Contains(sensitive.Body.String(), "CLUSTERFORGE_TEST_SSH_KEY") {
		t.Fatalf("sensitive export status=%d body=%s", sensitive.Code, sensitive.Body.String())
	}
	var sensitiveDocument map[string]any
	if err := json.Unmarshal(sensitive.Body.Bytes(), &sensitiveDocument); err != nil {
		t.Fatal(err)
	}
	sensitiveDocument["snapshot"].(map[string]any)["variables"] = map[string]any{"IMAGE_REGISTRY": "registry.example:5000/"}
	tamperedBytes, err := json.Marshal(sensitiveDocument)
	if err != nil {
		t.Fatal(err)
	}
	var tamperedDocument map[string]any
	if err := json.Unmarshal(tamperedBytes, &tamperedDocument); err != nil {
		t.Fatal(err)
	}
	tamperedDocument["containsCredentialReferences"] = false
	tamperedInput := map[string]any{"document": tamperedDocument, "target": map[string]any{"kind": "new", "name": "Unconfirmed Imported Environment"}, "changeReason": "验证凭据确认门禁"}
	tamperedPlan := f.request(http.MethodPost, "/api/v1/environment-imports/plan", tamperedInput, owner)
	if tamperedPlan.Code != http.StatusOK || !strings.Contains(tamperedPlan.Body.String(), "必须明确确认复用") {
		t.Fatalf("tampered import plan status=%d body=%s", tamperedPlan.Code, tamperedPlan.Body.String())
	}
	tamperedInput["expectedPlanDigest"] = decodeEnvelope(t, tamperedPlan)["data"].(map[string]any)["planDigest"]
	if blocked := f.request(http.MethodPost, "/api/v1/environment-imports", tamperedInput, owner); blocked.Code != http.StatusBadRequest || !strings.Contains(blocked.Body.String(), "confirmCredentialReferences") {
		t.Fatalf("tampered credential confirmation status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	planInput := map[string]any{"document": sensitiveDocument, "target": map[string]any{"kind": "new", "name": "Imported Environment"}, "changeReason": "验收环境导入", "confirmCredentialReferences": true}
	planResponse := f.request(http.MethodPost, "/api/v1/environment-imports/plan", planInput, owner)
	if planResponse.Code != http.StatusOK {
		t.Fatalf("import plan status=%d body=%s", planResponse.Code, planResponse.Body.String())
	}
	planInput["expectedPlanDigest"] = decodeEnvelope(t, planResponse)["data"].(map[string]any)["planDigest"]
	imported := f.request(http.MethodPost, "/api/v1/environment-imports", planInput, owner)
	if imported.Code != http.StatusCreated || !strings.Contains(imported.Body.String(), "Imported Environment") {
		t.Fatalf("environment import status=%d body=%s", imported.Code, imported.Body.String())
	}
	importedRevision := decodeEnvelope(t, imported)["data"].(map[string]any)["currentRevision"].(map[string]any)
	if registry := importedRevision["variables"].(map[string]any)["IMAGE_REGISTRY"]; registry != "registry.example:5000" {
		t.Fatalf("imported IMAGE_REGISTRY=%v, want normalized value", registry)
	}
}

func TestEnvironmentImportPlanSerializesEmptyWarningsAsArray(t *testing.T) {
	f := newAPIFixture(t)
	owner := f.session(seed.EnvironmentOwnerID)
	input := map[string]any{
		"document": map[string]any{
			"formatVersion": "clusterforge-environment/v1",
			"exportedAt":    "2026-08-28T00:00:00Z",
			"source": map[string]any{
				"environmentId": "environment-source", "environmentName": "Source",
				"revisionId": "environment-revision-source", "revision": 1,
			},
			"snapshot": map[string]any{
				"facts": completeTestEnvironmentFacts(), "hosts": []any{},
				"variables": map[string]any{}, "credentialRefs": []any{},
			},
		},
		"target":       map[string]any{"kind": "new", "name": "Imported Empty Environment"},
		"changeReason": "验证空警告契约",
	}

	response := f.request(http.MethodPost, "/api/v1/environment-imports/plan", input, owner)
	if response.Code != http.StatusOK {
		t.Fatalf("environment import plan status=%d body=%s", response.Code, response.Body.String())
	}
	data := decodeEnvelope(t, response)["data"].(map[string]any)
	warnings, ok := data["warnings"].([]any)
	if !ok || len(warnings) != 0 {
		t.Fatalf("environment import warnings=%#v, want empty array", data["warnings"])
	}
}

func TestComponentImportPlanDoesNotWriteAndRejectsDatabaseDrift(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	entry := map[string]any{"component": map[string]any{"name": "Imported Unit", "slug": "imported-unit", "description": "", "layer": "runtime_state", "tags": []any{"runtime"}}, "release": map[string]any{"lineName": "Imported Unit 1.0", "version": "1.0.0", "environmentConstraints": map[string]any{}, "parameters": []any{}, "dependencies": []any{}, "actions": []any{}}, "playbooks": []any{}}
	var before int
	_ = f.database.DB().QueryRow(`SELECT COUNT(*) FROM components`).Scan(&before)
	plan := f.request(http.MethodPost, "/api/v1/component-imports/plan", map[string]any{"entries": []any{entry}}, alice)
	if plan.Code != http.StatusOK {
		t.Fatalf("component import plan status=%d body=%s", plan.Code, plan.Body.String())
	}
	var after int
	_ = f.database.DB().QueryRow(`SELECT COUNT(*) FROM components`).Scan(&after)
	if after != before {
		t.Fatalf("component import preview wrote rows: before=%d after=%d", before, after)
	}
	now := time.Now().UTC()
	if err := f.database.CreateComponent(context.Background(), domain.Component{ID: "component-import-drift", Slug: "imported-unit", Name: "Drift", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: seed.ComponentOwnerRuntimeID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	digest := decodeEnvelope(t, plan)["data"].(map[string]any)["planDigest"]
	submit := f.request(http.MethodPost, "/api/v1/component-imports", map[string]any{"entries": []any{entry}, "expectedPlanDigest": digest}, alice)
	if submit.Code != http.StatusConflict {
		t.Fatalf("drifted component import status=%d body=%s", submit.Code, submit.Body.String())
	}
}

func TestComponentImportRejectsUnknownActionAndRiskEnums(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	entry := func(slug, actionType, releaseRisk, actionRisk string) map[string]any {
		actions := []any{}
		playbooks := []any{}
		if actionType != "" {
			actions = append(actions, map[string]any{"name": "install", "type": actionType, "playbook": "install.yml", "hostGroup": "test_nodes", "timeoutSeconds": 60, "riskLevel": actionRisk})
			playbooks = append(playbooks, map[string]any{"filename": "install.yml", "content": "---\n- hosts: all\n  tasks: []\n"})
		}
		return map[string]any{
			"component": map[string]any{"name": "Invalid Enum", "slug": slug, "layer": "runtime_state", "tags": []any{"runtime"}},
			"release":   map[string]any{"lineName": slug + " 1.0", "version": "1.0.0", "riskLevel": releaseRisk, "environmentConstraints": map[string]any{}, "parameters": []any{}, "dependencies": []any{}, "actions": actions},
			"playbooks": playbooks,
		}
	}
	for _, test := range []struct {
		name  string
		entry map[string]any
		want  string
	}{
		{"unknown action", entry("invalid-action", "execute_anything", "low", "low"), "invalid action kind"},
		{"invalid release risk", entry("invalid-release-risk", "", "urgent", ""), "invalid release risk level"},
		{"invalid action risk", entry("invalid-action-risk", "install", "low", "urgent"), "invalid action risk level"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := f.request(http.MethodPost, "/api/v1/component-imports/plan", map[string]any{"entries": []any{test.entry}}, alice)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("invalid enum status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func componentImportEntry(slug string) map[string]any {
	filename := "install.yml"
	return map[string]any{
		"component": map[string]any{"name": "Imported " + slug, "slug": slug, "description": "", "layer": "runtime_state", "tags": []any{"runtime"}},
		"release":   map[string]any{"lineName": strings.TrimSpace(slug) + " 1.0", "version": "1.0.0", "environmentConstraints": map[string]any{}, "parameters": []any{}, "dependencies": []any{}, "actions": []any{map[string]any{"name": "install", "type": "install", "playbook": filename, "hostGroup": "test_nodes", "timeoutSeconds": 60, "idempotent": true}}},
		"playbooks": []any{map[string]any{"filename": filename, "content": "---\n- hosts: all\n  tasks: []\n"}},
	}
}

func TestComponentImportAtomicallyCommitsNormalizedCatalogAndPlaybook(t *testing.T) {
	f := newAPIFixture(t)
	root := t.TempDir()
	f.configurePlaybookRoot(root)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	entry := componentImportEntry(" imported-atomic ")
	plan := f.request(http.MethodPost, "/api/v1/component-imports/plan", map[string]any{"entries": []any{entry}}, alice)
	if plan.Code != http.StatusOK {
		t.Fatalf("component import plan status=%d body=%s", plan.Code, plan.Body.String())
	}
	digest := decodeEnvelope(t, plan)["data"].(map[string]any)["planDigest"]
	response := f.request(http.MethodPost, "/api/v1/component-imports", map[string]any{"entries": []any{entry}, "expectedPlanDigest": digest}, alice)
	if response.Code != http.StatusCreated {
		t.Fatalf("component import status=%d body=%s", response.Code, response.Body.String())
	}
	data := decodeEnvelope(t, response)["data"].(map[string]any)
	releaseID := data["createdDrafts"].(map[string]any)["imported-atomic"].(string)
	release, err := f.database.GetComponentRelease(context.Background(), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Actions) != 1 || release.Actions[0].Playbook == "" {
		t.Fatalf("imported release actions=%#v", release.Actions)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(release.Actions[0].Playbook))); err != nil {
		t.Fatalf("managed Playbook missing: %v", err)
	}
	var audits int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='component_import.completed' AND resource_id=?`, digest).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("component import audit count=%d err=%v", audits, err)
	}
}

func TestComponentImportDatabaseFailureRollsBackRowsAuditsAndFiles(t *testing.T) {
	f := newAPIFixture(t)
	root := t.TempDir()
	f.configurePlaybookRoot(root)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	entries := []any{componentImportEntry("atomic-first"), componentImportEntry("atomic-second")}
	plan := f.request(http.MethodPost, "/api/v1/component-imports/plan", map[string]any{"entries": entries}, alice)
	if plan.Code != http.StatusOK {
		t.Fatalf("component import plan status=%d body=%s", plan.Code, plan.Body.String())
	}
	if _, err := f.database.DB().Exec(`CREATE TRIGGER fail_component_import BEFORE INSERT ON components WHEN NEW.slug='atomic-second' BEGIN SELECT RAISE(ABORT,'forced component import failure'); END`); err != nil {
		t.Fatal(err)
	}
	digest := decodeEnvelope(t, plan)["data"].(map[string]any)["planDigest"]
	response := f.request(http.MethodPost, "/api/v1/component-imports", map[string]any{"entries": entries, "expectedPlanDigest": digest}, alice)
	if response.Code < http.StatusBadRequest {
		t.Fatalf("failed component import status=%d body=%s", response.Code, response.Body.String())
	}
	var rows, audits int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM components WHERE slug IN ('atomic-first','atomic-second')`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE resource_id=?`, digest).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || audits != 0 {
		t.Fatalf("failed import left rows=%d audits=%d", rows, audits)
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && !strings.Contains(path, "/managed/fixtures/") {
			return fmt.Errorf("failed import left file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFailedIdempotentInstallRetryRebindsBackupToNewRun(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	alice := f.session(seed.ComponentOwnerRuntimeID)
	now := time.Now().UTC()
	component := domain.Component{ID: "component-retry-safe", Slug: "retry-safe", Name: "Retry Safe", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: seed.ComponentOwnerRuntimeID, CreatedAt: now, UpdatedAt: now}
	if err := f.database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "release-retry-safe", ComponentID: component.ID, Version: "1.0.0", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now, Actions: []domain.ActionDefinition{
		{ID: "action-retry-install", ReleaseID: "release-retry-safe", Name: "install", Kind: domain.ActionInstall, Playbook: "tests/retry/install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, Idempotent: true},
		{ID: "action-retry-rollback", ReleaseID: "release-retry-safe", Name: "rollback", Kind: domain.ActionRollback, Playbook: "tests/retry/rollback.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
	}}
	if err := f.database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, f.database, f.runner.root, release.ID)
	request := map[string]any{"environmentId": "environment-test", "mode": "install_verify"}
	plan := f.request(http.MethodPost, "/api/v1/component-releases/release-retry-safe/test-plan", request, alice)
	if plan.Code != http.StatusOK {
		t.Fatalf("retry source plan status=%d body=%s", plan.Code, plan.Body.String())
	}
	planData := decodeEnvelope(t, plan)["data"].(map[string]any)
	if requirements, ok := planData["deliveryRequirements"].([]any); !ok || len(requirements) != 0 {
		t.Fatalf("empty delivery requirements=%#v, want empty array", planData["deliveryRequirements"])
	}
	request["expectedPlanDigest"] = planData["planDigest"]
	f.runner.failPlaybook = "tests/retry/install.yml"
	started := f.request(http.MethodPost, "/api/v1/component-releases/release-retry-safe/test-runs", request, alice)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start retry source status=%d body=%s", started.Code, started.Body.String())
	}
	sourceID := decodeEnvelope(t, started)["data"].(map[string]any)["id"].(string)
	waitForRun(t, f.database, sourceID, domain.RunFailed)
	f.runner.failPlaybook = ""
	retryPlan := f.request(http.MethodPost, "/api/v1/runs/"+sourceID+"/retry-plan", nil, alice)
	if retryPlan.Code != http.StatusOK {
		t.Fatalf("retry plan status=%d body=%s", retryPlan.Code, retryPlan.Body.String())
	}
	retryDigest := decodeEnvelope(t, retryPlan)["data"].(map[string]any)["planDigest"]
	retried := f.request(http.MethodPost, "/api/v1/runs/"+sourceID+"/retry-runs", map[string]any{"expectedPlanDigest": retryDigest}, alice)
	if retried.Code != http.StatusAccepted || !strings.Contains(retried.Body.String(), sourceID) {
		t.Fatalf("retry run status=%d body=%s", retried.Code, retried.Body.String())
	}
	retriedID := decodeEnvelope(t, retried)["data"].(map[string]any)["id"].(string)
	waitForRun(t, f.database, retriedID, domain.RunSucceeded)
	installation, err := f.database.GetEnvironmentComponentInstallation(ctx, "environment-test", component.ID)
	if err != nil {
		t.Fatal(err)
	}
	if installation.InstallRunID != retriedID || installation.Backup.InstallRunID != retriedID || !strings.Contains(installation.BackupRef, retriedID) {
		t.Fatalf("retry backup provenance=%#v, want run %s", installation, retriedID)
	}
	rollback := f.request(http.MethodPost, "/api/v1/component-releases/release-retry-safe/test-plan", map[string]any{
		"environmentId": "environment-test", "mode": "rollback", "rollbackVerification": map[string]any{"kind": "rollback_only"},
	}, alice)
	if rollback.Code != http.StatusOK || !strings.Contains(rollback.Body.String(), retriedID) {
		t.Fatalf("rollback did not accept retry backup status=%d body=%s", rollback.Code, rollback.Body.String())
	}
	secondPlan := f.request(http.MethodPost, "/api/v1/runs/"+sourceID+"/retry-plan", nil, alice)
	if secondPlan.Code != http.StatusOK {
		t.Fatalf("second retry plan status=%d body=%s", secondPlan.Code, secondPlan.Body.String())
	}
	f.runner.failPlaybook = "tests/retry/install.yml"
	secondRetry := f.request(http.MethodPost, "/api/v1/runs/"+sourceID+"/retry-runs", map[string]any{"expectedPlanDigest": decodeEnvelope(t, secondPlan)["data"].(map[string]any)["planDigest"]}, alice)
	if secondRetry.Code != http.StatusAccepted {
		t.Fatalf("second retry status=%d body=%s", secondRetry.Code, secondRetry.Body.String())
	}
	secondData := decodeEnvelope(t, secondRetry)["data"].(map[string]any)
	if secondData["retryAttempt"] != float64(2) {
		t.Fatalf("second retry attempt=%v body=%s", secondData["retryAttempt"], secondRetry.Body.String())
	}
	waitForRun(t, f.database, secondData["id"].(string), domain.RunFailed)
}

func TestEvolutionRoundTripRetryKeepsRollbackBackupConsistent(t *testing.T) {
	tests := []struct {
		name           string
		failPlaybook   string
		failOccurrence int
		wantInstallRun string
	}{
		{name: "parent install", failPlaybook: "tests/runtime/install.yml", failOccurrence: 1, wantInstallRun: "retry"},
		{name: "parent verify", failPlaybook: "tests/runtime/verify.yml", failOccurrence: 1, wantInstallRun: "retry"},
		{name: "target verify", failPlaybook: "tests/runtime/verify.yml", failOccurrence: 2, wantInstallRun: "source"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newAPIFixture(t)
			if _, err := f.database.DB().Exec(`UPDATE action_definitions SET idempotent=1 WHERE id='action-test-runtime-install-1.0'`); err != nil {
				t.Fatal(err)
			}
			alice := f.session(seed.ComponentOwnerRuntimeID)
			dave := f.session(seed.EnvironmentOwnerID)
			f.runner.failPlaybook = test.failPlaybook
			f.runner.failPlaybookOccurrence = test.failOccurrence

			started := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
				"environmentId": "environment-test", "mode": "evolution_round_trip",
			}, alice)
			if started.Code != http.StatusAccepted {
				t.Fatalf("start status=%d body=%s", started.Code, started.Body.String())
			}
			startData := decodeEnvelope(t, started)["data"].(map[string]any)
			if startData["status"] == string(domain.RunAwaitingApproval) {
				approval := startData["approval"].(map[string]any)
				if approved := f.request(http.MethodPost, "/api/v1/approvals/"+approval["id"].(string)+"/approve", nil, dave); approved.Code != http.StatusOK {
					t.Fatalf("approve source status=%d body=%s", approved.Code, approved.Body.String())
				}
			}
			sourceID := startData["id"].(string)
			waitForRun(t, f.database, sourceID, domain.RunFailed)
			f.runner.failPlaybook = ""

			preview := f.request(http.MethodPost, "/api/v1/runs/"+sourceID+"/retry-plan", nil, alice)
			if preview.Code != http.StatusOK {
				t.Fatalf("retry preview status=%d body=%s", preview.Code, preview.Body.String())
			}
			planDigest := decodeEnvelope(t, preview)["data"].(map[string]any)["planDigest"]
			retried := f.request(http.MethodPost, "/api/v1/runs/"+sourceID+"/retry-runs", map[string]any{"expectedPlanDigest": planDigest}, alice)
			if retried.Code != http.StatusAccepted {
				t.Fatalf("retry status=%d body=%s", retried.Code, retried.Body.String())
			}
			retryData := decodeEnvelope(t, retried)["data"].(map[string]any)
			if retryData["status"] == string(domain.RunAwaitingApproval) {
				approval := retryData["approval"].(map[string]any)
				if approved := f.request(http.MethodPost, "/api/v1/approvals/"+approval["id"].(string)+"/approve", nil, dave); approved.Code != http.StatusOK {
					t.Fatalf("approve retry status=%d body=%s", approved.Code, approved.Body.String())
				}
			}
			retryID := retryData["id"].(string)
			waitForRun(t, f.database, retryID, domain.RunSucceeded)

			run, err := f.database.GetRun(context.Background(), retryID)
			if err != nil {
				t.Fatal(err)
			}
			steps, ok := run.InputSnapshot["steps"].([]any)
			if !ok {
				t.Fatalf("retry steps=%#v", run.InputSnapshot["steps"])
			}
			var rollbackBackup, upgradeBackup map[string]any
			for _, raw := range steps {
				step := raw.(map[string]any)
				backup, _ := step["backup"].(map[string]any)
				switch step["action"] {
				case string(domain.ActionUpgrade):
					upgradeBackup = backup
				case string(domain.ActionRollback):
					rollbackBackup = backup
				}
			}
			if rollbackBackup == nil {
				t.Fatalf("retry has no rollback backup: %#v", steps)
			}
			wantRunID := retryID
			if test.wantInstallRun == "source" {
				wantRunID = sourceID
			}
			if rollbackBackup["installRunId"] != wantRunID {
				t.Fatalf("rollback installRunId=%v want=%s", rollbackBackup["installRunId"], wantRunID)
			}
			if test.wantInstallRun == "retry" && (upgradeBackup == nil || rollbackBackup["installRunId"] != upgradeBackup["installRunId"]) {
				t.Fatalf("retry upgrade/rollback backup mismatch upgrade=%#v rollback=%#v", upgradeBackup, rollbackBackup)
			}
		})
	}
}

func TestRetryChainRejectsASecondActiveBranch(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	source := domain.Run{ID: "run-retry-root", Kind: domain.RunComponentTest, Status: domain.RunFailed, RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1", ComponentReleaseID: "release-test-runtime-1.1.0", Action: domain.ActionInstall, InputSnapshot: map[string]any{}, ArtifactDigest: "fixed-tree-digest", CreatedAt: now, FinishedAt: &now}
	if err := f.database.CreateRun(ctx, source, nil); err != nil {
		t.Fatal(err)
	}
	failedBranch := source
	failedBranch.ID = "run-retry-failed-branch"
	failedBranch.RetryOfRunID = source.ID
	failedBranch.RetryRootRunID = source.ID
	failedBranch.RetryAttempt = 1
	if err := f.database.CreateRun(ctx, failedBranch, nil); err != nil {
		t.Fatal(err)
	}
	duplicateAttempt := failedBranch
	duplicateAttempt.ID = "run-retry-duplicate-attempt"
	if err := f.database.CreateRun(ctx, duplicateAttempt, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate retry attempt store error=%v, want conflict", err)
	}
	activeBranch := source
	activeBranch.ID = "run-retry-active-branch"
	activeBranch.Status = domain.RunAwaitingApproval
	activeBranch.RetryOfRunID = source.ID
	activeBranch.RetryRootRunID = source.ID
	activeBranch.RetryAttempt = 2
	activeBranch.FinishedAt = nil
	if err := f.database.CreateRun(ctx, activeBranch, nil); err != nil {
		t.Fatal(err)
	}
	response := f.request(http.MethodPost, "/api/v1/runs/"+failedBranch.ID+"/retry-plan", nil, f.session(seed.ComponentOwnerRuntimeID))
	if response.Code != http.StatusConflict {
		t.Fatalf("second retry branch status=%d body=%s", response.Code, response.Body.String())
	}
	secondActive := activeBranch
	secondActive.ID = "run-retry-active-branch-2"
	secondActive.RetryOfRunID = failedBranch.ID
	if err := f.database.CreateRun(ctx, secondActive, nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second active retry store error=%v, want conflict", err)
	}
}

func TestCreateRunRejectsWithdrawnScenarioDraftAtFinalStoreBoundary(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	createDraft := func(suffix string) domain.ComponentRelease {
		component := domain.Component{ID: "component-run-fence-" + suffix, Slug: "run-fence-" + suffix, Name: "Run Fence " + suffix, Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: seed.ComponentOwnerRuntimeID, CreatedAt: now, UpdatedAt: now}
		if err := f.database.CreateComponent(ctx, component); err != nil {
			t.Fatal(err)
		}
		release := domain.ComponentRelease{ID: "release-run-fence-" + suffix, ComponentID: component.ID, Version: "1.0.0", Status: domain.ReleaseDraft, Candidate: true, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
		if err := f.database.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
		return release
	}
	createRun := func(id string, release domain.ComponentRelease) error {
		return f.database.CreateRun(ctx, domain.Run{
			ID: id, Kind: domain.RunScenarioTest, Status: domain.RunQueued,
			RequestedBy: seed.ScenarioOwnerID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
			ScenarioRevisionID: "scenario-test-runtime-r2", InputSnapshot: map[string]any{"steps": []any{map[string]any{"releaseId": release.ID}}}, CreatedAt: now,
		}, nil)
	}

	withdrawn := createDraft("withdrawn")
	if err := f.setCandidate(ctx, withdrawn.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := createRun("run-withdrawn-candidate", withdrawn); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("withdrawn candidate run error=%v, want conflict", err)
	}

	deprecated := createDraft("deprecated")
	if err := f.database.DeprecateComponentRelease(ctx, deprecated.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := createRun("run-deprecated-draft", deprecated); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("deprecated draft run error=%v, want conflict", err)
	}
}

func TestEnvironmentOwnerWholeClusterRollbackPreviewApprovalAndCleanup(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	owner := f.session(seed.EnvironmentOwnerID)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	if err := f.database.DeleteEnvironmentComponentInstallation(ctx, "environment-test", "component-test-runtime", "run-installed-runtime-1.1"); err != nil {
		t.Fatal(err)
	}

	type fixtureComponent struct {
		id, releaseID, installActionID, installPlaybook, rollbackPlaybook string
	}
	fixtures := []fixtureComponent{
		{id: "component-clean-foundation", releaseID: "release-clean-foundation", installActionID: "action-clean-foundation-install", installPlaybook: "tests/clean/foundation-install.yml", rollbackPlaybook: "tests/clean/foundation-rollback.yml"},
		{id: "component-clean-service", releaseID: "release-clean-service", installActionID: "action-clean-service-install", installPlaybook: "tests/clean/service-install.yml", rollbackPlaybook: "tests/clean/service-rollback.yml"},
	}
	for _, item := range fixtures {
		component := domain.Component{ID: item.id, Slug: item.id, Name: item.id, Layer: domain.LayerRuntimeState, Tags: []string{"runtime", "core"}, OwnerID: seed.ComponentOwnerRuntimeID, CreatedAt: now, UpdatedAt: now}
		if err := f.database.CreateComponent(ctx, component); err != nil {
			t.Fatal(err)
		}
		release := domain.ComponentRelease{
			ID: item.releaseID, ComponentID: item.id, Version: "clean-v1",
			Status: domain.ReleaseReleased, RiskLevel: domain.RiskLow,
			EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now, ReleasedAt: &now,
			Actions: []domain.ActionDefinition{
				{ID: item.installActionID, ReleaseID: item.releaseID, Name: "install", Kind: domain.ActionInstall, Playbook: item.installPlaybook, TimeoutSeconds: 60},
				{ID: "action-" + item.id + "-rollback", ReleaseID: item.releaseID, Name: "rollback", Kind: domain.ActionRollback, Playbook: item.rollbackPlaybook, TimeoutSeconds: 60},
			},
		}
		if err := f.database.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
	}

	for i := range fixtures {
		item := &fixtures[i]
		testutil.Workspaces(t, f.database, f.runner.root, item.releaseID)
		item.installPlaybook = "managed/fixtures/" + item.releaseID + "/install.yml"
		item.rollbackPlaybook = "managed/fixtures/" + item.releaseID + "/rollback.yml"
	}
	sourceRunIDs := []string{"run-clean-foundation-install", "run-clean-service-install"}
	lockedInstall := func(id string, item fixtureComponent, limit string) map[string]any {
		return map[string]any{
			"id": id, "nodeId": id, "name": item.id + " · install", "componentId": item.id, "componentName": item.id,
			"releaseId": item.releaseID, "releaseVersion": "clean-v1", "releaseSpecDigest": "locked", "actionId": item.installActionID,
			"action": "install", "playbook": item.installPlaybook, "playbookDigest": fmt.Sprintf("%x", sha256.Sum256([]byte(testutil.Playbook))),
			"limit": limit, "variables": map[string]any{}, "requiredCredentials": []any{}, "timeoutSeconds": 60,
		}
	}
	later := now.Add(time.Minute)
	sourceRuns := []domain.Run{
		{
			ID: sourceRunIDs[0], Kind: domain.RunScenarioTest, Status: domain.RunSucceeded, RequestedBy: seed.ScenarioOwnerID,
			EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1", ScenarioRevisionID: "scenario-test-runtime-r1",
			InputSnapshot: map[string]any{"steps": []any{
				lockedInstall("foundation-control", fixtures[0], "test_nodes"),
				lockedInstall("foundation-workers", fixtures[0], "test_nodes"),
			}, "treeDigest": "fixed-tree-digest"},
			ArtifactDigest: "fixed-tree-digest", CreatedAt: now, StartedAt: &now, FinishedAt: &now,
		},
		{
			ID: sourceRunIDs[1], Kind: domain.RunScenarioTest, Status: domain.RunSucceeded, RequestedBy: seed.ScenarioOwnerID,
			EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1", ScenarioRevisionID: "scenario-test-runtime-r1",
			InputSnapshot:  map[string]any{"steps": []any{lockedInstall("service", fixtures[1], "test_nodes")}, "treeDigest": "fixed-tree-digest"},
			ArtifactDigest: "fixed-tree-digest", CreatedAt: later, StartedAt: &later, FinishedAt: &later,
		},
	}
	for _, sourceRun := range sourceRuns {
		if err := f.database.CreateRun(ctx, sourceRun, nil); err != nil {
			t.Fatal(err)
		}
	}
	for index, item := range fixtures {
		sourceRunID := sourceRunIDs[index]
		capturedAt := now
		if index == 1 {
			capturedAt = later
		}
		backup := domain.BackupMetadata{
			EnvironmentID: "environment-test", ComponentID: item.id, ReleaseID: item.releaseID, ActionID: item.installActionID,
			InstallRunID: sourceRunID, CapturedAt: capturedAt, PlaybookSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(testutil.Playbook))),
			DependencySnapshot: map[string]any{"dependencies": []any{}},
		}
		if err := f.database.UpsertEnvironmentComponentInstallation(ctx, domain.EnvironmentComponentInstallation{
			EnvironmentID: "environment-test", ComponentID: item.id, ReleaseID: item.releaseID, InstallRunID: sourceRunID,
			BackupRef: "/var/lib/clusterforge/backups/environment-test/" + item.id + "/" + item.releaseID + "/" + sourceRunID,
			Backup:    backup, TestOnly: true, InstalledAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if denied := f.request(http.MethodPost, "/api/v1/environments/environment-test/cluster-rollback-plan", nil, alice); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner preview status=%d body=%s", denied.Code, denied.Body.String())
	}
	preview := f.request(http.MethodPost, "/api/v1/environments/environment-test/cluster-rollback-plan", nil, owner)
	if preview.Code != http.StatusOK {
		t.Fatalf("rollback preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	plan := decodeEnvelope(t, preview)["data"].(map[string]any)
	if plan["componentCount"] != float64(2) || plan["nodeCount"] != float64(3) || plan["requiresApproval"] != true {
		t.Fatalf("rollback plan summary=%#v", plan)
	}
	sources := plan["sources"].([]any)
	if len(sources) != 2 || sources[0].(map[string]any)["runId"] != sourceRunIDs[1] {
		t.Fatalf("rollback sources=%#v", plan["sources"])
	}
	steps := plan["steps"].([]any)
	if steps[0].(map[string]any)["componentId"] != fixtures[1].id || steps[1].(map[string]any)["componentId"] != fixtures[0].id || steps[2].(map[string]any)["componentId"] != fixtures[0].id {
		t.Fatalf("rollback steps are not reverse ordered: %#v", steps)
	}
	if requirements, ok := plan["deliveryRequirements"].([]any); !ok || len(requirements) != 0 {
		t.Fatalf("empty rollback delivery requirements=%#v, want empty array", plan["deliveryRequirements"])
	}
	if mismatch := f.request(http.MethodPost, "/api/v1/environments/environment-test/cluster-rollback-runs", map[string]any{
		"expectedPlanDigest": plan["planDigest"], "confirmEnvironmentName": "wrong environment",
	}, owner); mismatch.Code != http.StatusBadRequest {
		t.Fatalf("confirmation mismatch status=%d body=%s", mismatch.Code, mismatch.Body.String())
	}
	if stale := f.request(http.MethodPost, "/api/v1/environments/environment-test/cluster-rollback-runs", map[string]any{
		"expectedPlanDigest": "stale-plan", "confirmEnvironmentName": "Test Environment",
	}, owner); stale.Code != http.StatusConflict {
		t.Fatalf("stale rollback plan status=%d body=%s", stale.Code, stale.Body.String())
	}
	created := f.request(http.MethodPost, "/api/v1/environments/environment-test/cluster-rollback-runs", map[string]any{
		"expectedPlanDigest": plan["planDigest"], "confirmEnvironmentName": "Test Environment",
	}, owner)
	if created.Code != http.StatusAccepted {
		t.Fatalf("rollback run status=%d body=%s", created.Code, created.Body.String())
	}
	run := decodeEnvelope(t, created)["data"].(map[string]any)
	if run["kind"] != string(domain.RunEnvironmentRollback) || run["status"] != string(domain.RunAwaitingApproval) {
		t.Fatalf("rollback run=%#v", run)
	}
	blocked := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
		"environmentId": "environment-test", "mode": "install_verify",
	}, alice)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("rollback fence allowed another active Run status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	approvalID := run["approval"].(map[string]any)["id"].(string)
	if approved := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/approve", map[string]any{"reason": "approved whole-cluster clean rollback"}, owner); approved.Code != http.StatusOK {
		t.Fatalf("approve rollback status=%d body=%s", approved.Code, approved.Body.String())
	}
	waitForRun(t, f.database, run["id"].(string), domain.RunSucceeded)
	for _, item := range fixtures {
		if _, err := f.database.GetEnvironmentComponentInstallation(ctx, "environment-test", item.id); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("installation %s remained after rollback: %v", item.id, err)
		}
	}
	f.runner.mu.Lock()
	defer f.runner.mu.Unlock()
	last := f.runner.requests[len(f.runner.requests)-3:]
	if last[1].Variables["clusterforge_backup_cleanup_on_success"] != false || last[2].Variables["clusterforge_backup_cleanup_on_success"] != true {
		t.Fatalf("duplicate component cleanup flags=%#v %#v", last[1].Variables, last[2].Variables)
	}
}

func TestEnvironmentRollbackFailsClosedWhenInstallationSetChanges(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	owner := f.session(seed.EnvironmentOwnerID)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	release, err := f.database.GetComponentRelease(ctx, "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	for index := range release.Actions {
		if release.Actions[index].Kind == domain.ActionRollback {
			release.Actions[index].FromReleaseID = ""
			release.Actions[index].ToReleaseID = ""
		}
	}
	if err := f.database.UpdateDraftRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	installed := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
		"environmentId": "environment-test", "mode": "install_verify",
	}, alice)
	if installed.Code != http.StatusAccepted {
		t.Fatalf("install baseline status=%d body=%s", installed.Code, installed.Body.String())
	}
	waitForRun(t, f.database, decodeEnvelope(t, installed)["data"].(map[string]any)["id"].(string), domain.RunSucceeded)
	preview := f.request(http.MethodPost, "/api/v1/environments/environment-test/cluster-rollback-plan", nil, owner)
	if preview.Code != http.StatusOK {
		t.Fatalf("rollback preview status=%d body=%s", preview.Code, preview.Body.String())
	}
	plan := decodeEnvelope(t, preview)["data"].(map[string]any)
	created := f.request(http.MethodPost, "/api/v1/environments/environment-test/cluster-rollback-runs", map[string]any{
		"expectedPlanDigest": plan["planDigest"], "confirmEnvironmentName": "Test Environment",
	}, owner)
	if created.Code != http.StatusAccepted {
		t.Fatalf("rollback run status=%d body=%s", created.Code, created.Body.String())
	}
	run := decodeEnvelope(t, created)["data"].(map[string]any)
	installation, err := f.database.GetEnvironmentComponentInstallation(ctx, "environment-test", "component-test-runtime")
	if err != nil {
		t.Fatal(err)
	}
	installation.BackupRef += "-changed-after-submit"
	if err := f.database.UpsertEnvironmentComponentInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	approvalID := run["approval"].(map[string]any)["id"].(string)
	if approved := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/approve", map[string]any{"reason": "exercise stale baseline fence"}, owner); approved.Code != http.StatusOK {
		t.Fatalf("approve stale rollback status=%d body=%s", approved.Code, approved.Body.String())
	}
	waitForRun(t, f.database, run["id"].(string), domain.RunFailed)
	failed, err := f.database.GetRun(ctx, run["id"].(string))
	if err != nil || !strings.Contains(failed.Error, "baseline changed") {
		t.Fatalf("stale rollback failure=%q err=%v", failed.Error, err)
	}
}

func TestBatchApprovalRequiresAndTrimsReason(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	run := domain.Run{
		ID: "run-batch-reason", Kind: domain.RunComponentTest, Status: domain.RunAwaitingApproval,
		RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
		ComponentReleaseID: "release-test-runtime-1.1.0", Action: domain.ActionInstall, Destructive: true,
		InputSnapshot: map[string]any{}, CreatedAt: now,
	}
	approval := domain.Approval{ID: "approval-batch-reason", RunID: run.ID, Status: "pending", RequestedAt: now}
	if err := f.database.CreateRun(ctx, run, &approval); err != nil {
		t.Fatal(err)
	}
	owner := f.session(seed.EnvironmentOwnerID)
	blank := f.request(http.MethodPost, "/api/v1/approvals/batch", map[string]any{
		"approvalIds": []any{approval.ID}, "decision": "rejected", "reason": "   ",
	}, owner)
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("blank batch reason status=%d body=%s", blank.Code, blank.Body.String())
	}
	pending, err := f.database.GetApproval(ctx, approval.ID)
	if err != nil || pending.Status != "pending" {
		t.Fatalf("blank reason consumed approval=%+v err=%v", pending, err)
	}
	decided := f.request(http.MethodPost, "/api/v1/approvals/batch", map[string]any{
		"approvalIds": []any{approval.ID}, "decision": "rejected", "reason": "  maintenance denied  ",
	}, owner)
	if decided.Code != http.StatusOK {
		t.Fatalf("valid batch reason status=%d body=%s", decided.Code, decided.Body.String())
	}
	stored, err := f.database.GetApproval(ctx, approval.ID)
	if err != nil || stored.Status != "rejected" || stored.Reason != "maintenance denied" {
		t.Fatalf("stored batch decision=%+v err=%v", stored, err)
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
	updated := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"IMAGE_REGISTRY": "registry.example.test:5000", "FILE_STATION": station}}, dave)
	if updated.Code != http.StatusOK {
		t.Fatalf("configure file station status=%d body=%s", updated.Code, updated.Body.String())
	}
	f.recordReleaseReadiness("release-test-runtime-1.1.0")
	if err := f.setCandidate(context.Background(), "release-test-runtime-1.1.0", true); err != nil {
		t.Fatal(err)
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
	artifactRelativePath := "components/component-test-runtime/v1.1.0/runtime.tar.gz"
	if err != nil || len(release.Artifacts) != 1 || release.Artifacts[0].Filename != "runtime.tar.gz" {
		t.Fatalf("release artifacts=%+v err=%v", release.Artifacts, err)
	}
	decorated, decorateErr := f.platform.Catalog().GetComponentRelease(context.Background(), release.ID)
	if decorateErr != nil || release.Candidate || release.Review.Status != domain.ReleaseReviewNotSubmitted || decorated.Readiness.Status != domain.ReadinessBlocked {
		t.Fatalf("artifact upload candidate/readiness: candidate=%v readiness=%+v err=%v", release.Candidate, decorated.Readiness, decorateErr)
	}
	if stored, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifactRelativePath))); err != nil || !bytes.Equal(stored, contents) {
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
	targetRevision := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"IMAGE_REGISTRY": "registry.example.test:5000", "FILE_STATION": targetStation}}, dave)
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
	requirements := plan["deliveryRequirements"].([]any)
	decisions := make([]any, 0, len(requirements))
	for _, rawRequirement := range requirements {
		requirement := rawRequirement.(map[string]any)
		decisions = append(decisions, map[string]any{"requirementId": requirement["id"], "mode": "transfer"})
	}
	if approved := f.request(http.MethodPost, "/api/v1/approvals/"+approvalID+"/approve", map[string]any{"reason": "mirror artifact", "deliveryDecisions": decisions}, dave); approved.Code != http.StatusOK {
		t.Fatalf("approve artifact transfer status=%d body=%s", approved.Code, approved.Body.String())
	}
	waitForRun(t, f.database, runData["id"].(string), domain.RunSucceeded)
	if mirrored, err := os.ReadFile(filepath.Join(targetRoot, filepath.FromSlash(artifactRelativePath))); err != nil || !bytes.Equal(mirrored, contents) {
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
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(artifactRelativePath))); err != nil {
		t.Fatalf("detach deleted physical media: %v", err)
	}
	detachedRelease, err := f.database.GetComponentRelease(context.Background(), release.ID)
	detachedDecorated, detachedDecorateErr := f.platform.Catalog().GetComponentRelease(context.Background(), release.ID)
	if err != nil || detachedDecorateErr != nil || detachedRelease.Candidate || detachedRelease.Review.Status != domain.ReleaseReviewNotSubmitted || detachedDecorated.Readiness.Status != domain.ReadinessReady {
		t.Fatalf("artifact detach candidate/readiness: candidate=%v readiness=%+v err=%v/%v", detachedRelease.Candidate, detachedDecorated.Readiness, err, detachedDecorateErr)
	}
	imageHash := strings.Repeat("a", 64)
	imageBuild := domain.ComponentImageBuild{
		ID: "image-build-cross-registry", ReleaseID: release.ID, RequestedBy: seed.ComponentOwnerRuntimeID,
		Status: domain.ImageBuildSucceeded, DockerfileSHA256: strings.Repeat("b", 64), ImageTag: "v1.1.0",
		ImageRef:    "origin.example.test:5000/components/component-test-runtime:v1.1.0",
		ImageDigest: "origin.example.test:5000/components/component-test-runtime@sha256:" + imageHash,
		CreatedAt:   time.Now().UTC(), FinishedAt: func() *time.Time { value := time.Now().UTC(); return &value }(),
	}
	if err := f.database.CreateComponentImageBuild(context.Background(), imageBuild); err != nil {
		t.Fatal(err)
	}
	if err := f.database.UpsertDraftComponentImage(context.Background(), domain.ComponentImage{
		ID: "image-cross-registry", ReleaseID: release.ID, LogicalName: "runtime",
		Digest: "sha256:" + imageHash, SourceRef: imageBuild.ImageDigest,
		SourceUpdatedBy: seed.ComponentOwnerRuntimeID, SourceUpdatedAt: time.Now().UTC(),
		CreatedBy: seed.ComponentOwnerRuntimeID, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	imageDelivery := &fakeImageDelivery{}
	f.platform.ConfigureDeliveryAdapters(nil, imageDelivery)
	imagePreview := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-plan", map[string]any{"environmentId": "environment-test", "mode": "install_verify"}, alice)
	if imagePreview.Code != http.StatusOK || decodeEnvelope(t, imagePreview)["data"].(map[string]any)["requiresApproval"] != true {
		t.Fatalf("cross-registry image preview status=%d body=%s", imagePreview.Code, imagePreview.Body.String())
	}
	targetDigest := "registry.example.test:5000/components/component-test-runtime@sha256:" + imageHash
	if err := f.database.RecordComponentImageMirror(context.Background(), "registry.example.test:5000", imageBuild.ImageDigest, "registry.example.test:5000/components/component-test-runtime:v1.1.0", targetDigest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	imageDelivery.targetPresent = true
	reusedImagePreview := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-plan", map[string]any{"environmentId": "environment-test", "mode": "install_verify"}, alice)
	if reusedImagePreview.Code != http.StatusOK || decodeEnvelope(t, reusedImagePreview)["data"].(map[string]any)["requiresApproval"] != false {
		t.Fatalf("mirrored image requested approval again status=%d body=%s", reusedImagePreview.Code, reusedImagePreview.Body.String())
	}
}

func TestComponentArtifactRegisterRejectsDigestMismatchAsConflict(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	contents := []byte("artifact-content")
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(contents)
	}))
	defer source.Close()

	response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/artifacts/register", map[string]any{
		"alias": "mismatch", "filename": "artifact.bin", "sourceUrl": source.URL,
		"sha256": strings.Repeat("0", 64),
	}, alice)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "artifact source SHA-256 does not match content identity") {
		t.Fatalf("digest mismatch status=%d body=%s", response.Code, response.Body.String())
	}
	release, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range release.Artifacts {
		if artifact.Alias == "mismatch" {
			t.Fatalf("digest mismatch persisted artifact=%+v", artifact)
		}
	}
}

func TestComponentArtifactRegisterKeepsSourceOutageAsInternalError(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	source := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	sourceURL := source.URL
	source.Close()

	response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/artifacts/register", map[string]any{
		"alias": "offline", "filename": "artifact.bin", "sourceUrl": sourceURL,
		"sha256": strings.Repeat("0", 64),
	}, alice)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("source outage status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWorkbenchIsRoleScopedAndActionable(t *testing.T) {
	f := newAPIFixture(t)
	if response := f.request(http.MethodGet, "/api/v1/workbench", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous workbench status=%d body=%s", response.Code, response.Body.String())
	}

	tests := []struct {
		userID       string
		role         domain.Role
		expectedKind string
	}{
		{userID: seed.ComponentOwnerRuntimeID, role: domain.RoleComponentOwner, expectedKind: "component_draft"},
		{userID: seed.ScenarioOwnerID, role: domain.RoleScenarioOwner, expectedKind: "scenario_revision"},
		{userID: seed.EnvironmentOwnerID, role: domain.RoleEnvironmentOwner, expectedKind: "environment"},
	}
	for _, test := range tests {
		t.Run(string(test.role), func(t *testing.T) {
			response := f.request(http.MethodGet, "/api/v1/workbench", nil, f.session(test.userID))
			if response.Code != http.StatusOK {
				t.Fatalf("workbench status=%d body=%s", response.Code, response.Body.String())
			}
			data := decodeEnvelope(t, response)["data"].(map[string]any)
			if data["role"] != string(test.role) || data["generatedAt"] == "" {
				t.Fatalf("workbench identity=%#v", data)
			}
			items, ok := data["items"].([]any)
			if !ok || len(items) == 0 {
				t.Fatalf("workbench items=%#v", data["items"])
			}
			foundExpected := false
			for _, raw := range items {
				item := raw.(map[string]any)
				if item["kind"] == test.expectedKind {
					foundExpected = true
				}
				reasons, reasonsOK := item["reasons"].([]any)
				action, actionOK := item["primaryAction"].(map[string]any)
				href, hrefOK := action["href"].(string)
				if !reasonsOK || len(reasons) == 0 || !actionOK || action["label"] == "" || !hrefOK || !strings.HasPrefix(href, "/") {
					t.Fatalf("work item is not actionable: %#v", item)
				}
				for _, rawReason := range reasons {
					reason := rawReason.(map[string]any)
					cause, causeOK := reason["cause"].(map[string]any)
					nextAction, nextActionOK := reason["nextAction"].(map[string]any)
					if !causeOK || cause["summary"] == "" || !nextActionOK || nextAction["label"] == "" || !strings.HasPrefix(fmt.Sprint(nextAction["href"]), "/") {
						t.Fatalf("work reason does not explain cause and next step: %#v", reason)
					}
				}
			}
			if !foundExpected {
				t.Fatalf("role %s did not receive %s: %#v", test.role, test.expectedKind, items)
			}
			if strings.Contains(response.Body.String(), "credentialRefs") || strings.Contains(response.Body.String(), "reference\"") {
				t.Fatalf("workbench leaked credential configuration: %s", response.Body.String())
			}
		})
	}
}

func TestWorkbenchLatestSuccessClearsEarlierFailure(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	failedAt := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	failed := domain.Run{
		ID: "run-workbench-failed", Kind: domain.RunEnvironmentRollback, Status: domain.RunFailed,
		RequestedBy: seed.EnvironmentOwnerID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
		InputSnapshot: map[string]any{}, Error: "rollback failed", CreatedAt: failedAt, StartedAt: &failedAt, FinishedAt: &failedAt,
	}
	if err := f.database.CreateRun(ctx, failed, nil); err != nil {
		t.Fatal(err)
	}
	owner := f.session(seed.EnvironmentOwnerID)
	response := f.request(http.MethodGet, "/api/v1/workbench", nil, owner)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "run:run-workbench-failed") {
		t.Fatalf("current failure missing status=%d body=%s", response.Code, response.Body.String())
	}

	succeededAt := failedAt.Add(time.Minute)
	succeeded := domain.Run{
		ID: "run-workbench-succeeded", Kind: domain.RunEnvironmentRollback, Status: domain.RunSucceeded,
		RequestedBy: seed.EnvironmentOwnerID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
		InputSnapshot: map[string]any{}, CreatedAt: succeededAt, StartedAt: &succeededAt, FinishedAt: &succeededAt,
	}
	if err := f.database.CreateRun(ctx, succeeded, nil); err != nil {
		t.Fatal(err)
	}
	response = f.request(http.MethodGet, "/api/v1/workbench", nil, owner)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "run:run-workbench-failed") {
		t.Fatalf("superseded failure remained status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWorkbenchKeepsInvalidTestPassedScenarioBlocked(t *testing.T) {
	f := newAPIFixture(t)
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	if err := f.database.SetScenarioRevisionStatus(context.Background(), "scenario-test-runtime-r2", []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTestPassed, now); err != nil {
		t.Fatal(err)
	}

	response := f.request(http.MethodGet, "/api/v1/workbench", nil, f.session(seed.ScenarioOwnerID))
	if response.Code != http.StatusOK {
		t.Fatalf("workbench status=%d body=%s", response.Code, response.Body.String())
	}
	items := decodeEnvelope(t, response)["data"].(map[string]any)["items"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["id"] != "scenario_revision:scenario-test-runtime-r2" {
			continue
		}
		action := item["primaryAction"].(map[string]any)
		if item["status"] != string(domain.WorkStatusBlocked) || action["label"] != "检查场景问题" || strings.Contains(action["href"].(string), "action=publish") {
			t.Fatalf("invalid test-passed scenario was presented as publishable: %#v", item)
		}
		if strings.Contains(fmt.Sprint(item["reasons"]), "scenario.ready_to_publish") {
			t.Fatalf("invalid scenario retained ready-to-publish reason: %#v", item)
		}
		return
	}
	t.Fatal("scenario work item missing")
}

func TestWorkbenchApprovalActionMatchesViewerPermission(t *testing.T) {
	f := newAPIFixture(t)
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	run := domain.Run{
		ID: "run-workbench-awaiting-approval", Kind: domain.RunComponentTest, Status: domain.RunAwaitingApproval,
		RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
		ComponentReleaseID: "release-test-runtime-1.1.0", Action: domain.ActionUpgrade, Destructive: true,
		InputSnapshot: map[string]any{"steps": []any{map[string]any{"releaseId": "release-test-runtime-1.1.0", "action": "upgrade"}}}, CreatedAt: now,
	}
	approval := &domain.Approval{ID: "approval-workbench", RunID: run.ID, Status: "pending", RequestedAt: now}
	if err := f.database.CreateRun(context.Background(), run, approval); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.FailInvalidActiveRuns(context.Background(), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	retained, err := f.database.GetRun(context.Background(), run.ID)
	if err != nil || retained.Status != domain.RunAwaitingApproval {
		t.Fatalf("valid approval run was invalidated: %+v err=%v", retained, err)
	}

	for _, test := range []struct {
		userID string
		label  string
	}{
		{userID: seed.ComponentOwnerRuntimeID, label: "查看审批状态"},
		{userID: seed.EnvironmentOwnerID, label: "复核并审批"},
	} {
		response := f.request(http.MethodGet, "/api/v1/workbench", nil, f.session(test.userID))
		if response.Code != http.StatusOK {
			t.Fatalf("user %s workbench status=%d body=%s", test.userID, response.Code, response.Body.String())
		}
		items := decodeEnvelope(t, response)["data"].(map[string]any)["items"].([]any)
		found := false
		for _, raw := range items {
			item := raw.(map[string]any)
			if item["id"] != "run:"+run.ID {
				continue
			}
			found = true
			if label := item["primaryAction"].(map[string]any)["label"]; label != test.label {
				t.Fatalf("user %s approval action=%v want=%s", test.userID, label, test.label)
			}
		}
		if !found {
			t.Fatalf("user %s did not receive awaiting-approval run", test.userID)
		}
	}
}

type fakeRunner struct {
	root                   string
	mu                     sync.Mutex
	calls                  []string
	requests               []ansiblerunner.Request
	failPlaybook           string
	failPlaybookOccurrence int
	matchingPlaybookCalls  int
}

type fakeImageDelivery struct{ targetPresent bool }

func (delivery *fakeImageDelivery) Probe(_ context.Context, location service.ImageLocation, _ service.ImageDigest) error {
	if strings.Contains(location.Ref, "registry.example.test:5000/") && !delivery.targetPresent {
		return service.ErrDeliveryTargetMissing
	}
	if location.ObservedDigest != nil {
		*location.ObservedDigest = location.Ref
	}
	return nil
}

func (*fakeImageDelivery) Transfer(context.Context, service.ImageTransfer) error { return nil }

func (f *fakeRunner) Digest(playbook string) (string, string, error) {
	content, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(playbook)))
	return fmt.Sprintf("%x", sha256.Sum256(content)), "fixed-tree-digest", err
}

func (f *fakeRunner) DigestPlan(playbooks []string) (map[string]string, string, error) {
	digests := make(map[string]string, len(playbooks))
	for _, playbook := range playbooks {
		digest, _, err := f.Digest(playbook)
		if err != nil {
			return nil, "", err
		}
		digests[playbook] = digest
	}
	return digests, "fixed-tree-digest", nil
}

func (f *fakeRunner) Run(_ context.Context, request ansiblerunner.Request) (ansiblerunner.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, request.Playbook)
	f.requests = append(f.requests, request)
	shouldFail := false
	if f.failPlaybook == request.Playbook || (strings.HasPrefix(f.failPlaybook, "tests/") && filepath.Base(f.failPlaybook) == filepath.Base(request.Playbook)) {
		f.matchingPlaybookCalls++
		shouldFail = f.failPlaybookOccurrence == 0 || f.matchingPlaybookCalls == f.failPlaybookOccurrence
	}
	f.mu.Unlock()
	if shouldFail {
		return ansiblerunner.Result{TreeSHA256: "fixed-tree-digest", Phases: []ansiblerunner.PhaseResult{{Phase: ansiblerunner.PhaseExecute, ExitCode: 2}}}, errors.New("forced test failure")
	}
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
	t           *testing.T
	database    *store.Store
	platform    *service.Platform
	handler     http.Handler
	runner      *fakeRunner
	evidenceSeq int
}

func completeTestEnvironmentFacts() map[string]any {
	return map[string]any{
		"architecture": "amd64", "operatingSystem": "Ubuntu", "operatingSystemVersion": "24.04",
		"ipFamily": "IPv4",
	}
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
	root := t.TempDir()
	testutil.Workspaces(t, database, root)
	stored, err := database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	digest := domain.ComponentReleaseSpecDigest(stored)
	now := time.Now().UTC()
	if err := database.SubmitComponentReleaseReview(context.Background(), stored.ID, digest, stored.PublicationGeneration, now); err != nil {
		t.Fatal(err)
	}
	if err := database.DecideComponentReleaseReview(context.Background(), stored.ID, domain.ReleaseReviewApproved, seed.PlatformAdminID, "fixture approval", digest, now); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{root: root}
	platform := service.NewPlatform(database, runner, service.NewEventHub())
	platform.ConfigurePlaybookRoot(root)
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
	if err := database.EnsurePlatformOption(ctx, "hostGroup", domain.PlatformOption{
		ID: "platform-option-test-nodes", Value: "test_nodes", Label: "Test nodes", SortOrder: 2000,
		CreatedBy: seed.PlatformAdminID, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	component := func(id, name, owner string) domain.Component {
		return domain.Component{
			ID: id, Slug: id, Name: name, Layer: domain.LayerRuntimeState, Tags: []string{"runtime"},
			OwnerID: owner, CreatedAt: now, UpdatedAt: now,
		}
	}
	oldRelease := domain.ComponentRelease{
		ID: "release-test-runtime-1.0.0", ComponentID: "component-test-runtime", LineID: "line-release-test-runtime-1.0.0", LineName: "v1.0.0", Version: "v1.0.0",
		Compatibility: domain.CompatibilityNotApplicable,
		Status:        domain.ReleaseReleased, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now,
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{{Name: "expected_version", Description: "expected runtime version", Type: domain.ParameterTypeString, Required: true, FixedValue: "1.0.0", Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderComponentOwner}},
		Actions: []domain.ActionDefinition{
			{ID: "action-test-runtime-install-1.0", ReleaseID: "release-test-runtime-1.0.0", Name: "install", Kind: domain.ActionInstall, Playbook: "tests/runtime/install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
			{ID: "action-test-runtime-verify-1.0", ReleaseID: "release-test-runtime-1.0.0", Name: "verify", Kind: domain.ActionVerify, Playbook: "tests/runtime/verify.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
		},
	}
	newRelease := domain.ComponentRelease{
		ID: "release-test-runtime-1.1.0", ComponentID: "component-test-runtime", LineID: oldRelease.LineID, LineName: oldRelease.LineName,
		ParentReleaseID: oldRelease.ID, TemplateSourceReleaseID: oldRelease.ID, Version: "v1.1.0",
		Compatibility: domain.CompatibilityCompatible,
		Status:        domain.ReleaseDraft, RiskLevel: domain.RiskLow, CreatedAt: now.Add(time.Second),
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{{Name: "expected_version", Description: "expected runtime version", Type: domain.ParameterTypeString, Required: true, FixedValue: "1.1.0", Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderComponentOwner}},
		Actions: []domain.ActionDefinition{
			{ID: "action-test-runtime-install-1.1", ReleaseID: "release-test-runtime-1.1.0", Name: "install", Kind: domain.ActionInstall, Playbook: "tests/runtime/install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow},
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
	storedNewRelease, err := database.GetComponentRelease(ctx, newRelease.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewDigest := domain.ComponentReleaseSpecDigest(storedNewRelease)
	if err := database.SubmitComponentReleaseReview(ctx, newRelease.ID, reviewDigest, storedNewRelease.PublicationGeneration, now); err != nil {
		t.Fatal(err)
	}
	if err := database.DecideComponentReleaseReview(ctx, newRelease.ID, domain.ReleaseReviewApproved, seed.PlatformAdminID, "fixture approval", reviewDigest, now); err != nil {
		t.Fatal(err)
	}
	consumer := component("component-test-consumer", "Test Consumer", seed.ComponentOwnerK8sID)
	consumer.Layer, consumer.Tags = domain.LayerPlatformExtension, []string{"platform"}
	if err := database.CreateComponent(ctx, consumer); err != nil {
		t.Fatal(err)
	}
	consumerRelease := domain.ComponentRelease{
		ID: "release-test-consumer-1.0.0", ComponentID: consumer.ID, Version: "v1.0.0",
		Status: domain.ReleaseReleased, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now,
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
		Graph:     domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime-install", Name: "Install runtime", ReleaseID: oldRelease.ID, Action: domain.ActionInstall, HostGroup: "test_nodes", ParameterValues: map[string]any{}}}, Edges: []domain.ScenarioEdge{}},
		CreatedAt: now, TestPassedAt: &testedAt, ReleasedAt: &releasedAt,
	}
	if err := database.CreateScenario(ctx, scenario, revision1); err != nil {
		t.Fatal(err)
	}
	revision2 := domain.ScenarioRevision{
		ID: "scenario-test-runtime-r2", ScenarioID: scenario.ID, Revision: 2, Status: domain.RevisionDraft,
		Graph:     domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime-upgrade", Name: "Upgrade runtime", ReleaseID: newRelease.ID, Action: domain.ActionUpgrade, HostGroup: "test_nodes", ParameterValues: map[string]any{}}}, Edges: []domain.ScenarioEdge{}},
		CreatedAt: now.Add(time.Second),
	}
	if err := database.CreateScenarioRevision(ctx, revision2); err != nil {
		t.Fatal(err)
	}
	inventory, _ := json.Marshal(map[string]any{"hosts": []any{map[string]any{"name": "localhost", "address": "127.0.0.1", "groups": []any{"test_nodes"}}}})
	environment := domain.Environment{ID: "environment-test", Name: "Test Environment", OwnerID: seed.EnvironmentOwnerID, CreatedAt: now, UpdatedAt: now}
	environmentRevision := domain.EnvironmentRevision{ID: "environment-test-r1", EnvironmentID: environment.ID, Revision: 1, Facts: completeTestEnvironmentFacts(), Inventory: inventory, Variables: map[string]string{"IMAGE_REGISTRY": "registry.example.test:5000"}, CredentialRefs: []domain.CredentialRef{}, CreatedAt: now}
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
			PlaybookSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(testutil.Playbook))), DependencySnapshot: map[string]any{"dependencies": []any{}},
		},
		TestOnly: true, InstalledAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func componentRequest(name, slug string) map[string]any {
	return map[string]any{
		"name": name, "slug": slug, "layer": "runtime_state", "tags": []string{"runtime"},
	}
}

func (f *apiFixture) createNewLineDraft(componentID string, definition map[string]any, cookie *http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	f.configurePlaybookRoot(f.t.TempDir())
	version, _ := definition["version"].(string)
	releaseNotes, _ := definition["releaseNotes"].(string)
	if releaseNotes == "" {
		releaseNotes = "test draft " + version
	}
	request := map[string]any{
		"mode": "new_line", "lineName": "Test line " + version, "version": version,
		"releaseNotes": releaseNotes, "compatibility": "not_applicable",
	}
	for _, key := range []string{"riskLevel", "environmentConstraints"} {
		if value, ok := definition[key]; ok {
			request[key] = value
		}
	}
	preview := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-draft-plan", request, cookie)
	if preview.Code != http.StatusOK {
		f.t.Fatalf("preview new-line draft status=%d body=%s", preview.Code, preview.Body.String())
	}
	request["expectedPlanDigest"] = decodeEnvelope(f.t, preview)["data"].(map[string]any)["planDigest"]
	created := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-drafts", request, cookie)
	if created.Code != http.StatusCreated {
		f.t.Fatalf("create new-line draft status=%d body=%s", created.Code, created.Body.String())
	}
	releaseID := decodeEnvelope(f.t, created)["data"].(map[string]any)["id"].(string)
	if rawActions, ok := definition["actions"].([]any); ok && len(rawActions) > 0 {
		persistedActions := make([]any, 0, len(rawActions))
		workspace := f.request(http.MethodGet, "/api/v1/component-releases/"+releaseID+"/playbook-workspace", nil, cookie)
		if workspace.Code != http.StatusOK {
			f.t.Fatalf("load new-line workspace status=%d body=%s", workspace.Code, workspace.Body.String())
		}
		treeSHA, _ := decodeEnvelope(f.t, workspace)["data"].(map[string]any)["treeSha256"].(string)
		for _, rawAction := range rawActions {
			action := rawAction.(map[string]any)
			kind, _ := action["kind"].(string)
			saved := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID+"/playbook", map[string]any{
				"actionKind": kind, "action": action, "content": "---\n- hosts: all\n  tasks: []\n", "expectedSha256": "", "expectedTreeSha256": treeSHA,
			}, cookie)
			if saved.Code != http.StatusOK {
				f.t.Fatalf("save new-line action %s status=%d body=%s", kind, saved.Code, saved.Body.String())
			}
			data := decodeEnvelope(f.t, saved)["data"].(map[string]any)
			persistedAction := data["action"].(map[string]any)
			delete(persistedAction, "playbook")
			delete(persistedAction, "releaseId")
			persistedActions = append(persistedActions, persistedAction)
			workspace = f.request(http.MethodGet, "/api/v1/component-releases/"+releaseID+"/playbook-workspace", nil, cookie)
			treeSHA, _ = decodeEnvelope(f.t, workspace)["data"].(map[string]any)["treeSha256"].(string)
		}
		definition["actions"] = persistedActions
	}
	definition["releaseNotes"] = releaseNotes
	updated := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID, definition, cookie)
	if updated.Code != http.StatusOK {
		f.t.Fatalf("configure new-line draft status=%d body=%s", updated.Code, updated.Body.String())
	}
	return updated
}

func (f *apiFixture) setCandidate(ctx context.Context, id string, candidate bool) error {
	r, err := f.database.GetComponentRelease(ctx, id)
	if err != nil {
		return err
	}
	return f.database.SetReleaseCandidate(ctx, id, candidate, r.PublicationGeneration, domain.ComponentReleaseSpecDigest(r))
}

func (f *apiFixture) recordReleaseReadiness(releaseID string) {
	f.t.Helper()
	release, err := f.database.GetComponentRelease(context.Background(), releaseID)
	if err != nil {
		f.t.Fatal(err)
	}
	digest := service.ComponentReleaseSpecDigest(release)
	if release.Status == domain.ReleaseDraft && (release.Review.Status != domain.ReleaseReviewApproved || release.Review.ContractDigest != digest) {
		now := time.Now().UTC()
		if err := f.database.SubmitComponentReleaseReview(context.Background(), releaseID, digest, release.PublicationGeneration, now); err != nil {
			f.t.Fatal(err)
		}
		if err := f.database.DecideComponentReleaseReview(context.Background(), releaseID, domain.ReleaseReviewApproved, seed.PlatformAdminID, "test approval", digest, now); err != nil {
			f.t.Fatal(err)
		}
		release, err = f.database.GetComponentRelease(context.Background(), releaseID)
		if err != nil {
			f.t.Fatal(err)
		}
		digest = service.ComponentReleaseSpecDigest(release)
	}
	if release.ParentReleaseID != "" {
		f.evidenceSeq++
		now := time.Now().UTC()
		run := domain.Run{
			ID: fmt.Sprintf("run-readiness-%d", f.evidenceSeq), Kind: domain.RunComponentTest, Status: domain.RunSucceeded,
			RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
			ComponentReleaseID: releaseID, Action: domain.ActionUpgrade,
			InputSnapshot: map[string]any{"componentReleaseSpecDigest": digest, "componentTestEvidence": "evolution_round_trip"},
			CreatedAt:     now, StartedAt: &now, FinishedAt: &now,
		}
		if err := f.database.CreateRun(context.Background(), run, nil); err != nil {
			f.t.Fatal(err)
		}
		return
	}
	for _, evidence := range []struct {
		name   string
		action domain.ActionKind
	}{{"install_verify", domain.ActionInstall}, {"rollback_verify", domain.ActionRollback}} {
		f.evidenceSeq++
		now := time.Now().UTC()
		run := domain.Run{
			ID: fmt.Sprintf("run-readiness-%d", f.evidenceSeq), Kind: domain.RunComponentTest, Status: domain.RunSucceeded,
			RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
			ComponentReleaseID: releaseID, Action: evidence.action,
			InputSnapshot: map[string]any{"componentReleaseSpecDigest": digest, "componentTestEvidence": evidence.name},
			CreatedAt:     now, StartedAt: &now, FinishedAt: &now,
		}
		if err := f.database.CreateRun(context.Background(), run, nil); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *apiFixture) invalidateReleaseReadiness(releaseID string) {
	f.t.Helper()
	release, err := f.database.GetComponentRelease(context.Background(), releaseID)
	if err != nil {
		f.t.Fatal(err)
	}
	release.ReleaseNotes += " readiness-invalidated"
	if err := f.database.UpdateDraftRelease(context.Background(), release); err != nil {
		f.t.Fatal(err)
	}
}

func TestReleaseReadinessSurvivesServiceRestartAndEmptyEventHub(t *testing.T) {
	f := newAPIFixture(t)
	f.recordReleaseReadiness("release-test-runtime-1.1.0")

	restarted := service.NewPlatform(f.database, f.runner, service.NewEventHub())
	defer restarted.Close()
	restarted.ConfigurePlaybookRoot(f.runner.root)
	release, err := restarted.Catalog().GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if release.Readiness.Status != domain.ReadinessReady || release.Readiness.TransitionEvidenceRunID == "" {
		t.Fatalf("restart-derived readiness=%+v", release.Readiness)
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
		f.t.Fatalf("unsafe session cookie: %#v", cookies)
	}
	return cookies[0]
}

func (f *apiFixture) completeReleaseDelivery(releaseID string, owner, environmentOwner *http.Cookie, preserveInstallation bool) {
	f.t.Helper()
	runTest := func(mode string) {
		response := f.request(http.MethodPost, "/api/v1/component-releases/"+releaseID+"/test-runs", map[string]any{
			"environmentId": "environment-test", "mode": mode,
		}, owner)
		if response.Code != http.StatusAccepted {
			f.t.Fatalf("%s evidence status=%d body=%s", mode, response.Code, response.Body.String())
		}
		data := decodeEnvelope(f.t, response)["data"].(map[string]any)
		if data["status"] == string(domain.RunAwaitingApproval) {
			approval := data["approval"].(map[string]any)
			approved := f.request(http.MethodPost, "/api/v1/approvals/"+approval["id"].(string)+"/approve", nil, environmentOwner)
			if approved.Code != http.StatusOK {
				f.t.Fatalf("approve %s evidence status=%d body=%s", mode, approved.Code, approved.Body.String())
			}
		}
		waitForRun(f.t, f.database, data["id"].(string), domain.RunSucceeded)
	}

	release, err := f.database.GetComponentRelease(context.Background(), releaseID)
	if err != nil {
		f.t.Fatalf("read release before delivery evidence: %v", err)
	}
	if release.ParentReleaseID != "" {
		var installation domain.EnvironmentComponentInstallation
		if preserveInstallation {
			installation, err = f.database.GetEnvironmentComponentInstallation(context.Background(), "environment-test", release.ComponentID)
			if err != nil {
				f.t.Fatalf("read installation before evolution evidence: %v", err)
			}
		}
		runTest("evolution_round_trip")
		if preserveInstallation {
			if err := f.database.UpsertEnvironmentComponentInstallation(context.Background(), installation); err != nil {
				f.t.Fatalf("restore installation fixture after evolution evidence: %v", err)
			}
		}
		return
	}
	runTest("install_verify")
	installation, err := f.database.GetEnvironmentComponentInstallation(context.Background(), "environment-test", release.ComponentID)
	if err != nil {
		f.t.Fatalf("read installation after install evidence: %v", err)
	}
	runTest("rollback")
	if preserveInstallation {
		if err := f.database.UpsertEnvironmentComponentInstallation(context.Background(), installation); err != nil {
			f.t.Fatalf("restore installation fixture after delivery evidence: %v", err)
		}
	}
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
	f.configurePlaybookRoot(root)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	bob := f.session(seed.ComponentOwnerK8sID)
	carol := f.session(seed.ScenarioOwnerID)
	dave := f.session(seed.EnvironmentOwnerID)
	releasePath := "/api/v1/component-releases/release-test-runtime-1.1.0/playbook"
	releasedRelativePath := "managed/fixtures/release-test-runtime-1.0.0/install.yml"
	draft, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	var install domain.ActionDefinition
	for _, action := range draft.Actions {
		if action.Kind == domain.ActionInstall {
			install = action
			break
		}
	}
	if install.ID == "" {
		t.Fatal("fixture has no install Action")
	}
	actionJSON, err := json.Marshal(install)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests", "runtime"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(releasedRelativePath)), []byte("---\n- hosts: released\n  tasks: []\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	for label, cookie := range map[string]*http.Cookie{"component owner": bob, "scenario owner": carol, "environment owner": dave} {
		response := f.request(http.MethodGet, "/api/v1/component-releases/release-test-runtime-1.0.0/playbook?path="+releasedRelativePath, nil, cookie)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "hosts: released") {
			t.Fatalf("%s read released Playbook status=%d body=%s", label, response.Code, response.Body.String())
		}
	}
	if response := f.multipartFileRequest(releasePath, nil, "playbook", "install.yml", []byte("---\n[]\n"), alice); response.Code != http.StatusBadRequest {
		t.Fatalf("non-atomic Playbook upload status=%d body=%s", response.Code, response.Body.String())
	}

	uploaded := f.multipartFileRequest(releasePath, map[string]string{
		"actionKind": "install", "action": string(actionJSON),
		"expectedSha256": draft.Actions[0].PlaybookSHA256, "expectedTreeSha256": draft.PlaybookTreeSHA256,
	}, "playbook", "install.yml", []byte("---\n- hosts: all\n  tasks: []\n"), alice)
	if uploaded.Code != http.StatusCreated {
		t.Fatalf("upload Playbook status=%d body=%s", uploaded.Code, uploaded.Body.String())
	}
	data := decodeEnvelope(t, uploaded)["data"].(map[string]any)
	managedPath, ok := data["path"].(string)
	if !ok || managedPath != draft.PlaybookWorkspaceRoot+"install.yml" {
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
	draft, err = f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	f.recordReleaseReadiness("release-test-runtime-1.1.0")
	if err := f.setCandidate(context.Background(), "release-test-runtime-1.1.0", true); err != nil {
		t.Fatal(err)
	}
	for label, cookie := range map[string]*http.Cookie{"component owner": bob, "scenario owner": carol, "environment owner": dave} {
		response := f.request(http.MethodGet, releasePath+"?path="+managedPath, nil, cookie)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "hosts: all") {
			t.Fatalf("%s read shared candidate Playbook status=%d body=%s", label, response.Code, response.Body.String())
		}
	}
	install = draft.Actions[0]
	for _, action := range draft.Actions {
		if action.Kind == domain.ActionInstall {
			install = action
			break
		}
	}
	edited := f.request(http.MethodPut, releasePath, map[string]any{
		"actionKind": install.Kind, "action": install, "content": "---\n- hosts: workers\n  tasks: []\n",
		"expectedSha256": data["sha256"], "expectedTreeSha256": draft.PlaybookTreeSHA256,
	}, alice)
	if edited.Code != http.StatusOK || !strings.Contains(edited.Body.String(), "hosts: workers") {
		t.Fatalf("edit Playbook status=%d body=%s", edited.Code, edited.Body.String())
	}
	release, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	decorated, decorateErr := f.platform.Catalog().GetComponentRelease(context.Background(), release.ID)
	if err != nil || decorateErr != nil || release.Candidate || release.Review.Status != domain.ReleaseReviewNotSubmitted || decorated.Readiness.Status != domain.ReadinessBlocked {
		t.Fatalf("Playbook edit candidate/readiness: candidate=%v readiness=%+v err=%v/%v", release.Candidate, decorated.Readiness, err, decorateErr)
	}
	install = release.Actions[0]
	for _, action := range release.Actions {
		if action.Kind == domain.ActionInstall {
			install = action
			break
		}
	}
	if response := f.request(http.MethodPut, releasePath, map[string]any{
		"actionKind": install.Kind, "action": install, "content": "---\n[]\n",
		"expectedSha256": decodeEnvelope(t, edited)["data"].(map[string]any)["sha256"], "expectedTreeSha256": release.PlaybookTreeSHA256,
	}, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other owner edit Playbook status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodGet, releasePath+"?path="+managedPath, nil, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other owner read private Draft Playbook status=%d body=%s", response.Code, response.Body.String())
	}
	released, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	releasedInstall := released.Actions[0]
	for _, action := range released.Actions {
		if action.Kind == domain.ActionInstall {
			releasedInstall = action
			break
		}
	}
	if response := f.request(http.MethodPut, "/api/v1/component-releases/release-test-runtime-1.0.0/playbook", map[string]any{
		"actionKind": releasedInstall.Kind, "action": releasedInstall, "content": "---\n[]\n",
		"expectedSha256": releasedInstall.PlaybookSHA256, "expectedTreeSha256": released.PlaybookTreeSHA256,
	}, alice); response.Code != http.StatusConflict {
		t.Fatalf("released Playbook edit status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "managed", "component-test-runtime", "release-test-runtime-1.0.0", "install.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released Playbook edit replaced a managed file: %v", err)
	}
	if response := f.request(http.MethodPut, releasePath, map[string]any{"content": "not atomic"}, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("non-atomic Playbook write status=%d body=%s", response.Code, response.Body.String())
	}
	outside := filepath.Join(t.TempDir(), "outside.yml")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	escapePath := filepath.Join(filepath.Dir(filepath.Join(root, filepath.FromSlash(managedPath))), "escape.yml")
	if err := os.Symlink(outside, escapePath); err != nil {
		t.Fatal(err)
	}
	if response := f.request(http.MethodGet, releasePath+"?path="+draft.PlaybookWorkspaceRoot+"escape.yml", nil, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("escaping Playbook symlink status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestDraftPlaybookWorkspaceManagesAuxiliaryFilesAndRejectsUnsafePaths(t *testing.T) {
	f := newAPIFixture(t)
	root := t.TempDir()
	f.configurePlaybookRoot(root)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	bob := f.session(seed.ComponentOwnerK8sID)
	base := "/api/v1/component-releases/release-test-runtime-1.1.0/playbook-workspace"
	if response := f.request(http.MethodPut, "/api/v1/component-releases/release-test-runtime-1.1.0/playbook", map[string]any{"actionKind": "install", "content": "---\n- hosts: all\n"}, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("action Playbook save without precondition status=%d body=%s", response.Code, response.Body.String())
	}
	created := f.request(http.MethodPut, base+"/file", map[string]any{"path": "templates/runtime.conf.j2", "content": "port={{ runtime_port }}\n", "expectedSha256": ""}, alice)
	if created.Code != http.StatusOK {
		t.Fatalf("create workspace text file status=%d body=%s", created.Code, created.Body.String())
	}
	createdSHA := decodeEnvelope(t, created)["data"].(map[string]any)["sha256"].(string)
	listing := f.request(http.MethodGet, base, nil, alice)
	if listing.Code != http.StatusOK || !strings.Contains(listing.Body.String(), "templates/runtime.conf.j2") || !strings.Contains(listing.Body.String(), "treeSha256") {
		t.Fatalf("workspace listing status=%d body=%s", listing.Code, listing.Body.String())
	}
	if response := f.request(http.MethodGet, base, nil, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other owner read private workspace status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPut, base+"/file", map[string]any{"path": "../outside", "content": "secret", "expectedSha256": ""}, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("workspace traversal status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPut, base+"/file", map[string]any{"path": "templates/runtime.conf.j2", "content": "port=9090\n", "expectedSha256": createdSHA}, alice); response.Code != http.StatusOK {
		t.Fatalf("conditional workspace save status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPut, base+"/file", map[string]any{"path": "templates/runtime.conf.j2", "content": "stale", "expectedSha256": createdSHA}, alice); response.Code != http.StatusConflict {
		t.Fatalf("stale workspace save status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPut, base+"/file", map[string]any{"path": "templates/runtime.conf.j2", "content": "missing precondition"}, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("workspace save without precondition status=%d body=%s", response.Code, response.Body.String())
	}
	binary := []byte{0, 1, 2, 3, 4}
	upload := f.multipartFileRequest(base+"/file", map[string]string{"path": "files/helper.bin", "expectedSha256": ""}, "file", "ignored-client-path.bin", binary, alice)
	if upload.Code != http.StatusCreated || !strings.Contains(upload.Body.String(), `"editable":false`) {
		t.Fatalf("binary upload status=%d body=%s", upload.Code, upload.Body.String())
	}
	binarySHA := decodeEnvelope(t, upload)["data"].(map[string]any)["sha256"].(string)
	download := f.request(http.MethodGet, base+"/file?path=files%2Fhelper.bin&download=1", nil, alice)
	if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), binary) {
		t.Fatalf("binary download status=%d body=%v", download.Code, download.Body.Bytes())
	}
	renamed := f.request(http.MethodPatch, base+"/file", map[string]any{"from": "files/helper.bin", "to": "files/helper-v2.bin", "expectedSha256": binarySHA}, alice)
	if renamed.Code != http.StatusOK || !strings.Contains(renamed.Body.String(), "files/helper-v2.bin") {
		t.Fatalf("workspace rename status=%d body=%s", renamed.Code, renamed.Body.String())
	}
	deleted := f.request(http.MethodDelete, base+"/file?path=files%2Fhelper-v2.bin&expectedSha256="+binarySHA, nil, alice)
	if deleted.Code != http.StatusOK || strings.Contains(deleted.Body.String(), "files/helper-v2.bin") {
		t.Fatalf("workspace delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestDraftActionAPICommitsMetadataAndEntrypointTogether(t *testing.T) {
	f := newAPIFixture(t)
	root := t.TempDir()
	f.configurePlaybookRoot(root)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	releaseID := "release-test-runtime-1.1.0"
	release, err := f.database.GetComponentRelease(context.Background(), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	var install domain.ActionDefinition
	for _, action := range release.Actions {
		if action.Kind == domain.ActionInstall {
			install = action
			break
		}
	}
	if install.ID == "" {
		t.Fatal("fixture has no install action")
	}
	install.Name = "Atomic install"
	path := "/api/v1/component-releases/" + releaseID + "/playbook"
	saved := f.request(http.MethodPut, path, map[string]any{
		"actionKind": install.Kind, "action": install,
		"content": "---\n- hosts: all\n  tasks: []\n", "expectedSha256": install.PlaybookSHA256, "expectedTreeSha256": release.PlaybookTreeSHA256,
	}, alice)
	if saved.Code != http.StatusOK {
		t.Fatalf("atomic action save status=%d body=%s", saved.Code, saved.Body.String())
	}
	data := decodeEnvelope(t, saved)["data"].(map[string]any)
	if data["action"].(map[string]any)["name"] != "Atomic install" {
		t.Fatalf("atomic action response=%#v", data)
	}
	persisted, err := f.database.GetComponentRelease(context.Background(), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.PlaybookFiles) != len(release.PlaybookFiles) {
		t.Fatalf("manifest files=%+v", persisted.PlaybookFiles)
	}
	found := false
	for _, action := range persisted.Actions {
		if action.ID == install.ID {
			found = action.Name == "Atomic install" && action.PlaybookSHA256 == data["sha256"].(string)
		}
	}
	if !found {
		t.Fatalf("action metadata was not committed with entrypoint: %+v", persisted.Actions)
	}
	workspace := f.request(http.MethodGet, "/api/v1/component-releases/"+releaseID+"/playbook-workspace", nil, alice)
	workspaceData := decodeEnvelope(t, workspace)["data"].(map[string]any)
	deleted := f.request(http.MethodDelete, path+"?actionKind=install&expectedSha256="+url.QueryEscape(data["sha256"].(string))+"&expectedTreeSha256="+url.QueryEscape(workspaceData["treeSha256"].(string)), nil, alice)
	if deleted.Code != http.StatusOK {
		t.Fatalf("atomic action delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	persisted, err = f.database.GetComponentRelease(context.Background(), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range persisted.Actions {
		if action.Kind == domain.ActionInstall {
			t.Fatalf("atomic delete left action metadata: %+v", action)
		}
	}
	if len(persisted.PlaybookFiles) != len(release.PlaybookFiles)-1 {
		t.Fatalf("atomic delete left manifest: %+v", persisted.PlaybookFiles)
	}
}

func TestDraftReleaseDeprecationAllowsActiveTestAndPreservesHistory(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	alice := f.session(seed.ComponentOwnerRuntimeID)
	bob := f.session(seed.ComponentOwnerK8sID)
	releaseID := "release-test-runtime-1.1.0"
	path := "/api/v1/component-releases/" + releaseID + "/deprecate"
	f.recordReleaseReadiness(releaseID)
	if err := f.setCandidate(ctx, releaseID, true); err != nil {
		t.Fatal(err)
	}
	activeRun := domain.Run{
		ID: "run-active-draft-deprecation", Kind: domain.RunComponentTest, Status: domain.RunQueued,
		RequestedBy: seed.ComponentOwnerRuntimeID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
		ComponentReleaseID: releaseID, Action: domain.ActionInstall, InputSnapshot: map[string]any{}, CreatedAt: time.Now().UTC(),
	}
	if err := f.database.CreateRun(ctx, activeRun, nil); err != nil {
		t.Fatal(err)
	}
	impact := f.request(http.MethodGet, "/api/v1/component-releases/"+releaseID+"/impact", nil, alice)
	if impact.Code != http.StatusOK || !strings.Contains(impact.Body.String(), "scenario-test-runtime") {
		t.Fatalf("Draft impact status=%d body=%s", impact.Code, impact.Body.String())
	}
	if response := f.request(http.MethodPost, path, nil, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other owner deprecate Draft status=%d body=%s", response.Code, response.Body.String())
	}
	deprecated := f.request(http.MethodPost, path, nil, alice)
	if deprecated.Code != http.StatusOK {
		t.Fatalf("deprecate Draft status=%d body=%s", deprecated.Code, deprecated.Body.String())
	}
	data := decodeEnvelope(t, deprecated)["data"].(map[string]any)
	if data["status"] != string(domain.ReleaseDeprecated) || data["candidate"] != false || data["deprecatedAt"] == nil {
		t.Fatalf("deprecated Draft response=%#v", data)
	}
	if _, found := data["verified"]; found {
		t.Fatalf("deprecated Draft exposed removed verified field: %#v", data)
	}
	restored := f.request(http.MethodPost, "/api/v1/component-releases/"+releaseID+"/restore", nil, alice)
	if restored.Code != http.StatusOK {
		t.Fatalf("restore Draft status=%d body=%s", restored.Code, restored.Body.String())
	}
	restoredData := decodeEnvelope(t, restored)["data"].(map[string]any)
	if restoredData["status"] != string(domain.ReleaseDraft) || restoredData["deprecatedAt"] != nil {
		t.Fatalf("restored Draft response=%#v", restoredData)
	}
	if response := f.request(http.MethodPost, path, nil, alice); response.Code != http.StatusOK {
		t.Fatalf("deprecate restored Draft status=%d body=%s", response.Code, response.Body.String())
	}
	blockedDelete := f.request(http.MethodDelete, "/api/v1/component-releases/"+releaseID, nil, alice)
	if blockedDelete.Code != http.StatusConflict || !strings.Contains(blockedDelete.Body.String(), "Run") {
		t.Fatalf("delete Run-locked Draft status=%d body=%s", blockedDelete.Code, blockedDelete.Body.String())
	}
	if response := f.request(http.MethodPost, path, nil, alice); response.Code != http.StatusConflict {
		t.Fatalf("repeat Draft deprecation status=%d body=%s", response.Code, response.Body.String())
	}
	var deprecationAuditCount, restoreAuditCount int
	if err := f.database.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='component_release.deprecated' AND resource_id=?`, releaseID).Scan(&deprecationAuditCount); err != nil || deprecationAuditCount != 2 {
		t.Fatalf("deprecation audit count=%d err=%v", deprecationAuditCount, err)
	}
	if err := f.database.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='component_release.restored' AND resource_id=?`, releaseID).Scan(&restoreAuditCount); err != nil || restoreAuditCount != 1 {
		t.Fatalf("restore audit count=%d err=%v", restoreAuditCount, err)
	}
}

func TestNeverPublishedDeprecatedReleaseCanRestoreOrPermanentlyDelete(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	alice := f.session(seed.ComponentOwnerRuntimeID)
	bob := f.session(seed.ComponentOwnerK8sID)
	now := time.Now().UTC()
	lineID := "line-disposable-release"
	releaseID := "release-disposable-draft"
	if _, err := f.database.DB().ExecContext(ctx, `INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES(?,?,?,?)`, lineID, "component-test-runtime", "Disposable", now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := f.database.CreateComponentRelease(ctx, domain.ComponentRelease{
		ID: releaseID, ComponentID: "component-test-runtime", LineID: lineID, Version: "discard-me", Status: domain.ReleaseDraft,
		Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	playbookRoot := t.TempDir()
	managedDirectory := filepath.Join(playbookRoot, "managed", "component-test-runtime", "disposable", "discard-me--release-disposable-draft")
	if err := os.MkdirAll(managedDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedDirectory, "install.yml"), []byte("---\n[]\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	f.configurePlaybookRoot(playbookRoot)
	path := "/api/v1/component-releases/" + releaseID
	if response := f.request(http.MethodDelete, path, nil, alice); response.Code != http.StatusConflict {
		t.Fatalf("delete active Draft status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPost, path+"/deprecate", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("deprecate disposable Draft status=%d body=%s", response.Code, response.Body.String())
	}
	conflictingReleaseID := "release-disposable-conflict"
	if err := f.database.CreateComponentRelease(ctx, domain.ComponentRelease{
		ID: conflictingReleaseID, ComponentID: "component-test-runtime", LineID: lineID, Version: "replacement", Status: domain.ReleaseDraft,
		Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if response := f.request(http.MethodPost, path+"/restore", nil, alice); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "新的 Draft") {
		t.Fatalf("restore conflicting Draft status=%d body=%s", response.Code, response.Body.String())
	}
	conflictingPath := "/api/v1/component-releases/" + conflictingReleaseID
	if response := f.request(http.MethodPost, conflictingPath+"/deprecate", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("deprecate conflicting Draft status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodDelete, conflictingPath, nil, alice); response.Code != http.StatusOK {
		t.Fatalf("delete conflicting Draft status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPost, path+"/restore", nil, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other owner restore status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPost, path+"/restore", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("restore disposable Draft status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPost, path+"/deprecate", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("deprecate restored disposable Draft status=%d body=%s", response.Code, response.Body.String())
	}
	referencingScenario := domain.Scenario{
		ID: "scenario-references-disposable-release", Slug: "references-disposable-release", Name: "References disposable Release",
		OwnerID: seed.ScenarioOwnerID, CreatedAt: now, UpdatedAt: now,
	}
	referencingRevision := domain.ScenarioRevision{
		ID: "scenario-references-disposable-release-r1", ScenarioID: referencingScenario.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "disposable", Name: "Disposable", ReleaseID: releaseID, Action: domain.ActionInstall, HostGroup: "test_nodes", ParameterValues: map[string]any{}}}, Edges: []domain.ScenarioEdge{}}, CreatedAt: now,
	}
	referencingScenario.CurrentRevisionID = referencingRevision.ID
	if err := f.database.CreateScenario(ctx, referencingScenario, referencingRevision); err != nil {
		t.Fatal(err)
	}
	if response := f.request(http.MethodDelete, path, nil, alice); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "仍被") {
		t.Fatalf("delete referenced disposable Draft status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := f.database.DB().ExecContext(ctx, `UPDATE scenario_revisions SET graph_json='{"nodes":[],"edges":[]}' WHERE id=?`, referencingRevision.ID); err != nil {
		t.Fatal(err)
	}
	if response := f.request(http.MethodDelete, path, nil, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other owner delete status=%d body=%s", response.Code, response.Body.String())
	}
	deleted := f.request(http.MethodDelete, path, nil, alice)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete disposable Draft status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, err := f.database.GetComponentRelease(ctx, releaseID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted Release still exists: %v", err)
	}
	if _, err := os.Stat(managedDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed Playbook directory was not deleted: %v", err)
	}
	var lineCount, deleteAuditCount int
	if err := f.database.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM component_release_lines WHERE id=?`, lineID).Scan(&lineCount); err != nil || lineCount != 0 {
		t.Fatalf("empty Release line count=%d err=%v", lineCount, err)
	}
	if err := f.database.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action='component_release.deleted' AND resource_id=?`, releaseID).Scan(&deleteAuditCount); err != nil || deleteAuditCount != 1 {
		t.Fatalf("Release delete audit count=%d err=%v", deleteAuditCount, err)
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

	created := f.request(http.MethodPost, "/api/v1/components", componentRequest("Owner A private", "alice-private"), alice)
	if created.Code != http.StatusCreated {
		t.Fatalf("owner create status=%d body=%s", created.Code, created.Body.String())
	}
	createdData := decodeEnvelope(t, created)["data"].(map[string]any)
	if createdData["layer"] != "runtime_state" || fmt.Sprint(createdData["tags"]) != "[runtime]" {
		t.Fatalf("component classification response=%#v", createdData)
	}
	for _, removed := range []string{"category", "kind", "requiredness"} {
		if _, found := createdData[removed]; found {
			t.Fatalf("component response exposed removed %s field: %#v", removed, createdData)
		}
	}
	componentID := createdData["id"].(string)
	if response := f.request(http.MethodPatch, "/api/v1/components/"+componentID, map[string]any{"name": "hijacked"}, bob); response.Code != http.StatusForbidden {
		t.Fatalf("other component owner update status=%d", response.Code)
	}
	if response := f.request(http.MethodPost, "/api/v1/components", componentRequest("bad", "bad"), carol); response.Code != http.StatusForbidden {
		t.Fatalf("scenario owner component create status=%d", response.Code)
	}
	invalidClassification := componentRequest("Invalid", "invalid-classification")
	invalidClassification["layer"] = "dns"
	if response := f.request(http.MethodPost, "/api/v1/components", invalidClassification, alice); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid classification status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPut, "/api/v1/environments/environment-test/parameters", map[string]any{"values": map[string]any{}, "changeReason": "unauthorized"}, alice); response.Code != http.StatusForbidden {
		t.Fatalf("component owner environment parameter update status=%d", response.Code)
	}
	if response := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"IMAGE_REGISTRY": "hijacked.invalid"}}, alice); response.Code != http.StatusForbidden {
		t.Fatalf("component owner environment variable update status=%d", response.Code)
	}
	variablesUpdated := f.request(http.MethodPut, "/api/v1/environments/environment-test/variables", map[string]any{"variables": map[string]any{"IMAGE_REGISTRY": "registry.example.test:5000/"}}, dave)
	if variablesUpdated.Code != http.StatusOK || !strings.Contains(variablesUpdated.Body.String(), `"IMAGE_REGISTRY":"registry.example.test:5000"`) {
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
	rejected := f.request(http.MethodPut, "/api/v1/environments/environment-test/parameters", map[string]any{"values": map[string]any{"registryPassword": secret}}, dave)
	if rejected.Code != http.StatusBadRequest || strings.Contains(rejected.Body.String(), secret) {
		t.Fatalf("unknown environment parameter status=%d body=%s", rejected.Code, rejected.Body.String())
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
	script := "#!/bin/sh\nif [ \"$1\" = image ]; then printf '%s\\n' 'registry.example.test:5000/components/component-test-runtime:v1.1.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; else printf '%s ok\\n' \"$1\"; fi\n"
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
	if data["imageRef"] != "registry.example.test:5000/components/component-test-runtime:v1.1.0" {
		t.Fatalf("image reference was not forced to configured registry: %#v", data)
	}
	if data["environmentId"] != "environment-test" || data["environmentRevisionId"] == "" {
		t.Fatalf("image build did not lock the environment revision: %#v", data)
	}
	var auditCount int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='component.image_build_requested' AND metadata_json LIKE '%environmentRevisionId%'`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("image build environment audit count=%d err=%v", auditCount, err)
	}

	waitForImageBuild(t, f.database, buildID, domain.ImageBuildSucceeded)
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

func TestSimplifiedComponentAndReleaseMetadata(t *testing.T) {
	f := newAPIFixture(t)
	response := f.request(http.MethodGet, "/api/v1/components/component-kubernetes", nil, f.session(seed.ComponentOwnerK8sID))
	if response.Code != http.StatusOK {
		t.Fatalf("component response status=%d body=%s", response.Code, response.Body.String())
	}
	data := decodeEnvelope(t, response)["data"].(map[string]any)
	lines := data["releaseLines"].([]any)
	if data["layer"] != string(domain.LayerOrchestrationCore) || len(data["tags"].([]any)) == 0 {
		t.Fatalf("simplified component metadata missing: %#v", data)
	}
	if len(lines) == 0 || len(lines[0].(map[string]any)["releaseIds"].([]any)) == 0 {
		t.Fatalf("component release lines missing: %#v", lines)
	}
	if _, found := data["kind"]; found {
		t.Fatalf("component exposed removed kind: %#v", data)
	}
	if _, found := data["latestRelease"]; found {
		t.Fatalf("component exposed removed cross-line latestRelease: %#v", data)
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
	dave := f.session(seed.EnvironmentOwnerID)
	carol := f.session(seed.ScenarioOwnerID)

	for _, removed := range []string{"category", "kind", "requiredness"} {
		input := componentRequest("Removed "+removed, "removed-"+removed)
		input[removed] = "legacy"
		response := f.request(http.MethodPost, "/api/v1/components", input, alice)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unknown field") {
			t.Fatalf("removed component field %s status=%d body=%s", removed, response.Code, response.Body.String())
		}
	}
	legacyEnvironment := f.request(http.MethodPost, "/api/v1/environments", map[string]any{"name": "Legacy concurrency", "facts": map[string]any{}, "maxConcurrent": 2}, dave)
	if legacyEnvironment.Code != http.StatusBadRequest || !strings.Contains(legacyEnvironment.Body.String(), "unknown field") {
		t.Fatalf("removed maxConcurrent status=%d body=%s", legacyEnvironment.Code, legacyEnvironment.Body.String())
	}

	for name, definition := range map[string]map[string]any{
		"release state alias": {
			"mode": "new_line", "lineName": "Strict state", "version": "strict-state", "releaseNotes": "strict", "state": "draft",
		},
		"action type alias": {
			"mode": "new_line", "lineName": "Strict action", "version": "strict-action", "releaseNotes": "strict", "actions": []any{},
		},
		"removed release type": {
			"mode": "new_line", "lineName": "Strict type", "version": "strict-type", "releaseNotes": "strict", "type": "atomic",
		},
	} {
		t.Run(name, func(t *testing.T) {
			response := f.request(http.MethodPost, "/api/v1/components/component-test-runtime/release-draft-plan", definition, alice)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unknown field") {
				t.Fatalf("unknown first-version field status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	legacyActionPath := f.request(http.MethodPut, "/api/v1/component-releases/release-test-runtime-1.1.0", map[string]any{
		"version": "1.1.0", "actions": []any{map[string]any{"name": "install", "kind": "install", "playbook": "caller-selected.yml"}},
	}, alice)
	if legacyActionPath.Code != http.StatusBadRequest || !strings.Contains(legacyActionPath.Body.String(), "unknown field") {
		t.Fatalf("client-selected action playbook status=%d body=%s", legacyActionPath.Code, legacyActionPath.Body.String())
	}

	flatGraph := f.request(http.MethodPut, "/api/v1/scenario-revisions/scenario-test-runtime-r1/graph", map[string]any{
		"nodes": []any{map[string]any{"id": "flat", "releaseId": "release-test-runtime-1.0.0", "action": "install"}},
		"edges": []any{},
	}, alice)
	if flatGraph.Code != http.StatusBadRequest || !strings.Contains(flatGraph.Body.String(), "unknown field") {
		t.Fatalf("old flat graph status=%d body=%s", flatGraph.Code, flatGraph.Body.String())
	}
	edgeLabel := f.request(http.MethodPut, "/api/v1/scenario-revisions/scenario-test-runtime-r2/graph", map[string]any{
		"nodes": []any{}, "edges": []any{map[string]any{"id": "legacy-label", "source": "a", "target": "b", "label": "legacy"}}, "executionPolicy": map[string]any{},
	}, alice)
	if edgeLabel.Code != http.StatusBadRequest || !strings.Contains(edgeLabel.Body.String(), "unknown field") {
		t.Fatalf("edge label outside V1 contract status=%d body=%s", edgeLabel.Code, edgeLabel.Body.String())
	}
	legacyNodeParameters := f.request(http.MethodPut, "/api/v1/scenario-revisions/scenario-test-runtime-r2/graph", map[string]any{
		"nodes": []any{map[string]any{"id": "legacy-runtime", "type": "component", "position": map[string]any{"x": 0, "y": 0}, "data": map[string]any{"label": "legacy", "releaseId": "release-test-runtime-1.1.0", "action": "install", "values": map[string]any{}, "runInputs": []any{"temporary"}}}},
		"edges": []any{},
	}, carol)
	if legacyNodeParameters.Code != http.StatusBadRequest || !strings.Contains(legacyNodeParameters.Body.String(), "unknown field") {
		t.Fatalf("legacy scenario parameters status=%d body=%s", legacyNodeParameters.Code, legacyNodeParameters.Body.String())
	}
	legacyScenarioRun := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r2/test-runs", map[string]any{"environmentId": "environment-test", "runInput": map[string]any{"temporary": true}}, carol)
	if legacyScenarioRun.Code != http.StatusBadRequest || !strings.Contains(legacyScenarioRun.Body.String(), "unknown field") {
		t.Fatalf("legacy scenario runInput status=%d body=%s", legacyScenarioRun.Code, legacyScenarioRun.Body.String())
	}
	legacyComponentTest := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-plan", map[string]any{"environmentId": "environment-test", "mode": "install_verify", "dependencyFixtures": map[string]any{"temporary": true}}, alice)
	if legacyComponentTest.Code != http.StatusBadRequest || !strings.Contains(legacyComponentTest.Body.String(), "unknown field") {
		t.Fatalf("legacy dependencyFixtures status=%d body=%s", legacyComponentTest.Code, legacyComponentTest.Body.String())
	}
	if presets := f.request(http.MethodGet, "/api/v1/run-input-presets", nil, carol); presets.Code != http.StatusNotFound {
		t.Fatalf("removed run input presets status=%d body=%s", presets.Code, presets.Body.String())
	}
}

func TestPublishReleaseRequiresCurrentDeliveryEvidence(t *testing.T) {
	t.Run("install verification", func(t *testing.T) {
		f := newAPIFixture(t)
		alice := f.session(seed.ComponentOwnerRuntimeID)
		response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/publish", nil, alice)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "缺少父版本安装、升级、验证和回退闭环证据") {
			t.Fatalf("unverified publish status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("complete lifecycle", func(t *testing.T) {
		f := newAPIFixture(t)
		alice := f.session(seed.ComponentOwnerRuntimeID)
		createdComponent := f.request(http.MethodPost, "/api/v1/components", componentRequest("Incomplete", "incomplete"), alice)
		componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
		createdRelease := f.createNewLineDraft(componentID, map[string]any{
			"version": "1.0.0",
			"actions": []any{map[string]any{"name": "install", "kind": "install", "hostGroup": "test_nodes", "timeoutSeconds": 60}},
		}, alice)
		releaseID := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["id"].(string)
		response := f.request(http.MethodPost, "/api/v1/component-releases/"+releaseID+"/publish", nil, alice)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "缺少 Verify 生命周期动作") || !strings.Contains(response.Body.String(), "缺少 Rollback 生命周期动作") {
			t.Fatalf("incomplete lifecycle publish status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("rollback evidence", func(t *testing.T) {
		f := newAPIFixture(t)
		alice := f.session(seed.ComponentOwnerRuntimeID)
		f.recordReleaseReadiness("release-test-runtime-1.1.0")
		if _, err := f.database.DB().Exec(`DELETE FROM runs WHERE component_release_id=? AND json_extract(input_snapshot_json,'$.componentTestEvidence')='evolution_round_trip'`, "release-test-runtime-1.1.0"); err != nil {
			t.Fatal(err)
		}
		response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/publish", nil, alice)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "缺少父版本安装、升级、验证和回退闭环证据") {
			t.Fatalf("missing rollback evidence status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("targeted rollback cannot bypass target verification", func(t *testing.T) {
		f := newAPIFixture(t)
		alice := f.session(seed.ComponentOwnerRuntimeID)
		f.recordReleaseReadiness("release-test-runtime-1.1.0")
		if _, err := f.database.DB().Exec(`DELETE FROM runs WHERE component_release_id=? AND json_extract(input_snapshot_json,'$.componentTestEvidence')='evolution_round_trip'`, "release-test-runtime-1.1.0"); err != nil {
			t.Fatal(err)
		}
		response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/test-runs", map[string]any{
			"environmentId": "environment-test", "mode": "rollback", "rollbackVerification": map[string]any{"kind": "rollback_only"},
		}, alice)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "clean-state rollback") {
			t.Fatalf("targeted rollback-only status=%d body=%s", response.Code, response.Body.String())
		}
		candidate := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/candidate", map[string]any{"candidate": true}, alice)
		if candidate.Code != http.StatusConflict || !strings.Contains(candidate.Body.String(), "缺少父版本安装、升级、验证和回退闭环证据") {
			t.Fatalf("rollback-only accepted as candidate evidence status=%d body=%s", candidate.Code, candidate.Body.String())
		}
	})

	t.Run("stale specification", func(t *testing.T) {
		f := newAPIFixture(t)
		alice := f.session(seed.ComponentOwnerRuntimeID)
		dave := f.session(seed.EnvironmentOwnerID)
		f.completeReleaseDelivery("release-test-runtime-1.1.0", alice, dave, true)
		release, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
		if err != nil {
			t.Fatal(err)
		}
		owner, err := f.database.GetUser(context.Background(), seed.ComponentOwnerRuntimeID)
		if err != nil {
			t.Fatal(err)
		}
		release.ReleaseNotes = "contract changed after delivery evidence"
		if _, err := f.platform.UpdateRelease(context.Background(), owner, release.ID, release); err != nil {
			t.Fatal(err)
		}
		response := f.request(http.MethodPost, "/api/v1/component-releases/"+release.ID+"/publish", nil, alice)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "缺少父版本安装、升级、验证和回退闭环证据") {
			t.Fatalf("stale evidence publish status=%d body=%s", response.Code, response.Body.String())
		}
		errorBody := decodeEnvelope(t, response)["error"].(map[string]any)
		explanation := errorBody["explanation"].(map[string]any)
		reasons := explanation["reasons"].([]any)
		if reasons[0].(map[string]any)["code"] != "release.evidence_missing" || explanation["primaryAction"].(map[string]any)["href"] == "" {
			t.Fatalf("publish conflict is not actionable: %#v", errorBody)
		}

		workbench := f.request(http.MethodGet, "/api/v1/workbench", nil, alice)
		items := decodeEnvelope(t, workbench)["data"].(map[string]any)["items"].([]any)
		foundUnifiedReadiness := false
		for _, rawItem := range items {
			item := rawItem.(map[string]any)
			if item["id"] != "component_draft:"+release.ID {
				continue
			}
			for _, rawReason := range item["reasons"].([]any) {
				reason := rawReason.(map[string]any)
				if reason["code"] != "evolution_evidence_missing" {
					continue
				}
				cause := reason["cause"].(map[string]any)
				nextAction := reason["nextAction"].(map[string]any)
				foundUnifiedReadiness = cause["kind"] == "platform_rule" && strings.Contains(fmt.Sprint(nextAction["href"]), "action=validate")
			}
		}
		if !foundUnifiedReadiness {
			t.Fatalf("workbench did not reuse unified readiness blockers: %s", workbench.Body.String())
		}
	})
}

func TestReleaseImpactNotifiesDownstreamAndScenarioOwners(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	dave := f.session(seed.EnvironmentOwnerID)
	draftOnly := domain.Scenario{ID: "scenario-draft-only-impact", Slug: "draft-only-impact", Name: "Draft only impact", OwnerID: seed.ScenarioOwnerID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	draftRevision := domain.ScenarioRevision{
		ID: "scenario-draft-only-impact-r1", ScenarioID: draftOnly.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph:     domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "agent", Name: "Agent", ReleaseID: "release-test-runtime-1.0.0", Action: domain.ActionInstall, HostGroup: "test_nodes"}}, Edges: []domain.ScenarioEdge{}},
		CreatedAt: draftOnly.CreatedAt,
	}
	if err := f.database.CreateScenario(context.Background(), draftOnly, draftRevision); err != nil {
		t.Fatal(err)
	}
	f.completeReleaseDelivery("release-test-runtime-1.1.0", alice, dave, false)
	response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/publish", nil, alice)
	if response.Code != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", response.Code, response.Body.String())
	}
	published := decodeEnvelope(t, response)["data"].(map[string]any)
	if published["status"] != string(domain.ReleaseReleased) || published["readiness"].(map[string]any)["status"] != string(domain.ReadinessReady) {
		t.Fatalf("unexpected ready publish: %#v", published)
	}
	if _, found := published["verified"]; found {
		t.Fatalf("published Release exposed removed verified field: %#v", published)
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
		"version": "1.0.0", "parameters": []any{},
		"actions": []any{map[string]any{"name": "install", "kind": "install", "hostGroup": "test_nodes", "timeoutSeconds": 60, "requiredCredentials": []any{"ansible_ssh_pass"}, "idempotent": true}},
	}
	createdRelease := f.createNewLineDraft(componentID, definition, alice)
	if createdRelease.Code != http.StatusOK {
		t.Fatalf("create release status=%d body=%s", createdRelease.Code, createdRelease.Body.String())
	}
	releaseID := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["id"].(string)
	createdAction := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["actions"].([]any)[0].(map[string]any)
	if got, ok := createdAction["requiredCredentials"].([]any); !ok || len(got) != 1 || got[0] != "ansible_ssh_pass" {
		t.Fatalf("requiredCredentials API round trip=%#v", createdAction["requiredCredentials"])
	}
	if createdAction["idempotent"] != true {
		t.Fatalf("idempotent API round trip=%#v", createdAction["idempotent"])
	}
	f.recordReleaseReadiness(releaseID)
	definition["releaseNotes"] = "changed after test"
	updated := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID, definition, alice)
	if updated.Code != http.StatusOK {
		t.Fatalf("update release status=%d body=%s", updated.Code, updated.Body.String())
	}
	updatedData := decodeEnvelope(t, updated)["data"].(map[string]any)
	if updatedData["readiness"].(map[string]any)["status"] != string(domain.ReadinessBlocked) {
		t.Fatalf("draft update retained stale evidence: %#v", updatedData)
	}
	if _, found := updatedData["verified"]; found {
		t.Fatalf("draft update exposed removed verified field: %#v", updatedData)
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

func TestDirectComponentReadMatchesCandidateVisibility(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	carol := f.session(seed.ScenarioOwnerID)
	dave := f.session(seed.EnvironmentOwnerID)
	f.completeReleaseDelivery("release-test-runtime-1.1.0", alice, dave, true)
	shared := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/candidate", map[string]any{"candidate": true}, alice)
	if shared.Code != http.StatusOK {
		t.Fatalf("share candidate status=%d body=%s", shared.Code, shared.Body.String())
	}
	visible := f.request(http.MethodGet, "/api/v1/components/component-test-runtime", nil, carol)
	if visible.Code != http.StatusOK || !strings.Contains(visible.Body.String(), "release-test-runtime-1.1.0") {
		t.Fatalf("direct component read hid ready candidate status=%d body=%s", visible.Code, visible.Body.String())
	}
	contracts := f.request(http.MethodGet, "/api/v1/components?view=contracts", nil, carol)
	if contracts.Code != http.StatusOK || !strings.Contains(contracts.Body.String(), "release-test-runtime-1.1.0") {
		t.Fatalf("contract view hid ready candidate: %s", contracts.Body.String())
	}
	f.invalidateReleaseReadiness("release-test-runtime-1.1.0")
	filtered := f.request(http.MethodGet, "/api/v1/components/component-test-runtime", nil, carol)
	if filtered.Code != http.StatusOK || strings.Contains(filtered.Body.String(), "release-test-runtime-1.1.0") {
		t.Fatalf("contract change did not withdraw the candidate from non-owner visibility status=%d body=%s", filtered.Code, filtered.Body.String())
	}
	admin := f.session(seed.PlatformAdminID)
	for _, status := range []string{"not_submitted", "pending", "rejected", "approved"} {
		// Model an already-corrupt row without weakening the production write gate.
		if _, err := f.database.DB().ExecContext(context.Background(), "UPDATE component_releases SET candidate=1,review_status=?,review_contract_digest='stale' WHERE id=?", status, "release-test-runtime-1.1.0"); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/v1/components", "/api/v1/components?view=contracts", "/api/v1/components/component-test-runtime"} {
			response := f.request(http.MethodGet, path, nil, carol)
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "release-test-runtime-1.1.0") {
				t.Fatalf("invalid candidate escaped %s: %s", path, response.Body.String())
			}
		}
		for _, session := range []*http.Cookie{alice, admin} {
			response := f.request(http.MethodGet, "/api/v1/components/component-test-runtime", nil, session)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "release-test-runtime-1.1.0") {
				t.Fatalf("owner visibility changed: %s", response.Body.String())
			}
		}
	}
}

func TestDraftRequiredCredentialsDistinguishesOmittedFromExplicitEmpty(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	createdComponent := f.request(http.MethodPost, "/api/v1/components", componentRequest("Credential semantics", "credential-semantics"), alice)
	componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
	definition := map[string]any{
		"version": "1.0.0", "parameters": []any{},
		"actions": []any{map[string]any{
			"name": "install", "kind": "install", "hostGroup": "test_nodes", "timeoutSeconds": 60,
			"requiredCredentials": []any{"K8S_BOOTSTRAP_TOKEN"},
		}},
	}
	created := f.createNewLineDraft(componentID, definition, alice)
	if created.Code != http.StatusOK {
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
	playbook := f.request(http.MethodGet, "/api/v1/component-releases/"+releaseID+"/playbook?actionKind=install", nil, alice)
	playbookData := decodeEnvelope(t, playbook)["data"].(map[string]any)
	workspace := f.request(http.MethodGet, "/api/v1/component-releases/"+releaseID+"/playbook-workspace", nil, alice)
	workspaceData := decodeEnvelope(t, workspace)["data"].(map[string]any)
	cleared := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID+"/playbook", map[string]any{
		"actionKind": "install", "action": action, "content": playbookData["content"],
		"expectedSha256": playbookData["sha256"], "expectedTreeSha256": workspaceData["treeSha256"],
	}, alice)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear credential update status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	clearedAction := decodeEnvelope(t, cleared)["data"].(map[string]any)["action"].(map[string]any)
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

func TestUpdatingReleaseContractPreservesActionsAndScopesDraftUpstream(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	createdComponent := f.request(http.MethodPost, "/api/v1/components", componentRequest("Contract Edit", "contract-edit"), alice)
	componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
	createdRelease := f.createNewLineDraft(componentID, map[string]any{
		"version": "1.0.0", "riskLevel": "medium",
		"actions": []any{map[string]any{
			"name": "custom-install", "kind": "install",
			"hostGroup": "test_nodes", "timeoutSeconds": 60, "riskLevel": "high",
		}},
	}, alice)
	if createdRelease.Code != http.StatusOK {
		t.Fatalf("create release status=%d body=%s", createdRelease.Code, createdRelease.Body.String())
	}
	releaseID := decodeEnvelope(t, createdRelease)["data"].(map[string]any)["id"].(string)

	updated := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID+"/contract", map[string]any{
		"parameters": []any{map[string]any{
			"name": "runtimeRoot", "description": "runtime install root", "type": "string", "visibility": "public",
			"modifiable": true, "valueProvider": "scenario_owner", "suggestedValue": "/opt/runtime", "testValue": "/opt/runtime",
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
	if len(release.Actions) != 1 || release.Actions[0].Name != "custom-install" || release.Actions[0].HostGroup != "test_nodes" || release.Actions[0].RiskLevel != domain.RiskHigh {
		t.Fatalf("contract edit changed action metadata: %+v", release.Actions)
	}
	if len(release.Dependencies) != 1 || release.Dependencies[0].UpstreamReleaseID != "release-test-runtime-1.0.0" {
		t.Fatalf("contract dependencies=%+v", release.Dependencies)
	}

	sameOwnerDraft := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID+"/contract", map[string]any{
		"parameters": []any{},
		"dependencies": []any{map[string]any{
			"upstreamComponentId": "component-test-runtime", "upstreamReleaseId": "release-test-runtime-1.1.0", "purpose": "same-owner import chain",
		}},
	}, alice)
	if sameOwnerDraft.Code != http.StatusOK {
		t.Fatalf("same-owner Draft upstream status=%d body=%s", sameOwnerDraft.Code, sameOwnerDraft.Body.String())
	}

	privateUpstream := domain.ComponentRelease{
		ID: "release-test-consumer-private", ComponentID: "component-test-consumer", Version: "private",
		Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, Actions: []domain.ActionDefinition{}, CreatedAt: time.Now().UTC(),
	}
	if err := f.database.CreateComponentRelease(context.Background(), privateUpstream); err != nil {
		t.Fatal(err)
	}
	rejected := f.request(http.MethodPut, "/api/v1/component-releases/"+releaseID+"/contract", map[string]any{
		"parameters": []any{},
		"dependencies": []any{map[string]any{
			"upstreamComponentId": "component-test-consumer", "upstreamReleaseId": privateUpstream.ID, "purpose": "cross-owner private draft",
		}},
	}, alice)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "private Draft owned by the same") {
		t.Fatalf("cross-owner private Draft status=%d body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestCloneReleaseCanOverrideEnvironmentConstraints(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	createdComponent := f.request(http.MethodPost, "/api/v1/components", componentRequest("Clone Env", "clone-env"), alice)
	componentID := decodeEnvelope(t, createdComponent)["data"].(map[string]any)["id"].(string)
	now := time.Now().UTC()
	releaseID := "release-clone-env-1.0.0"
	if err := f.database.CreateComponentRelease(context.Background(), domain.ComponentRelease{
		ID: releaseID, ComponentID: componentID, Version: "1.0.0",
		Status: domain.ReleaseReleased, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{"architecture": []any{"amd64"}, "operatingSystem": []any{"SUSE"}},
		Parameters:             []domain.ParameterDefinition{},
		Actions:                []domain.ActionDefinition{{ID: "action-clone-env-install", ReleaseID: releaseID, Name: "install", Kind: domain.ActionInstall, Playbook: "tests/runtime/install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}},
		CreatedAt:              now, ReleasedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, f.database, f.runner.root, releaseID)
	const sourceArtifactID = "artifact-clone-env-source"
	if _, err := f.database.DB().ExecContext(context.Background(), `INSERT INTO component_release_artifacts(id,release_id,alias,filename,sha256,size_bytes,source_url,source_updated_by,source_updated_at,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, sourceArtifactID, releaseID, "package", "package.tgz", strings.Repeat("a", 64), 128, "https://files.example.invalid/components/clone-env/package.tgz", seed.ComponentOwnerRuntimeID, now.Format(time.RFC3339Nano), seed.ComponentOwnerRuntimeID, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	cloneInput := map[string]any{
		"mode": "new_line", "lineName": "Clone Env 1.1", "templateSourceReleaseId": releaseID,
		"version": "1.1.0", "releaseNotes": "add arm64", "riskLevel": "high", "compatibility": "not_applicable",
		"environmentConstraints": map[string]any{"architecture": []any{"amd64", "arm64"}, "operatingSystem": []any{"SUSE", "Kylin"}, "ipFamily": []any{"IPv4"}},
	}
	clonePlan := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-draft-plan", cloneInput, alice)
	cloneInput["expectedPlanDigest"] = decodeEnvelope(t, clonePlan)["data"].(map[string]any)["planDigest"]
	cloned := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-drafts", cloneInput, alice)
	if cloned.Code != http.StatusCreated {
		t.Fatalf("clone status=%d body=%s", cloned.Code, cloned.Body.String())
	}
	clonedData := decodeEnvelope(t, cloned)["data"].(map[string]any)
	if clonedData["riskLevel"] != "high" {
		t.Fatalf("cloned riskLevel=%#v", clonedData["riskLevel"])
	}
	constraints := clonedData["environmentConstraints"].(map[string]any)
	if !reflect.DeepEqual(constraints["architecture"], []any{"amd64", "arm64"}) {
		t.Fatalf("cloned architecture=%#v", constraints["architecture"])
	}
	if !reflect.DeepEqual(constraints["ipFamily"], []any{"IPv4"}) {
		t.Fatalf("cloned ipFamily=%#v", constraints["ipFamily"])
	}
	clonedArtifacts := clonedData["artifacts"].([]any)
	if len(clonedArtifacts) != 1 {
		t.Fatalf("cloned artifacts=%#v", clonedArtifacts)
	}
	clonedArtifact := clonedArtifacts[0].(map[string]any)
	if clonedArtifact["id"] == sourceArtifactID || clonedArtifact["releaseId"] != clonedData["id"] {
		t.Fatalf("cloned artifact retained source identity: %#v", clonedArtifact)
	}
	keptInput := map[string]any{
		"mode": "new_line", "lineName": "Clone Env 1.1.1", "templateSourceReleaseId": releaseID,
		"version": "1.1.1", "releaseNotes": "keep source constraints", "compatibility": "not_applicable",
	}
	keptPlan := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-draft-plan", keptInput, alice)
	keptInput["expectedPlanDigest"] = decodeEnvelope(t, keptPlan)["data"].(map[string]any)["planDigest"]
	kept := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-drafts", keptInput, alice)
	if kept.Code != http.StatusCreated {
		t.Fatalf("clone without constraints status=%d body=%s", kept.Code, kept.Body.String())
	}
	keptConstraints := decodeEnvelope(t, kept)["data"].(map[string]any)["environmentConstraints"].(map[string]any)
	if !reflect.DeepEqual(keptConstraints["architecture"], []any{"amd64"}) {
		t.Fatalf("source constraints were not kept: %#v", keptConstraints)
	}
	if _, err := f.database.DB().ExecContext(context.Background(), `CREATE TRIGGER fail_cloned_artifact BEFORE INSERT ON component_release_artifacts WHEN NEW.release_id<>'release-clone-env-1.0.0' BEGIN SELECT RAISE(ABORT, 'forced cloned artifact failure'); END`); err != nil {
		t.Fatal(err)
	}
	failedInput := map[string]any{"mode": "new_line", "lineName": "Clone Env 1.1.2", "templateSourceReleaseId": releaseID, "version": "1.1.2", "releaseNotes": "force artifact failure", "compatibility": "not_applicable"}
	failedPlan := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-draft-plan", failedInput, alice)
	failedInput["expectedPlanDigest"] = decodeEnvelope(t, failedPlan)["data"].(map[string]any)["planDigest"]
	if failed := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-drafts", failedInput, alice); failed.Code < 400 {
		t.Fatalf("forced artifact failure status=%d body=%s", failed.Code, failed.Body.String())
	}
	var partialReleases, partialAudits int
	if err := f.database.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM component_releases WHERE component_id=? AND version='1.1.2'`, componentID).Scan(&partialReleases); err != nil {
		t.Fatal(err)
	}
	if err := f.database.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_events WHERE action='component_release.draft_created' AND metadata_json LIKE '%1.1.2%'`).Scan(&partialAudits); err != nil {
		t.Fatal(err)
	}
	if partialReleases != 0 || partialAudits != 0 {
		t.Fatalf("failed clone left partial state: releases=%d audits=%d", partialReleases, partialAudits)
	}
}

func TestPublishRejectsCrossReleaseTransitionMismatch(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	componentResponse := f.request(http.MethodPost, "/api/v1/components", componentRequest("Transitions", "transitions"), alice)
	componentID := decodeEnvelope(t, componentResponse)["data"].(map[string]any)["id"].(string)
	now := time.Now().UTC()
	oldID := "release-transitions-1.0.0"
	if err := f.database.CreateComponentRelease(context.Background(), domain.ComponentRelease{
		ID: oldID, ComponentID: componentID, Version: "1.0.0",
		Status: domain.ReleaseReleased, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{},
		Actions:   []domain.ActionDefinition{{ID: "action-transitions-install", ReleaseID: oldID, Name: "install", Kind: domain.ActionInstall, Playbook: "tests/runtime/install-v1.0.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}},
		CreatedAt: now, ReleasedAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, f.database, f.runner.root, oldID)
	draftInput := map[string]any{"mode": "evolution", "parentReleaseId": oldID, "version": "2.0.0", "releaseNotes": "bad transition", "compatibility": "compatible"}
	preview := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-draft-plan", draftInput, alice)
	draftInput["expectedPlanDigest"] = decodeEnvelope(t, preview)["data"].(map[string]any)["planDigest"]
	newResponse := f.request(http.MethodPost, "/api/v1/components/"+componentID+"/release-drafts", draftInput, alice)
	newID := decodeEnvelope(t, newResponse)["data"].(map[string]any)["id"].(string)
	f.configurePlaybookRoot(t.TempDir())
	actionInputs := []any{map[string]any{
		"name": "upgrade", "kind": "upgrade", "hostGroup": "test_nodes", "timeoutSeconds": 60,
		"fromReleaseId": oldID, "toReleaseId": "another-release",
	}, map[string]any{
		"name": "rollback", "kind": "rollback", "hostGroup": "test_nodes", "timeoutSeconds": 60,
		"fromReleaseId": newID, "toReleaseId": oldID,
	}}
	persistedActions := make([]any, 0, len(actionInputs)+1)
	for _, raw := range decodeEnvelope(t, newResponse)["data"].(map[string]any)["actions"].([]any) {
		existing := raw.(map[string]any)
		delete(existing, "playbook")
		delete(existing, "releaseId")
		persistedActions = append(persistedActions, existing)
	}
	treeSHA := decodeEnvelope(t, newResponse)["data"].(map[string]any)["playbookTreeSha256"].(string)
	for _, raw := range actionInputs {
		action := raw.(map[string]any)
		kind := action["kind"].(string)
		saved := f.request(http.MethodPut, "/api/v1/component-releases/"+newID+"/playbook", map[string]any{
			"actionKind": kind, "action": action, "content": "---\n- hosts: all\n  tasks: []\n", "expectedSha256": "", "expectedTreeSha256": treeSHA,
		}, alice)
		if saved.Code != http.StatusOK {
			t.Fatalf("save mismatched %s action status=%d body=%s", kind, saved.Code, saved.Body.String())
		}
		data := decodeEnvelope(t, saved)["data"].(map[string]any)
		persistedAction := data["action"].(map[string]any)
		delete(persistedAction, "playbook")
		delete(persistedAction, "releaseId")
		persistedActions = append(persistedActions, persistedAction)
		workspace := f.request(http.MethodGet, "/api/v1/component-releases/"+newID+"/playbook-workspace", nil, alice)
		treeSHA = decodeEnvelope(t, workspace)["data"].(map[string]any)["treeSha256"].(string)
	}
	updated := f.request(http.MethodPut, "/api/v1/component-releases/"+newID, map[string]any{
		"version": "2.0.0", "releaseNotes": "bad transition", "compatibility": "compatible",
		"actions": persistedActions,
	}, alice)
	if updated.Code != http.StatusOK {
		t.Fatalf("configure mismatched transition status=%d body=%s", updated.Code, updated.Body.String())
	}
	badPublish := f.request(http.MethodPost, "/api/v1/component-releases/"+newID+"/publish", nil, alice)
	if badPublish.Code != http.StatusBadRequest || !strings.Contains(badPublish.Body.String(), "upgrade action must point") {
		t.Fatalf("mismatched transition publish status=%d body=%s", badPublish.Code, badPublish.Body.String())
	}
}

func TestOnlyCurrentScenarioRevisionCanBeMutatedOrTested(t *testing.T) {
	f := newAPIFixture(t)
	carol := f.session(seed.ScenarioOwnerID)
	blockedClone := f.request(http.MethodPost, "/api/v1/scenarios/scenario-test-runtime/revisions", map[string]any{"sourceRevisionId": "scenario-test-runtime-r1"}, carol)
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
	clonePlan := f.request(http.MethodPost, "/api/v1/scenarios/scenario-test-runtime/revision-clone-plan", map[string]any{"sourceRevisionId": "scenario-test-runtime-r1"}, carol)
	planDigest := decodeEnvelope(t, clonePlan)["data"].(map[string]any)["planDigest"]
	cloned := f.request(http.MethodPost, "/api/v1/scenarios/scenario-test-runtime/revisions", map[string]any{"sourceRevisionId": "scenario-test-runtime-r1", "expectedPlanDigest": planDigest}, carol)
	if cloned.Code != http.StatusCreated {
		t.Fatalf("clone revision status=%d body=%s", cloned.Code, cloned.Body.String())
	}
	if revision := decodeEnvelope(t, cloned)["data"].(map[string]any); revision["revision"] != float64(3) {
		t.Fatalf("cloned revision=%#v", revision)
	}
	if duplicate := f.request(http.MethodPost, "/api/v1/scenarios/scenario-test-runtime/revisions", map[string]any{"sourceRevisionId": "scenario-test-runtime-r1", "expectedPlanDigest": planDigest}, carol); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate active draft clone status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	oldGraphEdit := f.request(http.MethodPut, "/api/v1/scenario-revisions/scenario-test-runtime-r2/graph", map[string]any{
		"nodes": []any{map[string]any{"id": "old", "type": "component", "position": map[string]any{"x": 0, "y": 0}, "data": map[string]any{"label": "old", "releaseId": "release-test-runtime-1.0.0", "action": "install", "hostGroup": "test_nodes"}}},
		"edges": []any{},
	}, carol)
	if oldGraphEdit.Code != http.StatusBadRequest || !strings.Contains(oldGraphEdit.Body.String(), "unknown field") {
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

func TestScenarioDeletionRetainsPublishedAndRunHistory(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	carol := f.session(seed.ScenarioOwnerID)
	alice := f.session(seed.ComponentOwnerRuntimeID)

	create := func(name, slug string) (string, string) {
		response := f.request(http.MethodPost, "/api/v1/scenarios", map[string]any{"name": name, "slug": slug}, carol)
		if response.Code != http.StatusCreated {
			t.Fatalf("create scenario status=%d body=%s", response.Code, response.Body.String())
		}
		data := decodeEnvelope(t, response)["data"].(map[string]any)
		return data["id"].(string), data["currentRevisionId"].(string)
	}

	deletableID, _ := create("Disposable Scenario", "disposable-scenario")
	if denied := f.request(http.MethodDelete, "/api/v1/scenarios/"+deletableID, nil, alice); denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner delete status=%d body=%s", denied.Code, denied.Body.String())
	}
	deleted := f.request(http.MethodDelete, "/api/v1/scenarios/"+deletableID, nil, carol)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete unused scenario status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, err := f.database.GetScenario(ctx, deletableID, true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted scenario still exists: %v", err)
	}
	var auditCount int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='scenario.deleted' AND resource_id=?`, deletableID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("scenario delete audit count=%d err=%v", auditCount, err)
	}
	now := time.Now().UTC()

	if blocked := f.request(http.MethodDelete, "/api/v1/scenarios/scenario-test-runtime", nil, carol); blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "已发布") {
		t.Fatalf("published scenario delete status=%d body=%s", blocked.Code, blocked.Body.String())
	}

	runLockedID, runLockedRevisionID := create("Run Locked Scenario", "run-locked-scenario")
	finished := now.Add(time.Minute)
	if err := f.database.CreateRun(ctx, domain.Run{
		ID: "run-locks-draft-scenario", Kind: domain.RunScenarioTest, Status: domain.RunFailed,
		RequestedBy: seed.ScenarioOwnerID, EnvironmentID: "environment-test", EnvironmentRevisionID: "environment-test-r1",
		ScenarioRevisionID: runLockedRevisionID, InputSnapshot: map[string]any{}, CreatedAt: now, FinishedAt: &finished,
	}, nil); err != nil {
		t.Fatal(err)
	}
	blocked := f.request(http.MethodDelete, "/api/v1/scenarios/"+runLockedID, nil, carol)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "运行记录") {
		t.Fatalf("run-locked scenario delete status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	if _, err := f.database.GetScenario(ctx, runLockedID, true); err != nil {
		t.Fatalf("run-locked scenario was deleted: %v", err)
	}
}

func TestUnrunScenarioReferenceAllowsComponentReleaseDeprecation(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	path := "/api/v1/component-releases/release-test-runtime-1.0.0"
	if response := f.request(http.MethodPost, path+"/deprecate", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("deprecate unrun referenced release status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodPost, path+"/restore", nil, alice); response.Code != http.StatusConflict {
		t.Fatalf("restore previously published release status=%d body=%s", response.Code, response.Body.String())
	}
	if response := f.request(http.MethodDelete, path, nil, alice); response.Code != http.StatusConflict {
		t.Fatalf("delete previously published release status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestScenarioRunHistoryBlocksComponentReleaseDeprecation(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	carol := f.session(seed.ScenarioOwnerID)
	runResponse := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r1/runs", map[string]any{"environmentId": "environment-test"}, carol)
	if runResponse.Code != http.StatusAccepted {
		t.Fatalf("run released scenario status=%d body=%s", runResponse.Code, runResponse.Body.String())
	}
	runID := decodeEnvelope(t, runResponse)["data"].(map[string]any)["id"].(string)
	waitForRun(t, f.database, runID, domain.RunSucceeded)
	impact := f.request(http.MethodGet, "/api/v1/component-releases/release-test-runtime-1.0.0/impact", nil, alice)
	if impact.Code != http.StatusOK {
		t.Fatalf("release impact status=%d body=%s", impact.Code, impact.Body.String())
	}
	if count := decodeEnvelope(t, impact)["data"].(map[string]any)["scenarioRunCount"]; count != float64(1) {
		t.Fatalf("scenarioRunCount=%v body=%s", count, impact.Body.String())
	}
	deprecate := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.0.0/deprecate", nil, alice)
	if deprecate.Code != http.StatusConflict || !strings.Contains(deprecate.Body.String(), "已被场景引用并运行") {
		t.Fatalf("deprecate scenario-run release status=%d body=%s", deprecate.Code, deprecate.Body.String())
	}
	if err := f.database.DeprecateComponentRelease(context.Background(), "release-test-runtime-1.0.0", time.Now().UTC()); !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "scenario run") {
		t.Fatalf("store allowed scenario-run release deprecation: %v", err)
	}
	retained, err := f.database.GetComponentRelease(context.Background(), "release-test-runtime-1.0.0")
	if err != nil || retained.Status != domain.ReleaseReleased || retained.DeprecatedAt != nil {
		t.Fatalf("retained release=%#v err=%v", retained, err)
	}
	var auditCount int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='component_release.deprecated' AND resource_id='release-test-runtime-1.0.0'`).Scan(&auditCount); err != nil || auditCount != 0 {
		t.Fatalf("blocked deprecation audit count=%d err=%v", auditCount, err)
	}
}

func TestRollbackVerifiesTargetReleaseDefaults(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	carol := f.session(seed.ScenarioOwnerID)
	dave := f.session(seed.EnvironmentOwnerID)
	f.completeReleaseDelivery("release-test-runtime-1.1.0", alice, dave, true)
	if response := f.request(http.MethodPost, "/api/v1/component-releases/release-test-runtime-1.1.0/publish", nil, alice); response.Code != http.StatusOK {
		t.Fatalf("publish rollback source status=%d body=%s", response.Code, response.Body.String())
	}
	now := time.Now().UTC()
	scenario := domain.Scenario{ID: "scenario-rollback-target", Slug: "rollback-target", Name: "Rollback target", OwnerID: seed.ScenarioOwnerID, CreatedAt: now, UpdatedAt: now}
	revision := domain.ScenarioRevision{
		ID: "scenario-rollback-target-r1", ScenarioID: scenario.ID, Revision: 1, Status: domain.RevisionDraft,
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{
			ID: "rollback", Name: "Rollback", ReleaseID: "release-test-runtime-1.1.0", Action: domain.ActionRollback,
			HostGroup: "test_nodes", ParameterValues: map[string]any{},
		}}, Edges: []domain.ScenarioEdge{}}, CreatedAt: now,
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
	if len(requests) < 2 {
		t.Fatalf("rollback execution requests=%+v", requests)
	}
	requests = requests[len(requests)-2:]
	if requests[0].Playbook != "managed/fixtures/release-test-runtime-1.1.0/rollback.yml" || requests[1].Playbook != "managed/fixtures/release-test-runtime-1.0.0/verify.yml" || requests[1].Variables["expected_version"] != "1.0.0" {
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
	if len(steps) != 2 || steps[0].(map[string]any)["playbook"] != "managed/fixtures/release-test-runtime-1.1.0/rollback.yml" || steps[1].(map[string]any)["playbook"] != "managed/fixtures/release-test-runtime-1.0.0/verify.yml" || steps[1].(map[string]any)["releaseId"] != "release-test-runtime-1.0.0" {
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
	release, err := f.platform.Catalog().GetComponentRelease(context.Background(), "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if release.Readiness.InstallEvidenceRunID != "" || release.Readiness.Status != domain.ReadinessBlocked {
		t.Fatalf("rollback-only component test incorrectly satisfied install readiness: %+v", release.Readiness)
	}
}

func TestDraftRollbackPlanPreviewStrategiesAndDigest(t *testing.T) {
	f := newAPIFixture(t)
	alice := f.session(seed.ComponentOwnerRuntimeID)
	now := time.Now().UTC()
	for _, target := range []domain.ComponentRelease{
		{
			ID: "release-test-runtime-0.9.0", ComponentID: "component-test-runtime", Version: "v0.9.0",
			Status: domain.ReleaseDeprecated, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now,
			EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{{Name: "expected_version", Description: "expected runtime version", Type: domain.ParameterTypeString, Required: true, FixedValue: "0.9.0", Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderComponentOwner}},
			Actions: []domain.ActionDefinition{{ID: "action-test-runtime-verify-0.9", ReleaseID: "release-test-runtime-0.9.0", Name: "verify", Kind: domain.ActionVerify, Playbook: "tests/runtime/verify.yml", HostGroup: "test_nodes", TimeoutSeconds: 60}},
		},
		{
			ID: "release-test-runtime-bad-host", ComponentID: "component-test-runtime", Version: "v0.8.0",
			Status: domain.ReleaseReleased, RiskLevel: domain.RiskLow, CreatedAt: now.Add(-time.Second), ReleasedAt: &now,
			EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{{Name: "expected_version", Description: "expected runtime version", Type: domain.ParameterTypeString, Required: true, FixedValue: "0.8.0", Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderComponentOwner}},
			Actions: []domain.ActionDefinition{{ID: "action-test-runtime-verify-bad-host", ReleaseID: "release-test-runtime-bad-host", Name: "verify", Kind: domain.ActionVerify, Playbook: "tests/runtime/verify.yml", HostGroup: "k8smaster", TimeoutSeconds: 60}},
		},
		{
			ID: "release-test-runtime-no-verify", ComponentID: "component-test-runtime", Version: "v0.7.0",
			Status: domain.ReleaseReleased, RiskLevel: domain.RiskLow, CreatedAt: now.Add(-2 * time.Second), ReleasedAt: &now,
			EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, Actions: []domain.ActionDefinition{},
		},
	} {
		if err := f.database.CreateComponentRelease(context.Background(), target); err != nil {
			t.Fatal(err)
		}
	}

	testutil.Workspaces(t, f.database, f.runner.root, "release-test-runtime-0.9.0", "release-test-runtime-bad-host")
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
	if rollbackOnly.Code != http.StatusBadRequest || !strings.Contains(rollbackOnly.Body.String(), "clean-state rollback") {
		t.Fatalf("targeted rollback-only preview status=%d body=%s", rollbackOnly.Code, rollbackOnly.Body.String())
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
		"rollbackVerification": map[string]any{"kind": "target_release", "releaseId": "release-test-runtime-0.9.0"}, "expectedPlanDigest": alternateData["planDigest"],
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

func TestComponentTestPlanReturnsActionableMissingCredentialDiagnosis(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	alice := f.session(seed.ComponentOwnerRuntimeID)
	release, err := f.database.GetComponentRelease(ctx, "release-test-runtime-1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	for index := range release.Actions {
		if release.Actions[index].Kind == domain.ActionUpgrade || release.Actions[index].Kind == domain.ActionVerify {
			release.Actions[index].RequiredCredentials = []string{"K8S_ENCRYPTION_KEY"}
		}
	}
	if err := f.database.UpdateDraftRelease(ctx, release); err != nil {
		t.Fatal(err)
	}

	response := f.request(http.MethodPost, "/api/v1/component-releases/"+release.ID+"/test-plan", map[string]any{
		"environmentId": "environment-test",
		"mode":          "install_verify",
	}, alice)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing credential preview status=%d body=%s", response.Code, response.Body.String())
	}
	errorBody := decodeEnvelope(t, response)["error"].(map[string]any)
	if !strings.Contains(errorBody["message"].(string), "K8S_ENCRYPTION_KEY") {
		t.Fatalf("missing credential name was not preserved: %#v", errorBody)
	}
	explanation := errorBody["explanation"].(map[string]any)
	reason := explanation["reasons"].([]any)[0].(map[string]any)
	action := explanation["primaryAction"].(map[string]any)
	if reason["code"] != "environment.credentials_missing" || action["href"] != "/environments?selected=environment-test&tab=credentials" {
		t.Fatalf("missing credential error is not actionable: %#v", explanation)
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
	f.completeReleaseDelivery("release-test-runtime-1.1.0", alice, dave, true)
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
	evidence := f.request(http.MethodGet, "/api/v1/component-releases/release-test-runtime-1.1.0/run-evidence", nil, alice)
	if evidence.Code != http.StatusOK {
		t.Fatalf("component release run evidence status=%d body=%s", evidence.Code, evidence.Body.String())
	}
	if strings.Contains(evidence.Body.String(), "inputSnapshot") || strings.Contains(evidence.Body.String(), "input_snapshot") {
		t.Fatalf("release run evidence exposed raw input snapshot: %s", evidence.Body.String())
	}
	foundScenarioEvidence := false
	for _, raw := range decodeEnvelope(t, evidence)["items"].([]any) {
		item := raw.(map[string]any)
		if item["id"] != runID {
			continue
		}
		foundScenarioEvidence = item["scenarioName"] != "" && item["environmentName"] != ""
	}
	if !foundScenarioEvidence {
		t.Fatalf("scenario/environment metadata missing from release evidence: %s", evidence.Body.String())
	}
	for label, viewer := range map[string]*http.Cookie{
		"unrelated component owner": f.session(seed.ComponentOwnerK8sID),
		"scenario owner":            carol,
		"environment owner":         dave,
	} {
		response := f.request(http.MethodGet, "/api/v1/component-releases/release-test-runtime-1.1.0/run-evidence", nil, viewer)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s release evidence status=%d body=%s", label, response.Code, response.Body.String())
		}
	}
	waitForRun(t, f.database, runID, domain.RunSucceeded)
	if response := f.request(http.MethodPost, "/api/v1/scenario-revisions/scenario-test-runtime-r2/publish", nil, carol); response.Code != http.StatusOK {
		t.Fatalf("tested scenario publish status=%d body=%s", response.Code, response.Body.String())
	}
	openFuyaoFacts := completeTestEnvironmentFacts()
	openFuyaoFacts["operatingSystem"] = "Kylin"
	if response := f.request(http.MethodPut, "/api/v1/environments/environment-openfuyao-template/facts", map[string]any{"facts": openFuyaoFacts, "changeReason": "补齐当前必填事实"}, dave); response.Code != http.StatusOK {
		t.Fatalf("complete OpenFuyao facts status=%d body=%s", response.Code, response.Body.String())
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
	dave := f.session(seed.EnvironmentOwnerID)

	scenario := f.request(http.MethodGet, "/api/v1/scenarios/scenario-k8s-1.17.5", nil, carol)
	if scenario.Code != http.StatusOK {
		t.Fatalf("get seeded Kubernetes 1.17.5 scenario status=%d body=%s", scenario.Code, scenario.Body.String())
	}
	scenarioData := decodeEnvelope(t, scenario)["data"].(map[string]any)
	if scenarioData["currentRevisionId"] != "scenario-k8s-1.17.5-r1" {
		t.Fatalf("unexpected seeded Kubernetes 1.17.5 revision: %#v", scenarioData)
	}
	currentRevision, ok := scenarioData["currentRevision"].(map[string]any)
	if !ok || len(currentRevision["nodes"].([]any)) != 21 || len(currentRevision["edges"].([]any)) != 48 {
		t.Fatalf("unexpected Kubernetes 1.17.5 DAG: %#v", currentRevision)
	}

	f.runner.mu.Lock()
	beforeCalls, beforeRequests := len(f.runner.calls), len(f.runner.requests)
	f.runner.mu.Unlock()
	kubernetesFacts := completeTestEnvironmentFacts()
	kubernetesFacts["operatingSystem"] = "SUSE"
	factsResponse := f.request(http.MethodPut, "/api/v1/environments/environment-k8s-1.17.5-template/facts", map[string]any{"facts": kubernetesFacts, "changeReason": "补齐当前必填事实"}, dave)
	if factsResponse.Code != http.StatusOK {
		t.Fatalf("complete Kubernetes 1.17.5 facts status=%d body=%s", factsResponse.Code, factsResponse.Body.String())
	}
	lockedEnvironmentRevisionID := decodeEnvelope(t, factsResponse)["data"].(map[string]any)["currentRevisionId"].(string)
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
	if runData["scenarioRevisionId"] != "scenario-k8s-1.17.5-r1" || runData["environmentRevisionId"] != lockedEnvironmentRevisionID {
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
		if !ok || !strings.HasPrefix(playbook, "managed/fixtures/") || limit == "" {
			t.Fatalf("locked step %d=%#v", index, rawStep)
		}
		if strings.Contains(strings.ToLower(playbook), "recovery") || strings.Contains(strings.ToLower(playbook), "housekeeping") || strings.Contains(strings.ToLower(playbook), "uninstall") {
			t.Fatalf("locked step %d references a forbidden lifecycle path: %s", index, playbook)
		}
		if limitsByPlaybook[fmt.Sprint(step["componentId"])] == nil {
			limitsByPlaybook[fmt.Sprint(step["componentId"])] = map[string]bool{}
		}
		limitsByPlaybook[fmt.Sprint(step["componentId"])][limit] = true
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
	for _, componentID := range []string{"component-docker", "component-kubernetes-distribution", "component-flannel", "component-kubelet", "component-kube-proxy"} {
		if !limitsByPlaybook[componentID]["k8smaster"] || !limitsByPlaybook[componentID]["k8snode"] || len(limitsByPlaybook[componentID]) != 2 {
			t.Fatalf("component branches for %s: %v", componentID, limitsByPlaybook[componentID])
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

func TestPlatformAdminManagesDynamicOptionDirectoryAndCannotWriteOwnerResources(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.session(seed.PlatformAdminID)
	alice := f.session(seed.ComponentOwnerRuntimeID)

	listed := f.request(http.MethodGet, "/api/v1/platform-option-categories", nil, alice)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"key":"hostGroup"`) || !strings.Contains(listed.Body.String(), `"key":"architecture"`) {
		t.Fatalf("option directory list status=%d body=%s", listed.Code, listed.Body.String())
	}
	if denied := f.request(http.MethodPost, "/api/v1/platform-option-categories", map[string]any{"label": "机房"}, alice); denied.Code != http.StatusForbidden {
		t.Fatalf("owner created option category status=%d body=%s", denied.Code, denied.Body.String())
	}
	created := f.request(http.MethodPost, "/api/v1/platform-option-categories", map[string]any{"label": "机房"}, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("admin create category status=%d body=%s", created.Code, created.Body.String())
	}
	category := decodeEnvelope(t, created)["data"].(map[string]any)
	categoryID, categoryKey := category["id"].(string), category["key"].(string)
	if !strings.HasPrefix(categoryKey, "dimension_") || category["environmentRequired"] != false || category["kind"] != "environment_dimension" {
		t.Fatalf("created category=%#v", category)
	}
	createdOption := f.request(http.MethodPost, "/api/v1/platform-option-categories/"+categoryID+"/options", map[string]any{"label": "上海一号"}, admin)
	if createdOption.Code != http.StatusCreated {
		t.Fatalf("admin create option status=%d body=%s", createdOption.Code, createdOption.Body.String())
	}
	option := decodeEnvelope(t, createdOption)["data"].(map[string]any)
	if !strings.HasPrefix(option["value"].(string), "option_") {
		t.Fatalf("created option=%#v", option)
	}
	deleted := f.request(http.MethodDelete, "/api/v1/platform-option-categories/"+categoryID, nil, admin)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete empty category status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	var cascaded int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM platform_options WHERE category_id=?`, categoryID).Scan(&cascaded); err != nil || cascaded != 0 {
		t.Fatalf("category cascade options=%d err=%v", cascaded, err)
	}
	referencedCategory := f.request(http.MethodPost, "/api/v1/platform-option-categories", map[string]any{"label": "受引用维度"}, admin)
	referencedCategoryData := decodeEnvelope(t, referencedCategory)["data"].(map[string]any)
	referencedCategoryID, referencedCategoryKey := referencedCategoryData["id"].(string), referencedCategoryData["key"].(string)
	referencedOption := f.request(http.MethodPost, "/api/v1/platform-option-categories/"+referencedCategoryID+"/options", map[string]any{"label": "被组件引用"}, admin)
	referencedOptionData := decodeEnvelope(t, referencedOption)["data"].(map[string]any)
	referencedOptionID, referencedOptionValue := referencedOptionData["id"].(string), referencedOptionData["value"].(string)
	constraints, _ := json.Marshal(map[string]any{referencedCategoryKey: []string{referencedOptionValue}})
	if _, err := f.database.DB().Exec(`UPDATE component_releases SET environment_constraints_json=? WHERE id='release-test-runtime-1.0.0'`, string(constraints)); err != nil {
		t.Fatal(err)
	}
	if denied := f.request(http.MethodPatch, "/api/v1/platform-option-categories/"+referencedCategoryID, map[string]any{"label": "无权更名"}, alice); denied.Code != http.StatusForbidden {
		t.Fatalf("owner renamed option category status=%d body=%s", denied.Code, denied.Body.String())
	}
	renamedCategory := f.request(http.MethodPatch, "/api/v1/platform-option-categories/"+referencedCategoryID, map[string]any{"label": "已更名维度"}, admin)
	renamedCategoryData := decodeEnvelope(t, renamedCategory)["data"].(map[string]any)
	if renamedCategory.Code != http.StatusOK || renamedCategoryData["label"] != "已更名维度" || renamedCategoryData["key"] != referencedCategoryKey {
		t.Fatalf("rename referenced category status=%d body=%s", renamedCategory.Code, renamedCategory.Body.String())
	}
	renamedOption := f.request(http.MethodPatch, "/api/v1/platform-options/"+referencedOptionID, map[string]any{"label": "已更名选项"}, admin)
	renamedOptionData := decodeEnvelope(t, renamedOption)["data"].(map[string]any)
	if renamedOption.Code != http.StatusOK || renamedOptionData["label"] != "已更名选项" || renamedOptionData["value"] != referencedOptionValue {
		t.Fatalf("rename referenced option status=%d body=%s", renamedOption.Code, renamedOption.Body.String())
	}
	var storedConstraints string
	if err := f.database.DB().QueryRow(`SELECT environment_constraints_json FROM component_releases WHERE id='release-test-runtime-1.0.0'`).Scan(&storedConstraints); err != nil || storedConstraints != string(constraints) {
		t.Fatalf("rename changed technical references constraints=%s err=%v", storedConstraints, err)
	}
	referencedDelete := f.request(http.MethodDelete, "/api/v1/platform-option-categories/"+referencedCategoryID, nil, admin)
	if referencedDelete.Code != http.StatusConflict || !strings.Contains(referencedDelete.Body.String(), "platform_option_category.in_use") || !strings.Contains(referencedDelete.Body.String(), `"componentReleases":1`) {
		t.Fatalf("referenced category delete status=%d body=%s", referencedDelete.Code, referencedDelete.Body.String())
	}

	categories := decodeEnvelope(t, f.request(http.MethodGet, "/api/v1/platform-option-categories", nil, admin))["items"].([]any)
	var hostCategoryID, testNodesID string
	for _, raw := range categories {
		item := raw.(map[string]any)
		if item["key"] != "hostGroup" {
			continue
		}
		hostCategoryID = item["id"].(string)
		for _, rawOption := range item["options"].([]any) {
			candidate := rawOption.(map[string]any)
			if candidate["value"] == "test_nodes" {
				testNodesID = candidate["id"].(string)
			}
		}
	}
	protected := f.request(http.MethodDelete, "/api/v1/platform-option-categories/"+hostCategoryID, nil, admin)
	if protected.Code != http.StatusConflict || !strings.Contains(protected.Body.String(), "platform_option_category.protected") {
		t.Fatalf("protected host category status=%d body=%s", protected.Code, protected.Body.String())
	}
	inUse := f.request(http.MethodDelete, "/api/v1/platform-options/"+testNodesID, nil, admin)
	if inUse.Code != http.StatusConflict || !strings.Contains(inUse.Body.String(), "platform_option.in_use") || !strings.Contains(inUse.Body.String(), "componentReleases") {
		t.Fatalf("in-use host option status=%d body=%s", inUse.Code, inUse.Body.String())
	}
	if forbidden := f.request(http.MethodPost, "/api/v1/components", componentRequest("Admin forbidden", "admin-forbidden"), admin); forbidden.Code != http.StatusForbidden {
		t.Fatalf("platform admin wrote owner resource status=%d body=%s", forbidden.Code, forbidden.Body.String())
	}
	adminComponents := f.request(http.MethodGet, "/api/v1/components", nil, admin)
	if adminComponents.Code != http.StatusOK || !strings.Contains(adminComponents.Body.String(), "release-test-runtime-1.1.0") {
		t.Fatalf("platform admin global component read status=%d body=%s", adminComponents.Code, adminComponents.Body.String())
	}
	adminScenarios := f.request(http.MethodGet, "/api/v1/scenarios", nil, admin)
	if adminScenarios.Code != http.StatusOK || !strings.Contains(adminScenarios.Body.String(), "scenario-test-runtime-r2") {
		t.Fatalf("platform admin global scenario read status=%d body=%s", adminScenarios.Code, adminScenarios.Body.String())
	}
	adminEnvironments := f.request(http.MethodGet, "/api/v1/environments?includeArchived=true", nil, admin)
	if adminEnvironments.Code != http.StatusOK || !strings.Contains(adminEnvironments.Body.String(), "environment-test") {
		t.Fatalf("platform admin global environment read status=%d body=%s", adminEnvironments.Code, adminEnvironments.Body.String())
	}
	var directoryAudits int
	if err := f.database.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE actor_id=? AND resource_type IN ('platform_option_category','platform_option')`, seed.PlatformAdminID).Scan(&directoryAudits); err != nil || directoryAudits < 5 {
		t.Fatalf("platform directory audits=%d err=%v", directoryAudits, err)
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
	t.Fatalf("run %s status=%s error=%q err=%v, want %s", runID, run.Status, run.Error, err, status)
}

func waitForImageBuild(t *testing.T, database *store.Store, buildID string, status domain.ImageBuildStatus) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		build, err := database.GetComponentImageBuild(context.Background(), buildID)
		if err == nil && build.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	build, err := database.GetComponentImageBuild(context.Background(), buildID)
	t.Fatalf("image build %s status=%s err=%v, want %s", buildID, build.Status, err, status)
}

func (f *apiFixture) configurePlaybookRoot(root string) {
	f.t.Helper()
	previous := f.runner.root
	if previous != root {
		err := filepath.WalkDir(previous, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(previous, path)
			if err != nil {
				return err
			}
			target := filepath.Join(root, relative)
			if entry.IsDir() {
				return os.MkdirAll(target, 0700)
			}
			if _, err := os.Stat(target); err == nil {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, content, 0600)
		})
		if err != nil {
			f.t.Fatal(err)
		}
	}
	f.runner.root = root
	f.platform.ConfigurePlaybookRoot(root)
}
