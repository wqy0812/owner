package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

var presetContexts = map[string]string{
	"component_install_verify": "component_release",
	"component_rollback":       "component_release",
	"scenario_test":            "scenario_revision",
	"scenario_run":             "scenario_revision",
}

func (p *Platform) presetDefinitionDigest(ctx context.Context, user domain.User, resourceType, resourceID, presetContext string) (string, error) {
	if presetContexts[presetContext] != resourceType {
		return "", fmt.Errorf("%w: preset context does not match resource type", domain.ErrInvalid)
	}
	switch resourceType {
	case "component_release":
		release, err := p.store.GetComponentRelease(ctx, resourceID)
		if err != nil {
			return "", err
		}
		component, err := p.store.GetComponent(ctx, release.ComponentID, false)
		if err != nil {
			return "", err
		}
		if user.Role != domain.RoleEnvironmentOwner && (user.Role != domain.RoleComponentOwner || user.ID != component.OwnerID) {
			return "", domain.ErrForbidden
		}
		return componentReleaseSpecDigest(release), nil
	case "scenario_revision":
		revision, err := p.store.GetScenarioRevision(ctx, resourceID)
		if err != nil {
			return "", err
		}
		scenario, err := p.store.GetScenario(ctx, revision.ScenarioID, false)
		if err != nil {
			return "", err
		}
		if user.Role != domain.RoleEnvironmentOwner && (user.Role != domain.RoleScenarioOwner || user.ID != scenario.OwnerID) {
			return "", domain.ErrForbidden
		}
		return scenarioRevisionSpecDigest(revision), nil
	default:
		return "", fmt.Errorf("%w: invalid preset resource type", domain.ErrInvalid)
	}
}

func validatePresetValues(values map[string]any) error {
	if values == nil {
		return fmt.Errorf("%w: preset values are required", domain.ErrInvalid)
	}
	for key := range values {
		if key != "runInput" && key != "dependencyFixtures" {
			return fmt.Errorf("%w: preset field %q is not supported", domain.ErrInvalid, key)
		}
	}
	for _, key := range []string{"runInput", "dependencyFixtures"} {
		if value, exists := values[key]; exists {
			if _, ok := value.(map[string]any); !ok {
				return fmt.Errorf("%w: preset %s must be an object", domain.ErrInvalid, key)
			}
		}
	}
	if err := rejectSensitiveMap(values, "run input preset"); err != nil {
		return err
	}
	return nil
}

func (p *Platform) ListRunInputPresets(ctx context.Context, user domain.User, resourceType, resourceID, presetContext string) ([]domain.RunInputPreset, error) {
	digest, err := p.presetDefinitionDigest(ctx, user, resourceType, resourceID, presetContext)
	if err != nil {
		return nil, err
	}
	presets, err := p.store.ListRunInputPresets(ctx, user.ID, resourceType, resourceID, presetContext)
	if err != nil {
		return nil, err
	}
	for index := range presets {
		presets[index].Stale = presets[index].DefinitionDigest != digest
	}
	return presets, nil
}

func (p *Platform) SaveRunInputPreset(ctx context.Context, user domain.User, preset domain.RunInputPreset) (domain.RunInputPreset, error) {
	preset.Name = strings.TrimSpace(preset.Name)
	if preset.Name == "" || len([]rune(preset.Name)) > 80 {
		return preset, fmt.Errorf("%w: preset name is required and must be at most 80 characters", domain.ErrInvalid)
	}
	if err := validatePresetValues(preset.Values); err != nil {
		return preset, err
	}
	digest, err := p.presetDefinitionDigest(ctx, user, preset.ResourceType, preset.ResourceID, preset.Context)
	if err != nil {
		return preset, err
	}
	now := time.Now().UTC()
	if preset.ID == "" {
		preset.ID, preset.CreatedAt = newID("run-preset"), now
	} else {
		existing, getErr := p.store.GetRunInputPreset(ctx, preset.ID)
		if getErr != nil {
			return preset, getErr
		}
		if existing.CreatedBy != user.ID || existing.ResourceType != preset.ResourceType || existing.ResourceID != preset.ResourceID || existing.Context != preset.Context {
			return preset, domain.ErrForbidden
		}
		preset.CreatedAt = existing.CreatedAt
	}
	preset.CreatedBy, preset.DefinitionDigest, preset.UpdatedAt, preset.Stale = user.ID, digest, now, false
	if err := p.store.SaveRunInputPreset(ctx, preset); err != nil {
		return preset, err
	}
	p.audit(ctx, user, "run_input_preset.saved", "run_input_preset", preset.ID, map[string]any{"resourceType": preset.ResourceType, "resourceId": preset.ResourceID, "context": preset.Context, "name": preset.Name})
	return preset, nil
}

func (p *Platform) DeleteRunInputPreset(ctx context.Context, user domain.User, id string) error {
	if err := p.store.DeleteRunInputPreset(ctx, id, user.ID); err != nil {
		return err
	}
	p.audit(ctx, user, "run_input_preset.deleted", "run_input_preset", id, nil)
	return nil
}
