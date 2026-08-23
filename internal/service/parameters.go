package service

import (
	"fmt"
	"sort"
	"strings"

	"codex/platform-demo/internal/domain"
)

const (
	parameterSourceDefault           = "default"
	parameterSourceNodeValue         = "node_value"
	parameterSourceRunInput          = "run_input"
	parameterSourceDependencyMapping = "dependency_mapping"
	parameterSourceDependencyFixture = "dependency_fixture"
)

type resolvedParameter struct {
	Value             any    `json:"value"`
	Source            string `json:"source"`
	SourceNodeID      string `json:"sourceNodeId,omitempty"`
	UpstreamParameter string `json:"upstreamParameter,omitempty"`
	TargetParameter   string `json:"targetParameter,omitempty"`
}

func validateReleaseParameters(release domain.ComponentRelease) error {
	seen := map[string]domain.ParameterDefinition{}
	for _, parameter := range release.Parameters {
		name := strings.TrimSpace(parameter.Name)
		if name == "" {
			return fmt.Errorf("%w: parameter name is required", domain.ErrInvalid)
		}
		if strings.TrimSpace(parameter.Description) == "" {
			return fmt.Errorf("%w: parameter %q requires a description", domain.ErrInvalid, name)
		}
		if !parameter.Type.Valid() {
			return fmt.Errorf("%w: parameter %q has invalid type %q", domain.ErrInvalid, name, parameter.Type)
		}
		if !parameter.Visibility.Valid() {
			return fmt.Errorf("%w: parameter %q visibility must be internal or public", domain.ErrInvalid, name)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: duplicate parameter %q", domain.ErrInvalid, name)
		}
		if isSensitiveKey(name) {
			return fmt.Errorf("%w: sensitive parameter %q must use a CredentialRef", domain.ErrInvalid, name)
		}
		if parameter.HasDefault() && !matchesParameterType(parameter.DefaultValue, string(parameter.Type)) {
			return fmt.Errorf("%w: default value for %q must be of type %s", domain.ErrInvalid, name, parameter.Type)
		}
		if parameter.MinLength > 0 && parameter.Type != domain.ParameterTypeString {
			return fmt.Errorf("%w: minLength is only valid for string parameter %q", domain.ErrInvalid, name)
		}
		if parameter.HasDefault() && parameter.MinLength > 0 {
			text, _ := parameter.DefaultValue.(string)
			if len([]rune(text)) < parameter.MinLength {
				return fmt.Errorf("%w: default value for %q is shorter than minLength", domain.ErrInvalid, name)
			}
		}
		for _, value := range parameter.Enum {
			if !matchesParameterType(value, string(parameter.Type)) {
				return fmt.Errorf("%w: enum value for %q must be of type %s", domain.ErrInvalid, name, parameter.Type)
			}
		}
		if parameter.HasDefault() && len(parameter.Enum) > 0 && !containsParameterValue(parameter.Enum, parameter.DefaultValue) {
			return fmt.Errorf("%w: default value for %q is not one of the allowed values", domain.ErrInvalid, name)
		}
		if err := rejectSensitiveValue(parameter.DefaultValue, name); err != nil {
			return err
		}
		seen[name] = parameter
	}

	mappedTargets := map[string]bool{}
	for _, dependency := range release.Dependencies {
		for _, mapping := range dependency.ParameterMappings {
			if strings.TrimSpace(mapping.UpstreamParameter) == "" || strings.TrimSpace(mapping.TargetParameter) == "" {
				return fmt.Errorf("%w: parameter mappings require upstreamParameter and targetParameter", domain.ErrInvalid)
			}
			if isSensitiveKey(mapping.UpstreamParameter) || isSensitiveKey(mapping.TargetParameter) {
				return fmt.Errorf("%w: sensitive parameter mapping %q must use a CredentialRef", domain.ErrInvalid, mapping.TargetParameter)
			}
			target, ok := seen[mapping.TargetParameter]
			if !ok {
				return fmt.Errorf("%w: mapping target %q is not declared by this release", domain.ErrInvalid, mapping.TargetParameter)
			}
			if mappedTargets[mapping.TargetParameter] {
				return fmt.Errorf("%w: parameter %q is mapped more than once", domain.ErrInvalid, mapping.TargetParameter)
			}
			mappedTargets[mapping.TargetParameter] = true
			_ = target
		}
	}

	for _, action := range release.Actions {
		for _, name := range action.AllowedParameters {
			if _, ok := seen[name]; !ok {
				return fmt.Errorf("%w: action %s allowed parameter %q is not declared", domain.ErrInvalid, action.Name, name)
			}
		}
	}
	return nil
}

