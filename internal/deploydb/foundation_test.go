package deploydb

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/store"
)

func oldFoundationSource(t *testing.T) string {
	t.Helper()
	source := businessFixture(t)
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Model the pre-workspace / pre-archive contract, including removed tables
	// and columns. Relabeling a current schema alone would hide copy-DDL bugs.
	_, err = db.Exec(`
DROP VIEW retained_run_history;
DROP TRIGGER archived_run_no_log_insert;
DROP TRIGGER archived_run_no_update;
DROP TRIGGER archived_run_no_step_update;
DROP TRIGGER archived_run_no_step_insert;
DROP TRIGGER archived_run_no_approval_update;
DROP TABLE run_archive_files;
DROP TABLE run_archive_tasks;
DROP TABLE run_cleanup_history;
DROP TABLE run_retention_cursors;
DROP TABLE run_retention_policy;
DROP TABLE playbook_action_mutations;
DROP TABLE component_playbook_files;
DROP INDEX idx_releases_playbook_workspace_root;
ALTER TABLE component_releases DROP COLUMN playbook_workspace_root;
ALTER TABLE component_releases DROP COLUMN playbook_tree_sha256;
ALTER TABLE action_definitions DROP COLUMN playbook_sha256;
ALTER TABLE scenario_revisions DROP COLUMN environment_constraints_json;
UPDATE schema_contract SET version='clusterforge-v1-20260903-run-evidence-indexes';
INSERT INTO platform_option_categories(id,technical_key,label,category_type,created_by,created_at) VALUES('os','operatingSystem','OS','environment_dimension','owner','2026-09-05');
INSERT INTO platform_option_categories(id,parent_category_id,technical_key,label,category_type,created_by,created_at) VALUES('os-version','os','operatingSystemVersion','Version','environment_dimension','owner','2026-09-05');
INSERT INTO platform_options(id,category_id,technical_value,label,created_by,created_at) VALUES('os-a','os','OS-A','OS A','owner','2026-09-05');
INSERT INTO platform_options(id,category_id,parent_option_id,technical_value,label,created_by,created_at) VALUES('os-a-v1','os-version','os-a','1','Version 1','owner','2026-09-05');
INSERT INTO environment_variable_definitions VALUES('registry','IMAGE_REGISTRY','Registry','','owner','2026-09-05');
INSERT INTO environment_parameter_definitions(id,technical_key,label,parameter_type,created_by,created_at) VALUES('old-default','old-default','Old default','string','owner','2026-09-05');
INSERT INTO environment_parameter_defaults VALUES('old-default','"removed-global-default"');
`)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestFoundationResetConvertsOnlyDirectoryToCurrentContract(t *testing.T) {
	ctx := context.Background()
	source := oldFoundationSource(t)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "foundation.db")
	if err := BusinessSnapshot(ctx, source, target, true); err != nil {
		t.Fatal(err)
	}
	if err := Verify(ctx, target, store.CurrentSchemaContract); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBusiness(ctx, target, true); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"users": 1, "platform_option_categories": 2, "platform_options": 2, "environment_variable_definitions": 1, "components": 0, "environments": 0, "scenarios": 0, "runs": 0, "environment_parameter_definitions": 0, "environment_parameter_defaults": 0, "component_playbook_files": 0} {
		var count int
		if err := db.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
	var parent string
	if err := db.DB().QueryRow("SELECT parent_option_id FROM platform_options WHERE id='os-a-v1'").Scan(&parent); err != nil || parent != "os-a" {
		t.Fatalf("parent link lost: %s %v", parent, err)
	}
	var archive, cleanup int
	if err := db.DB().QueryRow("SELECT auto_archive,auto_cleanup FROM run_retention_policy WHERE id=1").Scan(&archive, &cleanup); err != nil || archive != 0 || cleanup != 0 {
		t.Fatalf("automatic policy enabled: %v", err)
	}
	db.Close()
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, excluded := range []string{"must-not-be-copied", "run-history-secret", "removed-global-default"} {
		if bytes.Contains(data, []byte(excluded)) {
			t.Fatalf("source data leaked: %s", excluded)
		}
	}
	// The failure-recovery image keeps the old schema and business definitions;
	// its verification/export must tolerate tables absent from that old schema.
	backup := filepath.Join(t.TempDir(), "business.db")
	if err := BusinessSnapshot(ctx, source, backup, false); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBusiness(ctx, backup, false); err != nil {
		t.Fatal(err)
	}
	if err := Verify(ctx, backup, "clusterforge-v1-20260903-run-evidence-indexes"); err != nil {
		t.Fatal(err)
	}
	if err := ExportBusiness(ctx, backup, filepath.Join(t.TempDir(), "business.json")); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("source bytes changed: %v", err)
	}
}

func TestFoundationResetRejectsUnsafeSourceAndPreservesExistingTarget(t *testing.T) {
	for _, change := range []string{"UPDATE runs SET status='running'", "UPDATE users SET role='unsupported-role'", "DROP TABLE platform_options"} {
		source := oldFoundationSource(t)
		db, err := sql.Open("sqlite", source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("PRAGMA ignore_check_constraints=ON;" + change); err != nil {
			t.Fatal(err)
		}
		db.Close()
		target := filepath.Join(t.TempDir(), "foundation.db")
		if err := BusinessSnapshot(context.Background(), source, target, true); err == nil {
			t.Fatalf("accepted %s", change)
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("partial output remains: %v", err)
		}
	}
	source := oldFoundationSource(t)
	if err := BusinessSnapshot(context.Background(), source, source, true); err == nil {
		t.Fatal("source overwritten")
	}
}
