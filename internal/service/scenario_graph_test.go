package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestSaveScenarioGraphPersistsGeneratedDependencyAndRejectsReverseSequence(t *testing.T) {
	ctx := context.Background()
	platform, database := readinessTestPlatform(t)
	now := time.Now().UTC()
	action := func(id string) []domain.ActionDefinition {
		return []domain.ActionDefinition{{ID: id, Name: "Install", Kind: domain.ActionInstall, Playbook: "install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}}
	}
	upstream := domain.ComponentRelease{
		ID: "release-auto-upstream", ComponentID: "component-1", LineID: "line-upstream", LineName: "Upstream", Version: "1.0.0",
		Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{}, Actions: action("action-upstream"), CreatedAt: now, ReleasedAt: &now,
	}
	dependency := domain.ComponentDependency{ID: "dep-upstream", ReleaseID: "release-auto-downstream", UpstreamComponentID: "component-1", UpstreamReleaseID: upstream.ID}
	downstream := domain.ComponentRelease{
		ID: "release-auto-downstream", ComponentID: "component-1", LineID: "line-downstream", LineName: "Downstream", Version: "2.0.0",
		Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{}, Dependencies: []domain.ComponentDependency{dependency}, Actions: action("action-downstream"), CreatedAt: now, ReleasedAt: &now,
	}
	for _, release := range []domain.ComponentRelease{upstream, downstream} {
		if err := database.CreateComponentRelease(ctx, release); err != nil {
			t.Fatal(err)
		}
	}
	owner := domain.User{ID: "scenario-auto-owner", Name: "Scenario Owner", Role: domain.RoleScenarioOwner, CreatedAt: now}
	if err := database.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	scenario, err := platform.scenarios.Create(ctx, owner, domain.Scenario{Name: "Automatic dependencies", Slug: "automatic-dependencies"})
	if err != nil {
		t.Fatal(err)
	}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{
		{ID: "upstream", ReleaseID: upstream.ID, Action: domain.ActionInstall},
		{ID: "downstream", ReleaseID: downstream.ID, Action: domain.ActionInstall},
	}}
	saved, err := platform.scenarios.SaveGraph(ctx, owner, scenario.CurrentRevisionID, graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Graph.Edges) != 1 || saved.Graph.Edges[0].Kind != domain.ScenarioEdgeDependency || saved.Graph.Nodes[1].DependencySources[dependency.ID] != "upstream" {
		t.Fatalf("saved graph=%+v", saved.Graph)
	}

	// An incomplete order is saveable, but validation rejects execution readiness.
	ambiguous := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{
		{ID: "first", ReleaseID: upstream.ID, Action: domain.ActionInstall, HostGroup: "test_nodes"},
		{ID: "second", ReleaseID: upstream.ID, Action: domain.ActionInstall, HostGroup: "test_nodes"},
	}}
	if _, err := platform.scenarios.SaveGraph(ctx, owner, scenario.CurrentRevisionID, ambiguous); err != nil {
		t.Fatal(err)
	}
	issues, err := platform.scenarios.Validate(ctx, owner, scenario.CurrentRevisionID)
	if err != nil || !hasValidationIssue(issues, "execution_order_undetermined") {
		t.Fatalf("issues=%v err=%v", issues, err)
	}

	env := domain.Environment{ID: "order-env", Name: "Order test", OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	envRevision := domain.EnvironmentRevision{ID: "order-env-r1", EnvironmentID: env.ID, Revision: 1, Facts: map[string]any{}, Inventory: []byte(`{"hosts":[]}`), CreatedAt: now}
	if err := database.CreateEnvironment(ctx, env, envRevision); err != nil {
		t.Fatal(err)
	}
	assertOrderError := func(err error) {
		t.Helper()
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("expected order validation, got %v", err)
		}
		details, ok := validation.Details.([]domain.ValidationIssue)
		if !ok || !hasValidationIssue(details, "execution_order_undetermined") {
			t.Fatalf("unexpected details: %v", validation.Details)
		}
	}
	_, err = platform.execution.StartScenarioTest(ctx, owner, scenario.CurrentRevisionID, env.ID)
	assertOrderError(err)
	set, err := platform.releases.CandidateReleaseSet(ctx, owner, scenario.CurrentRevisionID)
	if err != nil || set.Ready || !hasValidationIssue(set.Issues, "execution_order_undetermined") {
		t.Fatalf("candidate set=%+v err=%v", set, err)
	}
	if err := database.SetScenarioRevisionStatus(ctx, scenario.CurrentRevisionID, []domain.RevisionStatus{domain.RevisionDraft}, domain.RevisionTestPassed, now); err != nil {
		t.Fatal(err)
	}
	_, err = platform.releases.PublishScenario(ctx, owner, scenario.CurrentRevisionID)
	// A legacy test_passed label cannot bypass current acceptance evidence.
	// Graph ordering is independently asserted by validation and both run paths.
	if !errors.Is(err, domain.ErrConflict) || !strings.Contains(err.Error(), "场景业务验收作业必填") {
		t.Fatalf("unproven scenario publication=%v", err)
	}
	if err := database.SetScenarioRevisionStatus(ctx, scenario.CurrentRevisionID, []domain.RevisionStatus{domain.RevisionTestPassed}, domain.RevisionReleased, now); err != nil {
		t.Fatal(err)
	}
	_, err = platform.execution.StartScenarioRun(ctx, owner, scenario.CurrentRevisionID, env.ID)
	assertOrderError(err)
	// Restore the fixture to exercise editing; production never rewrites a released graph.
	if err := database.SetScenarioRevisionStatus(ctx, scenario.CurrentRevisionID, []domain.RevisionStatus{domain.RevisionReleased}, domain.RevisionDraft, now); err != nil {
		t.Fatal(err)
	}
	ambiguous.Edges = []domain.ScenarioEdge{{ID: "owner-choice", Source: "second", Target: "first", Kind: domain.ScenarioEdgeSequence}}
	if _, err := platform.scenarios.SaveGraph(ctx, owner, scenario.CurrentRevisionID, ambiguous); err != nil {
		t.Fatal(err)
	}
	issues, err = platform.scenarios.Validate(ctx, owner, scenario.CurrentRevisionID)
	if err != nil || hasValidationIssue(issues, "execution_order_undetermined") {
		t.Fatalf("resolved issues=%v err=%v", issues, err)
	}
	graph.Edges = []domain.ScenarioEdge{{ID: "reverse", Source: "downstream", Target: "upstream", Kind: domain.ScenarioEdgeSequence}}
	if _, err := platform.scenarios.SaveGraph(ctx, owner, scenario.CurrentRevisionID, graph); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("reverse sequence error=%v", err)
	} else {
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("reverse sequence details=%v", err)
		}
		issues, ok := validation.Details.([]domain.ValidationIssue)
		if !ok || !hasValidationIssue(issues, "sequence_conflicts_dependency") {
			t.Fatalf("reverse sequence details=%v", err)
		}
	}
}

