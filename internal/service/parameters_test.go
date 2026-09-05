package service

import (
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestValidateReleaseParametersRejectsInvalidContract(t *testing.T) {
	valid := domain.ComponentRelease{
		Version: "1.0.0",
		Parameters: []domain.ParameterDefinition{{
			Name: "root", Description: "install root", Type: domain.ParameterTypeString,
			Required: true, FixedValue: "/opt", Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner, MinLength: 1,
		}},
	}
	if err := validateRelease(valid); err != nil {
		t.Fatal(err)
	}
	for name, release := range map[string]domain.ComponentRelease{
		"missing visibility": {Version: "1", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "x", Type: domain.ParameterTypeString}}},
		"duplicate": {Version: "1", Parameters: []domain.ParameterDefinition{
			{Name: "root", Description: "a", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, FixedValue: "a", ValueProvider: domain.ParameterProviderComponentOwner},
			{Name: "root", Description: "b", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, FixedValue: "b", ValueProvider: domain.ParameterProviderComponentOwner},
		}},
		"sensitive public":       {Version: "1", Parameters: []domain.ParameterDefinition{{Name: "registryPassword", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic, FixedValue: "x", ValueProvider: domain.ParameterProviderComponentOwner}}},
		"unknown type":           {Version: "1", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "x", Type: "blob", Visibility: domain.ParameterInternal, FixedValue: "x", ValueProvider: domain.ParameterProviderComponentOwner}}},
		"missing mapping target": {Version: "1", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, FixedValue: "x", ValueProvider: domain.ParameterProviderComponentOwner}}, Dependencies: []domain.ComponentDependency{{UpstreamComponentID: "up", UpstreamReleaseID: "up-1", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "a", TargetParameter: "missing"}}}}},
		"duplicate dependency":   {Version: "1", ComponentID: "down", Dependencies: []domain.ComponentDependency{{UpstreamComponentID: "up", UpstreamReleaseID: "up-1"}, {UpstreamComponentID: "up", UpstreamReleaseID: "up-2"}}},
		"invalid provider":       {Version: "1", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, Modifiable: true, ValueProvider: domain.ParameterProviderComponentOwner}}},
		"scenario raw object":    {Version: "1", Parameters: []domain.ParameterDefinition{{Name: "options", Description: "x", Type: domain.ParameterTypeObject, Visibility: domain.ParameterInternal, Modifiable: true, ValueProvider: domain.ParameterProviderScenarioOwner}}},
		"environment raw array":  {Version: "1", Parameters: []domain.ParameterDefinition{{Name: "peers", Description: "x", Type: domain.ParameterTypeArray, Visibility: domain.ParameterInternal, Modifiable: true, ValueProvider: domain.ParameterProviderEnvironmentOwner, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}}}},
	} {
		if err := validateRelease(release); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestValidateReleaseAllowsStandaloneInstallRollback(t *testing.T) {
	base := domain.ComponentRelease{
		Version: "1.0.0",
		Actions: []domain.ActionDefinition{{
			Name: "rollback", Kind: domain.ActionRollback, Playbook: "rollback.yml", HostGroup: "all", TimeoutSeconds: 60,
		}},
	}
	if err := validateRelease(base); err != nil {
		t.Fatalf("standalone install rollback rejected: %v", err)
	}
	base.Actions[0].Tags = []string{rollbackSelfVerifyTag}
	if err := validateRelease(base); err != nil {
		t.Fatalf("standalone self-verifying rollback rejected: %v", err)
	}
	base.Actions[0].FromReleaseID = "release-current"
	base.Actions[0].ToReleaseID = "release-previous"
	if err := validateRelease(base); err == nil || !strings.Contains(err.Error(), rollbackSelfVerifyTag) {
		t.Fatalf("targeted self-verifying rollback accepted: %v", err)
	}
	base.Actions[0].Tags = nil
	base.Actions[0].ToReleaseID = ""
	base.Actions[0].FromReleaseID = "release-current"
	if err := validateRelease(base); err == nil || !strings.Contains(err.Error(), "both fromReleaseId and toReleaseId") {
		t.Fatalf("half-configured version rollback accepted: %v", err)
	}
}

func TestValidateReleaseRejectsRollbackSelfVerificationTagOnOtherActions(t *testing.T) {
	release := domain.ComponentRelease{
		Version: "1.0.0",
		Actions: []domain.ActionDefinition{{
			Name: "verify", Kind: domain.ActionVerify, Playbook: "verify.yml", HostGroup: "all", TimeoutSeconds: 60,
			Tags: []string{rollbackSelfVerifyTag},
		}},
	}
	if err := validateRelease(release); err == nil || !strings.Contains(err.Error(), rollbackSelfVerifyTag) {
		t.Fatalf("non-rollback self-verification tag accepted: %v", err)
	}
}

func TestMappedParameterCannotBeLocallyOverridden(t *testing.T) {
	release := domain.ComponentRelease{
		Parameters:   []domain.ParameterDefinition{{Name: "kubeRoot", Description: "root", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderUpstreamMapping}},
		Dependencies: []domain.ComponentDependency{{ID: "dep-1", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}}}},
	}
	node := domain.ScenarioNode{ID: "proxy", ParameterValues: map[string]any{"kubeRoot": "/tmp"}}
	if conflicts := nodeOverridesMappedParameter(node, release); len(conflicts) != 1 || conflicts[0] != "kubeRoot" {
		t.Fatalf("conflicts=%v", conflicts)
	}
}

