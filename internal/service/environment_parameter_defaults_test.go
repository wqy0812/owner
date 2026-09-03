package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
)

func TestGlobalDefaultsAreFrozenInEnvironmentRevisionsAndCanBeOverridden(t *testing.T) {
	ctx := context.Background()
	p, db := readinessTestPlatform(t)
	admin := domain.User{ID: seed.PlatformAdminID, Role: domain.RolePlatformAdmin}
	owner := domain.User{ID: "defaults-environment-owner", Name: "Environment Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: time.Now().UTC()}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	definitions := []domain.EnvironmentParameterDefinition{
		{Label: "Capacity", Description: "Capacity class", Type: domain.ParameterTypeString, Enum: []any{"small", "medium", "large"}, DefaultValue: "small"},
		{Label: "Feature enabled", Description: "Feature switch", Type: domain.ParameterTypeBoolean, DefaultValue: false},
		{Label: "Retry count", Description: "Retry budget", Type: domain.ParameterTypeInteger, DefaultValue: 0},
		{Label: "Placement", Description: "Environment placement", Type: domain.ParameterTypeString},
	}
	parameters := []domain.ParameterDefinition{}
	for i, d := range definitions {
		created, err := p.PlatformOptions().CreateEnvironmentParameterDefinition(ctx, admin, d)
		if err != nil {
			t.Fatal(err)
		}
		definitions[i] = created
		parameters = append(parameters, domain.ParameterDefinition{Name: d.Label, Description: d.Description, Type: d.Type, Enum: d.Enum, ValueProvider: domain.ParameterProviderEnvironmentOwner, Modifiable: true, Visibility: domain.ParameterPublic, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingGlobal, DefinitionID: created.ID}})
	}
	release := domain.ComponentRelease{ID: "defaults-release", ComponentID: "component-1", LineName: "Defaults", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, Parameters: parameters, CreatedAt: time.Now().UTC()}
	if err := db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	environment, err := p.CreateEnvironment(ctx, owner, domain.Environment{Name: "Defaults environment"}, completeServiceTestFacts())
	if err != nil {
		t.Fatal(err)
	}
	key := func(i int) string { return "global:" + definitions[i].ID }
	initial := environment.Revision
	if initial.Parameters[key(0)] != "small" || initial.Parameters[key(1)] != false || initial.Parameters[key(2)] != float64(0) || len(initial.Parameters) != 3 {
		t.Fatalf("initial defaults=%#v", initial.Parameters)
	}
	fields, err := p.EnvironmentParameterFields(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range fields {
		if !field.Required {
			t.Fatalf("global field must be filled: %#v", field)
		}
	}
	if _, err := p.PlatformOptions().UpdateEnvironmentParameterDefault(ctx, owner, definitions[0].ID, "large"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("owner default mutation: %v", err)
	}
	if _, err := p.PlatformOptions().UpdateEnvironmentParameterDefault(ctx, admin, definitions[0].ID, "large"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PlatformOptions().UpdateEnvironmentParameterDefault(ctx, admin, definitions[1].ID, nil); err != nil {
		t.Fatal(err)
	}
	retained, err := db.GetEnvironmentRevision(ctx, initial.ID)
	if err != nil || !reflect.DeepEqual(retained.Parameters, initial.Parameters) {
		t.Fatalf("default update changed retained revision: %#v %v", retained, err)
	}
	values := map[string]any{key(0): "medium", key(1): false, key(2): 0, key(3): "zone-a"}
	updated, err := p.UpdateEnvironmentParameters(ctx, owner, environment.ID, values, "Override defaults")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision.Parameters[key(0)] != "medium" || updated.Revision.Parameters[key(1)] != false {
		t.Fatalf("owner overrides=%#v", updated.Revision.Parameters)
	}
	for _, missing := range []any{nil, "", "   "} {
		values[key(3)] = missing
		if _, err := p.UpdateEnvironmentParameters(ctx, owner, environment.ID, values); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("missing=%#v err=%v", missing, err)
		}
	}
	delete(values, key(3))
	if _, err := p.UpdateEnvironmentParameters(ctx, owner, environment.ID, values); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("omitted required global: %v", err)
	}
	values[key(3)] = "zone-b"
	delete(values, key(0))
	updated, err = p.UpdateEnvironmentParameters(ctx, owner, environment.ID, values)
	if err != nil || updated.Revision.Parameters[key(0)] != "large" {
		t.Fatalf("save should materialize current default: %#v %v", updated.Revision, err)
	}
	restored, err := p.RestoreEnvironmentRevision(ctx, owner, environment.ID, initial.ID, "Restore original snapshot")
	if err != nil || !reflect.DeepEqual(restored.Revision.Parameters, initial.Parameters) {
		t.Fatalf("restore must preserve original defaults: %#v %v", restored.Revision, err)
	}
	resolved, _, err := resolveOwnParameters(release, domain.ScenarioNode{}, *restored.Revision, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := domain.ValidateResolvedParameters(release.Parameters, resolved); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("run must reject a missing global value instead of using live defaults: %v", err)
	}
}

func TestGlobalDefaultValidationAndClearing(t *testing.T) {
	ctx := context.Background()
	p, _ := readinessTestPlatform(t)
	admin := domain.User{ID: seed.PlatformAdminID, Role: domain.RolePlatformAdmin}
	cases := []struct {
		name       string
		definition domain.EnvironmentParameterDefinition
		invalid    any
	}{
		{"type", domain.EnvironmentParameterDefinition{Type: domain.ParameterTypeBoolean, DefaultValue: false}, "false"},
		{"integer", domain.EnvironmentParameterDefinition{Type: domain.ParameterTypeInteger, DefaultValue: 0}, 0.5},
		{"enum", domain.EnvironmentParameterDefinition{Type: domain.ParameterTypeString, Enum: []any{"small", "large"}, DefaultValue: "small"}, "other"},
		{"length", domain.EnvironmentParameterDefinition{Type: domain.ParameterTypeString, MinLength: 2, DefaultValue: "ab"}, "a"},
		{"blank", domain.EnvironmentParameterDefinition{Type: domain.ParameterTypeString, DefaultValue: "set"}, " "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.definition
			d.Label, d.Description = tc.name, "Typed global default"
			created, err := p.PlatformOptions().CreateEnvironmentParameterDefinition(ctx, admin, d)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.PlatformOptions().UpdateEnvironmentParameterDefault(ctx, admin, created.ID, tc.invalid); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("invalid default=%#v error=%v", tc.invalid, err)
			}
			d.Label += " invalid"
			d.DefaultValue = tc.invalid
			if _, err := p.PlatformOptions().CreateEnvironmentParameterDefinition(ctx, admin, d); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("invalid creation: %v", err)
			}
			cleared, err := p.PlatformOptions().UpdateEnvironmentParameterDefault(ctx, admin, created.ID, nil)
			if err != nil || cleared.DefaultValue != nil {
				t.Fatalf("clear default=%#v err=%v", cleared, err)
			}
		})
	}
}
