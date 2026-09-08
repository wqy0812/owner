package runmigration

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/runarchive"
	"codex/platform-demo/internal/store"
)

//go:embed testdata/source_schema.sql
var sourceSchema string

func TestMigrationTargetIsPrivateAndRejectsPermissionDrift(t *testing.T) {
	o, db := sourceFixture(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(o.Source, 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	o.ExpectedSourceDigest, err = SourceDigest(o.Source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Migrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{o.Target, o.Target + ".source.db", o.ReportPath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private migration artifact %s: %v %v", path, info, err)
		}
	}
	if err = VerifyReady(context.Background(), o.Source, o.Target, o.ReportPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(o.Target, 0644); err != nil {
		t.Fatal(err)
	}
	if err = VerifyReady(context.Background(), o.Source, o.Target, o.ReportPath); err == nil || !strings.Contains(err.Error(), "private regular file") {
		t.Fatalf("permission drift accepted: %v", err)
	}
}

func sourceFixture(t *testing.T) (Options, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	o := Options{Source: filepath.Join(dir, "source.db"), Target: filepath.Join(dir, "target.db"), ReportPath: filepath.Join(dir, "ready.json"), ArchiveRoot: dir}
	db, e := openDB(o.Source, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := db.Exec(query, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(sourceSchema)
	exec(`INSERT INTO users(id,name,role,created_at) VALUES('owner','Owner','environment_owner','2026-09-01')`)
	exec(`INSERT INTO environments(id,name,owner_id,created_at,updated_at) VALUES('env','Env','owner','2026-09-01','2026-09-01')`)
	exec(`INSERT INTO environment_revisions(id,environment_id,revision,created_at) VALUES('revision','env',1,'2026-09-01')`)
	for i, status := range []string{"succeeded", "failed", "interrupted", "rejected", "cancelled"} {
		plan := domain.RunExecutionPlan{Steps: []domain.RunPlanStep{{ID: "step", NodeID: "node", Variables: map[string]any{"runId": "not-a-reference"}}}, TreeDigest: "preserved-content", DeliveryResults: []domain.RunDeliveryResult{{RequirementID: "image", Mode: "transfer", Status: "delivered", ActualLocation: "immutable/image@sha256:abc"}}}
		if i == 1 {
			plan.Steps[0].Backup = &domain.BackupMetadata{InstallRunID: "run-0"}
		}
		raw, e := json.Marshal(plan)
		if e != nil {
			t.Fatal(e)
		}
		exec(`INSERT INTO runs(rowid,id,kind,status,requested_by,environment_id,environment_revision_id,input_snapshot_json,artifact_digest,created_at,finished_at) VALUES(?,?,'component_test',?,'owner','env','revision',?,'job-digest','2026-09-01','2026-09-02')`, 100+i, fmt.Sprintf("run-%d", i), status, string(raw))
	}
	exec(`UPDATE runs SET retry_of_run_id='run-0',retry_root_run_id='run-0',retry_attempt=1,retry_start_step=1,input_snapshot_json=json_set(input_snapshot_json,'$.retryRecoveryStateDigest','recovery-state') WHERE id='run-1'`)
	exec(`INSERT INTO run_jobs(run_id,digest,bundle,exit_code) VALUES('run-0','existing-native-job',?,0)`, []byte{0, 1, 7, 255, 23})
	exec(`INSERT INTO run_cleanup_history(id,kind,status,requested_by,environment_id,environment_revision_id,action_kind,created_at,finished_at,cleaned_at,actor_id,reason,identity_json) VALUES('cleaned','component_test','failed','owner','env','revision','install','2026-09-01','2026-09-02','2026-09-03','owner','retention','{"cleaned":true,"componentReleaseSpecDigest":"retained-digest","steps":[]}')`)
	return o, db
}
func addArchive(t *testing.T, db *sql.DB, root string) string {
	t.Helper()
	dir := t.TempDir()
	for n, v := range map[string]string{"run.json": `{"id":"run-0","inputSnapshot":{"old":"unchanged"}}`, "run_steps.json": "[]", "approvals.json": "[]", "logs.ndjson": ""} {
		if e := os.WriteFile(filepath.Join(dir, n), []byte(v), 0600); e != nil {
			t.Fatal(e)
		}
	}
	path := filepath.Join(root, "run-0.tar.gz")
	if e := runarchive.Pack(context.Background(), dir, path, "run-0", 0); e != nil {
		t.Fatal(e)
	}
	hash, size, e := runarchive.HashFile(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO run_archive_files(run_id,relative_path,format_version,sha256,size_bytes,log_count,source_digest,archived_at) VALUES('run-0','run-0.tar.gz',?,?,?,0,'original-source-digest','2026-09-03')`, runarchive.FormatVersion, hash, size)
	if e != nil {
		t.Fatal(e)
	}
	return hash
}
func TestOfflineMigrationPreservesDataArtifactsAndOrdering(t *testing.T) {
	o, db := sourceFixture(t)
	archiveHash := addArchive(t, db, o.ArchiveRoot)
	db.Close()
	before, e := SourceDigest(o.Source)
	if e != nil {
		t.Fatal(e)
	}
	pre := o
	pre.DryRun = true
	pre.ReportPath = filepath.Join(filepath.Dir(o.Source), "preflight.json")
	report, e := Migrate(context.Background(), pre)
	if e != nil {
		t.Fatal(e)
	}
	if report.Status != "checked" || report.SourceDigest != before {
		t.Fatalf("preflight=%+v", report)
	}
	if _, e = os.Stat(o.Target); !os.IsNotExist(e) {
		t.Fatal("dry-run published target")
	}
	o.ExpectedSourceDigest = report.SourceDigest
	report, e = Migrate(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	if report.Status != "ready" || len(report.Runs) != 5 || len(report.Archives) != 1 {
		t.Fatalf("report=%+v", report)
	}
	if e = VerifyReady(context.Background(), o.Source, o.Target, o.ReportPath); e != nil {
		t.Fatal(e)
	}
	after, e := SourceDigest(o.Source)
	if e != nil || after != before {
		t.Fatal("source changed", e)
	}
	if h, _, e := runarchive.HashFile(filepath.Join(o.ArchiveRoot, "run-0.tar.gz")); e != nil || h != archiveHash {
		t.Fatal("archive bytes changed", e)
	}
	target, e := store.Open(context.Background(), o.Target)
	if e != nil {
		t.Fatal(e)
	}
	r, e := target.GetRun(context.Background(), "run-1")
	if e != nil {
		t.Fatal(e)
	}
	if r.RetryOfRunID != "run-0" || r.RetryAttempt != 1 || r.ArtifactDigest != "job-digest" || r.Snapshot.Plan.TreeDigest != "preserved-content" || r.DeliveryResults[0].Status != "delivered" {
		t.Fatalf("identity changed: %+v", r)
	}
	var rowid int
	var bundle []byte
	var ref string
	if e = target.DB().QueryRow(`SELECT rowid FROM runs WHERE id='run-1'`).Scan(&rowid); e != nil || rowid != 101 {
		t.Fatal("rowid lost", rowid, e)
	}
	if e = target.DB().QueryRow(`SELECT bundle FROM run_jobs WHERE run_id='run-0'`).Scan(&bundle); e != nil || !reflect.DeepEqual(bundle, []byte{0, 1, 7, 255, 23}) {
		t.Fatal("job bytes changed", e)
	}
	if e = target.DB().QueryRow(`SELECT referenced_run_id FROM run_snapshot_references WHERE run_id='run-1'`).Scan(&ref); e != nil || ref != "run-0" {
		t.Fatal("reference lost", e)
	}
	var refs int
	if e = target.DB().QueryRow(`SELECT COUNT(*) FROM run_snapshot_references`).Scan(&refs); e != nil || refs != 1 {
		t.Fatal("user parameter became reference", refs, e)
	}
	var digest string
	if e = target.DB().QueryRow(`SELECT component_spec_digest FROM retained_run_history WHERE id='cleaned'`).Scan(&digest); e != nil || digest != "retained-digest" {
		t.Fatal("cleanup identity lost", e)
	}
	if _, e = target.DB().Exec(`UPDATE runs SET execution_snapshot_json=json_set(execution_snapshot_json,'$.plan.treeDigest','changed') WHERE id='run-1'`); e == nil {
		t.Fatal("freeze trigger was not restored")
	}
	target.Close()
	target, e = store.Open(context.Background(), o.Target)
	if e != nil {
		t.Fatal("reopen", e)
	}
	target.Close()
	// Before opening writes, rollback is a program/database pair switch. Original
	// database and independent backup remain readable with the exact source schema.
	backup, e := openDB(report.SourceBackupPath, true)
	if e != nil {
		t.Fatal(e)
	}
	defer backup.Close()
	if e = backup.QueryRow(`SELECT json_extract(input_snapshot_json,'$.treeDigest') FROM runs WHERE id='run-1'`).Scan(&digest); e != nil || digest != "preserved-content" {
		t.Fatal("rollback backup lost source", e)
	}
}
func TestMigrationRejectsWithoutChangingSourceOrPublishingTarget(t *testing.T) {
	for _, tc := range []struct{ name, sql, contains string }{
		{"unknown field", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.unknownControl',true) WHERE id='run-1'`, "unknownControl"},
		{"wrong integer", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.steps[0].timeoutSeconds','30') WHERE id='run-1'`, "timeoutSeconds"},
		{"missing boolean", `UPDATE runs SET input_snapshot_json=json_remove(input_snapshot_json,'$.steps[0].become') WHERE id='run-1'`, "snapshot.steps[0].become"},
		{"null boolean", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.steps[0].become',NULL) WHERE id='run-1'`, "snapshot.steps[0].become"},
		{"orphan scenario baseline", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.baselineGeneration',0) WHERE id='run-1'`, "snapshot.baselineGeneration"},
		{"orphan scenario receipts", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.recoveredReceipts',json('[]')) WHERE id='run-1'`, "snapshot.recoveredReceipts"},
		{"queued", `UPDATE runs SET status='queued' WHERE id='run-1'`, "active work"},
		{"running", `UPDATE runs SET status='running' WHERE id='run-1'`, "active work"},
		{"approval", `UPDATE runs SET status='awaiting_approval' WHERE id='run-1'`, "active work"},
		{"wrong contract", `UPDATE schema_contract SET version='unexpected'`, "contract"},
		{"missing table", `DROP TABLE run_retention_cursors`, "table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, db := sourceFixture(t)
			if _, e := db.Exec(tc.sql); e != nil {
				t.Fatal(e)
			}
			db.Close()
			before, _ := SourceDigest(o.Source)
			o.ExpectedSourceDigest = before
			if _, e := Migrate(context.Background(), o); e == nil || !strings.Contains(e.Error(), tc.contains) {
				t.Fatalf("rejection=%v", e)
			}
			after, _ := SourceDigest(o.Source)
			if before != after {
				t.Fatal("source mutated on failure")
			}
			for _, p := range []string{o.Target, o.Target + ".source.db", o.ReportPath} {
				if _, e := os.Stat(p); !os.IsNotExist(e) {
					t.Fatal("published failure", p, e)
				}
			}
		})
	}
}
func TestMigrationOutputConflictCancellationAndSourceBinding(t *testing.T) {
	for _, reason := range []string{"target", "report", "cancel", "source-changed"} {
		t.Run(reason, func(t *testing.T) {
			o, db := sourceFixture(t)
			db.Close()
			o.ExpectedSourceDigest, _ = SourceDigest(o.Source)
			ctx := context.Background()
			switch reason {
			case "target":
				os.WriteFile(o.Target, []byte("existing"), 0600)
			case "report":
				os.WriteFile(o.ReportPath, []byte("existing"), 0600)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "source-changed":
				o.ExpectedSourceDigest = "old"
			}
			if _, e := Migrate(ctx, o); e == nil {
				t.Fatal("unsafe migration accepted")
			}
			if reason == "target" {
				b, _ := os.ReadFile(o.Target)
				if string(b) != "existing" {
					t.Fatal("overwrote target")
				}
			}
		})
	}
}

func TestVerifiedMigrationRejectsChangedArtifacts(t *testing.T) {
	for _, kind := range []string{"source", "target", "backup", "archive", "checked-report"} {
		t.Run(kind, func(t *testing.T) {
			o, db := sourceFixture(t)
			addArchive(t, db, o.ArchiveRoot)
			db.Close()
			o.ExpectedSourceDigest, _ = SourceDigest(o.Source)
			report, e := Migrate(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "source":
				changed, e := openDB(o.Source, false)
				if e != nil {
					t.Fatal(e)
				}
				_, e = changed.Exec(`UPDATE runs SET error_text='new write' WHERE id='run-1'`)
				changed.Close()
				if e != nil {
					t.Fatal(e)
				}
			case "target":
				os.WriteFile(o.Target, []byte("other database"), 0600)
			case "backup":
				os.WriteFile(report.SourceBackupPath, []byte("other backup"), 0600)
			case "archive":
				os.WriteFile(filepath.Join(o.ArchiveRoot, "run-0.tar.gz"), []byte("other archive"), 0600)
			case "checked-report":
				report.Status = "checked"
				raw, _ := json.Marshal(report)
				os.WriteFile(o.ReportPath, raw, 0600)
			}
			if e = VerifyReady(context.Background(), o.Source, o.Target, o.ReportPath); e == nil {
				t.Fatal("changed artifact accepted")
			}
		})
	}
}
