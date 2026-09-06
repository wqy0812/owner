package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

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
	s.hub.Publish("platform_parameters.updated", map[string]any{"definitionId": input.ID, "action": "created"})
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
	s.hub.Publish("platform_parameters.updated", map[string]any{"definitionId": id, "action": "deleted"})
	return nil
}