func hasValidationIssue(issues []domain.ValidationIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestNormalizeScenarioGraphGeneratesDependenciesAndKeepsSequences(t *testing.T) {
	dependency := domain.ComponentDependency{ID: "dep-runtime", UpstreamReleaseID: "runtime-r1"}
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{
			{ID: "runtime", ReleaseID: "runtime-r1", Action: domain.ActionInstall},
			{ID: "control", ReleaseID: "control-r1", Action: domain.ActionInstall},
			{ID: "dns", ReleaseID: "dns-r1", Action: domain.ActionInstall},
		},
		Edges: []domain.ScenarioEdge{{ID: "manual", Source: "control", Target: "dns", Kind: domain.ScenarioEdgeSequence}},
	}
	normalized, issues := normalizeScenarioGraph(graph, map[string]domain.ComponentRelease{
		"runtime": {ID: "runtime-r1"},
		"control": {ID: "control-r1", Dependencies: []domain.ComponentDependency{dependency}},
		"dns":     {ID: "dns-r1"},
	})
	if len(issues) != 0 {
		t.Fatalf("issues=%v", issues)
	}
	if got := normalized.Nodes[1].DependencySources[dependency.ID]; got != "runtime" {
		t.Fatalf("dependency source=%q want runtime", got)
	}
	if len(normalized.Edges) != 2 {
		t.Fatalf("edges=%v", normalized.Edges)
	}
	var generated domain.ScenarioEdge
	for _, edge := range normalized.Edges {
		if edge.Kind == domain.ScenarioEdgeDependency {
			generated = edge
		}
	}
	if generated.Source != "runtime" || generated.Target != "control" || generated.DependencyID != dependency.ID {
		t.Fatalf("generated edge=%+v", generated)
	}
	if generated.ID != scenarioDependencyEdgeID(dependency.ID, "runtime", "control") {
		t.Fatalf("generated id=%q", generated.ID)
	}
}

func TestNormalizeScenarioGraphReversesRollbackDependencies(t *testing.T) {
	dependency := domain.ComponentDependency{ID: "dep-runtime", UpstreamReleaseID: "runtime-r1"}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{
		{ID: "runtime", ReleaseID: "runtime-r1", Action: domain.ActionRollback},
		{ID: "control", ReleaseID: "control-r1", Action: domain.ActionRollback},
	}}
	normalized, issues := normalizeScenarioGraph(graph, map[string]domain.ComponentRelease{
		"runtime": {ID: "runtime-r1"},
		"control": {ID: "control-r1", Dependencies: []domain.ComponentDependency{dependency}},
	})
	if len(issues) != 0 || len(normalized.Edges) != 1 {
		t.Fatalf("edges=%+v issues=%+v", normalized.Edges, issues)
	}
	if edge := normalized.Edges[0]; edge.Source != "control" || edge.Target != "runtime" {
		t.Fatalf("reverse dependency edge=%+v", edge)
	}
	if normalized.Nodes[1].DependencySources[dependency.ID] != "runtime" {
		t.Fatalf("logical parameter source was not retained: %+v", normalized.Nodes[1])
	}
}

