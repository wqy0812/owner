package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestRetryApprovalCarriesLockedDeliveryChoices(t *testing.T) {
	platform := &DeliveryService{}
	plan := lockedPlan{
		DeliveryRequirements: []DeliveryRequirement{{ID: "artifact:runtime", Kind: "artifact", Source: "https://source.test/runtime.tgz"}},
		DeliveryDecisions:    []DeliveryDecision{{RequirementID: "artifact:runtime", Mode: "direct", DecidedBy: "environment-owner"}},
	}
	approvedAt := time.Now().UTC()
	finalized, err := platform.finalizeDeliveryPlan(context.Background(), plan, nil, domain.User{ID: "environment-owner"}, approvedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(finalized.DeliveryDecisions) != 1 || finalized.DeliveryDecisions[0].Mode != "direct" || finalized.DeliveryDecisions[0].DecidedAt != approvedAt {
		t.Fatalf("carried retry delivery decision=%+v", finalized.DeliveryDecisions)
	}
	if len(finalized.DeliveryResults) != 1 || finalized.DeliveryResults[0].Status != "direct" || finalized.DeliveryResults[0].ActualLocation != "https://source.test/runtime.tgz" {
		t.Fatalf("carried retry delivery result=%+v", finalized.DeliveryResults)
	}
}

type presentArtifactDelivery struct{}

func (presentArtifactDelivery) Probe(context.Context, ArtifactLocation, ArtifactIdentity) error {
	return nil
}
func (presentArtifactDelivery) Transfer(context.Context, ArtifactTransfer) error {
	return errors.New("transfer must not run during approval finalization")
}

func TestDeliveryApprovalReusesTargetThatAppearedWhilePending(t *testing.T) {
	platform := &DeliveryService{artifactDelivery: presentArtifactDelivery{}}
	plan := lockedPlan{
		Steps: []lockedStep{{ID: "step-1", Variables: map[string]any{}}},
		DeliveryRequirements: []DeliveryRequirement{{
			ID: "artifact:runtime", Kind: "artifact", Name: "runtime", Identity: "sha256:" + strings.Repeat("a", 64),
			Source: "https://source.test/runtime.tgz", Target: "http://target.test/components/runtime.tgz",
			TransferAvailable: true, TargetStation: "target.test", RelativePath: "components/runtime.tgz", StepIDs: []string{"step-1"},
		}},
	}
	finalized, err := platform.finalizeDeliveryPlan(context.Background(), plan, []DeliveryDecisionInput{{RequirementID: "artifact:runtime", Mode: "transfer"}}, domain.User{ID: "environment-owner"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(finalized.ArtifactTransfers) != 0 || len(finalized.DeliveryResults) != 1 || finalized.DeliveryResults[0].Status != "reused_target" {
		t.Fatalf("target-appeared plan=%+v", finalized)
	}
}

func TestUnreadableDeliverySourceRoutesToComponentOwner(t *testing.T) {
	component := domain.Component{ID: "component-runtime", Name: "Runtime", OwnerID: "component-owner"}
	release := domain.ComponentRelease{ID: "release-runtime", Version: "1.0.0"}
	err := deliverySourceError(component, release, "介质 runtime", "https://source.invalid/runtime.tgz", "/components?selected=component-runtime", errors.New("unreachable"))
	var actionable *domain.ActionableError
	if !errors.As(err, &actionable) || actionable.Explanation.Reasons[0].Code != "delivery.source_unreadable" || actionable.Explanation.PrimaryAction.Href != "/components?selected=component-runtime" || !strings.Contains(err.Error(), "component-owner") {
		t.Fatalf("unreadable source error=%#v", err)
	}
}

func TestResolveParametersOwnerLayers(t *testing.T) {
	got := ResolveParameters(
		map[string]any{"version": "default", "nested": map[string]any{"a": 1, "b": 1}},
		map[string]any{"version": "node", "nested": map[string]any{"b": 2}},
		map[string]any{"version": "environment", "region": "cn"},
	)
	want := map[string]any{"version": "environment", "region": "cn", "nested": map[string]any{"a": 1, "b": 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved = %#v, want %#v", got, want)
	}
}

func TestRequiredCredentialsAreCheckedByDeclaredName(t *testing.T) {
	steps := []lockedStep{{RequiredCredentials: []string{"ansible_ssh_pass", "registry_user"}}}
	refs := []domain.CredentialRef{{Name: "ansible_ssh_pass"}}
	err := validateRequiredCredentials(refs, steps)
	if err == nil || !strings.Contains(err.Error(), "registry_user") || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("missing credential validation=%v", err)
	}
	refs = append(refs, domain.CredentialRef{Name: "registry_user"})
	if err := validateRequiredCredentials(refs, steps); err != nil {
		t.Fatalf("configured credentials rejected: %v", err)
	}
}

func TestScenarioValuesOnlyAcceptScenarioOwnedParameters(t *testing.T) {
	release := domain.ComponentRelease{Parameters: []domain.ParameterDefinition{{Name: "replicas", Type: domain.ParameterTypeInteger, Modifiable: true, ValueProvider: domain.ParameterProviderScenarioOwner}}}
	if err := validateScenarioParameterValues(release, domain.ScenarioNode{ID: "node", ParameterValues: map[string]any{"replicas": 3}}); err != nil {
		t.Fatal(err)
	}
	if err := validateScenarioParameterValues(release, domain.ScenarioNode{ID: "node", ParameterValues: map[string]any{"undeclared": true}}); err == nil {
		t.Fatal("undeclared scenario value was accepted")
	}
}

func TestResolvedParameterRequiredTypeAndEnumValidation(t *testing.T) {
	parameters := []domain.ParameterDefinition{
		{Name: "endpoint", Type: domain.ParameterTypeString, Required: true, MinLength: 1},
		{Name: "replicas", Type: domain.ParameterTypeInteger, Required: true},
		{Name: "mode", Type: domain.ParameterTypeString, Enum: []any{"safe", "fast"}},
	}
	if err := validateResolvedParameters(parameters, map[string]any{"endpoint": "localhost", "replicas": float64(3), "mode": "safe"}); err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]map[string]any{
		"missing":  {"endpoint": "localhost"},
		"fraction": {"endpoint": "localhost", "replicas": 1.5},
		"type":     {"endpoint": 7, "replicas": 1},
		"enum":     {"endpoint": "localhost", "replicas": 1, "mode": "unsafe"},
		"empty":    {"endpoint": "", "replicas": 1, "mode": "safe"},
	} {
		if err := validateResolvedParameters(parameters, values); err == nil {
			t.Fatalf("%s invalid values were accepted: %#v", name, values)
		}
	}
}

func TestVersionNamesContainingSecretAreNotTreatedAsCredentials(t *testing.T) {
	if err := rejectSensitiveMap(map[string]any{"versions": map[string]any{"harbor_secret_version": "v1.0.0"}}, "parameters"); err != nil {
		t.Fatalf("version metadata rejected as credential: %v", err)
	}
	if err := rejectSensitiveMap(map[string]any{"harbor_secret": "actual-secret"}, "parameters"); err == nil {
		t.Fatal("actual secret field was accepted")
	}
}

func TestRequiredComponentFixedParameterIsStaticallyBound(t *testing.T) {
	release := domain.ComponentRelease{Parameters: []domain.ParameterDefinition{
		{Name: "endpoint", Type: domain.ParameterTypeString, Required: true, FixedValue: "localhost", Visibility: domain.ParameterInternal, Description: "api", ValueProvider: domain.ParameterProviderComponentOwner},
	}}
	var issues []domain.ValidationIssue
	validateRequiredParameters(release, domain.ScenarioNode{ID: "node", ParameterValues: map[string]any{}}, &issues)
	if len(issues) != 0 {
		t.Fatalf("required parameter with default reported unbound: %+v", issues)
	}
}

func TestComponentReleaseSpecDigestChangesOnMutableDefinition(t *testing.T) {
	release := domain.ComponentRelease{
		Version: "1.0.0", RiskLevel: domain.RiskLow,
		Parameters: []domain.ParameterDefinition{{Name: "region", Description: "region", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, FixedValue: "cn", ValueProvider: domain.ParameterProviderComponentOwner}},
		Actions:    []domain.ActionDefinition{{Name: "install", Kind: domain.ActionInstall, Playbook: "install.yml", TimeoutSeconds: 60}},
	}
	before := componentReleaseSpecDigest(release)
	release.Actions[0].Playbook = "install-v2.yml"
	if after := componentReleaseSpecDigest(release); before == after {
		t.Fatal("release definition digest did not change after action mutation")
	}
	before = componentReleaseSpecDigest(release)
	release.Actions[0].PlaybookSHA256 = strings.Repeat("a", 64)
	if after := componentReleaseSpecDigest(release); before == after {
		t.Fatal("release definition digest did not include Playbook content identity")
	}
	before = componentReleaseSpecDigest(release)
	release.Actions[0].RequiredCredentials = []string{"ansible_ssh_pass"}
	if after := componentReleaseSpecDigest(release); before == after {
		t.Fatal("release definition digest did not include required credentials")
	}
	before = componentReleaseSpecDigest(release)
	release.Actions[0].Idempotent = true
	if after := componentReleaseSpecDigest(release); before == after {
		t.Fatal("release definition digest did not include idempotent capability")
	}
	before = componentReleaseSpecDigest(release)
	release.Parameters[0].FixedValue = "us"
	if after := componentReleaseSpecDigest(release); before == after {
		t.Fatal("release definition digest did not include parameters")
	}
	before = componentReleaseSpecDigest(release)
	release.Dependencies = []domain.ComponentDependency{{
		UpstreamComponentID: "upstream", UpstreamReleaseID: "upstream-1",
		ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "root", TargetParameter: "region"}},
	}}
	if after := componentReleaseSpecDigest(release); before == after {
		t.Fatal("release definition digest did not include parameter mappings")
	}
}

