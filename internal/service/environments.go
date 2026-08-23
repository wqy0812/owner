package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

type InventoryHost struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Groups  []string `json:"groups"`
	Port    int      `json:"port,omitempty"`
	User    string   `json:"user,omitempty"`
}

type InventoryDocument struct {
	Hosts []InventoryHost `json:"hosts"`
}

func (p *Platform) CreateEnvironment(ctx context.Context, user domain.User, environment domain.Environment, facts map[string]any) (domain.Environment, error) {
	if err := domain.ValidateRole(user, domain.RoleEnvironmentOwner); err != nil {
		return environment, err
	}
	if err := rejectSensitiveMap(facts, "environment fact"); err != nil {
		return environment, err
	}
	if strings.TrimSpace(environment.Name) == "" {
		return environment, fmt.Errorf("%w: environment name is required", domain.ErrInvalid)
	}
	now := time.Now().UTC()
	environment.ID, environment.OwnerID, environment.CreatedAt, environment.UpdatedAt = newID("environment"), user.ID, now, now
	inventory, _ := json.Marshal(InventoryDocument{Hosts: []InventoryHost{}})
	revision := domain.EnvironmentRevision{
		ID: newID("environment-revision"), EnvironmentID: environment.ID, Revision: 1,
		Facts: facts, Inventory: inventory, Parameters: map[string]any{}, Variables: map[string]string{}, CredentialRefs: []domain.CredentialRef{}, MaxConcurrent: 1, CreatedAt: now,
	}
	environment.CurrentRevisionID, environment.Revision = revision.ID, &revision
	if err := p.store.CreateEnvironment(ctx, environment, revision); err != nil {
		return environment, err
	}
	p.audit(ctx, user, "environment.created", "environment", environment.ID, map[string]any{"revisionId": revision.ID})
	return environment, nil
}

func (p *Platform) ListEnvironments(ctx context.Context, user domain.User) ([]domain.Environment, error) {
	environments, err := p.store.ListEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	for i := range environments {
		if environments[i].Revision != nil {
			privileged := user.Role == domain.RoleEnvironmentOwner && user.ID == environments[i].OwnerID
			environments[i].Revision.CredentialRefs = domain.RedactCredentialRefs(environments[i].Revision.CredentialRefs, privileged)
		}
	}
	return environments, nil
}

func (p *Platform) UpdateInventory(ctx context.Context, user domain.User, environmentID string, hosts []InventoryHost) (domain.Environment, error) {
	for _, host := range hosts {
		if strings.TrimSpace(host.Name) == "" || strings.TrimSpace(host.Address) == "" {
			return domain.Environment{}, fmt.Errorf("%w: every inventory host requires a name and address", domain.ErrInvalid)
		}
		if len(host.Groups) == 0 {
			return domain.Environment{}, fmt.Errorf("%w: inventory host %q requires at least one group", domain.ErrInvalid, host.Name)
		}
	}
	document, _ := json.Marshal(InventoryDocument{Hosts: hosts})
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		revision.Inventory = document
		return nil
	}, "environment.inventory_updated")
}

func (p *Platform) UpdateEnvironmentParameters(ctx context.Context, user domain.User, environmentID string, parameters map[string]any) (domain.Environment, error) {
	if err := rejectSensitiveMap(parameters, "environment parameter"); err != nil {
		return domain.Environment{}, err
	}
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		revision.Parameters = parameters
		return nil
	}, "environment.parameters_updated")
}

func (p *Platform) UpdateEnvironmentFacts(ctx context.Context, user domain.User, environmentID string, facts map[string]any) (domain.Environment, error) {
	if err := rejectSensitiveMap(facts, "environment fact"); err != nil {
		return domain.Environment{}, err
	}
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		revision.Facts = facts
		return nil
	}, "environment.facts_updated")
}

var environmentVariablePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func normalizeEnvironmentVariables(variables map[string]string, refs []domain.CredentialRef) (map[string]string, error) {
	credentialNames := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		credentialNames[ref.Name] = struct{}{}
	}
	keys := make([]string, 0, len(variables))
	for name := range variables {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	normalized := make(map[string]string, len(variables))
	for _, name := range keys {
		if !environmentVariablePattern.MatchString(name) {
			return nil, fmt.Errorf("%w: environment variable %q must be an uppercase identifier", domain.ErrInvalid, name)
		}
		if isSensitiveKey(name) {
			return nil, fmt.Errorf("%w: sensitive environment variable %q must use a CredentialRef", domain.ErrInvalid, name)
		}
		if _, exists := credentialNames[name]; exists {
			return nil, fmt.Errorf("%w: environment variable %q conflicts with a CredentialRef", domain.ErrInvalid, name)
		}
		value := variables[name]
		if name == imageRegistryVariable {
			registry, err := normalizeImageRegistry(value)
			if err != nil {
				return nil, err
			}
			value = registry
		}
		normalized[name] = value
	}
	return normalized, nil
}

func (p *Platform) UpdateEnvironmentVariables(ctx context.Context, user domain.User, environmentID string, variables map[string]string) (domain.Environment, error) {
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		normalized, err := normalizeEnvironmentVariables(variables, revision.CredentialRefs)
		if err != nil {
			return err
		}
		revision.Variables = normalized
		return nil
	}, "environment.variables_updated")
}

func rejectSensitiveMap(values map[string]any, label string) error {
	if path, ok := findSensitiveParameter(values, ""); ok {
		return fmt.Errorf("%w: sensitive %s %q must use a CredentialRef", domain.ErrInvalid, label, path)
	}
	return nil
}

func findSensitiveParameter(parameters map[string]any, prefix string) (string, bool) {
	return findSensitiveValue(parameters, prefix)
}

func findSensitiveValue(value any, prefix string) (string, bool) {
	switch values := value.(type) {
	case map[string]any:
		for key, child := range values {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			if isSensitiveKey(key) {
				return path, true
			}
			if nested, found := findSensitiveValue(child, path); found {
				return nested, true
			}
		}
	case []any:
		for index, child := range values {
			path := fmt.Sprintf("%s[%d]", prefix, index)
			if nested, found := findSensitiveValue(child, path); found {
				return nested, true
			}
		}
	}
	return "", false
}

func (p *Platform) UpdateCredentialRefs(ctx context.Context, user domain.User, environmentID string, refs []domain.CredentialRef) (domain.Environment, error) {
	if err := ValidateCredentialRefs(refs); err != nil {
		return domain.Environment{}, err
	}
	return p.updateEnvironmentRevision(ctx, user, environmentID, func(revision *domain.EnvironmentRevision) error {
		if _, err := normalizeEnvironmentVariables(revision.Variables, refs); err != nil {
			return err
		}
		revision.CredentialRefs = refs
		return nil
	}, "environment.credentials_updated")
}

func (p *Platform) updateEnvironmentRevision(ctx context.Context, user domain.User, environmentID string, mutate func(*domain.EnvironmentRevision) error, auditAction string) (domain.Environment, error) {
	environment, err := p.store.GetEnvironment(ctx, environmentID, false)
	if err != nil {
		return environment, err
	}
	if err := requireOwner(user, domain.RoleEnvironmentOwner, environment.OwnerID); err != nil {
		return environment, err
	}
	if environment.Revision == nil {
		return environment, fmt.Errorf("%w: environment has no current revision", domain.ErrConflict)
	}
	revision := *environment.Revision
	revision.CredentialRefs = append([]domain.CredentialRef(nil), revision.CredentialRefs...)
	revision.Parameters = cloneMap(revision.Parameters)
	revision.Facts = cloneMap(revision.Facts)
	revision.Variables = cloneStringMap(revision.Variables)
	if err := mutate(&revision); err != nil {
		return environment, err
	}
	next, err := p.store.NextEnvironmentRevision(ctx, environmentID)
	if err != nil {
		return environment, err
	}
	revision.ID, revision.Revision, revision.CreatedAt = newID("environment-revision"), next, time.Now().UTC()
	if err := p.store.CreateEnvironmentRevision(ctx, revision); err != nil {
		return environment, err
	}
	environment.CurrentRevisionID, environment.Revision, environment.UpdatedAt = revision.ID, &revision, revision.CreatedAt
	p.audit(ctx, user, auditAction, "environment", environmentID, map[string]any{"revisionId": revision.ID, "revision": next})
	return environment, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	return deepCopy(input).(map[string]any)
}
