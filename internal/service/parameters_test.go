package service

import (
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestValidateReleaseParametersRejectsInvalidContract(t *testing.T) {
	valid := domain.ComponentRelease{
		Version: "1.0.0", Type: domain.ReleaseAtomic,
		Parameters: []domain.ParameterDefinition{{
			Name: "root", Description: "install root", Type: domain.ParameterTypeString,
			Required: true, DefaultValue: "/opt", Visibility: domain.ParameterPublic, MinLength: 1,
		}},
	}
	if err := validateRelease(valid); err != nil {
		t.Fatal(err)
	}
	for name, release := range map[string]domain.ComponentRelease{
		"missing visibility": {Version: "1", Type: domain.ReleaseAtomic, Parameters: []domain.ParameterDefinition{{Name: "root", Description: "x", Type: domain.ParameterTypeString}}},
		"duplicate": {Version: "1", Type: domain.ReleaseAtomic, Parameters: []domain.ParameterDefinition{
			{Name: "root", Description: "a", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal},
			{Name: "root", Description: "b", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal},
		}},
		"sensitive public":       {Version: "1", Type: domain.ReleaseAtomic, Parameters: []domain.ParameterDefinition{{Name: "registryPassword", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic}}},
		"unknown type":           {Version: "1", Type: domain.ReleaseAtomic, Parameters: []domain.ParameterDefinition{{Name: "root", Description: "x", Type: "blob", Visibility: domain.ParameterInternal}}},
		"missing mapping target": {Version: "1", Type: domain.ReleaseAtomic, Parameters: []domain.ParameterDefinition{{Name: "root", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal}}, Dependencies: []domain.ComponentDependency{{UpstreamComponentID: "up", UpstreamReleaseID: "up-1", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "a", TargetParameter: "missing"}}}}},
		"duplicate dependency":   {Version: "1", Type: domain.ReleaseAtomic, ComponentID: "down", Dependencies: []domain.ComponentDependency{{UpstreamComponentID: "up", UpstreamReleaseID: "up-1"}, {UpstreamComponentID: "up", UpstreamReleaseID: "up-2"}}},
		"action unknown param":   {Version: "1", Type: domain.ReleaseAtomic, Parameters: []domain.ParameterDefinition{{Name: "root", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal}}, Actions: []domain.ActionDefinition{{Name: "install", Kind: domain.ActionInstall, Playbook: "a.yml", TimeoutSeconds: 1, AllowedParameters: []string{"missing"}}}},
	} {
		if err := validateRelease(release); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestValidateReleaseAllowsStandaloneInstallRollback(t *testing.T) {
	base := domain.ComponentRelease{
		Version: "1.0.0", Type: domain.ReleaseAtomic,
		Actions: []domain.ActionDefinition{{
			Name: "rollback", Kind: domain.ActionRollback, Playbook: "rollback.yml", TimeoutSeconds: 60,
		}},
	}
	if err := validateRelease(base); err != nil {
		t.Fatalf("standalone install rollback rejected: %v", err)
	}
	base.Actions[0].FromReleaseID = "release-current"
	if err := validateRelease(base); err == nil || !strings.Contains(err.Error(), "both fromReleaseId and toReleaseId") {
		t.Fatalf("half-configured version rollback accepted: %v", err)
	}
}

func TestMappedParameterCannotBeLocallyOverridden(t *testing.T) {
	release := domain.ComponentRelease{
		Parameters:   []domain.ParameterDefinition{{Name: "kubeRoot", Description: "root", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal}},
		Dependencies: []domain.ComponentDependency{{ID: "dep-1", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}}}},
	}
	node := domain.ScenarioNode{ID: "proxy", Values: map[string]any{"kubeRoot": "/tmp"}, Bindings: map[string]string{"kubeRoot": "kubernetes.installRoot"}, RunInputs: []string{"kubeRoot"}}
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
		Edges: []domain.ScenarioEdge{{Source: "kubelet-master", Target: "proxy-master"}, {Source: "kubelet-worker", Target: "proxy-master"}},
	}
	releaseByNode := map[string]domain.ComponentRelease{
		"kubelet-master": {ID: "rel-kubelet"},
		"kubelet-worker": {ID: "rel-kubelet"},
		"proxy-master":   {ID: "rel-proxy"},
	}
	reachable := graphReachability(graph)
	dep := domain.ComponentDependency{ID: "dep-1", UpstreamReleaseID: "rel-kubelet"}
	selected, err := selectDependencySource(graph.Nodes[2], dep, graph, releaseByNode, reachable)
	if err != nil || selected != "kubelet-master" {
		t.Fatalf("explicit source=%s err=%v", selected, err)
	}
	ambiguous := graph.Nodes[2]
	ambiguous.DependencySources = nil
	if _, err := selectDependencySource(ambiguous, dep, graph, releaseByNode, reachable); err == nil || !strings.Contains(err.Error(), "must choose a source") {
		t.Fatalf("ambiguous source err=%v", err)
	}
	uniqueGraph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{{ID: "only", ReleaseID: "rel-kubelet"}, {ID: "down", ReleaseID: "rel-proxy"}},
		Edges: []domain.ScenarioEdge{{Source: "only", Target: "down"}},
	}
	unique, err := selectDependencySource(uniqueGraph.Nodes[1], dep, uniqueGraph, map[string]domain.ComponentRelease{"only": {ID: "rel-kubelet"}}, graphReachability(uniqueGraph))
	if err != nil || unique != "only" {
		t.Fatalf("unique source=%s err=%v", unique, err)
	}
}