func TestScenarioRevisionSpecDigestChangesWithGraphContent(t *testing.T) {
	revision := domain.ScenarioRevision{
		Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "runtime", ReleaseID: "release-runtime-1", Action: domain.ActionInstall, HostGroup: "workers", ParameterValues: map[string]any{"region": "cn"}}}},
	}
	before := scenarioRevisionSpecDigest(revision)
	revision.Graph.Nodes[0].HostGroup = "control_plane"
	if after := scenarioRevisionSpecDigest(revision); before == after {
		t.Fatal("scenario definition digest did not include graph")
	}
	before = scenarioRevisionSpecDigest(revision)
	revision.Graph.Nodes[0].Name = "changed"
	if after := scenarioRevisionSpecDigest(revision); before == after {
		t.Fatal("scenario definition digest did not include node metadata")
	}
}

func TestIdempotentInstallCanServeUpgradeWithoutDuplicateAction(t *testing.T) {
	install := domain.ActionDefinition{ID: "install", Kind: domain.ActionInstall, Playbook: "install.yml", TimeoutSeconds: 60, Idempotent: true}
	release := domain.ComponentRelease{Version: "2.0.0", Actions: []domain.ActionDefinition{install}}
	action, err := actionFor(release, domain.ActionUpgrade)
	if err != nil {
		t.Fatal(err)
	}
	if action.ID != install.ID || action.Playbook != install.Playbook || action.Kind != domain.ActionUpgrade {
		t.Fatalf("reused upgrade action=%+v", action)
	}

	explicit := domain.ActionDefinition{ID: "upgrade", Kind: domain.ActionUpgrade, Playbook: "upgrade.yml", TimeoutSeconds: 60}
	release.Actions = append(release.Actions, explicit)
	action, err = actionFor(release, domain.ActionUpgrade)
	if err != nil || action.ID != explicit.ID {
		t.Fatalf("explicit upgrade should take precedence: action=%+v err=%v", action, err)
	}

	install.Idempotent = false
	if _, err := actionFor(domain.ComponentRelease{Version: "2.0.0", Actions: []domain.ActionDefinition{install}}, domain.ActionUpgrade); err == nil {
		t.Fatal("non-idempotent install unexpectedly served upgrade")
	}
}