func rejectSensitiveValue(value any, label string) error {
	if path, ok := findSensitiveValue(value, label); ok {
		return fmt.Errorf("%w: sensitive %s %q must use a CredentialRef", domain.ErrInvalid, "parameter default", path)
	}
	return nil
}

func parameterDefaults(parameters []domain.ParameterDefinition) map[string]any {
	out := map[string]any{}
	for _, parameter := range parameters {
		if parameter.HasDefault() {
			out[parameter.Name] = deepCopy(parameter.DefaultValue)
		}
	}
	return out
}

func validateResolvedParameters(parameters []domain.ParameterDefinition, resolved map[string]any) error {
	for _, parameter := range parameters {
		value, exists := resolved[parameter.Name]
		if !exists {
			if parameter.Required {
				return fmt.Errorf("%w: required parameter %q has no resolved value", domain.ErrInvalid, parameter.Name)
			}
			continue
		}
		if !matchesParameterType(value, string(parameter.Type)) {
			return fmt.Errorf("%w: parameter %q must be of type %s", domain.ErrInvalid, parameter.Name, parameter.Type)
		}
		if parameter.MinLength > 0 {
			text, isString := value.(string)
			if isString && len([]rune(text)) < parameter.MinLength {
				return fmt.Errorf("%w: parameter %q must contain at least %d characters", domain.ErrInvalid, parameter.Name, parameter.MinLength)
			}
		}
		if len(parameter.Enum) > 0 && !containsParameterValue(parameter.Enum, value) {
			return fmt.Errorf("%w: parameter %q is not one of the allowed values", domain.ErrInvalid, parameter.Name)
		}
	}
	return nil
}

func resolveOwnParameters(
	release domain.ComponentRelease,
	node domain.ScenarioNode,
	runInput map[string]any,
	allowedRunInput []string,
) (map[string]any, map[string]resolvedParameter, error) {
	mapped := domain.MappedTargets(release.Dependencies)
	resolved := map[string]any{}
	provenance := map[string]resolvedParameter{}

	assign := func(name string, value any, source string) {
		copied := deepCopy(value)
		resolved[name] = copied
		provenance[name] = resolvedParameter{Value: copied, Source: source}
	}

	for _, parameter := range release.Parameters {
		if parameter.HasDefault() {
			assign(parameter.Name, parameter.DefaultValue, parameterSourceDefault)
		}
	}
	for name, value := range node.Values {
		if _, skip := mapped[name]; skip {
			continue
		}
		assign(name, value, parameterSourceNodeValue)
	}
	allowed := make(map[string]struct{}, len(allowedRunInput))
	for _, key := range allowedRunInput {
		allowed[key] = struct{}{}
	}
	for name, value := range runInput {
		if _, ok := allowed[name]; !ok {
			return nil, nil, fmt.Errorf("%w: run input %q was not declared by the scenario", domain.ErrInvalid, name)
		}
		if _, skip := mapped[name]; skip {
			return nil, nil, fmt.Errorf("%w: mapped parameter %q cannot be supplied as run input", domain.ErrInvalid, name)
		}
		assign(name, value, parameterSourceRunInput)
	}
	return resolved, provenance, nil
}

func applyParameterMappings(
	release domain.ComponentRelease,
	node domain.ScenarioNode,
	graph domain.ScenarioGraph,
	releaseByNode map[string]domain.ComponentRelease,
	resolvedByNode map[string]map[string]any,
	resolved map[string]any,
	provenance map[string]resolvedParameter,
) error {
	reachable := graphReachability(graph)
	for _, dependency := range release.Dependencies {
		if len(dependency.ParameterMappings) == 0 {
			continue
		}
		sourceID, err := selectDependencySource(node, dependency, graph, releaseByNode, reachable)
		if err != nil {
			return err
		}
		upstreamResolved := resolvedByNode[sourceID]
		for _, mapping := range dependency.ParameterMappings {
			value, ok := upstreamResolved[mapping.UpstreamParameter]
			if !ok {
				target, _ := domain.ParameterByName(release.Parameters, mapping.TargetParameter)
				if target.Required {
					return fmt.Errorf("%w: required mapped parameter %q has no final value from node %s", domain.ErrInvalid, mapping.TargetParameter, sourceID)
				}
				continue
			}
			copied := deepCopy(value)
			resolved[mapping.TargetParameter] = copied
			provenance[mapping.TargetParameter] = resolvedParameter{
				Value: copied, Source: parameterSourceDependencyMapping,
				SourceNodeID: sourceID, UpstreamParameter: mapping.UpstreamParameter, TargetParameter: mapping.TargetParameter,
			}
		}
	}
	return nil
}