func TestSelectDependencySourceUniqueAndAmbiguous(t *testing.T) {
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{
			{ID: "kubelet-master", ReleaseID: "rel-kubelet"},
			{ID: "kubelet-worker", ReleaseID: "rel-kubelet"},
			{ID: "proxy-master", ReleaseID: "rel-proxy", DependencySources: map[string]string{"dep-1": "kubelet-master"}},
		},
		Edges: []domain.ScenarioEdge{{Source: "kubelet-master", Target: "proxy-master", Kind: domain.ScenarioEdgeDependency, DependencyID: "dep-1"}, {Source: "kubelet-worker", Target: "proxy-master", Kind: domain.ScenarioEdgeDependency, DependencyID: "dep-1"}},
	}
	releaseByNode := map[string]domain.ComponentRelease{
		"kubelet-master": {ID: "rel-kubelet"},
		"kubelet-worker": {ID: "rel-kubelet"},
		"proxy-master":   {ID: "rel-proxy"},
	}
	dep := domain.ComponentDependency{ID: "dep-1", UpstreamReleaseID: "rel-kubelet"}
	selected, err := selectDependencySource(graph.Nodes[2], dep, graph, releaseByNode)
	if err != nil || selected != "kubelet-master" {
		t.Fatalf("explicit source=%s err=%v", selected, err)
	}
	ambiguous := graph.Nodes[2]
	ambiguous.DependencySources = nil
	if _, err := selectDependencySource(ambiguous, dep, graph, releaseByNode); err == nil || !strings.Contains(err.Error(), "must choose a source") {
		t.Fatalf("ambiguous source err=%v", err)
	}
	uniqueGraph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{{ID: "only", ReleaseID: "rel-kubelet"}, {ID: "down", ReleaseID: "rel-proxy", DependencySources: map[string]string{"dep-1": "only"}}},
		Edges: []domain.ScenarioEdge{{Source: "only", Target: "down", Kind: domain.ScenarioEdgeDependency, DependencyID: "dep-1"}},
	}
	unique, err := selectDependencySource(uniqueGraph.Nodes[1], dep, uniqueGraph, map[string]domain.ComponentRelease{"only": {ID: "rel-kubelet"}})
	if err != nil || unique != "only" {
		t.Fatalf("unique source=%s err=%v", unique, err)
	}
}

