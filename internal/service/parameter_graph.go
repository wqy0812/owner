package service

import (
	"context"
	"fmt"
	"sort"

	"codex/platform-demo/internal/domain"
)

type parameterVertex struct{ NodeID, Name string }

// Parameters are resolved at field granularity. Two components may refer to
// each other's independent configuration inputs without forming a value cycle.
func scenarioParameterSources(graph domain.ScenarioGraph, releases map[string]domain.ComponentRelease) (map[parameterVertex]parameterVertex, error) {
	sources := map[parameterVertex]parameterVertex{}
	for _, node := range graph.Nodes {
		release := releases[node.ID]
		for _, dep := range release.Dependencies {
			if len(dep.ParameterMappings) == 0 {
				continue
			}
			sourceID, err := selectDependencySource(node, dep, graph, releases)
			if err != nil {
				return nil, err
			}
			for _, mapping := range dep.ParameterMappings {
				target := parameterVertex{node.ID, mapping.TargetParameter}
				source := parameterVertex{sourceID, mapping.UpstreamParameter}
				if _, exists := sources[target]; exists {
					return nil, fmt.Errorf("%w: duplicate parameter source for %s.%s", domain.ErrInvalid, node.ID, target.Name)
				}
				sp, exists := domain.ParameterByName(releases[sourceID].Parameters, source.Name)
				tp, declared := domain.ParameterByName(release.Parameters, target.Name)
				if !exists || !declared || sp.Visibility != domain.ParameterPublic || sp.Type != tp.Type || tp.ValueProvider != domain.ParameterProviderUpstreamMapping {
					return nil, fmt.Errorf("%w: invalid parameter reference %s.%s", domain.ErrInvalid, node.ID, target.Name)
				}
				sources[target] = source
			}
		}
	}
	state := map[parameterVertex]int{}
	var visit func(parameterVertex) error
	visit = func(v parameterVertex) error {
		if state[v] == 1 {
			return fmt.Errorf("%w: parameter reference cycle at %s.%s", domain.ErrInvalid, v.NodeID, v.Name)
		}
		if state[v] == 2 {
			return nil
		}
		state[v] = 1
		if source, exists := sources[v]; exists {
			if err := visit(source); err != nil {
				return err
			}
		}
		state[v] = 2
		return nil
	}
	for _, node := range graph.Nodes {
		for _, param := range releases[node.ID].Parameters {
			if err := visit(parameterVertex{node.ID, param.Name}); err != nil {
				return nil, err
			}
		}
	}
	return sources, nil
}

func resolveScenarioParameters(graph domain.ScenarioGraph, releases map[string]domain.ComponentRelease, environment domain.EnvironmentRevision) (map[string]map[string]any, map[string]map[string]resolvedParameter, error) {
	sources, err := scenarioParameterSources(graph, releases)
	if err != nil {
		return nil, nil, err
	}
	values := map[string]map[string]any{}
	provenance := map[string]map[string]resolvedParameter{}
	for _, node := range graph.Nodes {
		v, p, err := resolveOwnParameters(releases[node.ID], node, environment, false)
		if err != nil {
			return nil, nil, fmt.Errorf("node %s: %w", node.ID, err)
		}
		values[node.ID], provenance[node.ID] = v, p
	}
	done := map[parameterVertex]bool{}
	var resolve func(parameterVertex)
	resolve = func(v parameterVertex) {
		if done[v] {
			return
		}
		done[v] = true
		source, mapped := sources[v]
		if !mapped {
			return
		}
		resolve(source)
		value, exists := values[source.NodeID][source.Name]
		if !exists {
			return
		}
		copied := deepCopy(value)
		values[v.NodeID][v.Name] = copied
		provenance[v.NodeID][v.Name] = resolvedParameter{Value: copied, Source: parameterSourceDependencyMapping, SourceNodeID: source.NodeID, UpstreamParameter: source.Name, TargetParameter: v.Name}
	}
	for _, node := range graph.Nodes {
		for _, param := range releases[node.ID].Parameters {
			resolve(parameterVertex{node.ID, param.Name})
		}
		if err := validateResolvedParameters(releases[node.ID].Parameters, values[node.ID]); err != nil {
			return nil, nil, fmt.Errorf("node %s: %w", node.ID, err)
		}
	}
	return values, provenance, nil
}

// validateReferenceContracts checks the parameter graph independently of the
// execution graph, using synthetic nodes for exact Release contracts.
func validateReferenceContracts(releases map[string]domain.ComponentRelease) error {
	graph := domain.ScenarioGraph{}
	ids := make([]string, 0, len(releases))
	for id := range releases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		graph.Nodes = append(graph.Nodes, domain.ScenarioNode{ID: id, ReleaseID: id})
	}
	normalized, issues := normalizeScenarioGraph(graph, releases)
	if len(issues) > 0 {
		return fmt.Errorf("%w: %s", domain.ErrInvalid, issues[0].Message)
	}
	if _, err := topologicalNodes(normalized); err != nil {
		return err
	}
	_, err := scenarioParameterSources(normalized, releases)
	return err
}
func (p *ReleaseRules) validateConfigurationReferenceClosure(ctx context.Context, release domain.ComponentRelease) error {
	hasConfiguration := false
	releases := map[string]domain.ComponentRelease{release.ID: release}
	queue := []domain.ComponentRelease{release}
	for len(queue) > 0 {
		r := queue[0]
		queue = queue[1:]
		for _, dep := range r.Dependencies {
			if dep.Kind == domain.DependencyConfiguration {
				hasConfiguration = true
			}
			if _, ok := releases[dep.UpstreamReleaseID]; ok {
				continue
			}
			source, err := p.store.GetComponentRelease(ctx, dep.UpstreamReleaseID)
			if err != nil {
				return err
			}
			releases[source.ID] = source
			queue = append(queue, source)
		}
	}
	if !hasConfiguration {
		return nil
	}
	return validateReferenceContracts(releases)
}
func validateImportedParameterReferences(entries map[string]ComponentImportEntry) error {
	releases := map[string]domain.ComponentRelease{}
	for slug, entry := range entries {
		release := domain.ComponentRelease{ID: slug, Parameters: entry.Release.Parameters}
		for _, dep := range entry.Release.Dependencies {
			release.Dependencies = append(release.Dependencies, domain.ComponentDependency{ID: slug + ":" + dep.ComponentSlug, Kind: dep.Kind, UpstreamReleaseID: dep.ComponentSlug, ParameterMappings: dep.ParameterMappings})
		}
		releases[slug] = release
	}
	return validateReferenceContracts(releases)
}
