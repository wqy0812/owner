package domain

import (
	"errors"
	"testing"
)

func TestValidateComponentClassification(t *testing.T) {
	for _, layer := range []ComponentLayer{LayerHostFoundation, LayerRuntimeState, LayerOrchestrationCore, LayerClusterService, LayerObservabilityManagement, LayerPlatformExtension} {
		component := Component{Layer: layer, Tags: []string{"runtime", "core"}}
		if err := ValidateComponentClassification(component); err != nil {
			t.Fatalf("valid metadata %s: %v", layer, err)
		}
	}
	for _, invalid := range []Component{
		{Layer: "unknown"},
		{Layer: LayerRuntimeState, Tags: []string{"UPPER"}},
		{Layer: LayerRuntimeState, Tags: []string{"has space"}},
		{Layer: LayerRuntimeState, Tags: []string{"duplicate", "duplicate"}},
		{Layer: LayerRuntimeState, Tags: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}},
	} {
		if err := ValidateComponentClassification(invalid); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid classification %+v returned %v", invalid, err)
		}
	}
}

func TestValidateGraphRejectsCycleAndMissingFields(t *testing.T) {
	graph := ScenarioGraph{
		Nodes: []ScenarioNode{
			{ID: "a", ReleaseID: "r1", Action: ActionInstall, HostGroup: "workers"},
			{ID: "b", ReleaseID: "", Action: "", HostGroup: ""},
			{ID: "c", ReleaseID: "r1", Action: ActionVerify, HostGroup: "workers"},
		},
		Edges: []ScenarioEdge{{ID: "a-b", Source: "a", Target: "b"}, {ID: "b-a", Source: "b", Target: "a"}},
	}
	issues := ValidateGraph(graph)
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	for _, expected := range []string{"missing_release", "missing_action", "missing_host_group", "cycle"} {
		if !codes[expected] {
			t.Fatalf("missing %s in %#v", expected, issues)
		}
	}
	if codes["duplicate_release_node"] {
		t.Fatalf("the same release may be reused by multiple scenario nodes: %#v", issues)
	}
}

func TestActionNeedsApprovalUsesExplicitAndDefensiveMarkers(t *testing.T) {
	for _, action := range []ActionDefinition{
		{Kind: ActionUninstall},
		{Kind: ActionRollback},
		{Name: "cluster recovery"},
		{Playbook: "roles/clean.yml"},
		{RiskLevel: RiskDestructive},
		{Destructive: true},
	} {
		if !action.NeedsApproval() {
			t.Fatalf("action should require approval: %#v", action)
		}
	}
	if (ActionDefinition{Kind: ActionVerify, Playbook: "verify.yml"}).NeedsApproval() {
		t.Fatal("verify action unexpectedly requires approval")
	}
}

func TestCredentialRefsAreRedactedForNonOwner(t *testing.T) {
	refs := []CredentialRef{{Name: "ssh", Kind: "sshKeyPath", Reference: "/tmp/key"}}
	redacted := RedactCredentialRefs(refs, false)
	if redacted[0].Reference != "" || !redacted[0].Configured {
		t.Fatalf("redacted=%+v", redacted)
	}
	if refs[0].Reference == "" {
		t.Fatal("redaction mutated source")
	}
}
