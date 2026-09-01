package scenarios

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

type harness struct {
	cfg             config
	component       *apiClient
	scenario        *apiClient
	environment     *apiClient
	report          automationReport
	lockHeld        bool
	lockToken       string
	cleanupArmed    bool
	mutationStarted bool
	createdRuns     map[string]*apiClient
}

func newHarness(ctx context.Context, cfg config) (*harness, error) {
	lockToken, err := newLockToken()
	if err != nil {
		return nil, err
	}
	component, err := newAPIClient(cfg.Fixture.BaseURL)
	if err != nil {
		return nil, err
	}
	scenario, err := newAPIClient(cfg.Fixture.BaseURL)
	if err != nil {
		return nil, err
	}
	environment, err := newAPIClient(cfg.Fixture.BaseURL)
	if err != nil {
		return nil, err
	}
	for client, userID := range map[*apiClient]string{
		component: cfg.Fixture.ComponentUserID, scenario: cfg.Fixture.ScenarioUserID, environment: cfg.Fixture.EnvironmentUserID,
	} {
		if err := client.switchIdentity(ctx, userID); err != nil {
			return nil, err
		}
	}
	return &harness{
		cfg: cfg, component: component, scenario: scenario, environment: environment,
		lockToken: lockToken,
		report: automationReport{
			StartedAt: time.Now().UTC(), BaseURL: cfg.Fixture.BaseURL, EnvironmentID: cfg.Fixture.EnvironmentID,
			Checks: []string{}, Runs: []runRecord{},
		},
		createdRuns: make(map[string]*apiClient),
	}, nil
}

func newLockToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate real-environment lock token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func (h *harness) preflight(ctx context.Context) error {
	var environments []environmentDTO
	if err := h.environment.items(ctx, "/api/v1/environments", &environments); err != nil {
		return err
	}
	var selected *environmentDTO
	for index := range environments {
		if environments[index].ID == h.cfg.Fixture.EnvironmentID {
			selected = &environments[index]
			break
		}
	}
	if selected == nil || selected.Name != h.cfg.Fixture.EnvironmentName || selected.ArchivedAt != nil {
		return fmt.Errorf("expected active environment %s (%s)", h.cfg.Fixture.EnvironmentName, h.cfg.Fixture.EnvironmentID)
	}
	if len(selected.CurrentRevision.Hosts) != h.cfg.Fixture.ExpectedHostCount {
		return fmt.Errorf("environment host count=%d, want %d", len(selected.CurrentRevision.Hosts), h.cfg.Fixture.ExpectedHostCount)
	}
	if selected.CurrentRevision.Variables["FILE_STATION"] != h.cfg.Fixture.FileStationEndpoint || selected.CurrentRevision.Variables["IMAGE_REGISTRY"] != h.cfg.Fixture.ImageRegistryEndpoint {
		return fmt.Errorf("environment delivery endpoints drifted: %#v", selected.CurrentRevision.Variables)
	}
	requiredGroups := make(map[string]bool, len(h.cfg.Fixture.RequiredEnvironmentGroups))
	for _, group := range h.cfg.Fixture.RequiredEnvironmentGroups {
		requiredGroups[group] = false
	}
	for _, host := range selected.CurrentRevision.Hosts {
		for group := range requiredGroups {
			if slices.Contains(host.Groups, group) {
				requiredGroups[group] = true
			}
		}
	}
	for group, present := range requiredGroups {
		if !present {
			return fmt.Errorf("environment is missing required host group %q", group)
		}
	}
	if err := h.requireCleanLifecycle(ctx); err != nil {
		return err
	}
	if err := h.validateScenarioFixture(ctx); err != nil {
		return err
	}
	controlled, err := h.validateComponentFixture(ctx, h.cfg.Fixture.ControlledComponentID, h.cfg.Fixture.ControlledReleaseID, h.cfg.Fixture.ControlledComponentSlug)
	if err != nil {
		return err
	}
	if err := h.validateControlledFixture(controlled); err != nil {
		return err
	}
	if err := h.validateControlledPlan(ctx); err != nil {
		return err
	}
	delivery, err := h.validateComponentFixture(ctx, h.cfg.Fixture.DeliveryComponentID, h.cfg.Fixture.DeliveryReleaseID, h.cfg.Fixture.DeliveryComponentSlug)
	if err != nil {
		return err
	}
	if err := h.validateDeliveryFixture(delivery); err != nil {
		return err
	}
	if err := h.checkFSSCapability(ctx); err != nil {
		return err
	}
	if err := h.checkRegistryDeleteCapability(ctx); err != nil {
		return err
	}
	h.report.Checks = append(h.report.Checks, "environment contract", "fixture contracts", "FSS fetch route", "Registry scoped delete")
	return nil
}

