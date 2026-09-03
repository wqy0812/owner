package service

import (
	"context"
	"sort"

	"codex/platform-demo/internal/domain"
)

type EnvironmentParameterBindingUse struct {
	ComponentID   string `json:"componentId"`
	ComponentName string `json:"componentName"`
	ReleaseID     string `json:"releaseId"`
	Version       string `json:"version"`
	ParameterName string `json:"parameterName"`
}

type EnvironmentParameterField struct {
	ValueKey       string                           `json:"valueKey"`
	DefinitionID   string                           `json:"definitionId,omitempty"`
	Label          string                           `json:"label"`
	Description    string                           `json:"description"`
	Type           domain.ParameterType             `json:"type"`
	Required       bool                             `json:"required"`
	SuggestedValue any                              `json:"suggestedValue,omitempty"`
	DefaultValue   any                              `json:"defaultValue,omitempty"`
	Enum           []any                            `json:"enum,omitempty"`
	MinLength      int                              `json:"minLength,omitempty"`
	Bindings       []EnvironmentParameterBindingUse `json:"bindings"`
}

func (p *Platform) EnvironmentParameterFields(ctx context.Context) ([]EnvironmentParameterField, error) {
	components, err := p.store.ListComponents(ctx, domain.User{Role: domain.RolePlatformAdmin})
	if err != nil {
		return nil, err
	}
	fields := map[string]*EnvironmentParameterField{}
	for _, component := range components {
		for _, release := range component.Releases {
			// Published Releases remain runnable after deprecation through retained
			// Scenario Revisions, so their environment-owned values must stay
			// maintainable. A never-published abandoned Draft has no such consumer.
			if release.Status == domain.ReleaseDeprecated && release.ReleasedAt == nil {
				continue
			}
			for _, parameter := range release.Parameters {
				if parameter.ValueProvider != domain.ParameterProviderEnvironmentOwner || !parameter.Modifiable {
					continue
				}
				key := domain.EnvironmentParameterValueKey(release.ID, parameter)
				field := fields[key]
				if field == nil {
					field = &EnvironmentParameterField{ValueKey: key, Label: parameter.Name, Description: parameter.Description, Type: parameter.Type, Required: parameter.Required, SuggestedValue: parameter.SuggestedValue, Enum: parameter.Enum, MinLength: parameter.MinLength, Bindings: []EnvironmentParameterBindingUse{}}
					if parameter.EnvironmentBinding != nil {
						field.DefinitionID = parameter.EnvironmentBinding.DefinitionID
					}
					fields[key] = field
				}
				field.Required = field.Required || parameter.Required
				field.Bindings = append(field.Bindings, EnvironmentParameterBindingUse{ComponentID: component.ID, ComponentName: component.Name, ReleaseID: release.ID, Version: release.Version, ParameterName: parameter.Name})
			}
		}
	}
	definitions, err := p.store.ListEnvironmentParameterDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	for _, definition := range definitions {
		if field := fields["global:"+definition.ID]; field != nil {
			field.Label, field.Description = definition.Label, definition.Description
			field.Type, field.Enum, field.MinLength = definition.Type, definition.Enum, definition.MinLength
			field.DefaultValue, field.SuggestedValue, field.Required = definition.DefaultValue, nil, true
		}
	}
	out := make([]EnvironmentParameterField, 0, len(fields))
	for _, field := range fields {
		sort.Slice(field.Bindings, func(i, j int) bool {
			return field.Bindings[i].ComponentName+field.Bindings[i].Version < field.Bindings[j].ComponentName+field.Bindings[j].Version
		})
		out = append(out, *field)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

func (p *Platform) validateEnvironmentValues(ctx context.Context, values map[string]any, variables map[string]string, refs []domain.CredentialRef) error {
	snapshot, err := p.store.ReadCatalogValidation(ctx)
	if err != nil {
		return err
	}
	return snapshot.ValidateValues(values, variables, refs)
}
func (p *Platform) UpdateEnvironmentParameters(ctx context.Context, user domain.User, environmentID string, values map[string]any, changeReason ...string) (domain.Environment, error) {
	if err := p.validateEnvironmentValues(ctx, values, nil, nil); err != nil {
		return domain.Environment{}, err
	}
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error { revision.Parameters = cloneMap(values); return nil }, "environment.parameters_updated", firstReason(changeReason))
}
