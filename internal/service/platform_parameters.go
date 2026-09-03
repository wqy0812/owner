package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *PlatformOptionService) ListEnvironmentParameterDefinitions(ctx context.Context) ([]domain.EnvironmentParameterDefinition, error) {
	return s.store.ListEnvironmentParameterDefinitions(ctx)
}

func (s *PlatformOptionService) CreateEnvironmentParameterDefinition(ctx context.Context, actor domain.User, input domain.EnvironmentParameterDefinition) (domain.EnvironmentParameterDefinition, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return input, err
	}
	input.Label = strings.TrimSpace(input.Label)
	input.Description = strings.TrimSpace(input.Description)
	if input.Label == "" || input.Description == "" || !input.Type.Valid() {
		return input, fmt.Errorf("%w: label, description and valid parameter type are required", domain.ErrInvalid)
	}
	if input.MinLength > 0 && input.Type != domain.ParameterTypeString {
		return input, fmt.Errorf("%w: minLength is only valid for string definitions", domain.ErrInvalid)
	}
	if (input.Type == domain.ParameterTypeObject || input.Type == domain.ParameterTypeArray) && len(input.Enum) == 0 {
		return input, fmt.Errorf("%w: structured environment definitions require governed enum choices instead of raw JSON", domain.ErrInvalid)
	}
	for _, value := range input.Enum {
		if !matchesParameterType(value, string(input.Type)) {
			return input, fmt.Errorf("%w: enum value must match definition type", domain.ErrInvalid)
		}
	}
	if err := domain.ValidateEnvironmentParameterDefault(input); err != nil {
		return input, err
	}
	input.ID = newID("environment-parameter-definition")
	input.Key = generatedPlatformTechnicalValue("environment_parameter")
	input.CreatedBy, input.CreatedAt, input.Usage = actor.ID, time.Now().UTC(), 0
	audit := newAuditEvent(actor, "environment_parameter_definition.created", "environment_parameter_definition", input.ID, map[string]any{"key": input.Key, "label": input.Label})
	if err := s.store.CreateEnvironmentParameterDefinition(ctx, input, audit); err != nil {
		return input, err
	}
	s.platform.hub.Publish("platform_parameters.updated", map[string]any{"definitionId": input.ID, "action": "created"})
	return input, nil
}

func (s *PlatformOptionService) UpdateEnvironmentParameterDefault(ctx context.Context, actor domain.User, id string, value any) (domain.EnvironmentParameterDefinition, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return domain.EnvironmentParameterDefinition{}, err
	}
	audit := newAuditEvent(actor, "environment_parameter_definition.default_updated", "environment_parameter_definition", id, map[string]any{"defaultValue": value})
	item, err := s.store.UpdateEnvironmentParameterDefault(ctx, id, value, audit)
	if err != nil {
		return item, err
	}
	s.platform.hub.Publish("platform_parameters.updated", map[string]any{"definitionId": id, "action": "default_updated"})
	s.platform.requestPublicationBackup("environment-parameter-default:" + id)
	return item, nil
}

func (s *PlatformOptionService) DeleteEnvironmentParameterDefinition(ctx context.Context, actor domain.User, id string) error {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return err
	}
	audit := newAuditEvent(actor, "environment_parameter_definition.deleted", "environment_parameter_definition", id, nil)
	if err := s.store.DeleteEnvironmentParameterDefinition(ctx, id, audit); err != nil {
		return err
	}
	s.platform.hub.Publish("platform_parameters.updated", map[string]any{"definitionId": id, "action": "deleted"})
	return nil
}

func (s *PlatformOptionService) ListEnvironmentVariableDefinitions(ctx context.Context) ([]domain.EnvironmentVariableDefinition, error) {
	return s.store.ListEnvironmentVariableDefinitions(ctx)
}

func (s *PlatformOptionService) CreateEnvironmentVariableDefinition(ctx context.Context, actor domain.User, input domain.EnvironmentVariableDefinition) (domain.EnvironmentVariableDefinition, error) {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return input, err
	}
	input.Name, input.Label, input.Description = strings.TrimSpace(strings.ToUpper(input.Name)), strings.TrimSpace(input.Label), strings.TrimSpace(input.Description)
	if !environmentVariablePattern.MatchString(input.Name) || input.Label == "" {
		return input, fmt.Errorf("%w: uppercase variable name and label are required", domain.ErrInvalid)
	}
	if isSensitiveKey(input.Name) {
		return input, fmt.Errorf("%w: sensitive values must use CredentialRef", domain.ErrInvalid)
	}
	input.ID, input.CreatedBy, input.CreatedAt, input.Usage = newID("environment-variable-definition"), actor.ID, time.Now().UTC(), 0
	audit := newAuditEvent(actor, "environment_variable_definition.created", "environment_variable_definition", input.ID, map[string]any{"name": input.Name, "label": input.Label})
	if err := s.store.CreateEnvironmentVariableDefinition(ctx, input, audit); err != nil {
		return input, err
	}
	s.platform.hub.Publish("platform_parameters.updated", map[string]any{"definitionId": input.ID, "action": "created"})
	return input, nil
}

func (s *PlatformOptionService) DeleteEnvironmentVariableDefinition(ctx context.Context, actor domain.User, id string) error {
	if err := domain.ValidateRole(actor, domain.RolePlatformAdmin); err != nil {
		return err
	}
	audit := newAuditEvent(actor, "environment_variable_definition.deleted", "environment_variable_definition", id, nil)
	if err := s.store.DeleteEnvironmentVariableDefinition(ctx, id, audit); err != nil {
		return err
	}
	s.platform.hub.Publish("platform_parameters.updated", map[string]any{"definitionId": id, "action": "deleted"})
	return nil
}
