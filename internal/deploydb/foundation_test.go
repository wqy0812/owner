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

func foundationSource(t *testing.T) string {
	t.Helper()
	source := businessFixture(t)
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The reset copies only the current catalog fields.
	_, err = db.Exec(`
INSERT INTO platform_option_categories(id,technical_key,label,category_type,created_by,created_at) VALUES('os','operatingSystem','OS','environment_dimension','owner','2026-09-05');
INSERT INTO platform_option_categories(id,parent_category_id,technical_key,label,category_type,created_by,created_at) VALUES('os-version','os','operatingSystemVersion','Version','environment_dimension','owner','2026-09-05');
INSERT INTO platform_options(id,category_id,technical_value,label,created_by,created_at) VALUES('os-a','os','OS-A','OS A','owner','2026-09-05');
INSERT INTO platform_options(id,category_id,parent_option_id,technical_value,label,created_by,created_at) VALUES('os-a-v1','os-version','os-a','1','Version 1','owner','2026-09-05');
INSERT INTO environment_variable_definitions VALUES('registry','IMAGE_REGISTRY','Registry','','owner','2026-09-05');
`)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestFoundationResetCopiesOnlyCurrentDirectory(t *testing.T) {
	ctx := context.Background()
	source := foundationSource(t)
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
	for table, want := range map[string]int{"users": 1, "platform_option_categories": 2, "platform_options": 2, "environment_variable_definitions": 1, "components": 0, "environments": 0, "scenarios": 0, "runs": 0, "component_playbook_files": 0} {
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
	for _, excluded := range []string{"must-not-be-copied", "run-history-secret"} {
		if bytes.Contains(data, []byte(excluded)) {
			t.Fatalf("source data leaked: %s", excluded)
		}
	}
	// The recovery image preserves current business definitions without Runs.
	backup := filepath.Join(t.TempDir(), "business.db")
	if err := BusinessSnapshot(ctx, source, backup, false); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBusiness(ctx, backup, false); err != nil {
		t.Fatal(err)
	}
	if err := Verify(ctx, backup, store.CurrentSchemaContract); err != nil {
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
	for _, change := range []string{"UPDATE schema_contract SET version='clusterforge-v1-removed'", "UPDATE runs SET status='running'", "UPDATE users SET role='unsupported-role'", "DROP TABLE platform_options"} {
		source := foundationSource(t)
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
	source := foundationSource(t)
	if err := BusinessSnapshot(context.Background(), source, source, true); err == nil {
		t.Fatal("source overwritten")
	}
}
