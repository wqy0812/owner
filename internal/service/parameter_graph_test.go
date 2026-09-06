package service

import (
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
)

func configurationReferenceFixture() (domain.ScenarioGraph, map[string]domain.ComponentRelease, domain.EnvironmentRevision) {
	own := func(name string) domain.ParameterDefinition {
		return domain.ParameterDefinition{Name: name, Description: name, Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderEnvironmentOwner, Modifiable: true, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}}
	}
	mapped := func(name string) domain.ParameterDefinition {
		return domain.ParameterDefinition{Name: name, Description: name, Type: domain.ParameterTypeString, Required: true, Visibility: domain.ParameterInternal, ValueProvider: domain.ParameterProviderUpstreamMapping}
	}
	kubelet := domain.ComponentRelease{ID: "kubelet-r1", ComponentID: "kubelet", Parameters: []domain.ParameterDefinition{own("data_dir"), mapped("api_endpoint")}, Dependencies: []domain.ComponentDependency{{ID: "api-order", UpstreamComponentID: "api", UpstreamReleaseID: "api-r1", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "endpoint", TargetParameter: "api_endpoint"}}}}}
	api := domain.ComponentRelease{ID: "api-r1", ComponentID: "api", Parameters: []domain.ParameterDefinition{own("endpoint"), mapped("kubelet_root")}, Dependencies: []domain.ComponentDependency{{ID: "kubelet-config", Kind: domain.DependencyConfiguration, UpstreamComponentID: "kubelet", UpstreamReleaseID: "kubelet-r1", ParameterMappings: []domain.ParameterMapping{{UpstreamParameter: "data_dir", TargetParameter: "kubelet_root"}}}}}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "kubelet", ReleaseID: kubelet.ID, Action: domain.ActionInstall}, {ID: "api", ReleaseID: api.ID, Action: domain.ActionInstall}}}
	releases := map[string]domain.ComponentRelease{"kubelet": kubelet, "api": api}
	env := domain.EnvironmentRevision{Parameters: map[string]any{"release:kubelet-r1:data_dir": "/data/kubelet", "release:api-r1:endpoint": "api.example:6443"}}
	return graph, releases, env
}
func TestMutualConfigurationReferencesKeepExecutionAcyclic(t *testing.T) {
	graph, releases, env := configurationReferenceFixture()
	graph, issues := normalizeScenarioGraph(graph, releases)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	order, err := topologicalNodes(graph)
	if err != nil || len(graph.Edges) != 1 || order[0].ID != "api" {
		t.Fatalf("execution: %+v %v", graph, err)
	}
	values, provenance, err := resolveScenarioParameters(graph, releases, env)
	if err != nil {
		t.Fatal(err)
	}
	if values["api"]["kubelet_root"] != "/data/kubelet" || values["kubelet"]["api_endpoint"] != "api.example:6443" || provenance["api"]["kubelet_root"].SourceNodeID != "kubelet" {
		t.Fatalf("values=%v provenance=%v", values, provenance)
	}
	for i := range graph.Nodes {
		graph.Nodes[i].Action = domain.ActionRollback
	}
	graph, issues = normalizeScenarioGraph(graph, releases)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	order, err = topologicalNodes(graph)
	if err != nil || order[0].ID != "kubelet" {
		t.Fatal("rollback must reverse execution only")
	}
	values, _, err = resolveScenarioParameters(graph, releases, env)
	if err != nil || values["api"]["kubelet_root"] != "/data/kubelet" {
		t.Fatalf("rollback values %v %v", values, err)
	}
}
func TestConfigurationReferencesRejectFieldCyclesPrivateTypesAndOverrides(t *testing.T) {
	for _, name := range []string{"cycle", "private", "type", "override", "missing-value", "execution-cycle"} {
		t.Run(name, func(t *testing.T) {
			graph, releases, env := configurationReferenceFixture()
			api, kubelet := releases["api"], releases["kubelet"]
			switch name {
			case "cycle":
				api.Parameters[1].Visibility = domain.ParameterPublic
				kubelet.Parameters[0].ValueProvider = domain.ParameterProviderUpstreamMapping
				kubelet.Parameters[0].Modifiable = false
				kubelet.Parameters[0].EnvironmentBinding = nil
				kubelet.Dependencies[0].ParameterMappings = append(kubelet.Dependencies[0].ParameterMappings, domain.ParameterMapping{UpstreamParameter: "kubelet_root", TargetParameter: "data_dir"})
			case "private":
				kubelet.Parameters[0].Visibility = domain.ParameterInternal
			case "type":
				kubelet.Parameters[0].Type = domain.ParameterTypeBoolean
			case "override":
				graph.Nodes[1].ParameterValues = map[string]any{"kubelet_root": "/override"}
			case "missing-value":
				delete(env.Parameters, "release:kubelet-r1:data_dir")
			case "execution-cycle":
				api.Dependencies[0].Kind = ""
			}
			releases["api"], releases["kubelet"] = api, kubelet
			graph, _ = normalizeScenarioGraph(graph, releases)
			if name == "execution-cycle" {
				if _, err := topologicalNodes(graph); err == nil {
					t.Fatal("execution cycle accepted")
				}
				return
			}
			_, _, err := resolveScenarioParameters(graph, releases, env)
			if err == nil {
				t.Fatal("invalid parameter reference accepted")
			}
			if name == "cycle" && !strings.Contains(err.Error(), "cycle") {
				t.Fatal(err)
			}
		})
	}
}
func TestConfigurationReferenceRequiresAnExactUnambiguousNode(t *testing.T) {
	graph, releases, _ := configurationReferenceFixture()
	extra := graph.Nodes[0]
	extra.ID = "kubelet-other"
	graph.Nodes = append(graph.Nodes, extra)
	releases[extra.ID] = releases["kubelet"]
	graph, issues := normalizeScenarioGraph(graph, releases)
	if !hasValidationIssue(issues, "dependency_source_required") {
		t.Fatal(issues)
	}
	graph.Nodes[1].DependencySources = map[string]string{"kubelet-config": "kubelet-other"}
	graph, issues = normalizeScenarioGraph(graph, releases)
	if len(issues) > 0 || graph.Nodes[1].DependencySources["kubelet-config"] != "kubelet-other" {
		t.Fatal(issues)
	}
	graph.Nodes[2].ReleaseID = "wrong-version"
	releases[extra.ID] = domain.ComponentRelease{ID: "wrong-version"}
	graph, issues = normalizeScenarioGraph(graph, releases)
	if graph.Nodes[1].DependencySources["kubelet-config"] == "kubelet-other" {
		t.Fatal("stale source retained")
	}
}
