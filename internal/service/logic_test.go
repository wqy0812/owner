package service

import (
	"reflect"
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestResolveParametersPrecedenceAndAllowList(t *testing.T) {
	got, err := ResolveParameters(
		map[string]any{"version": "default", "nested": map[string]any{"a": 1, "b": 1}},
		map[string]any{"version": "node", "nested": map[string]any{"b": 2}},
		map[string]any{"version": "environment", "region": "cn"},
		map[string]any{"version": "run"},
		[]string{"version"},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"version": "run", "region": "cn", "nested": map[string]any{"a": 1, "b": 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved = %#v, want %#v", got, want)
	}
	if _, err := ResolveParameters(nil, nil, nil, map[string]any{"undeclared": true}, nil); err == nil {
		t.Fatal("expected undeclared run input to fail")
	}
}

func TestScenarioRunInputIsValidatedGloballyAndScopedPerNode(t *testing.T) {
	nodes := []domain.ScenarioNode{
		{ID: "control-plane", RunInputs: []string{"api_endpoint"}},
		{ID: "workers", RunInputs: []string{"worker_count"}},
	}
	input := map[string]any{"api_endpoint": "10.0.0.1", "worker_count": float64(3)}
	if err := validateScenarioRunInput(nodes, input); err != nil {
		t.Fatalf("globally declared run input rejected: %v", err)
	}
	controlPlane := runInputForNode(nodes[0], input)
	workers := runInputForNode(nodes[1], input)
	if !reflect.DeepEqual(controlPlane, map[string]any{"api_endpoint": "10.0.0.1"}) || !reflect.DeepEqual(workers, map[string]any{"worker_count": float64(3)}) {
		t.Fatalf("node run inputs control-plane=%#v workers=%#v", controlPlane, workers)
	}
	if err := validateScenarioRunInput(nodes, map[string]any{"undeclared": true}); err == nil {
		t.Fatal("undeclared scenario run input was accepted")
	}
}

func TestResolvedParameterRequiredTypeAndEnumValidation(t *testing.T) {
	schema := map[string]any{
		"required": []any{"endpoint", "replicas"},
		"properties": map[string]any{
			"endpoint": map[string]any{"type": "string"},
			"replicas": map[string]any{"type": "integer"},
			"mode":     map[string]any{"type": "string", "enum": []any{"safe", "fast"}},
		},
	}
	if err := validateResolvedParameters(schema, map[string]any{"endpoint": "localhost", "replicas": float64(3), "mode": "safe"}); err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string]map[string]any{
		"missing":  {"endpoint": "localhost"},
		"fraction": {"endpoint": "localhost", "replicas": 1.5},
		"type":     {"endpoint": 7, "replicas": 1},
		"enum":     {"endpoint": "localhost", "replicas": 1, "mode": "unsafe"},
	} {
		if err := validateResolvedParameters(schema, values); err == nil {
			t.Fatalf("%s invalid values were accepted: %#v", name, values)
		}
	}
}

func TestRequiredParameterWithSchemaDefaultIsStaticallyBound(t *testing.T) {
	schema := map[string]any{
		"required":   []any{"endpoint"},
		"properties": map[string]any{"endpoint": map[string]any{"type": "string", "default": "localhost"}},
	}
	var issues []domain.ValidationIssue
	validateRequiredParameters(schema, domain.ScenarioNode{ID: "node", Values: map[string]any{}, Bindings: map[string]string{}}, &issues)
	if len(issues) != 0 {
		t.Fatalf("required parameter with default reported unbound: %+v", issues)
	}
}

func TestComponentReleaseSpecDigestChangesOnMutableDefinition(t *testing.T) {
	release := domain.ComponentRelease{
		Version: "1.0.0", Type: domain.ReleaseAtomic, RiskLevel: domain.RiskLow,
		ParameterSchema: map[string]any{"properties": map[string]any{"region": map[string]any{"default": "cn"}}},
		Actions:         []domain.ActionDefinition{{Name: "install", Kind: domain.ActionInstall, Playbook: "install.yml", TimeoutSeconds: 60}},
	}
	before := componentReleaseSpecDigest(release)
	release.Actions[0].Playbook = "install-v2.yml"
	if after := componentReleaseSpecDigest(release); before == after {
		t.Fatal("release definition digest did not change after action mutation")
	}
}

func TestComponentTestAcceptsConfigureAndPreflightPrimaryActions(t *testing.T) {
	for _, kind := range []domain.ActionKind{domain.ActionConfigure, domain.ActionPreflight} {
		release := domain.ComponentRelease{Actions: []domain.ActionDefinition{{Kind: domain.ActionVerify}, {Kind: kind}}}
		action, found := primaryActionForComponentTest(release)
		if !found || action.Kind != kind {
			t.Fatalf("primary action for %s = %+v, found=%v", kind, action, found)
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
		Version: "1.0.0", Type: domain.ReleaseAtomic,
		ParameterSchema: map[string]any{"properties": map[string]any{"password": map[string]any{"type": "string", "default": "do-not-store"}}},
	}); err == nil || strings.Contains(err.Error(), "do-not-store") {
		t.Fatalf("sensitive release schema rejection=%v", err)
	}
}

func TestCredentialReferencesAndRedaction(t *testing.T) {
	valid := []domain.CredentialRef{
		{Name: "ssh", Kind: "sshKeyPath", Reference: "/tmp/demo-key"},
		{Name: "registry", Kind: "envVarRef", Reference: "DEMO_REGISTRY_TOKEN"},
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