func (h *harness) requireCleanLifecycle(ctx context.Context) error {
	var lifecycle lifecycleDTO
	if err := h.environment.data(ctx, http.MethodGet, "/api/v1/environments/"+h.cfg.Fixture.EnvironmentID+"/lifecycle", nil, &lifecycle); err != nil {
		return err
	}
	if lifecycle.Archived || lifecycle.ActiveRunCount != 0 || lifecycle.ActiveImageBuildCount != 0 || lifecycle.InstallationCount != 0 {
		return fmt.Errorf("environment is not clean: activeRuns=%d activeBuilds=%d installations=%d archived=%v", lifecycle.ActiveRunCount, lifecycle.ActiveImageBuildCount, lifecycle.InstallationCount, lifecycle.Archived)
	}
	return nil
}

func (h *harness) verifyRollbackPostconditions(ctx context.Context) error {
	var environments []environmentDTO
	if err := h.environment.items(ctx, "/api/v1/environments", &environments); err != nil {
		return err
	}
	var selected *environmentDTO
	for index := range environments {
		if environments[index].ID == h.cfg.Fixture.EnvironmentID {
			selected = &environments[index]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("environment %s disappeared before rollback postcondition checks", h.cfg.Fixture.EnvironmentID)
	}
	script := rollbackPostconditionScript(h.cfg.Fixture)
	for _, host := range selected.CurrentRevision.Hosts {
		target := h.cfg.Fixture.NodeSSHUser + "@" + host.Address
		command := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", target, "bash", "-s")
		command.Stdin = strings.NewReader(script)
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("rollback postconditions failed on %s (%s): %w: %s", host.Name, host.Address, err, strings.TrimSpace(string(output)))
		}
	}
	h.report.Checks = append(h.report.Checks, "host rollback postconditions")
	return nil
}

