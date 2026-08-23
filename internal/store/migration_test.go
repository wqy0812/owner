package store

import (
	"context"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestFreshDatabaseCreatesParameterContractAndRepeatStartupIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open fresh database: %v", err)
	}
	var parametersColumn, mappingsColumn int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('component_releases') WHERE name='parameters_json'`).Scan(&parametersColumn); err != nil || parametersColumn != 1 {
		t.Fatalf("parameters_json column count=%d err=%v", parametersColumn, err)
	}
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('component_dependencies') WHERE name='parameter_mappings_json'`).Scan(&mappingsColumn); err != nil || mappingsColumn != 1 {
		t.Fatalf("parameter_mappings_json column count=%d err=%v", mappingsColumn, err)
	}
	var schemaColumn int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('component_releases') WHERE name='parameter_schema_json'`).Scan(&schemaColumn); err != nil || schemaColumn != 0 {
		t.Fatalf("legacy parameter_schema_json still present: count=%d err=%v", schemaColumn, err)
	}
	var installationTable int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='environment_component_installations'`).Scan(&installationTable); err != nil || installationTable != 1 {
		t.Fatalf("environment component installation table count=%d err=%v", installationTable, err)
	}
	for table, column := range map[string]string{
		"environment_revisions":                  "variables_json",
		"component_image_builds":                 "environment_id",
		"component_image_builds environment rev": "environment_revision_id",
	} {
		if table == "component_image_builds environment rev" {
			table = "component_image_builds"
		}
		var count int
		if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s.%s column count=%d err=%v", table, column, count, err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("repeat startup: %v", err)
	}
	defer reopened.Close()
	var applied int
	if err := reopened.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied == 0 {
		t.Fatalf("migrations applied=%d err=%v", applied, err)
	}
	for _, user := range []domain.User{
		{ID: "component-alice", Name: "Alice", Role: domain.RoleComponentOwner},
	} {
		if err := reopened.UpsertUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	component := componentFixture("fresh-runtime", "component-alice")
	if err := reopened.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("fresh-runtime-1", "fresh-runtime", "1.0.0", domain.ReleaseDraft)
	release.Parameters = []domain.ParameterDefinition{{
		Name: "region", Description: "deploy region", Type: domain.ParameterTypeString,
		Required: true, DefaultValue: "cn", Visibility: domain.ParameterPublic,
	}}
	if err := reopened.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetComponentRelease(ctx, release.ID)
	if err != nil || len(got.Parameters) != 1 || got.Parameters[0].Name != "region" {
		t.Fatalf("fresh release parameters=%+v err=%v", got.Parameters, err)
	}
}
