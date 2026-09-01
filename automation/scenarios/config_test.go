package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigDisabledByDefault(t *testing.T) {
	t.Setenv("CLUSTERFORGE_REAL_E2E", "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("real environment automation must be disabled by default")
	}
}

func TestLoadConfigRequiresExternalAbsolutePath(t *testing.T) {
	t.Setenv("CLUSTERFORGE_REAL_E2E", "1")
	t.Setenv("CLUSTERFORGE_REAL_E2E_CONFIG", "")
	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("error=%v, want external absolute path rejection", err)
	}
}

func TestLoadConfigRejectsRepositoryPath(t *testing.T) {
	t.Setenv("CLUSTERFORGE_REAL_E2E", "1")
	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLUSTERFORGE_REAL_E2E_CONFIG", filepath.Join(projectRoot, "automation", "scenarios", "README.md"))
	_, err = loadConfig()
	if err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Fatalf("error=%v, want repository path rejection", err)
	}
}

func TestLoadConfigRequiresExactEnvironmentConfirmation(t *testing.T) {
	setEnabledConfig(t, validFixtureConfig())
	t.Setenv("CLUSTERFORGE_REAL_E2E_CONFIRM", "wrong environment")
	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "must exactly equal") {
		t.Fatalf("error=%v, want exact confirmation rejection", err)
	}
}

func TestLoadConfigRejectsURLQueryOrCredentials(t *testing.T) {
	for _, baseURL := range []string{
		"http://platform.invalid:8080?target=other",
		"http://user@platform.invalid:8080",
	} {
		t.Run(baseURL, func(t *testing.T) {
			fixture := validFixtureConfig()
			fixture.BaseURL = baseURL
			setEnabledConfig(t, fixture)
			if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "without credentials, query, or fragment") {
				t.Fatalf("error=%v, want unsafe URL rejection", err)
			}
		})
	}
}

func TestLoadConfigRequiresDeliveryResetForFullRun(t *testing.T) {
	setEnabledConfig(t, validFixtureConfig())
	t.Setenv("CLUSTERFORGE_REAL_E2E_RESET_DELIVERY", "")
	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "requires CLUSTERFORGE_REAL_E2E_RESET_DELIVERY=1") {
		t.Fatalf("error=%v, want reset confirmation rejection", err)
	}
}

func TestLoadConfigAllowsReadOnlyPreflightWithoutReset(t *testing.T) {
	fixture := validFixtureConfig()
	setEnabledConfig(t, fixture)
	t.Setenv("CLUSTERFORGE_REAL_E2E_PREFLIGHT_ONLY", "1")
	t.Setenv("CLUSTERFORGE_REAL_E2E_RESET_DELIVERY", "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PreflightOnly || cfg.ResetDelivery || cfg.Fixture.EnvironmentID != fixture.EnvironmentID {
		t.Fatalf("config=%+v", cfg)
	}
	if !strings.HasPrefix(cfg.OutputDir, "/") || !strings.Contains(cfg.OutputDir, "/output/real-scenario-e2e/") {
		t.Fatalf("output directory must resolve from the project root: %q", cfg.OutputDir)
	}
}

func TestRequireDeliveryChecksModeAndStatus(t *testing.T) {
	run := runDTO{ID: "run-test", DeliveryResults: []deliveryResult{
		{RequirementID: "artifact", Mode: "transfer", Status: "transferred"},
		{RequirementID: "image", Mode: "transfer", Status: "transferred"},
	}}
	if err := requireDelivery(run, "transferred", 2); err != nil {
		t.Fatal(err)
	}
	run.DeliveryResults[0].Mode = "direct"
	if err := requireDelivery(run, "transferred", 2); err == nil {
		t.Fatal("delivery assertion accepted a wrong decision mode")
	}
}

func TestDestructiveCleanupRequiresLockPreflightAndCreatedRun(t *testing.T) {
	h := &harness{cfg: config{Fixture: fixtureConfig{EnvironmentID: "environment-test"}}, createdRuns: map[string]*apiClient{}}
	if h.destructiveCleanupAllowed() {
		t.Fatal("cleanup was allowed before lock acquisition and preflight")
	}
	h.lockHeld = true
	if h.destructiveCleanupAllowed() {
		t.Fatal("cleanup was allowed before clean preflight")
	}
	h.cleanupArmed = true
	if h.destructiveCleanupAllowed() {
		t.Fatal("cleanup was allowed before this test created a Run")
	}
	if err := h.trackRun(runDTO{ID: "run-owned", EnvironmentID: "environment-test"}, nil); err != nil {
		t.Fatal(err)
	}
	if !h.destructiveCleanupAllowed() {
		t.Fatal("cleanup was not allowed after lock, preflight, and owned Run")
	}
}

func TestValidateRollbackSourcesRejectsForeignInstallations(t *testing.T) {
	h := &harness{createdRuns: map[string]*apiClient{"run-owned": nil}}
	if err := h.validateRollbackSources(nil); err == nil {
		t.Fatal("empty rollback sources were accepted")
	}
	if err := h.validateRollbackSources([]rollbackSource{{RunID: "run-owned"}, {RunID: "run-foreign"}}); err == nil || !strings.Contains(err.Error(), "run-foreign") {
		t.Fatalf("foreign rollback source error=%v", err)
	}
	if err := h.validateRollbackSources([]rollbackSource{{RunID: "run-owned"}}); err != nil {
		t.Fatalf("owned rollback source rejected: %v", err)
	}
}