func rollbackPostconditionScript(fixture fixtureConfig) string {
	var script strings.Builder
	script.WriteString("set -eu\n")
	for _, path := range fixture.RollbackAbsentPaths {
		fmt.Fprintf(&script, "test ! -e %s\n", shellQuote(path))
	}
	for _, name := range fixture.RollbackAbsentInterfaces {
		fmt.Fprintf(&script, "! ip link show %s >/dev/null 2>&1\n", shellQuote(name))
	}
	for _, name := range fixture.RollbackInactiveServices {
		fmt.Fprintf(&script, "! systemctl is-active --quiet %s\n", shellQuote(name))
	}
	for _, label := range fixture.RollbackAbsentDockerLabels {
		fmt.Fprintf(&script, "test -z \"$(docker ps -aq --filter %s)\"\n", shellQuote("label="+label))
	}
	return script.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (h *harness) validateScenarioFixture(ctx context.Context) error {
	var scenarios []scenarioDTO
	if err := h.scenario.items(ctx, "/api/v1/scenarios", &scenarios); err != nil {
		return err
	}
	for _, scenario := range scenarios {
		if scenario.CurrentRevisionID != h.cfg.Fixture.ScenarioRevisionID {
			continue
		}
		if len(scenario.CurrentRevision.Nodes) != h.cfg.Fixture.ScenarioNodeCount || len(scenario.CurrentRevision.Edges) != h.cfg.Fixture.ScenarioEdgeCount {
			return fmt.Errorf("scenario fixture topology=%d nodes/%d edges, want %d/%d", len(scenario.CurrentRevision.Nodes), len(scenario.CurrentRevision.Edges), h.cfg.Fixture.ScenarioNodeCount, h.cfg.Fixture.ScenarioEdgeCount)
		}
		if scenario.CurrentRevision.State != "test_passed" {
			return fmt.Errorf("scenario fixture state=%q, want test_passed", scenario.CurrentRevision.State)
		}
		if err := validateScenarioRollbackOrder(scenario.CurrentRevision, h.cfg.Fixture); err != nil {
			return err
		}
		definition, err := json.Marshal(map[string]any{
			"nodes": scenario.CurrentRevision.Nodes, "edges": scenario.CurrentRevision.Edges,
			"executionPolicy": scenario.CurrentRevision.ExecutionPolicy,
		})
		if err != nil {
			return err
		}
		if observed := fmt.Sprintf("%x", sha256.Sum256(definition)); observed != h.cfg.Fixture.ScenarioDefinitionSHA256 {
			return fmt.Errorf("scenario fixture definition SHA-256=%s, want %s", observed, h.cfg.Fixture.ScenarioDefinitionSHA256)
		}
		return nil
	}
	return fmt.Errorf("scenario fixture revision %s is not current", h.cfg.Fixture.ScenarioRevisionID)
}

func validateScenarioRollbackOrder(revision scenarioRevision, fixture fixtureConfig) error {
	type releaseNodes map[string]string
	nodes := map[string]releaseNodes{fixture.RollbackFirstReleaseID: {}, fixture.RollbackSecondReleaseID: {}}
	for _, raw := range revision.Nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		data, ok := node["data"].(map[string]any)
		if !ok {
			continue
		}
		releaseID, _ := data["releaseId"].(string)
		hostGroup, _ := data["hostGroup"].(string)
		id, _ := node["id"].(string)
		if releaseNodes, tracked := nodes[releaseID]; tracked && hostGroup != "" && id != "" {
			releaseNodes[hostGroup] = id
		}
	}
	edges := make(map[string]bool, len(revision.Edges))
	for _, raw := range revision.Edges {
		edge, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		source, _ := edge["source"].(string)
		target, _ := edge["target"].(string)
		edges[source+"->"+target] = true
	}
	for _, hostGroup := range fixture.RollbackDependencyGroups {
		rollbackFirst := nodes[fixture.RollbackFirstReleaseID][hostGroup]
		rollbackSecond := nodes[fixture.RollbackSecondReleaseID][hostGroup]
		if rollbackFirst == "" || rollbackSecond == "" {
			return fmt.Errorf("scenario rollback safety requires both configured release nodes for %s", hostGroup)
		}
		if edges[rollbackFirst+"->"+rollbackSecond] {
			return fmt.Errorf("scenario rollback order is unsafe for %s: the configured rollback-first release depends on the rollback-second release in the wrong direction", hostGroup)
		}
		if !edges[rollbackSecond+"->"+rollbackFirst] {
			return fmt.Errorf("scenario rollback safety requires the configured rollback-second -> rollback-first install dependency for %s", hostGroup)
		}
	}
	return nil
}

func (h *harness) validateComponentFixture(ctx context.Context, componentID, releaseID, slug string) (releaseDTO, error) {
	var component componentDTO
	if err := h.component.data(ctx, http.MethodGet, "/api/v1/components/"+componentID, nil, &component); err != nil {
		return releaseDTO{}, err
	}
	if component.Slug != slug {
		return releaseDTO{}, fmt.Errorf("component %s slug=%q, want %q", componentID, component.Slug, slug)
	}
	for _, release := range component.Releases {
		if release.ID == releaseID && release.Status == "draft" {
			return release, nil
		}
	}
	return releaseDTO{}, fmt.Errorf("component %s is missing expected draft %s", componentID, releaseID)
}