func TestCheckCannotDeclareExecutableRetryCapability(t *testing.T) {
	release := domain.ComponentRelease{Version: "1.0.0", Actions: []domain.ActionDefinition{{
		Kind: domain.ActionCheck, Playbook: "verify.yml", HostGroup: "all", TimeoutSeconds: 60, Idempotent: true,
	}}}
	if err := validateRelease(release); err == nil || !strings.Contains(err.Error(), "check") {
		t.Fatalf("invalid idempotent capability validation=%v", err)
	}
}

func TestComponentTestRequiresAnExecutablePrimaryAction(t *testing.T) {
	for _, kind := range []domain.ActionKind{domain.ActionConfigure, domain.ActionInstall, domain.ActionUpgrade} {
		release := domain.ComponentRelease{Actions: []domain.ActionDefinition{{Kind: domain.ActionVerify}, {Kind: kind}}}
		action, found := primaryActionForComponentTest(release)
		if !found || action.Kind != kind {
			t.Fatalf("primary action for %s = %+v, found=%v", kind, action, found)
		}
	}
	for _, kind := range []domain.ActionKind{domain.ActionCheck, domain.ActionPreflight, domain.ActionVerify} {
		if _, found := primaryActionForComponentTest(domain.ComponentRelease{Actions: []domain.ActionDefinition{{Kind: kind}}}); found {
			t.Fatalf("check-only release acquired an executable primary action: %s", kind)
		}
	}
	release := domain.ComponentRelease{Actions: []domain.ActionDefinition{{Kind: domain.ActionPreflight}, {Kind: domain.ActionConfigure}}}
	action, _ := primaryActionForComponentTest(release)
	if action.Kind != domain.ActionConfigure {
		t.Fatalf("configure should take precedence over preflight: %+v", action)
	}
}