func TestPlannerPassesUpstreamFinalValueAndBlocksEnvironmentAlias(t *testing.T) {
	kubelet := domain.ComponentRelease{
		ID: "rel-kubelet",
		Parameters: []domain.ParameterDefinition{{
			Name: "kubeInstallRoot", Description: "root", Type: domain.ParameterTypeString, Required: true,
			DefaultValue: "/approot1/paas/kube", Visibility: domain.ParameterPublic, EnvironmentPath: "kubernetes.installRoot",
		}},
	}
	proxy := domain.ComponentRelease{
		ID: "rel-proxy",
		Parameters: []domain.ParameterDefinition{{
			Name: "kubeRoot", Description: "imported", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal,
		}},
		Dependencies: []domain.ComponentDependency{{
			ID: "dep-1", UpstreamReleaseID: "rel-kubelet",
			ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}},
		}},
	}
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{
			{ID: "kubelet", ReleaseID: "rel-kubelet"},
			{ID: "proxy", ReleaseID: "rel-proxy", Values: map[string]any{"kubeRoot": "/should-not-win"}},
		},
		Edges: []domain.ScenarioEdge{{Source: "kubelet", Target: "proxy"}},
	}
	environment := map[string]any{
		"kubernetes": map[string]any{"installRoot": "/from-env"},
		"kubeRoot":   "/env-alias-should-not-win",
	}
	kubeletVars, kubeletProv, err := resolveOwnParameters(kubelet, graph.Nodes[0], environment, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if kubeletVars["kubeInstallRoot"] != "/from-env" || kubeletProv["kubeInstallRoot"].Source != parameterSourceEnvironment {
		t.Fatalf("kubelet vars=%#v prov=%#v", kubeletVars, kubeletProv)
	}
	proxyVars, proxyProv, err := resolveOwnParameters(proxy, graph.Nodes[1], environment, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := proxyVars["kubeRoot"]; leaked && proxyProv["kubeRoot"].Source == parameterSourceEnvironment {
		t.Fatalf("environment alias leaked into mapped target: %#v", proxyProv["kubeRoot"])
	}
	if err := applyParameterMappings(proxy, graph.Nodes[1], graph, map[string]domain.ComponentRelease{"kubelet": kubelet, "proxy": proxy}, map[string]map[string]any{"kubelet": kubeletVars}, proxyVars, proxyProv); err != nil {
		t.Fatal(err)
	}
	if proxyVars["kubeRoot"] != "/from-env" {
		t.Fatalf("mapped value=%v", proxyVars["kubeRoot"])
	}
	if proxyProv["kubeRoot"].Source != parameterSourceDependencyMapping || proxyProv["kubeRoot"].SourceNodeID != "kubelet" {
		t.Fatalf("provenance=%#v", proxyProv["kubeRoot"])
	}
}

func TestPlannerOnlyPassesDeclaredEnvironmentParameters(t *testing.T) {
	release := domain.ComponentRelease{Parameters: []domain.ParameterDefinition{
		{Name: "region", Description: "target region", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal},
		{Name: "installRoot", Description: "install root", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal, EnvironmentPath: "platform.installRoot"},
	}}
	environment := map[string]any{
		"region": "cn-east", "unrelated": "must-not-leak",
		"platform": map[string]any{"installRoot": "/opt/platform", "other": "must-not-leak"},
	}

	resolved, provenance, err := resolveOwnParameters(release, domain.ScenarioNode{}, environment, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved["region"] != "cn-east" || resolved["installRoot"] != "/opt/platform" {
		t.Fatalf("declared environment parameters=%#v", resolved)
	}
	if _, ok := resolved["unrelated"]; ok {
		t.Fatalf("undeclared environment parameter leaked: %#v", resolved)
	}
	if _, ok := resolved["platform"]; ok {
		t.Fatalf("environment container leaked: %#v", resolved)
	}
	if provenance["region"].Source != parameterSourceEnvironment || provenance["installRoot"].Source != parameterSourceEnvironment {
		t.Fatalf("provenance=%#v", provenance)
	}
}

func TestPlannerSupportsChainedPublicParameters(t *testing.T) {
	a := domain.ComponentRelease{ID: "a", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "root", Type: domain.ParameterTypeString, Required: true, DefaultValue: "/a", Visibility: domain.ParameterPublic}}}
	b := domain.ComponentRelease{ID: "b", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "root", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterPublic}}, Dependencies: []domain.ComponentDependency{{ID: "b-a", UpstreamReleaseID: "a", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "root", TargetParameter: "root"}}}}}
	c := domain.ComponentRelease{ID: "c", Parameters: []domain.ParameterDefinition{{Name: "root", Description: "root", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal}}, Dependencies: []domain.ComponentDependency{{ID: "c-b", UpstreamReleaseID: "b", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "root", TargetParameter: "root"}}}}}
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{{ID: "a", ReleaseID: "a"}, {ID: "b", ReleaseID: "b"}, {ID: "c", ReleaseID: "c"}},
		Edges: []domain.ScenarioEdge{{Source: "a", Target: "b"}, {Source: "b", Target: "c"}},
	}
	resolved := map[string]map[string]any{}
	byNode := map[string]domain.ComponentRelease{"a": a, "b": b, "c": c}
	for _, node := range graph.Nodes {
		release := byNode[node.ID]
		vars, prov, err := resolveOwnParameters(release, node, nil, nil, nil)
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
			{Name: "requiredTarget", Description: "x", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal},
			{Name: "optionalTarget", Description: "x", Type: domain.ParameterTypeString, Visibility: domain.ParameterInternal},
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
		Nodes: []domain.ScenarioNode{{ID: "up", ReleaseID: "up"}, {ID: "down", ReleaseID: "down"}},
		Edges: []domain.ScenarioEdge{{Source: "up", Target: "down"}},
	}
	vars, prov, err := resolveOwnParameters(downstream, graph.Nodes[1], nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = applyParameterMappings(downstream, graph.Nodes[1], graph, map[string]domain.ComponentRelease{"up": upstream}, map[string]map[string]any{"up": {}}, vars, prov)
	if err == nil || !strings.Contains(err.Error(), "required mapped parameter") {
		t.Fatalf("missing required mapping err=%v", err)
	}

	downstream.Dependencies[0].ParameterMappings = []domain.ParameterMapping{{UpstreamParameter: "optional", TargetParameter: "optionalTarget"}}
	vars, prov, err = resolveOwnParameters(downstream, graph.Nodes[1], nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyParameterMappings(downstream, graph.Nodes[1], graph, map[string]domain.ComponentRelease{"up": upstream}, map[string]map[string]any{"up": {}}, vars, prov); err != nil {
		t.Fatal(err)
	}
}

func TestDependencyFixturesAreRequiredAndTyped(t *testing.T) {
	release := domain.ComponentRelease{
		Parameters:   []domain.ParameterDefinition{{Name: "kubeRoot", Description: "root", Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal}},
		Dependencies: []domain.ComponentDependency{{ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "kubeInstallRoot", TargetParameter: "kubeRoot"}}}},
	}
	vars := map[string]any{}
	prov := map[string]resolvedParameter{}
	if err := applyDependencyFixtures(release, nil, vars, prov); err == nil {
		t.Fatal("missing fixture accepted")
	}
	if err := applyDependencyFixtures(release, map[string]any{"other": "/x"}, vars, prov); err == nil {
		t.Fatal("unknown fixture accepted")
	}
	if err := applyDependencyFixtures(release, map[string]any{"kubeRoot": 12}, vars, prov); err == nil {
		t.Fatal("typed fixture accepted")
	}
	if err := applyDependencyFixtures(release, map[string]any{"kubeRoot": "/approot1/paas/kube"}, vars, prov); err != nil {
		t.Fatal(err)
	}
	if vars["kubeRoot"] != "/approot1/paas/kube" || prov["kubeRoot"].Source != parameterSourceDependencyFixture {
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