func TestPlannerPassesUpstreamFinalValueAndIgnoresEnvironmentData(t *testing.T) {
	kubelet := domain.ComponentRelease{
		ID: "rel-kubelet",
		Parameters: []domain.ParameterDefinition{{
			Name: "kubeInstallRoot", Description: "root", Type: domain.ParameterTypeString, Required: true,
			FixedValue: "/approot1/paas/kube", Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner,
		}},
	}
	proxy := domain.ComponentRelease{
		ID: "rel-proxy",
		Parameters: []domain.ParameterDefinition{{
			Name: "kubeRoot", Description: "imported", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderUpstreamMapping,
		}},
		Dependencies: []domain.ComponentDependency{{
			ID: "dep-1", UpstreamReleaseID: "rel-kubelet",
			ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}},
		}},
	}
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{
			{ID: "kubelet", ReleaseID: "rel-kubelet"},
			{ID: "proxy", ReleaseID: "rel-proxy", DependencySources: map[string]string{"dep-1": "kubelet"}},
		},
		Edges: []domain.ScenarioEdge{{Source: "kubelet", Target: "proxy", Kind: domain.ScenarioEdgeDependency, DependencyID: "dep-1"}},
	}
	kubeletVars, kubeletProv, err := resolveOwnParameters(kubelet, graph.Nodes[0], domain.EnvironmentRevision{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if kubeletVars["kubeInstallRoot"] != "/approot1/paas/kube" || kubeletProv["kubeInstallRoot"].Source != parameterSourceComponentFixed {
		t.Fatalf("kubelet vars=%#v prov=%#v", kubeletVars, kubeletProv)
	}
	proxyVars, proxyProv, err := resolveOwnParameters(proxy, graph.Nodes[1], domain.EnvironmentRevision{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyParameterMappings(proxy, graph.Nodes[1], graph, map[string]domain.ComponentRelease{"kubelet": kubelet, "proxy": proxy}, map[string]map[string]any{"kubelet": kubeletVars}, proxyVars, proxyProv); err != nil {
		t.Fatal(err)
	}
	if proxyVars["kubeRoot"] != "/approot1/paas/kube" {
		t.Fatalf("mapped value=%v", proxyVars["kubeRoot"])
	}
	if proxyProv["kubeRoot"].Source != parameterSourceDependencyMapping || proxyProv["kubeRoot"].SourceNodeID != "kubelet" {
		t.Fatalf("provenance=%#v", proxyProv["kubeRoot"])
	}
}

func TestPlannerReadsOnlyDeclaredEnvironmentParameters(t *testing.T) {
	release := domain.ComponentRelease{Parameters: []domain.ParameterDefinition{
		{Name: "region", Description: "target region", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, Modifiable: true, ValueProvider: domain.ParameterProviderEnvironmentOwner, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}},
	}}
	release.ID = "release"
	resolved, provenance, err := resolveOwnParameters(release, domain.ScenarioNode{}, domain.EnvironmentRevision{Parameters: map[string]any{"release:release:region": "cn"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved["region"] != "cn" || provenance["region"].Source != parameterSourceEnvironmentValue {
		t.Fatalf("provenance=%#v", provenance)
	}
}

func TestPlannerSupportsChainedPublicParameters(t *testing.T) {
	a := domain.ComponentRelease{ID: "a", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "root", Type: domain.ParameterTypeString, Required: true, FixedValue: "/a", Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner}}}
	b := domain.ComponentRelease{ID: "b", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "root", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderUpstreamMapping}}, Dependencies: []domain.ComponentDependency{{ID: "b-a", UpstreamReleaseID: "a", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "root", TargetParameter: "root"}}}}}
	c := domain.ComponentRelease{ID: "c", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "root", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderUpstreamMapping}}, Dependencies: []domain.ComponentDependency{{ID: "c-b", UpstreamReleaseID: "b", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "root", TargetParameter: "root"}}}}}
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{{ID: "a", ReleaseID: "a"}, {ID: "b", ReleaseID: "b", DependencySources: map[string]string{"b-a": "a"}}, {ID: "c", ReleaseID: "c", DependencySources: map[string]string{"c-b": "b"}}},
		Edges: []domain.ScenarioEdge{{Source: "a", Target: "b", Kind: domain.ScenarioEdgeDependency, DependencyID: "b-a"}, {Source: "b", Target: "c", Kind: domain.ScenarioEdgeDependency, DependencyID: "c-b"}},
	}
	resolved := map[string]map[string]any{}
	byNode := map[string]domain.ComponentRelease{"a": a, "b": b, "c": c}
	for _, node := range graph.Nodes {
		release := byNode[node.ID]
		vars, prov, err := resolveOwnParameters(release, node, domain.EnvironmentRevision{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyParameterMappings(release, node, graph, byNode, resolved, vars, prov); err != nil {
			t.Fatal(err)
		}
		resolved[node.ID] = vars
	}
	if resolved["c"]["root"] != "/a" {
		t.Fatalf("chained value=%v", resolved["c"]["root"])
	}
}

func TestMissingRequiredMappedValueFailsAndOptionalIsSkipped(t *testing.T) {
	upstream := domain.ComponentRelease{ID: "up", Parameters: []domain.ParameterDefinition{{Name: "optional", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic}}}
	downstream := domain.ComponentRelease{
		ID: "down",
		Parameters: []domain.ParameterDefinition{
			{Name: "requiredTarget", Description: "x", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderUpstreamMapping},
			{Name: "optionalTarget", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderUpstreamMapping},
		},
		Dependencies: []domain.ComponentDependency{{
			ID: "dep", UpstreamReleaseID: "up",
			ParameterMappings: []domain.ParameterMapping{
				{UpstreamParameter: "optional", TargetParameter: "requiredTarget"},
				{UpstreamParameter: "optional", TargetParameter: "optionalTarget"},
			},
		}},
	}
	// duplicate target would fail validation; use two mappings to same upstream but only one required
	downstream.Dependencies[0].ParameterMappings = []domain.ParameterMapping{{UpstreamParameter: "optional", TargetParameter: "requiredTarget"}}
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{{ID: "up", ReleaseID: "up"}, {ID: "down", ReleaseID: "down", DependencySources: map[string]string{"dep": "up"}}},
		Edges: []domain.ScenarioEdge{{Source: "up", Target: "down", Kind: domain.ScenarioEdgeDependency, DependencyID: "dep"}},
	}
	vars, prov, err := resolveOwnParameters(downstream, graph.Nodes[1], domain.EnvironmentRevision{}, false)
	if err != nil {
		t.Fatal(err)
	}
	err = applyParameterMappings(downstream, graph.Nodes[1], graph, map[string]domain.ComponentRelease{"up": upstream}, map[string]map[string]any{"up": {}}, vars, prov)
	if err == nil || !strings.Contains(err.Error(), "required mapped parameter") {
		t.Fatalf("missing required mapping err=%v", err)
	}

	downstream.Dependencies[0].ParameterMappings = []domain.ParameterMapping{{UpstreamParameter: "optional", TargetParameter: "optionalTarget"}}
	vars, prov, err = resolveOwnParameters(downstream, graph.Nodes[1], domain.EnvironmentRevision{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyParameterMappings(downstream, graph.Nodes[1], graph, map[string]domain.ComponentRelease{"up": upstream}, map[string]map[string]any{"up": {}}, vars, prov); err != nil {
		t.Fatal(err)
	}
}

func TestComponentTestUsesMappedParameterTestValue(t *testing.T) {
	release := domain.ComponentRelease{
		Parameters:   []domain.ParameterDefinition{{Name: "kubeRoot", Description: "root", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderUpstreamMapping, TestValue: "/approot1/paas/kube"}},
		Dependencies: []domain.ComponentDependency{{ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}}}},
	}
	vars, prov, err := resolveOwnParameters(release, domain.ScenarioNode{}, domain.EnvironmentRevision{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if vars["kubeRoot"] != "/approot1/paas/kube" || prov["kubeRoot"].Source != parameterSourceTestValue {
		t.Fatalf("fixture result vars=%#v prov=%#v", vars, prov)
	}
}

func TestMappingContractsEqualForUpgrade(t *testing.T) {
	left := []domain.ComponentDependency{{UpstreamComponentID: "kubelet", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}}}}
	right := []domain.ComponentDependency{{UpstreamComponentID: "kubelet", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}}}}
	if !mappingContractsEqual(left, right) {
		t.Fatal("identical contracts compared unequal")
	}
	right[0].ParameterMappings[0].TargetParameter = "other"
	if mappingContractsEqual(left, right) {
		t.Fatal("changed contract compared equal")
	}
}
