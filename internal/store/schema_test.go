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

func TestPreviousContractsAreRejected(t *testing.T) {
	content, err := schemaFiles.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{
		"first-version-20260824",
		"first-version-20260824-idempotent-actions",
		"first-version-20260824-candidate-releases",
		"first-version-20260825-candidate-evidence",
		"first-version-20260825-safety-fences",
		"first-version-20260826-reuse-workflows",
		"clusterforge-v1-20260827-modular-readiness",
		"clusterforge-v1-20260828-modular-readiness",
		"clusterforge-v1-20260828-publication-guards",
		"clusterforge-v1-20260829-ssh-connectivity",
		"clusterforge-v1-20260902-container-runtime-matrix",
		"clusterforge-v1-20260903-run-evidence-indexes",
		"clusterforge-v1-20260904-playbook-workspaces",
		"clusterforge-v1-20260905-playbook-workspace-integrity",
	} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "previous.db")
			previous := strings.ReplaceAll(string(content), schemaContract, version)
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

			if _, openErr := Open(ctx, path); openErr == nil || !strings.Contains(openErr.Error(), "unsupported database schema contract") {
				t.Fatalf("previous contract should fail closed, got %v", openErr)
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
	for table, column := range map[string]string{
		"component_releases": "publication_generation",
		"scenario_revisions": "publication_generation",
		"environments":       "archived_at",
	} {
		var count int
		if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s.%s column count=%d err=%v", table, column, count, err)
		}
	}
	var publicationState int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='publication_state'`).Scan(&publicationState); err != nil || publicationState != 1 {
		t.Fatalf("publication_state table count=%d err=%v", publicationState, err)
	}
	var publicationTriggers int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE '%_publication_%'`).Scan(&publicationTriggers); err != nil || publicationTriggers != 12 {
		t.Fatalf("publication generation triggers=%d err=%v", publicationTriggers, err)
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
	var playbookDigestColumn int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('action_definitions') WHERE name='playbook_sha256'`).Scan(&playbookDigestColumn); err != nil || playbookDigestColumn != 1 {
		t.Fatalf("action_definitions.playbook_sha256 column count=%d err=%v", playbookDigestColumn, err)
	}
	var actionMutationTable int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='playbook_action_mutations'`).Scan(&actionMutationTable); err != nil || actionMutationTable != 1 {
		t.Fatalf("playbook_action_mutations table count=%d err=%v", actionMutationTable, err)
	}
	for table, column := range map[string]string{
		"components":            "category",
		"component_releases":    "verified",
		"scenario_revisions":    "execution_policy_json",
		"environment_revisions": "max_concurrent",
	} {
		var found int
		if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&found); err != nil || found != 0 {
			t.Fatalf("removed column %s.%s count=%d err=%v", table, column, found, err)
		}
	}
	var rollbackFenceTriggers int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'runs_environment_rollback_fence_%'`).Scan(&rollbackFenceTriggers); err != nil || rollbackFenceTriggers != 2 {
		t.Fatalf("environment rollback fence triggers=%d err=%v", rollbackFenceTriggers, err)
	}
	var retryAttemptIndex int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_runs_retry_attempt'`).Scan(&retryAttemptIndex); err != nil || retryAttemptIndex != 1 {
		t.Fatalf("retry attempt unique index=%d err=%v", retryAttemptIndex, err)
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
	var sshTable int
	if err := first.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='environment_ssh_checks'`).Scan(&sshTable); err != nil || sshTable != 1 {
		t.Fatalf("environment SSH checks table count=%d err=%v", sshTable, err)
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
		{ID: "component-owner-a", Name: "Owner A", Role: domain.RoleComponentOwner},
	} {
		if err := reopened.UpsertUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	component := componentFixture("fresh-runtime", "component-owner-a")
	seedStoreCatalog(t, reopened)
	if err := reopened.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("fresh-runtime-1", "fresh-runtime", "1.0.0", domain.ReleaseDraft)
	release.Parameters = []domain.ParameterDefinition{{
		Name: "region", Description: "deploy region", Type: domain.ParameterTypeString,
		Required: true, FixedValue: "cn", Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderComponentOwner,
	}}
	if err := reopened.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.GetComponentRelease(ctx, release.ID)
	if err != nil || len(got.Parameters) != 1 || got.Parameters[0].Name != "region" {
		t.Fatalf("fresh release parameters=%+v err=%v", got.Parameters, err)
	}
}