func TestNormalizeScenarioGraphRejectsMixedLifecycleDirections(t *testing.T) {
	dependency := domain.ComponentDependency{ID: "dep-runtime", UpstreamReleaseID: "runtime-r1"}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{
		{ID: "runtime", ReleaseID: "runtime-r1", Action: domain.ActionInstall},
		{ID: "control", ReleaseID: "control-r1", Action: domain.ActionRollback},
	}}
	normalized, issues := normalizeScenarioGraph(graph, map[string]domain.ComponentRelease{
		"runtime": {ID: "runtime-r1"},
		"control": {ID: "control-r1", Dependencies: []domain.ComponentDependency{dependency}},
	})
	if !hasValidationIssue(issues, "mixed_lifecycle_direction") || len(normalized.Edges) != 0 {
		t.Fatalf("edges=%+v issues=%+v", normalized.Edges, issues)
	}
}

func TestScenarioSequenceTopologyIssuesRejectsExistingReachability(t *testing.T) {
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Edges: []domain.ScenarioEdge{
			{ID: "a-b", Source: "a", Target: "b", Kind: domain.ScenarioEdgeDependency, DependencyID: "dep-b"},
			{ID: "b-c", Source: "b", Target: "c", Kind: domain.ScenarioEdgeSequence},
			{ID: "a-c", Source: "a", Target: "c", Kind: domain.ScenarioEdgeSequence},
		},
	}
	if issues := scenarioSequenceTopologyIssues(graph); !hasValidationIssue(issues, "sequence_redundant") {
		t.Fatalf("issues=%+v", issues)
	}
}

func TestNormalizeScenarioGraphRejectsUntypedEdges(t *testing.T) {
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "a"}, {ID: "b"}}, Edges: []domain.ScenarioEdge{{ID: "old", Source: "a", Target: "b"}}}
	normalized, _ := normalizeScenarioGraph(graph, nil)
	if !hasValidationIssue(domain.ValidateGraph(normalized), "invalid_edge_kind") {
		t.Fatal("untyped edge was accepted")
	}
}

func TestNormalizeScenarioGraphRequiresAChoiceForDuplicateUpstreams(t *testing.T) {
	dependency := domain.ComponentDependency{ID: "dep-runtime", UpstreamReleaseID: "runtime-r1"}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{
		{ID: "runtime-a", ReleaseID: "runtime-r1", Action: domain.ActionInstall},
		{ID: "runtime-b", ReleaseID: "runtime-r1", Action: domain.ActionInstall},
		{ID: "control", ReleaseID: "control-r1", Action: domain.ActionInstall},
	}}
	releases := map[string]domain.ComponentRelease{
		"runtime-a": {ID: "runtime-r1"}, "runtime-b": {ID: "runtime-r1"},
		"control": {ID: "control-r1", Dependencies: []domain.ComponentDependency{dependency}},
	}

	normalized, issues := normalizeScenarioGraph(graph, releases)
	if len(issues) != 1 || issues[0].Code != "dependency_source_required" {
		t.Fatalf("issues=%v", issues)
	}
	if len(normalized.Edges) != 0 {
		t.Fatalf("unexpected edges=%v", normalized.Edges)
	}

	graph.Nodes[2].DependencySources = map[string]string{dependency.ID: "runtime-b"}
	normalized, issues = normalizeScenarioGraph(graph, releases)
	if len(issues) != 0 || len(normalized.Edges) != 1 || normalized.Edges[0].Source != "runtime-b" {
		t.Fatalf("normalized=%+v issues=%v", normalized, issues)
	}
}