func (h *harness) validateControlledFixture(release releaseDTO) error {
	fixture := h.cfg.Fixture
	if release.Version != fixture.ControlledVersion || len(release.Actions) != 3 || len(release.Artifacts) != 0 || len(release.Images) != 0 {
		return fmt.Errorf("controlled fixture release contract drifted")
	}
	actions := make(map[string]actionDTO, len(release.Actions))
	for _, action := range release.Actions {
		actions[action.Kind] = action
	}
	install := actions["install"]
	rollback := actions["rollback"]
	verify := actions["verify"]
	if install.Limit != fixture.ControlledLimit || install.HostGroup != fixture.ControlledLimit || !install.Destructive || !slices.Contains(install.Tags, fixture.ControlledFailureTag) || install.TimeoutSeconds != fixture.ControlledInstallTimeout {
		return fmt.Errorf("controlled fixture install Action drifted: %+v", install)
	}
	if rollback.Limit != fixture.ControlledLimit || !rollback.Destructive || rollback.TimeoutSeconds != fixture.ControlledCleanupTimeout || verify.Limit != fixture.ControlledLimit || verify.TimeoutSeconds != fixture.ControlledCleanupTimeout {
		return fmt.Errorf("controlled fixture rollback/verify Actions drifted")
	}
	return nil
}

func (h *harness) validateControlledPlan(ctx context.Context) error {
	plan, _, err := h.previewComponent(ctx, h.cfg.Fixture.ControlledReleaseID, "install_verify")
	if err != nil {
		return err
	}
	if plan.PlanDigest != h.cfg.Fixture.ControlledPlanDigest || !plan.RequiresApproval || len(plan.Steps) != 2 || len(plan.DeliveryRequirements) != 0 {
		return fmt.Errorf("controlled fixture plan drifted: digest=%s approval=%v steps=%d delivery=%d", plan.PlanDigest, plan.RequiresApproval, len(plan.Steps), len(plan.DeliveryRequirements))
	}
	if plan.Steps[0].Action != "install" || plan.Steps[0].Limit != h.cfg.Fixture.ControlledLimit || !plan.Steps[0].NeedsApproval || plan.Steps[1].Action != "verify" {
		return fmt.Errorf("controlled fixture plan steps drifted: %+v", plan.Steps)
	}
	return nil
}

func (h *harness) validateDeliveryFixture(release releaseDTO) error {
	fixture := h.cfg.Fixture
	if release.Version != fixture.DeliveryVersion || len(release.Actions) != 3 || len(release.Artifacts) != 2 || len(release.Images) != 1 {
		return fmt.Errorf("delivery fixture release contract drifted")
	}
	artifacts := make(map[string]artifactDTO, len(release.Artifacts))
	for _, artifact := range release.Artifacts {
		artifacts[artifact.Alias] = artifact
	}
	payload := artifacts[fixture.DeliveryArtifactAlias]
	seed := artifacts[fixture.DeliverySeedAlias]
	if payload.Filename != fixture.DeliveryArtifactFilename || payload.SHA256 != fixture.ArtifactSHA256 || payload.SourceURL != fixture.ArtifactSourceURL {
		return fmt.Errorf("delivery payload Artifact drifted: %+v", payload)
	}
	if seed.Filename != fixture.DeliverySeedFilename || seed.SHA256 != fixture.ArtifactSHA256 || seed.SourceURL != fixture.ArtifactSourceURL {
		return fmt.Errorf("delivery seed Artifact drifted: %+v", seed)
	}
	image := release.Images[0]
	if image.LogicalName != fixture.DeliveryImageLogicalName || image.Digest != fixture.ImageDigest || image.SourceRef != fixture.ImageSourceRef {
		return fmt.Errorf("delivery Image drifted: %+v", image)
	}
	return nil
}

func (h *harness) checkFSSCapability(ctx context.Context) error {
	parsed, err := url.Parse(h.cfg.Fixture.ArtifactSourceURL)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.Scheme+"://"+parsed.Host+"/api/v1/fetch", strings.NewReader(`{}`))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return fmt.Errorf("FSS lacks /api/v1/fetch; provision a compatible service before running this suite")
	}
	if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusForbidden {
		return fmt.Errorf("unexpected FSS fetch capability status %s", response.Status)
	}
	return nil
}

