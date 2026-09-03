package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/seed"
	"codex/platform-demo/internal/store"
)

func TestGlobalDefaultCatalogRoundTripRequiresCurrentTables(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source, err := store.Open(ctx, filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	now := time.Now().UTC()
	admin := domain.User{ID: "default-admin", Name: "Platform Owner", Role: domain.RolePlatformAdmin, CreatedAt: now}
	owner := domain.User{ID: "default-owner", Name: "Component Owner", Role: domain.RoleComponentOwner, CreatedAt: now}
	for _, u := range []domain.User{admin, owner} {
		if err := source.UpsertUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	d := domain.EnvironmentParameterDefinition{ID: "default-field", Key: "default_field", Label: "Enabled", Description: "Feature switch", Type: domain.ParameterTypeBoolean, DefaultValue: false, CreatedBy: admin.ID, CreatedAt: now}
	if err := source.UpsertEnvironmentParameterDefinition(ctx, d); err != nil {
		t.Fatal(err)
	}
	c := domain.Component{ID: "default-component", Slug: "default-component", Name: "Default consumer", OwnerID: owner.ID, Layer: domain.LayerRuntimeState, CreatedAt: now, UpdatedAt: now}
	if err := source.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := domain.ComponentRelease{ID: "default-release", ComponentID: c.ID, LineName: "Baseline", Version: "1.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now, Parameters: []domain.ParameterDefinition{{Name: "enabled", Description: d.Description, Type: d.Type, ValueProvider: domain.ParameterProviderEnvironmentOwner, Modifiable: true, Visibility: domain.ParameterPublic, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingGlobal, DefinitionID: d.ID}}}}
	if err := source.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(root, "export")
	if err := os.MkdirAll(exported, 0700); err != nil {
		t.Fatal(err)
	}
	catalog, encoded, err := ExportCatalog(ctx, filepath.Join(root, "source.db"), root, exported)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCatalog(encoded); err != nil {
		t.Fatal(err)
	}
	for _, missingTable := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "missing-table"}[missingTable], func(t *testing.T) {
			data := catalog
			data.Tables = nil
			for _, table := range catalog.Tables {
				if !missingTable || table.Name != "environment_parameter_defaults" {
					data.Tables = append(data.Tables, table)
				}
			}
			if err := validateCatalog(data); missingTable {
				if err == nil || !strings.Contains(err.Error(), "missing table environment_parameter_defaults") {
					t.Fatalf("incomplete catalog error=%v", err)
				}
				return
			} else if err != nil {
				t.Fatal(err)
			}
			target, err := store.Open(ctx, filepath.Join(t.TempDir(), "restored.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if err := restoreTables(ctx, target.DB(), data); err != nil {
				t.Fatal(err)
			}
			got, err := target.GetEnvironmentParameterDefinition(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.DefaultValue != false {
				t.Fatalf("restored default=%#v", got.DefaultValue)
			}
		})
	}
	if _, err := source.DB().ExecContext(ctx, "DROP TABLE environment_parameter_defaults"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExportCatalog(ctx, filepath.Join(root, "source.db"), root, exported); err == nil {
		t.Fatal("incomplete source database must not be silently exported")
	}

}

func TestCatalogRestoreBootstrapsEnvironmentVariablesOnStartup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.db")
	source, err := store.Open(ctx, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := (seed.Seeder{Store: source}).SeedUsers(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	c := domain.Component{ID: "review-component", Slug: "review-component", Name: "Review component", OwnerID: seed.ComponentOwnerRuntimeID, Layer: domain.LayerRuntimeState, CreatedAt: now, UpdatedAt: now}
	if err := source.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := domain.ComponentRelease{ID: "review-release", ComponentID: c.ID, LineName: "Review", Version: "1.0.0", Status: domain.ReleaseReleased, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now, ReleasedAt: &now, EnvironmentConstraints: map[string]any{"architecture": []string{"amd64"}}}
	if err := source.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	exported := filepath.Join(root, "export")
	if err := os.MkdirAll(exported, 0700); err != nil {
		t.Fatal(err)
	}
	catalog, _, err := ExportCatalog(ctx, sourcePath, root, exported)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(ctx, filepath.Join(root, "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := restoreTables(ctx, target.DB(), catalog); err != nil {
		t.Fatal(err)
	}
	if err := (seed.Seeder{Store: target}).SeedUsers(ctx); err != nil {
		t.Fatal(err)
	}
	variables, err := target.ListEnvironmentVariableDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, v := range variables {
		names[v.Name] = true
	}
	if !names["IMAGE_REGISTRY"] || !names["FILE_STATION"] {
		t.Fatalf("restored directory is incomplete: %v", variables)
	}
	if err := domain.ValidateEnvironmentValues(nil, map[string]string{"IMAGE_REGISTRY": "registry.example.invalid:5000"}, nil, nil, nil, variables); err != nil {
		t.Fatalf("registry cannot be configured: %v", err)
	}
	categories, err := target.ListPlatformOptionCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(categories) != 1 || categories[0].Key != "architecture" {
		t.Fatalf("restored option directory was overwritten: %v", categories)
	}
	if err := (seed.Seeder{Store: target}).SeedUsers(ctx); err != nil {
		t.Fatal(err)
	}
}
