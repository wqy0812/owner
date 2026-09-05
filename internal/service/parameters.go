package service

import (
	"fmt"
	"sort"
	"strings"

	"codex/platform-demo/internal/domain"
)

const (
	parameterSourceComponentFixed    = "component_fixed"
	parameterSourceScenarioValue     = "scenario_value"
	parameterSourceEnvironmentValue  = "environment_value"
	parameterSourceTestValue         = "test_value"
	parameterSourceDependencyMapping = "dependency_mapping"
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
		if !parameter.ValueProvider.Valid() {
			return fmt.Errorf("%w: parameter %q has invalid valueProvider", domain.ErrInvalid, name)
		}
		switch parameter.ValueProvider {
		case domain.ParameterProviderComponentOwner:
			if parameter.Modifiable || !parameter.HasFixedValue() {
				return fmt.Errorf("%w: component-owned parameter %q must be fixed and unmodifiable", domain.ErrInvalid, name)
			}
			if parameter.EnvironmentBinding != nil {
				return fmt.Errorf("%w: component-owned parameter %q cannot have environmentBinding", domain.ErrInvalid, name)
			}
		case domain.ParameterProviderScenarioOwner:
			if !parameter.Modifiable || parameter.HasFixedValue() || parameter.EnvironmentBinding != nil {
				return fmt.Errorf("%w: scenario-owned parameter %q must be modifiable and cannot be fixed or environment-bound", domain.ErrInvalid, name)
			}
			if (parameter.Type == domain.ParameterTypeObject || parameter.Type == domain.ParameterTypeArray) && len(parameter.Enum) == 0 {
				return fmt.Errorf("%w: scenario-owned structured parameter %q requires governed enum choices instead of raw JSON", domain.ErrInvalid, name)
			}
		case domain.ParameterProviderEnvironmentOwner:
			if !parameter.Modifiable || parameter.HasFixedValue() || parameter.EnvironmentBinding == nil || !parameter.EnvironmentBinding.Valid() {
				return fmt.Errorf("%w: environment-owned parameter %q requires a valid environmentBinding", domain.ErrInvalid, name)
			}
			if (parameter.Type == domain.ParameterTypeObject || parameter.Type == domain.ParameterTypeArray) && len(parameter.Enum) == 0 {
				return fmt.Errorf("%w: environment-owned structured parameter %q requires governed enum choices instead of raw JSON", domain.ErrInvalid, name)
			}
		case domain.ParameterProviderUpstreamMapping:
			if parameter.Modifiable || parameter.HasFixedValue() || parameter.EnvironmentBinding != nil {
				return fmt.Errorf("%w: mapped parameter %q must be unmodifiable", domain.ErrInvalid, name)
			}
		}
		if parameter.MinLength > 0 && parameter.Type != domain.ParameterTypeString {
			return fmt.Errorf("%w: minLength is only valid for string parameter %q", domain.ErrInvalid, name)
		}
		for _, value := range parameter.Enum {
			if !matchesParameterType(value, string(parameter.Type)) {
				return fmt.Errorf("%w: enum value for %q must be of type %s", domain.ErrInvalid, name, parameter.Type)
			}
		}
		for label, value := range map[string]any{"fixedValue": parameter.FixedValue, "suggestedValue": parameter.SuggestedValue, "testValue": parameter.TestValue} {
			if value == nil {
				continue
			}
			if !matchesParameterType(value, string(parameter.Type)) {
				return fmt.Errorf("%w: %s for %q must be of type %s", domain.ErrInvalid, label, name, parameter.Type)
			}
			if parameter.MinLength > 0 {
				if text, ok := value.(string); ok && len([]rune(text)) < parameter.MinLength {
					return fmt.Errorf("%w: %s for %q is shorter than minLength", domain.ErrInvalid, label, name)
				}
			}
			if len(parameter.Enum) > 0 && !containsParameterValue(parameter.Enum, value) {
				return fmt.Errorf("%w: %s for %q is not one of the allowed values", domain.ErrInvalid, label, name)
			}
			if err := rejectSensitiveValue(value, name); err != nil {
				return err
			}
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
			if target.ValueProvider != domain.ParameterProviderUpstreamMapping {
				return fmt.Errorf("%w: mapping target %q must use upstream_mapping as valueProvider", domain.ErrInvalid, mapping.TargetParameter)
			}
		}
	}
	for _, parameter := range release.Parameters {
		_, mapped := mappedTargets[parameter.Name]
		if parameter.ValueProvider == domain.ParameterProviderUpstreamMapping && !mapped {
			return fmt.Errorf("%w: mapped parameter %q requires exactly one upstream mapping", domain.ErrInvalid, parameter.Name)
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

func parameterFixedValues(parameters []domain.ParameterDefinition) map[string]any {
	out := map[string]any{}
	for _, parameter := range parameters {
		if parameter.HasFixedValue() {
			out[parameter.Name] = deepCopy(parameter.FixedValue)
		}
	}
	return out
}

func validateResolvedParameters(parameters []domain.ParameterDefinition, resolved map[string]any) error {
	return domain.ValidateResolvedParameters(parameters, resolved)
}

func equalParameterValues(left, right []any) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !parameterValuesEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func resolveOwnParameters(
	release domain.ComponentRelease,
	node domain.ScenarioNode,
	environment domain.EnvironmentRevision,
	componentTest bool,
) (map[string]any, map[string]resolvedParameter, error) {
	if err := domain.ValidateComponentParameterAuthoring(release.Parameters); err != nil {
		return nil, nil, err
	}
	mapped := domain.MappedTargets(release.Dependencies)
	resolved := map[string]any{}
	provenance := map[string]resolvedParameter{}

	assign := func(name string, value any, source string) {
		copied := deepCopy(value)
		resolved[name] = copied
		provenance[name] = resolvedParameter{Value: copied, Source: source}
	}

	for _, parameter := range release.Parameters {
		if parameter.ValueProvider == domain.ParameterProviderComponentOwner && parameter.HasFixedValue() {
			assign(parameter.Name, parameter.FixedValue, parameterSourceComponentFixed)
		} else if componentTest && parameter.ValueProvider != domain.ParameterProviderEnvironmentOwner && parameter.HasTestValue() {
			assign(parameter.Name, parameter.TestValue, parameterSourceTestValue)
		} else if parameter.ValueProvider == domain.ParameterProviderEnvironmentOwner {
			key := domain.EnvironmentParameterValueKey(release.ID, parameter)
			if value, ok := environment.Parameters[key]; ok {
				assign(parameter.Name, value, parameterSourceEnvironmentValue)
			}
		}
	}
	for name, value := range node.ParameterValues {
		parameter, declared := domain.ParameterByName(release.Parameters, name)
		if !declared || parameter.ValueProvider != domain.ParameterProviderScenarioOwner || !parameter.Modifiable {
			return nil, nil, fmt.Errorf("%w: scenario value %q is not owned by the scenario", domain.ErrInvalid, name)
		}
		if _, skip := mapped[name]; skip {
			return nil, nil, fmt.Errorf("%w: mapped parameter %q cannot be supplied by the scenario", domain.ErrInvalid, name)
		}
		assign(name, value, parameterSourceScenarioValue)
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
	for _, dependency := range release.Dependencies {
		if len(dependency.ParameterMappings) == 0 {
			continue
		}
		sourceID, err := selectDependencySource(node, dependency, graph, releaseByNode)
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

func selectDependencySource(
	node domain.ScenarioNode,
	dependency domain.ComponentDependency,
	graph domain.ScenarioGraph,
	releaseByNode map[string]domain.ComponentRelease,
) (string, error) {
	if dependency.Kind == domain.DependencyConfiguration {
		selected := node.DependencySources[dependency.ID]
		if selected == "" {
			return "", fmt.Errorf("%w: node %s must choose a source for dependency %s", domain.ErrInvalid, node.ID, dependency.ID)
		}
		if source, ok := releaseByNode[selected]; !ok || source.ID != dependency.UpstreamReleaseID || selected == node.ID {
			return "", fmt.Errorf("%w: invalid configuration source %s", domain.ErrInvalid, selected)
		}
		return selected, nil
	}
	selected := ""
	if node.DependencySources != nil {
		selected = node.DependencySources[dependency.ID]
	}
	if selected == "" {
		return "", fmt.Errorf("%w: node %s must choose a source for dependency %s", domain.ErrInvalid, node.ID, dependency.ID)
	}
	if release, ok := releaseByNode[selected]; !ok || release.ID != dependency.UpstreamReleaseID {
		return "", fmt.Errorf("%w: selected source %s does not lock upstream release %s", domain.ErrInvalid, selected, dependency.UpstreamReleaseID)
	}
	for _, edge := range graph.Edges {
		if edge.Kind == domain.ScenarioEdgeDependency && edge.DependencyID == dependency.ID &&
			((edge.Source == selected && edge.Target == node.ID) || (edge.Source == node.ID && edge.Target == selected)) {
			return selected, nil
		}
	}
	return "", fmt.Errorf("%w: selected source %s is not bound by dependency %s", domain.ErrInvalid, selected, dependency.ID)
}

func nodeOverridesMappedParameter(node domain.ScenarioNode, release domain.ComponentRelease) []string {
	var conflicts []string
	for name := range domain.MappedTargets(release.Dependencies) {
		if _, ok := node.ParameterValues[name]; ok {
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
