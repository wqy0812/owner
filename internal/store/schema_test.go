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

func TestSupportedContractsMigrateToSafetyFences(t *testing.T) {
	content, err := schemaFiles.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	candidateTriggerSQL := `CREATE TRIGGER IF NOT EXISTS component_releases_candidate_requires_verified_insert
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
	rollbackTriggerSQL := `CREATE TRIGGER IF NOT EXISTS runs_environment_rollback_fence_insert
BEFORE INSERT ON runs
WHEN NEW.status IN ('awaiting_approval','queued','running') AND (
  (NEW.kind='environment_rollback' AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.environment_id=NEW.environment_id
      AND r.status IN ('awaiting_approval','queued','running')
  ))
  OR
  (NEW.kind<>'environment_rollback' AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.environment_id=NEW.environment_id
      AND r.kind='environment_rollback'
      AND r.status IN ('awaiting_approval','queued','running')
  ))
)
BEGIN
  SELECT RAISE(ABORT, 'environment rollback fence conflict');
END;

CREATE TRIGGER IF NOT EXISTS runs_environment_rollback_fence_update
BEFORE UPDATE OF status,environment_id,kind ON runs
WHEN NEW.status IN ('awaiting_approval','queued','running') AND (
  (NEW.kind='environment_rollback' AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.id<>NEW.id
      AND r.environment_id=NEW.environment_id
      AND r.status IN ('awaiting_approval','queued','running')
  ))
  OR
  (NEW.kind<>'environment_rollback' AND EXISTS (
    SELECT 1 FROM runs r
    WHERE r.id<>NEW.id
      AND r.environment_id=NEW.environment_id
      AND r.kind='environment_rollback'
      AND r.status IN ('awaiting_approval','queued','running')
  ))
)
BEGIN
  SELECT RAISE(ABORT, 'environment rollback fence conflict');
END;

`
	for _, version := range []string{legacySchemaContract, idempotentSchemaContract, candidateSchemaContract, evidenceSchemaContract} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "previous.db")
			previous := strings.ReplaceAll(string(content), schemaContract, version)
			previous = strings.Replace(previous, rollbackTriggerSQL, "", 1)
			if version != evidenceSchemaContract {
				previous = strings.Replace(previous, candidateTriggerSQL, "", 1)
			}
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
			if version == candidateSchemaContract {
				if _, execErr := database.ExecContext(ctx, `
INSERT INTO users(id,name,role,created_at) VALUES('candidate-owner','Owner','component_owner','2026-08-25T00:00:00Z');
INSERT INTO components(id,slug,name,description,layer,category,component_kind,requiredness,owner_id,created_at,updated_at)
VALUES('dirty-component','dirty-component','Dirty','','runtime_state','runtime','software','optional','candidate-owner','2026-08-25T00:00:00Z','2026-08-25T00:00:00Z');
INSERT INTO component_releases(id,component_id,version,release_type,status,release_notes,breaking,verified,candidate,risk_level,environment_constraints_json,parameters_json,created_at)
VALUES('dirty-release','dirty-component','1.0.0','atomic','draft','',0,0,1,'low','{}','[]','2026-08-25T00:00:00Z');`); execErr != nil {
					t.Fatal(execErr)
				}
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
			if queryErr := migrated.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'runs_environment_rollback_fence_%'`).Scan(&triggers); queryErr != nil || triggers != 2 {
				t.Fatalf("environment rollback fence triggers=%d err=%v", triggers, queryErr)
			}
			if version == candidateSchemaContract {
				var candidate int
				if queryErr := migrated.DB().QueryRowContext(ctx, `SELECT candidate FROM component_releases WHERE id='dirty-release'`).Scan(&candidate); queryErr != nil || candidate != 0 {
					t.Fatalf("dirty candidate repair=%d err=%v", candidate, queryErr)
				}
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
	var rollbackFenceTriggers int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'runs_environment_rollback_fence_%'`).Scan(&rollbackFenceTriggers); err != nil || rollbackFenceTriggers != 2 {
		t.Fatalf("environment rollback fence triggers=%d err=%v", rollbackFenceTriggers, err)
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
