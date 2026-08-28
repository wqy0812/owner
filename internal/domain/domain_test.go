package domain

import (
	"errors"
	"strings"
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

func TestParameterContractHelpers(t *testing.T) {
	parameters := []ParameterDefinition{
		{Name: "endpoint", Type: ParameterTypeString, Required: true},
		{Name: "replicas", Type: ParameterTypeInteger, DefaultValue: 3},
	}
	if got, ok := ParameterByName(parameters, "replicas"); !ok || got.DefaultValue != 3 || !got.HasDefault() {
		t.Fatalf("replicas lookup=%+v ok=%v", got, ok)
	}
	if _, ok := ParameterByName(parameters, "missing"); ok {
		t.Fatal("missing parameter was reported present")
	}
	if parameters[0].HasDefault() {
		t.Fatal("required parameter without a default reported one")
	}

	for _, valid := range []ParameterType{ParameterTypeString, ParameterTypeBoolean, ParameterTypeInteger, ParameterTypeNumber, ParameterTypeObject, ParameterTypeArray} {
		if !valid.Valid() {
			t.Fatalf("valid parameter type %q rejected", valid)
		}
	}
	if ParameterType("duration").Valid() {
		t.Fatal("unknown parameter type accepted")
	}
	if !ParameterInternal.Valid() || !ParameterPublic.Valid() || ParameterVisibility("secret").Valid() {
		t.Fatal("parameter visibility validation is inconsistent")
	}
}

func TestMappingContractIsStableAndMappedTargetsUseLastDefinition(t *testing.T) {
	dependencies := []ComponentDependency{
		{UpstreamComponentID: "database", ParameterMappings: []ParameterMapping{{UpstreamParameter: "host", TargetParameter: "db_host"}}},
		{UpstreamComponentID: "cache", ParameterMappings: []ParameterMapping{{UpstreamParameter: "endpoint", TargetParameter: "cache_url"}, {UpstreamParameter: "override", TargetParameter: "db_host"}}},
	}
	targets := MappedTargets(dependencies)
	if len(targets) != 2 || targets["db_host"].UpstreamParameter != "override" {
		t.Fatalf("mapped targets=%+v", targets)
	}
	contract := MappingContract(dependencies)
	if len(contract) != 3 || contract[0] != "cache\x00endpoint\x00cache_url" || contract[2] != "database\x00host\x00db_host" {
		t.Fatalf("sorted mapping contract=%q", contract)
	}
}

func TestComponentReleaseSpecDigestUsesContentIdentityNotSourceLocation(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	release := ComponentRelease{
		ID: "release-1", ComponentID: "component-1", Version: "1.0.0", Status: ReleaseDraft,
		Artifacts: []ComponentArtifact{
			{Alias: "zeta", Filename: "zeta.tgz", SHA256: checksum, SourceURL: "https://source-a.test/zeta.tgz"},
			{Alias: "alpha", Filename: "alpha.tgz", SHA256: strings.Repeat("b", 64), SourceURL: "https://source-a.test/alpha.tgz"},
		},
		Images: []ComponentImage{
			{LogicalName: "sidecar", SourceRef: "registry-a.test/sidecar:latest", Digest: "sha256:" + strings.Repeat("c", 64)},
			{LogicalName: "main", SourceRef: "registry-a.test/main:latest", Digest: "sha256:" + strings.Repeat("d", 64)},
		},
	}
	digest := ComponentReleaseSpecDigest(release)

	relocated := release
	relocated.ID = "another-id"
	relocated.Status = ReleaseReleased
	relocated.Artifacts = append([]ComponentArtifact(nil), release.Artifacts...)
	relocated.Images = append([]ComponentImage(nil), release.Images...)
	relocated.Artifacts[0].SourceURL = "https://source-b.test/zeta.tgz"
	relocated.Images[0].SourceRef = "registry-b.test/sidecar@" + relocated.Images[0].Digest
	relocated.Artifacts[0], relocated.Artifacts[1] = relocated.Artifacts[1], relocated.Artifacts[0]
	relocated.Images[0], relocated.Images[1] = relocated.Images[1], relocated.Images[0]
	if got := ComponentReleaseSpecDigest(relocated); got != digest {
		t.Fatalf("source-only relocation changed digest: before=%s after=%s", digest, got)
	}

	relocated.Artifacts[0].SHA256 = strings.Repeat("e", 64)
	if got := ComponentReleaseSpecDigest(relocated); got == digest {
		t.Fatal("artifact content identity change did not change digest")
	}
}

func TestScenarioRevisionSpecDigestTracksOnlyGraphDefinition(t *testing.T) {
	revision := ScenarioRevision{
		ID: "revision-1", ScenarioID: "scenario-1", Revision: 1, Status: RevisionDraft,
		Graph: ScenarioGraph{Nodes: []ScenarioNode{{ID: "node-1", ReleaseID: "release-1", Action: ActionInstall, HostGroup: "workers"}}},
	}
	digest := ScenarioRevisionSpecDigest(revision)
	revision.ID = "revision-2"
	revision.Status = RevisionReleased
	revision.Revision = 2
	if got := ScenarioRevisionSpecDigest(revision); got != digest {
		t.Fatalf("revision metadata changed graph digest: before=%s after=%s", digest, got)
	}
	revision.Graph.Nodes[0].ReleaseID = "release-2"
	if got := ScenarioRevisionSpecDigest(revision); got == digest {
		t.Fatal("graph content change did not change digest")
	}
}