func (h *harness) checkRegistryDeleteCapability(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodOptions, strings.TrimRight(h.cfg.Fixture.ImageTargetRegistry, "/")+"/v2/"+h.cfg.Fixture.ImageTargetRepository+"/manifests/"+h.cfg.Fixture.ImageDigest, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if !strings.Contains(response.Header.Get("Allow"), "DELETE") {
		return fmt.Errorf("target Registry does not advertise scoped manifest DELETE")
	}
	return nil
}

func (h *harness) acquireLock(ctx context.Context) error {
	const script = `set -eu
lock_path=$1
token=$2
created=0
cleanup_partial() {
  if [ "$created" = 1 ]; then
    rm -f "$lock_path/owner"
    rmdir "$lock_path" 2>/dev/null || true
  fi
}
trap cleanup_partial EXIT
mkdir "$lock_path"
created=1
umask 077
printf '%s\n' "$token" > "$lock_path/owner"
trap - EXIT
`
	output, err := h.runLockScript(ctx, script)
	if err != nil {
		return fmt.Errorf("acquire real-environment lock: %w: %s", err, strings.TrimSpace(string(output)))
	}
	h.lockHeld = true
	return nil
}

func (h *harness) armCleanup() error {
	if !h.lockHeld {
		return fmt.Errorf("cannot arm real-environment cleanup without the owned environment lock")
	}
	h.cleanupArmed = true
	return nil
}

func (h *harness) destructiveCleanupAllowed() bool {
	return h.lockHeld && h.cleanupArmed && h.mutationStarted
}