func applyDependencyFixtures(release domain.ComponentRelease, fixtures map[string]any, resolved map[string]any, provenance map[string]resolvedParameter) error {
	mapped := domain.MappedTargets(release.Dependencies)
	if len(mapped) == 0 {
		if len(fixtures) > 0 {
			return fmt.Errorf("%w: dependencyFixtures are only allowed for mapped parameters", domain.ErrInvalid)
		}
		return nil
	}
	if fixtures == nil {
		fixtures = map[string]any{}
	}
	for name := range fixtures {
		if _, ok := mapped[name]; !ok {
			return fmt.Errorf("%w: dependency fixture %q is not a mapping target", domain.ErrInvalid, name)
		}
	}
	targets := make([]string, 0, len(mapped))
	for name := range mapped {
		targets = append(targets, name)
	}
	sort.Strings(targets)
	for _, name := range targets {
		mapping := mapped[name]
		value, ok := fixtures[name]
		parameter, _ := domain.ParameterByName(release.Parameters, name)
		if !ok {
			if parameter.Required {
				return fmt.Errorf("%w: dependency fixture %q is required", domain.ErrInvalid, name)
			}
			continue
		}
		if !matchesParameterType(value, string(parameter.Type)) {
			return fmt.Errorf("%w: dependency fixture %q must be of type %s", domain.ErrInvalid, name, parameter.Type)
		}
		copied := deepCopy(value)
		resolved[name] = copied
		provenance[name] = resolvedParameter{
			Value: copied, Source: parameterSourceDependencyFixture,
			UpstreamParameter: mapping.UpstreamParameter, TargetParameter: mapping.TargetParameter,
		}
	}
	return nil
}

func selectDependencySource(
	node domain.ScenarioNode,
	dependency domain.ComponentDependency,
	graph domain.ScenarioGraph,
	releaseByNode map[string]domain.ComponentRelease,
	reachable map[string]map[string]bool,
) (string, error) {
	candidates := reachableUpstreamNodes(node.ID, dependency.UpstreamReleaseID, graph, reachable)
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("%w: locked upstream component release is absent from the graph", domain.ErrInvalid)
	case 1:
		return candidates[0], nil
	default:
		selected := ""
		if node.DependencySources != nil {
			selected = node.DependencySources[dependency.ID]
		}
		if selected == "" {
			return "", fmt.Errorf("%w: node %s must choose a source for dependency %s", domain.ErrInvalid, node.ID, dependency.ID)
		}
		found := false
		for _, candidate := range candidates {
			if candidate == selected {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("%w: selected source %s is not a reachable %s node", domain.ErrInvalid, selected, dependency.UpstreamReleaseID)
		}
		if release, ok := releaseByNode[selected]; !ok || release.ID != dependency.UpstreamReleaseID {
			return "", fmt.Errorf("%w: selected source %s does not lock upstream release %s", domain.ErrInvalid, selected, dependency.UpstreamReleaseID)
		}
		return selected, nil
	}
}

func reachableUpstreamNodes(downstreamID, upstreamReleaseID string, graph domain.ScenarioGraph, reachable map[string]map[string]bool) []string {
	var matches []string
	for _, candidate := range graph.Nodes {
		if candidate.ReleaseID != upstreamReleaseID {
			continue
		}
		if reachable[candidate.ID][downstreamID] {
			matches = append(matches, candidate.ID)
		}
	}
	sort.Strings(matches)
	return matches
}

func nodeOverridesMappedParameter(node domain.ScenarioNode, release domain.ComponentRelease) []string {
	var conflicts []string
	for name := range domain.MappedTargets(release.Dependencies) {
		if _, ok := node.Values[name]; ok {
			conflicts = append(conflicts, name)
		}
		if contains(node.RunInputs, name) {
			conflicts = append(conflicts, name)
		}
	}
	sort.Strings(conflicts)
	return uniqueStrings(conflicts)
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func releaseNeedsImportedParameters(release domain.ComponentRelease) bool {
	for _, dependency := range release.Dependencies {
		if len(dependency.ParameterMappings) > 0 {
			return true
		}
	}
	return false
}

func provenanceSnapshot(values map[string]map[string]resolvedParameter) map[string]any {
	out := make(map[string]any, len(values))
	for nodeID, parameters := range values {
		node := make(map[string]any, len(parameters))
		for name, parameter := range parameters {
			node[name] = map[string]any{
				"value":             parameter.Value,
				"source":            parameter.Source,
				"sourceNodeId":      parameter.SourceNodeID,
				"upstreamParameter": parameter.UpstreamParameter,
				"targetParameter":   parameter.TargetParameter,
			}
		}
		out[nodeID] = node
	}
	return out
}
