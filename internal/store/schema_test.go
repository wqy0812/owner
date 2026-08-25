package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestDatabaseWithoutFirstVersionContractIsRejected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "unsupported.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE users(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "missing schema contract") {
		t.Fatalf("unsupported database rejection error=%v", err)
	}
}

func TestSupportedContractsMigrateToCandidateEvidence(t *testing.T) {
	content, err := schemaFiles.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	triggerSQL := `CREATE TRIGGER IF NOT EXISTS component_releases_candidate_requires_verified_insert
BEFORE INSERT ON component_releases
WHEN NEW.candidate=1 AND NEW.verified<>1
BEGIN
  SELECT RAISE(ABORT, 'candidate release must be verified');
END;

CREATE TRIGGER IF NOT EXISTS component_releases_candidate_requires_verified_update
BEFORE UPDATE OF candidate,verified ON component_releases
WHEN NEW.candidate=1 AND NEW.verified<>1
BEGIN
  SELECT RAISE(ABORT, 'candidate release must be verified');
END;

`
	for _, version := range []string{legacySchemaContract, idempotentSchemaContract, candidateSchemaContract} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "previous.db")
			previous := strings.ReplaceAll(string(content), schemaContract, version)
			previous = strings.Replace(previous, triggerSQL, "", 1)
			if version == legacySchemaContract || version == idempotentSchemaContract {
				previous = strings.Replace(previous, "  candidate INTEGER NOT NULL DEFAULT 0 CHECK (candidate IN (0,1)),\n", "", 1)
			}
			if version == legacySchemaContract {
				previous = strings.Replace(previous, "  idempotent INTEGER NOT NULL DEFAULT 0 CHECK (idempotent IN (0,1)),\n", "", 1)
			}
			database, openErr := sql.Open("sqlite", path)
			if openErr != nil {
				t.Fatal(openErr)
			}
			if _, execErr := database.ExecContext(ctx, previous); execErr != nil {
				t.Fatal(execErr)
			}
			if closeErr := database.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}

			migrated, migrateErr := Open(ctx, path)
			if migrateErr != nil {
				t.Fatalf("migrate previous contract: %v", migrateErr)
			}
			defer migrated.Close()
			var contract string
			if queryErr := migrated.DB().QueryRowContext(ctx, `SELECT version FROM schema_contract WHERE id=1`).Scan(&contract); queryErr != nil || contract != schemaContract {
				t.Fatalf("migrated schema contract=%q err=%v", contract, queryErr)
			}
			for table, column := range map[string]string{"action_definitions": "idempotent", "component_releases": "candidate"} {
				var count int
				if queryErr := migrated.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); queryErr != nil || count != 1 {
					t.Fatalf("migrated %s.%s count=%d err=%v", table, column, count, queryErr)
				}
			}
			var triggers int
			if queryErr := migrated.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'component_releases_candidate_requires_verified_%'`).Scan(&triggers); queryErr != nil || triggers != 2 {
				t.Fatalf("candidate evidence triggers=%d err=%v", triggers, queryErr)
			}
		})
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
		t.Fatalf("removed parameter_schema_json present: count=%d err=%v", schemaColumn, err)
	}
	var installationTable int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='environment_component_installations'`).Scan(&installationTable); err != nil || installationTable != 1 {
		t.Fatalf("environment component installation table count=%d err=%v", installationTable, err)
	}
	var idempotentColumn int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('action_definitions') WHERE name='idempotent'`).Scan(&idempotentColumn); err != nil || idempotentColumn != 1 {
		t.Fatalf("action_definitions.idempotent column count=%d err=%v", idempotentColumn, err)
	}
	var candidateTriggers int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'component_releases_candidate_requires_verified_%'`).Scan(&candidateTriggers); err != nil || candidateTriggers != 2 {
		t.Fatalf("candidate evidence triggers=%d err=%v", candidateTriggers, err)
	}
	for table, column := range map[string]string{
		"environment_revisions":                  "variables_json",
		"environment_revisions created by":       "created_by",
		"environment_revisions change reason":    "change_reason",
		"component_image_builds":                 "environment_id",
		"component_image_builds environment rev": "environment_revision_id",
		"scenario_revisions":                     "abandoned_at",
	} {
		if table == "component_image_builds environment rev" {
			table = "component_image_builds"
		} else if strings.HasPrefix(table, "environment_revisions ") {
			table = "environment_revisions"
		}
		var count int
		if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s.%s column count=%d err=%v", table, column, count, err)
		}
	}
	var healthTable int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='environment_health_checks'`).Scan(&healthTable); err != nil || healthTable != 1 {
		t.Fatalf("environment health checks table count=%d err=%v", healthTable, err)
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
	var contract string
	if err := reopened.DB().QueryRowContext(ctx, `SELECT version FROM schema_contract WHERE id=1`).Scan(&contract); err != nil || contract != schemaContract {
		t.Fatalf("schema contract=%q err=%v", contract, err)
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
