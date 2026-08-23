package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestRemoveEnvironmentParametersMigrationDeprecatesAffectedContracts(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "migrations/011_remove_environment_parameters.sql" {
			break
		}
		contents, readErr := migrationFiles.ReadFile(name)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, execErr := database.ExecContext(ctx, string(contents)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
	now := "2026-08-23T00:00:00Z"
	for _, statement := range []string{
		`INSERT INTO users(id,name,role,created_at) VALUES('owner','Owner','component_owner','` + now + `'),('scenario-owner','Scenario','scenario_owner','` + now + `'),('environment-owner','Environment','environment_owner','` + now + `')`,
		`INSERT INTO components(id,slug,name,owner_id,created_at,updated_at) VALUES('component','component','Component','owner','` + now + `','` + now + `')`,
		`INSERT INTO component_releases(id,component_id,version,release_type,status,parameters_json,created_at) VALUES('release','component','1.0.0','atomic','released','[{"name":"media","environmentPath":"kubernetes.mediaArchiveUrl"}]','` + now + `')`,
		`INSERT INTO scenarios(id,slug,name,owner_id,current_revision_id,created_at,updated_at) VALUES('scenario','scenario','Scenario','scenario-owner','revision','` + now + `','` + now + `')`,
		`INSERT INTO scenario_revisions(id,scenario_id,revision,status,graph_json,execution_policy_json,created_at) VALUES('revision','scenario',1,'released','{"nodes":[{"id":"node","releaseId":"release","bindings":{"media":"kubernetes.mediaArchiveUrl"}}],"edges":[]}','{}','` + now + `')`,
		`INSERT INTO environments(id,name,owner_id,current_revision_id,created_at,updated_at) VALUES('environment','Environment','environment-owner','environment-r1','` + now + `','` + now + `')`,
		`INSERT INTO environment_revisions(id,environment_id,revision,facts_json,inventory_json,parameters_json,variables_json,credential_refs_json,max_concurrent,created_at) VALUES('environment-r1','environment',1,'{}','{}','{"kubernetes":{"mediaArchiveUrl":"http://old"}}','{}','[]',1,'` + now + `')`,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	contents, _ := migrationFiles.ReadFile("migrations/011_remove_environment_parameters.sql")
	if _, err := database.ExecContext(ctx, string(contents)); err != nil {
		t.Fatalf("apply removal migration: %v", err)
	}
	var releaseStatus, parametersJSON, revisionStatus, graphJSON string
	if err := database.QueryRowContext(ctx, `SELECT status,parameters_json FROM component_releases WHERE id='release'`).Scan(&releaseStatus, &parametersJSON); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT status,graph_json FROM scenario_revisions WHERE id='revision'`).Scan(&revisionStatus, &graphJSON); err != nil {
		t.Fatal(err)
	}
	if releaseStatus != "deprecated" || revisionStatus != "deprecated" || parametersJSON != `[{"name":"media"}]` || graphJSON == "" {
		t.Fatalf("release=%s parameters=%s scenario=%s graph=%s", releaseStatus, parametersJSON, revisionStatus, graphJSON)
	}
	var bindings, environmentParameters int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM json_each(json_extract((SELECT graph_json FROM scenario_revisions WHERE id='revision'),'$.nodes[0].bindings'))`).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("bindings count=%d err=%v", bindings, err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('environment_revisions') WHERE name='parameters_json'`).Scan(&environmentParameters); err != nil || environmentParameters != 0 {
		t.Fatalf("environment parameters column count=%d err=%v", environmentParameters, err)
	}
}

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
		"scenario_revisions":                     "abandoned_at",
	} {
		if table == "component_image_builds environment rev" {
			table = "component_image_builds"
		}
		var count int
		if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s.%s column count=%d err=%v", table, column, count, err)
		}
	}
	var activeDraftIndex int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_scenario_revisions_one_active'`).Scan(&activeDraftIndex); err != nil || activeDraftIndex != 1 {
		t.Fatalf("active scenario draft index count=%d err=%v", activeDraftIndex, err)
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