func TestDependencyMayUseAnyReachableNodeOfRepeatedRelease(t *testing.T) {
	reachable := map[string]map[string]bool{
		"runtime-control": {"kubelet-worker": false},
		"runtime-worker":  {"kubelet-worker": true},
	}
	if !anyUpstreamNodeReachable([]string{"runtime-control", "runtime-worker"}, "kubelet-worker", reachable) {
		t.Fatal("reachable repeated upstream release node was ignored")
	}
	if anyUpstreamNodeReachable([]string{"runtime-control"}, "kubelet-worker", reachable) {
		t.Fatal("unreachable upstream node was accepted")
	}
}

func TestInlineSensitiveMapsAreRejectedBeforePersistence(t *testing.T) {
	for _, values := range []map[string]any{
		{"registryPassword": "do-not-store"},
		{"nested": map[string]any{"access_token": "do-not-store"}},
		{"items": []any{map[string]any{"privateKey": "do-not-store"}}},
		{"K8S_ENCRYPTION_KEY": "do-not-store"},
	} {
		if err := rejectSensitiveMap(values, "test value"); err == nil || strings.Contains(err.Error(), "do-not-store") {
			t.Fatalf("sensitive map rejection=%v", err)
		}
	}
	if err := validateRelease(domain.ComponentRelease{
		Version:    "1.0.0",
		Parameters: []domain.ParameterDefinition{{Name: "password", Description: "bad", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, FixedValue: "do-not-store", ValueProvider: domain.ParameterProviderComponentOwner}},
	}); err == nil || strings.Contains(err.Error(), "do-not-store") {
		t.Fatalf("sensitive release parameter contract rejection=%v", err)
	}
}

func TestCredentialReferencesAndRedaction(t *testing.T) {
	valid := []domain.CredentialRef{
		{Name: "ssh", Kind: "sshKeyPath", Reference: "/tmp/test-key"},
		{Name: "registry", Kind: "envVarRef", Reference: "TEST_REGISTRY_TOKEN"},
	}
	if err := ValidateCredentialRefs(valid); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCredentialRefs([]domain.CredentialRef{{Name: "bad", Kind: "inline", Reference: "raw-secret"}}); err == nil {
		t.Fatal("expected inline secret kind to fail")
	}

	redacted := Redact(map[string]any{
		"password": "super-secret",
		"message":  "token is super-secret",
		"nested":   map[string]any{"safe": "ok"},
	}, "super-secret").(map[string]any)
	encoded := strings.Join([]string{redacted["password"].(string), redacted["message"].(string)}, " ")
	if strings.Contains(encoded, "super-secret") || redacted["nested"].(map[string]any)["safe"] != "ok" {
		t.Fatalf("redaction failed: %#v", redacted)
	}
}

func TestBuildImpactReportTraversesAndProtectsCycles(t *testing.T) {
	components := []domain.Component{
		{ID: "runtime", Name: "Runtime", OwnerID: "owner-a"},
		{ID: "k8s", Name: "Kubernetes", OwnerID: "owner-b"},
		{ID: "bundle", Name: "Cluster bundle", OwnerID: "owner-a"},
	}
	releases := []domain.ComponentRelease{
		{ID: "runtime-1", ComponentID: "runtime"},
		{ID: "k8s-1", ComponentID: "k8s", Dependencies: []domain.ComponentDependency{{UpstreamComponentID: "runtime"}}},
		{ID: "bundle-1", ComponentID: "bundle", Dependencies: []domain.ComponentDependency{{UpstreamComponentID: "k8s"}}},
		// Malformed historical data must not loop forever.
		{ID: "runtime-2", ComponentID: "runtime", Dependencies: []domain.ComponentDependency{{UpstreamComponentID: "bundle"}}},
	}
	scenarios := []domain.Scenario{{
		ID: "cluster", OwnerID: "scenario-owner",
		Revisions: []domain.ScenarioRevision{{Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "n", ReleaseID: "bundle-1"}}}}},
	}}
	report := BuildImpactReport("runtime", components, releases, scenarios)
	if len(report.Recipients) != 3 { // component owner A/B are separate role recipients + scenario owner
		t.Fatalf("recipients = %#v", report.Recipients)
	}
	var scenarioFound bool
	for _, recipient := range report.Recipients {
		if recipient.UserID == "scenario-owner" {
			scenarioFound = reflect.DeepEqual(recipient.ScenarioIDs, []string{"cluster"})
		}
	}
	if !scenarioFound {
		t.Fatalf("scenario recipient missing: %#v", report.Recipients)
	}
}

func TestEventHub(t *testing.T) {
	hub := NewEventHub()
	channel, unsubscribe := hub.Subscribe()
	hub.Publish("notification", map[string]any{"id": "n1"})
	event := <-channel
	if event.Type != "notification" || string(event.JSON()) == "" {
		t.Fatalf("unexpected event: %#v", event)
	}
	unsubscribe()
	unsubscribe()
}