func TestValidateScenarioRollbackOrder(t *testing.T) {
	fixture := validFixtureConfig()
	node := func(id, releaseID, hostGroup string) any {
		return map[string]any{"id": id, "data": map[string]any{"releaseId": releaseID, "hostGroup": hostGroup}}
	}
	edge := func(source, target string) any {
		return map[string]any{"source": source, "target": target}
	}
	revision := scenarioRevision{
		Nodes: []any{
			node("second-control", fixture.RollbackSecondReleaseID, "control"), node("first-control", fixture.RollbackFirstReleaseID, "control"),
			node("second-worker", fixture.RollbackSecondReleaseID, "worker"), node("first-worker", fixture.RollbackFirstReleaseID, "worker"),
		},
		Edges: []any{edge("second-control", "first-control"), edge("second-worker", "first-worker")},
	}
	if err := validateScenarioRollbackOrder(revision, fixture); err != nil {
		t.Fatal(err)
	}
	revision.Edges = []any{edge("first-control", "second-control"), edge("first-worker", "second-worker")}
	if err := validateScenarioRollbackOrder(revision, fixture); err == nil || !strings.Contains(err.Error(), "rollback order is unsafe") {
		t.Fatalf("error=%v, want unsafe rollback rejection", err)
	}
}

func TestRollbackPostconditionScriptUsesOnlyConfiguredChecks(t *testing.T) {
	fixture := validFixtureConfig()
	script := rollbackPostconditionScript(fixture)
	for _, expected := range []string{
		"test ! -e '/var/lib/test-suite/state'",
		"! ip link show 'test0'",
		"! systemctl is-active --quiet 'test-agent'",
		"--filter 'label=example.test/suite=fixture'",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("script=%q, missing %q", script, expected)
		}
	}
}

func setEnabledConfig(t *testing.T, fixture fixtureConfig) {
	t.Helper()
	payload, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "real-scenario.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLUSTERFORGE_REAL_E2E", "1")
	t.Setenv("CLUSTERFORGE_REAL_E2E_CONFIG", path)
	t.Setenv("CLUSTERFORGE_REAL_E2E_CONFIRM", fixture.EnvironmentName)
	t.Setenv("CLUSTERFORGE_REAL_E2E_RESET_DELIVERY", "1")
	t.Setenv("CLUSTERFORGE_REAL_E2E_PREFLIGHT_ONLY", "")
}

func validFixtureConfig() fixtureConfig {
	return fixtureConfig{
		BaseURL: "http://platform.invalid:8080", EnvironmentID: "environment-test", EnvironmentName: "Acceptance Environment",
		ComponentUserID: "component-owner", ScenarioUserID: "scenario-owner", EnvironmentUserID: "environment-owner",
		PlatformSSHHost: "operator@platform.invalid", FSSSSHHost: "operator@files.invalid", NodeSSHUser: "operator",
		FileStationEndpoint: "files.invalid:8080", ImageRegistryEndpoint: "registry.invalid:5000",
		ExpectedHostCount: 2, RequiredEnvironmentGroups: []string{"control", "worker"},
		ScenarioRevisionID: "scenario-revision-test", ScenarioDefinitionSHA256: strings.Repeat("1", 64),
		ScenarioNodeCount: 2, ScenarioEdgeCount: 1, ScenarioInstallStepCount: 4, ScenarioRollbackStepCount: 2,
		RollbackFirstReleaseID: "release-dependent", RollbackSecondReleaseID: "release-prerequisite", RollbackDependencyGroups: []string{"control", "worker"},
		RollbackAbsentPaths: []string{"/var/lib/test-suite/state"}, RollbackInactiveServices: []string{"test-agent"},
		RollbackAbsentInterfaces: []string{"test0"}, RollbackAbsentDockerLabels: []string{"example.test/suite=fixture"},
		ControlledComponentID: "component-controlled", ControlledReleaseID: "release-controlled", ControlledComponentSlug: "controlled-fixture",
		ControlledVersion: "v1", ControlledPlanDigest: strings.Repeat("2", 64), ControlledLimit: "control", ControlledFailureTag: "fail-once",
		ControlledInstallTimeout: 120, ControlledCleanupTimeout: 60,
		DeliveryComponentID: "component-delivery", DeliveryReleaseID: "release-delivery", DeliveryComponentSlug: "delivery-fixture", DeliveryVersion: "v1",
		DeliveryArtifactAlias: "payload", DeliveryArtifactFilename: "payload.bin", DeliverySeedAlias: "seed", DeliverySeedFilename: "seed.bin", DeliveryImageLogicalName: "main",
		ArtifactSHA256: strings.Repeat("3", 64), ArtifactSourceURL: "http://files.invalid:8080/components/source.bin",
		ArtifactTargetURL: "http://files.invalid:8080/components/target.bin", ArtifactTargetRoot: "/var/lib/test-suite/artifacts", ArtifactTargetPath: "/var/lib/test-suite/artifacts/target.bin",
		ImageSourceRef: "registry-source.invalid:5000/components/image:v1", ImageDigest: "sha256:" + strings.Repeat("4", 64),
		ImageTargetRegistry: "http://registry.invalid:5000", ImageTargetRepository: "components/target/main",
	}
}
