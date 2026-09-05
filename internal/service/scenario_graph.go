package service

import (
	"fmt"
	"sort"

	"codex/platform-demo/internal/domain"
)

func scenarioDependencyEdgeID(dependencyID, sourceID, targetID string) string {
	return fmt.Sprintf("dependency:%s:%s:%s", dependencyID, sourceID, targetID)
}

func scenarioDependenciesForNode(node domain.ScenarioNode, release domain.ComponentRelease) []domain.ComponentDependency {
	if node.Action != domain.ActionVerify {
		return release.Dependencies
	}
	dependencies := make([]domain.ComponentDependency, 0, len(release.Dependencies))
	for _, dependency := range release.Dependencies {
		if len(dependency.ParameterMappings) > 0 {
			dependencies = append(dependencies, dependency)
		}
	}
	return dependencies
}

func reverseScenarioDependencies(action domain.ActionKind) bool {
	return action == domain.ActionRollback || action == domain.ActionUninstall
}

func scenarioMutationDirection(action domain.ActionKind) int {
	switch action {
	case domain.ActionInstall, domain.ActionUpgrade, domain.ActionConfigure:
		return 1
	case domain.ActionRollback, domain.ActionUninstall:
		return -1
	default:
		return 0
	}
}

// normalizeScenarioGraph makes Release dependencies authoritative while retaining
// user-authored sequence edges. Missing or ambiguous dependencies are returned as
// validation issues so an incomplete Draft can still be saved.
func normalizeScenarioGraph(graph domain.ScenarioGraph, releaseByNode map[string]domain.ComponentRelease) (domain.ScenarioGraph, []domain.ValidationIssue) {
	normalized := graph
	normalized.Nodes = append([]domain.ScenarioNode(nil), graph.Nodes...)

	dependencyEdges := make([]domain.ScenarioEdge, 0)
	issues := make([]domain.ValidationIssue, 0)
	for nodeIndex := range normalized.Nodes {
		node := &normalized.Nodes[nodeIndex]
		release, ok := releaseByNode[node.ID]
		if !ok {
			continue
		}
		activeDependencies := scenarioDependenciesForNode(*node, release)
		nextSources := make(map[string]string, len(activeDependencies))
		for _, dependency := range activeDependencies {
			candidates := make([]string, 0)
			for _, candidate := range normalized.Nodes {
				if candidate.ID != node.ID && candidate.ReleaseID == dependency.UpstreamReleaseID {
					candidates = append(candidates, candidate.ID)
				}
			}
			sort.Strings(candidates)
			if len(candidates) == 0 {
				issues = append(issues, domain.ValidationIssue{
					Code: "missing_dependency_node", Message: "locked upstream component release is absent from the graph", NodeID: node.ID,
				})
				continue
			}

			selected := node.DependencySources[dependency.ID]
			invalidSelected := selected != "" && !containsNodeID(candidates, selected)
			if invalidSelected {
				selected = ""
			}
			if selected == "" {
				switch {
				case len(candidates) == 1:
					selected = candidates[0]
				default:
					code, message := "dependency_source_required", fmt.Sprintf("node %s must choose a source for dependency %s", node.ID, dependency.ID)
					if invalidSelected {
						code, message = "dependency_source_invalid", fmt.Sprintf("selected source for dependency %s is no longer a matching upstream node", dependency.ID)
					}
					issues = append(issues, domain.ValidationIssue{
						Code: code, Message: message, NodeID: node.ID,
					})
				}
			}
			if selected == "" {
				continue
			}
			nextSources[dependency.ID] = selected
			if dependency.Kind == domain.DependencyConfiguration {
				continue
			}
			selectedNode := domain.ScenarioNode{}
			for _, candidate := range normalized.Nodes {
				if candidate.ID == selected {
					selectedNode = candidate
					break
				}
			}
			if sourceDirection, targetDirection := scenarioMutationDirection(selectedNode.Action), scenarioMutationDirection(node.Action); sourceDirection != 0 && targetDirection != 0 && sourceDirection != targetDirection {
				issues = append(issues, domain.ValidationIssue{Code: "mixed_lifecycle_direction", Message: "a dependency chain cannot mix forward and reverse lifecycle actions", NodeID: node.ID})
				continue
			}
			source, target := selected, node.ID
			if reverseScenarioDependencies(node.Action) {
				source, target = node.ID, selected
			}
			edge := domain.ScenarioEdge{
				ID: scenarioDependencyEdgeID(dependency.ID, source, target), Source: source, Target: target,
				Kind: domain.ScenarioEdgeDependency, DependencyID: dependency.ID,
			}
			dependencyEdges = append(dependencyEdges, edge)
		}
		node.DependencySources = nextSources
	}
	sequenceEdges := make([]domain.ScenarioEdge, 0, len(graph.Edges))
	seenPairs := map[string]bool{}
	for _, edge := range graph.Edges {
		if edge.Kind == domain.ScenarioEdgeDependency {
			continue
		}
		pair := edge.Source + "\x00" + edge.Target
		if seenPairs[pair] {
			continue
		}
		if edge.Kind == domain.ScenarioEdgeSequence {
			edge.DependencyID = ""
		}
		seenPairs[pair] = true
		sequenceEdges = append(sequenceEdges, edge)
	}

	normalized.Edges = append(sequenceEdges, dependencyEdges...)
	sort.SliceStable(normalized.Edges, func(i, j int) bool {
		left, right := normalized.Edges[i], normalized.Edges[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Source != right.Source {
			return left.Source < right.Source
		}
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		return left.ID < right.ID
	})
	return normalized, issues
}

func scenarioSequenceDependencyIssues(graph domain.ScenarioGraph) []domain.ValidationIssue {
	dependencyGraph := domain.ScenarioGraph{Nodes: graph.Nodes}
	for _, edge := range graph.Edges {
		if edge.Kind == domain.ScenarioEdgeDependency {
			dependencyGraph.Edges = append(dependencyGraph.Edges, edge)
		}
	}
	reachable := graphReachability(dependencyGraph)
	issues := make([]domain.ValidationIssue, 0)
	for _, edge := range graph.Edges {
		if edge.Kind == domain.ScenarioEdgeSequence && reachable[edge.Target][edge.Source] {
			issues = append(issues, domain.ValidationIssue{
				Code: "sequence_conflicts_dependency", Message: "manual sequence edge conflicts with the Release dependency direction", NodeID: edge.Target,
			})
		}
	}
	return issues
}

func scenarioSequenceTopologyIssues(graph domain.ScenarioGraph) []domain.ValidationIssue {
	issues := make([]domain.ValidationIssue, 0)
	for index, edge := range graph.Edges {
		if edge.Kind != domain.ScenarioEdgeSequence {
			continue
		}
		without := domain.ScenarioGraph{Nodes: graph.Nodes, Edges: make([]domain.ScenarioEdge, 0, len(graph.Edges)-1)}
		without.Edges = append(without.Edges, graph.Edges[:index]...)
		without.Edges = append(without.Edges, graph.Edges[index+1:]...)
		if graphReachability(without)[edge.Source][edge.Target] {
			issues = append(issues, domain.ValidationIssue{
				Code: "sequence_redundant", Message: "manual sequence edge duplicates an existing dependency or sequence path", NodeID: edge.Target,
			})
		}
	}
	return issues
}

func containsNodeID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
