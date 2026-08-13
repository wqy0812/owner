package domain

import (
	"errors"
	"testing"
)

func TestValidateComponentClassification(t *testing.T) {
	for layer, categories := range componentCategoriesByLayer {
		for _, category := range categories {
			component := Component{Layer: layer, Category: category, Kind: ComponentSoftware, Requiredness: RequiredProfile}
			if err := ValidateComponentClassification(component); err != nil {
				t.Fatalf("valid classification %s/%s: %v", layer, category, err)
			}
		}
	}
	for _, invalid := range []Component{
		{Layer: LayerRuntimeState, Category: CategoryNetwork, Kind: ComponentSoftware, Requiredness: RequiredProfile},
		{Layer: "unknown", Category: CategoryRuntime, Kind: ComponentSoftware, Requiredness: RequiredProfile},
		{Layer: LayerRuntimeState, Category: CategoryRuntime, Kind: "unknown", Requiredness: RequiredProfile},
		{Layer: LayerRuntimeState, Category: CategoryRuntime, Kind: ComponentSoftware, Requiredness: "unknown"},
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
	for _, expected := range []string{"missing_release", "missing_action", "missing_host_group", "duplicate_release_node", "cycle"} {
		if !codes[expected] {
			t.Fatalf("missing %s in %#v", expected, issues)
		}
	}
}

func TestActionNeedsApprovalUsesExplicitAndDefensiveMarkers(t *testing.T) {
	for _, action := range []ActionDefinition{
		{Kind: ActionUninstall},
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