func (h *harness) verifyLockOwnership(ctx context.Context) error {
	if !h.lockHeld || h.lockToken == "" {
		return fmt.Errorf("real-environment lock is not owned by this test")
	}
	const script = `set -eu
lock_path=$1
token=$2
test -f "$lock_path/owner"
test "$(cat "$lock_path/owner")" = "$token"
`
	if output, err := h.runLockScript(ctx, script); err != nil {
		return fmt.Errorf("verify real-environment lock ownership: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (h *harness) releaseLock(ctx context.Context) error {
	if !h.lockHeld {
		return nil
	}
	const script = `set -eu
lock_path=$1
token=$2
test -f "$lock_path/owner"
test "$(cat "$lock_path/owner")" = "$token"
rm "$lock_path/owner"
rmdir "$lock_path"
`
	output, err := h.runLockScript(ctx, script)
	if err != nil {
		return fmt.Errorf("release real-environment lock: %w: %s", err, strings.TrimSpace(string(output)))
	}
	h.lockHeld = false
	return nil
}

func (h *harness) runLockScript(ctx context.Context, script string) ([]byte, error) {
	command := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", h.cfg.Fixture.PlatformSSHHost, "sh", "-s", "--", h.cfg.remoteLockPath(), h.lockToken)
	command.Stdin = strings.NewReader(script)
	return command.CombinedOutput()
}

func (h *harness) waitRun(ctx context.Context, client *apiClient, runID string, terminal ...string) (runDTO, error) {
	deadline := time.Now().Add(h.cfg.RunTimeout)
	wanted := make(map[string]bool, len(terminal))
	for _, status := range terminal {
		wanted[status] = true
	}
	for {
		var run runDTO
		if err := client.data(ctx, http.MethodGet, "/api/v1/runs/"+runID, nil, &run); err != nil {
			return run, err
		}
		if wanted[run.Status] {
			return run, nil
		}
		if time.Now().After(deadline) {
			return run, fmt.Errorf("run %s timed out in status %s", runID, run.Status)
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-time.After(h.cfg.PollInterval):
		}
	}
}

func (h *harness) approveIfNeeded(ctx context.Context, run runDTO, mode, reason string) error {
	if run.Status != "awaiting_approval" {
		return nil
	}
	if !h.cleanupArmed || !h.lockHeld {
		return fmt.Errorf("run %s cannot be approved before the clean preflight and owned environment lock", run.ID)
	}
	if err := h.verifyLockOwnership(ctx); err != nil {
		return err
	}
	if run.Approval == nil || run.Approval.ID == "" {
		return fmt.Errorf("run %s awaits approval without approval ID", run.ID)
	}
	decisions := make([]map[string]string, 0, len(run.DeliveryRequirements))
	for _, requirement := range run.DeliveryRequirements {
		decisions = append(decisions, map[string]string{"requirementId": requirement.ID, "mode": mode})
	}
	return h.environment.data(ctx, http.MethodPost, "/api/v1/approvals/"+run.Approval.ID+"/approve", map[string]any{"reason": reason, "deliveryDecisions": decisions}, nil)
}

func (h *harness) record(group, purpose string, run runDTO) {
	h.report.Runs = append(h.report.Runs, runRecord{Group: group, Purpose: purpose, ID: run.ID, Status: run.Status, Steps: len(run.Steps), Delivery: run.DeliveryResults})
}

func (h *harness) trackRun(run runDTO, client *apiClient) error {
	if run.ID == "" {
		return fmt.Errorf("created Run response has no ID")
	}
	if run.EnvironmentID != h.cfg.Fixture.EnvironmentID {
		return fmt.Errorf("created Run %s belongs to unexpected environment %s", run.ID, run.EnvironmentID)
	}
	h.createdRuns[run.ID] = client
	h.mutationStarted = true
	return nil
}

func (h *harness) validateRollbackSources(sources []rollbackSource) error {
	if len(sources) == 0 {
		return fmt.Errorf("automatic environment rollback has no installation source Runs")
	}
	for _, source := range sources {
		if source.RunID == "" {
			return fmt.Errorf("automatic environment rollback contains an empty source Run ID")
		}
		if _, owned := h.createdRuns[source.RunID]; !owned {
			return fmt.Errorf("refusing automatic environment rollback: installation source Run %s was not created by this test", source.RunID)
		}
	}
	return nil
}

func (h *harness) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if h.cfg.PreflightOnly {
		if err := h.writeReport(); err != nil {
			t.Errorf("write preflight report: %v", err)
		}
		return
	}
	if h.cleanupArmed && h.mutationStarted {
		cleanupSafe := true
		if err := h.cleanupOwnedRuns(ctx); err != nil {
			t.Errorf("cleanup active Runs: %v", err)
			cleanupSafe = false
		}
		var lifecycle lifecycleDTO
		if err := h.environment.data(ctx, http.MethodGet, "/api/v1/environments/"+h.cfg.Fixture.EnvironmentID+"/lifecycle", nil, &lifecycle); err != nil {
			t.Errorf("read cleanup lifecycle: %v", err)
		} else if lifecycle.InstallationCount > 0 && cleanupSafe && h.destructiveCleanupAllowed() {
			if _, err := h.environmentRollback(ctx, "automatic cleanup after real scenario automation"); err != nil {
				t.Errorf("automatic environment rollback: %v", err)
			}
		} else if lifecycle.InstallationCount > 0 {
			t.Errorf("cleanup left %d installation(s); refusing environment rollback without proven lock and Run ownership", lifecycle.InstallationCount)
		}
	}
	if err := h.releaseLock(ctx); err != nil {
		t.Errorf("release lock: %v", err)
	}
	if err := h.writeReport(); err != nil {
		t.Errorf("write automation report: %v", err)
	}
}

func (h *harness) cleanupOwnedRuns(ctx context.Context) error {
	for runID, client := range h.createdRuns {
		var run runDTO
		if err := client.data(ctx, http.MethodGet, "/api/v1/runs/"+runID, nil, &run); err != nil {
			return err
		}
		if run.EnvironmentID != h.cfg.Fixture.EnvironmentID {
			return fmt.Errorf("tracked Run %s belongs to unexpected environment %s", run.ID, run.EnvironmentID)
		}
		if run.Status != "awaiting_approval" && run.Status != "queued" && run.Status != "running" {
			continue
		}
		if err := client.data(ctx, http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", nil, nil); err != nil {
			var apiErr *apiError
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
				return err
			}
		}
		if _, err := h.waitRun(ctx, client, run.ID, "cancelled", "failed", "succeeded", "rejected"); err != nil {
			return err
		}
	}
	return nil
}

func commandWithInput(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = bytes.NewBufferString(stdin)
	return command.CombinedOutput()
}
