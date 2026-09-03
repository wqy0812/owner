package service

import (
	"context"
	"fmt"
	"sort"

	"codex/platform-demo/internal/domain"
)

type ScenarioParameterNodeOverview struct {
	NodeID          string                       `json:"nodeId"`
	Label           string                       `json:"label"`
	Action          domain.ActionKind            `json:"action"`
	HostGroup       string                       `json:"hostGroup"`
	Parameters      []domain.ParameterDefinition `json:"parameters"`
	ParameterValues map[string]any               `json:"parameterValues"`
	Completed       int                          `json:"completed"`
	Required        int                          `json:"required"`
	Errors          []string                     `json:"errors"`
	StaleKeys       []string                     `json:"staleKeys"`
}

type ScenarioParameterReleaseOverview struct {
	ReleaseID string                          `json:"releaseId"`
	Version   string                          `json:"version"`
	Nodes     []ScenarioParameterNodeOverview `json:"nodes"`
}

type ScenarioParameterComponentOverview struct {
	ComponentID   string                             `json:"componentId"`
	ComponentName string                             `json:"componentName"`
	Releases      []ScenarioParameterReleaseOverview `json:"releases"`
}

type ScenarioParameterOverview struct {
	RevisionID string                               `json:"revisionId"`
	Editable   bool                                 `json:"editable"`
	Components []ScenarioParameterComponentOverview `json:"components"`
}

func (p *Platform) ScenarioParameterOverview(ctx context.Context, user domain.User, revisionID string) (ScenarioParameterOverview, error) {
	revision, err := p.store.GetScenarioRevision(ctx, revisionID)
	if err != nil {
		return ScenarioParameterOverview{}, err
	}
	scenario, err := p.store.GetScenario(ctx, revision.ScenarioID, false)
	if err != nil {
		return ScenarioParameterOverview{}, err
	}
	owner := user.Role == domain.RoleScenarioOwner && user.ID == scenario.OwnerID
	if !owner && user.Role != domain.RolePlatformAdmin {
		return ScenarioParameterOverview{}, domain.ErrForbidden
	}
	overview := ScenarioParameterOverview{RevisionID: revisionID, Editable: owner && scenario.CurrentRevisionID == revisionID && revision.Status == domain.RevisionDraft, Components: []ScenarioParameterComponentOverview{}}
	type releaseGroup struct {
		component domain.Component
		release   domain.ComponentRelease
		nodes     []ScenarioParameterNodeOverview
	}
	groups := map[string]*releaseGroup{}
	for _, node := range revision.Graph.Nodes {
		release, releaseErr := p.store.GetComponentRelease(ctx, node.ReleaseID)
		if releaseErr != nil {
			return overview, releaseErr
		}
		parameters := []domain.ParameterDefinition{}
		allowed := map[string]domain.ParameterDefinition{}
		for _, parameter := range release.Parameters {
			if parameter.Modifiable && parameter.ValueProvider == domain.ParameterProviderScenarioOwner {
				parameters = append(parameters, parameter)
				allowed[parameter.Name] = parameter
			}
		}
		if len(parameters) == 0 && len(node.ParameterValues) == 0 {
			continue
		}
		sort.Slice(parameters, func(i, j int) bool { return parameters[i].Name < parameters[j].Name })
		item := ScenarioParameterNodeOverview{NodeID: node.ID, Label: node.Name, Action: node.Action, HostGroup: node.HostGroup, Parameters: parameters, ParameterValues: node.ParameterValues, Errors: []string{}, StaleKeys: []string{}}
		if item.ParameterValues == nil {
			item.ParameterValues = map[string]any{}
		}
		for _, parameter := range parameters {
			value, exists := item.ParameterValues[parameter.Name]
			if parameter.Required {
				item.Required++
			}
			if exists {
				if err := validateResolvedParameters([]domain.ParameterDefinition{parameter}, map[string]any{parameter.Name: value}); err != nil {
					item.Errors = append(item.Errors, err.Error())
				} else if parameter.Required {
					item.Completed++
				}
			} else if parameter.Required {
				item.Errors = append(item.Errors, fmt.Sprintf("required parameter %q has no value", parameter.Name))
			}
		}
		for key := range item.ParameterValues {
			if _, ok := allowed[key]; !ok {
				item.StaleKeys = append(item.StaleKeys, key)
				item.Errors = append(item.Errors, fmt.Sprintf("stale parameter %q must be removed", key))
			}
		}
		sort.Strings(item.StaleKeys)
		group := groups[release.ID]
		if group == nil {
			component, componentErr := p.store.GetComponent(ctx, release.ComponentID, false)
			if componentErr != nil {
				return overview, componentErr
			}
			group = &releaseGroup{component: component, release: release}
			groups[release.ID] = group
		}
		group.nodes = append(group.nodes, item)
	}
	componentIndex := map[string]int{}
	ordered := make([]*releaseGroup, 0, len(groups))
	for _, group := range groups {
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].component.Name == ordered[j].component.Name {
			return ordered[i].release.Version < ordered[j].release.Version
		}
		return ordered[i].component.Name < ordered[j].component.Name
	})
	for _, group := range ordered {
		sort.Slice(group.nodes, func(i, j int) bool {
			if group.nodes[i].Label == group.nodes[j].Label {
				return group.nodes[i].NodeID < group.nodes[j].NodeID
			}
			return group.nodes[i].Label < group.nodes[j].Label
		})
		index, ok := componentIndex[group.component.ID]
		if !ok {
			index = len(overview.Components)
			componentIndex[group.component.ID] = index
			overview.Components = append(overview.Components, ScenarioParameterComponentOverview{ComponentID: group.component.ID, ComponentName: group.component.Name, Releases: []ScenarioParameterReleaseOverview{}})
		}
		overview.Components[index].Releases = append(overview.Components[index].Releases, ScenarioParameterReleaseOverview{ReleaseID: group.release.ID, Version: group.release.Version, Nodes: group.nodes})
	}
	return overview, nil
}