func TestNormalizeScenarioGraphDoesNotInferAmbiguousDependencyFromSequence(t *testing.T) {
	dependency := domain.ComponentDependency{ID: "dep-runtime", UpstreamReleaseID: "runtime-r1"}
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{
			{ID: "runtime-a", ReleaseID: "runtime-r1", Action: domain.ActionInstall},
			{ID: "runtime-b", ReleaseID: "runtime-r1", Action: domain.ActionInstall},
			{ID: "control", ReleaseID: "control-r1", Action: domain.ActionInstall},
		},
		Edges: []domain.ScenarioEdge{{ID: "manual", Source: "runtime-a", Target: "control", Kind: domain.ScenarioEdgeSequence}},
	}
	normalized, issues := normalizeScenarioGraph(graph, map[string]domain.ComponentRelease{
		"runtime-a": {ID: "runtime-r1"}, "runtime-b": {ID: "runtime-r1"},
		"control": {ID: "control-r1", Dependencies: []domain.ComponentDependency{dependency}},
	})
	if !hasValidationIssue(issues, "dependency_source_required") || len(normalized.Edges) != 1 {
		t.Fatalf("edges=%v issues=%v", normalized.Edges, issues)
	}
	if edge := normalized.Edges[0]; edge.Kind != domain.ScenarioEdgeSequence || edge.Source != "runtime-a" {
		t.Fatalf("edge=%+v", edge)
	}
}

func TestNormalizeScenarioGraphSkipsVerifyInstallDependenciesWithoutMappings(t *testing.T) {
	dependency := domain.ComponentDependency{ID: "dep-runtime", UpstreamReleaseID: "runtime-r1"}
	graph := domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "verify", ReleaseID: "control-r1", Action: domain.ActionVerify}}}
	normalized, issues := normalizeScenarioGraph(graph, map[string]domain.ComponentRelease{
		"verify": {ID: "control-r1", Dependencies: []domain.ComponentDependency{dependency}},
	})
	if len(issues) != 0 || len(normalized.Edges) != 0 || len(normalized.Nodes[0].DependencySources) != 0 {
		t.Fatalf("normalized=%+v issues=%v", normalized, issues)
	}
}

func TestTypedSequenceEdgeCannotSupplyMappedDependency(t *testing.T) {
	dependency := domain.ComponentDependency{ID: "dep-runtime", UpstreamReleaseID: "runtime-r1"}
	graph := domain.ScenarioGraph{
		Nodes: []domain.ScenarioNode{
			{ID: "runtime", ReleaseID: "runtime-r1"},
			{ID: "control", ReleaseID: "control-r1", DependencySources: map[string]string{dependency.ID: "runtime"}},
		},
		Edges: []domain.ScenarioEdge{{ID: "manual", Source: "runtime", Target: "control", Kind: domain.ScenarioEdgeSequence}},
	}
	_, err := selectDependencySource(graph.Nodes[1], dependency, graph, map[string]domain.ComponentRelease{
		"runtime": {ID: "runtime-r1"}, "control": {ID: "control-r1"},
	})
	if err == nil {
		t.Fatal("sequence edge unexpectedly satisfied a mapped dependency")
	}
}

func TestScenarioExecutionOrderRequiresOwnerChoice(t *testing.T) {
	nodes := []domain.ScenarioNode{}
	for _, id := range []string{"a", "b", "c", "d"} {
		nodes = append(nodes, domain.ScenarioNode{ID: id, ReleaseID: "release", Action: domain.ActionInstall, HostGroup: "test_nodes"})
	}
	edge := func(a, b string) domain.ScenarioEdge {
		return domain.ScenarioEdge{ID: a + b, Source: a, Target: b, Kind: domain.ScenarioEdgeSequence}
	}
	for _, test := range []struct {
		name  string
		graph domain.ScenarioGraph
		want  string
	}{
		{"independent", domain.ScenarioGraph{Nodes: nodes[:2]}, "ab"},
		{"fork", domain.ScenarioGraph{Nodes: nodes, Edges: []domain.ScenarioEdge{edge("a", "b"), edge("a", "c"), edge("b", "d"), edge("c", "d")}}, "bc"},
		{"join", domain.ScenarioGraph{Nodes: nodes[:3], Edges: []domain.ScenarioEdge{edge("a", "c"), edge("b", "c")}}, "ab"},
		{"chain", domain.ScenarioGraph{Nodes: nodes[:3], Edges: []domain.ScenarioEdge{edge("a", "b"), edge("b", "c")}}, ""},
		{"single", domain.ScenarioGraph{Nodes: nodes[:1]}, ""},
		{"cycle", domain.ScenarioGraph{Nodes: nodes, Edges: []domain.ScenarioEdge{edge("a", "b"), edge("b", "a")}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			for attempt := 0; attempt < 2; attempt++ {
				issues := scenarioExecutionOrderIssues(test.graph)
				got := ""
				for _, issue := range issues {
					if issue.Code != "execution_order_undetermined" {
						t.Fatal(issue)
					}
					got += issue.NodeID
				}
				if got != test.want {
					t.Fatalf("got %q want %q", got, test.want)
				}
				for i, j := 0, len(test.graph.Nodes)-1; i < j; i, j = i+1, j-1 {
					test.graph.Nodes[i], test.graph.Nodes[j] = test.graph.Nodes[j], test.graph.Nodes[i]
				}
			}
		})
	}
}
