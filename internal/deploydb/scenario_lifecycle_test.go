package deploydb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func lifecycleConversionFixture(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "old.db")
	playbooks := filepath.Join(root, "source-playbooks")
	if err := os.MkdirAll(filepath.Join(playbooks, "managed", "templates"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(playbooks, "managed", "templates", "config.j2"), []byte("original source {{ value }}"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = db.UpsertUser(ctx, domain.User{ID: "owner", Name: "Owner", Role: domain.RoleScenarioOwner, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	revision := domain.ScenarioRevision{ID: "revision", ScenarioID: "scenario", Revision: 1, Status: domain.RevisionReleased, CreatedAt: now, ReleasedAt: &now, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}}}
	if err = db.CreateScenario(ctx, domain.Scenario{ID: "scenario", Slug: "scenario", Name: "Scenario", OwnerID: "owner", CreatedAt: now, UpdatedAt: now}, revision); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO environments(id,name,owner_id,current_revision_id,created_at,updated_at) VALUES('environment','Env','owner','env-revision','2026-09-05','2026-09-05')`,
		`INSERT INTO environment_revisions(id,environment_id,revision,inventory_json,created_at) VALUES('env-revision','environment',1,'{"hosts":[{"id":"original-node"}]}','2026-09-05')`,
		`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,input_snapshot_json,artifact_digest,created_at) VALUES('formal','scenario_run','succeeded','owner','environment','env-revision','revision','{"steps":[{"nodeId":"original-node","backupRef":"backup-original"}],"historicalIdentity":"immutable"}','old-digest','2026-09-05')`,
		`INSERT INTO run_steps(id,run_id,node_id,name,status,exit_code,summary) VALUES('step','formal','original-node','Original','succeeded',0,'immutable result')`,
		`INSERT INTO run_logs(run_id,step_id,stream,message,created_at) VALUES('formal','step','stdout','original execution log','2026-09-05')`,
		`DROP TABLE scenario_installations`, `DROP TABLE scenario_execution_submissions`,
		`ALTER TABLE scenarios DROP COLUMN forked_from_scenario_id`, `ALTER TABLE scenarios DROP COLUMN forked_from_revision_id`, `ALTER TABLE scenarios DROP COLUMN forked_from_digest`,
		`ALTER TABLE scenario_revisions DROP COLUMN lifecycle_json`,
		`UPDATE schema_contract SET version='clusterforge-v1-20260905-role-jobs' WHERE id=1`,
	}
	for _, statement := range statements {
		if _, err = db.DB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return source, playbooks
}
func TestScenarioLifecycleConversionPreservesHistoryAndSource(t *testing.T) {
	ctx := context.Background()
	source, playbooks := lifecycleConversionFixture(t)
	target := filepath.Join(filepath.Dir(source), "converted.db")
	targetRoot := filepath.Join(filepath.Dir(source), "converted-playbooks")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	report, err := ConvertScenarioLifecycle(ctx, source, target, playbooks, targetRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HistoryPreserved || !report.SourceUnchanged || report.WorkspaceFiles != 1 || report.PreservedRows < 5 {
		t.Fatalf("report=%+v", report)
	}
	after, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(original) != sha256.Sum256(after) {
		t.Fatalf("source bytes changed: %v", err)
	}
	db, err := store.Open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, err := db.GetRun(ctx, "formal")
	if err != nil || run.ArtifactDigest != "old-digest" || run.InputSnapshot["historicalIdentity"] != "immutable" || len(run.Steps) != 1 || run.Steps[0].NodeID != "original-node" {
		t.Fatalf("historic Run=%+v error=%v", run, err)
	}
	revision, err := db.GetScenarioRevision(ctx, "revision")
	if err != nil || revision.DigestVersion != 0 || revision.SourceRevisionID != "" || len(revision.AcceptanceJobs) != 0 {
		t.Fatalf("legacy revision rewritten: %+v %v", revision, err)
	}
	baseline, err := db.GetScenarioInstallation(ctx, "environment", "scenario")
	if err != nil || baseline.State != "unverified" || baseline.RunID != "" || !baseline.TestOnly {
		t.Fatalf("baseline falsely confirmed: %+v %v", baseline, err)
	}
	copied, err := os.ReadFile(filepath.Join(targetRoot, "managed", "templates", "config.j2"))
	if err != nil || string(copied) != "original source {{ value }}" {
		t.Fatalf("copy=%s %v", copied, err)
	}
	if err = os.WriteFile(filepath.Join(targetRoot, "managed", "templates", "config.j2"), []byte("independent"), 0600); err != nil {
		t.Fatal(err)
	}
	preserved, _ := os.ReadFile(filepath.Join(playbooks, "managed", "templates", "config.j2"))
	if string(preserved) != "original source {{ value }}" {
		t.Fatal("target workspace is linked to source")
	}
}
func TestScenarioLifecycleConversionFailsWithoutReplacingOutputs(t *testing.T) {
	ctx := context.Background()
	source, playbooks := lifecycleConversionFixture(t)
	root := filepath.Dir(source)
	target := filepath.Join(root, "converted.db")
	targetRoot := filepath.Join(root, "converted-playbooks")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertScenarioLifecycle(ctx, source, target, playbooks, targetRoot); err == nil {
		t.Fatal("existing destination accepted")
	}
	preserved, _ := os.ReadFile(target)
	if string(preserved) != "keep" {
		t.Fatal("existing destination overwritten")
	}
	os.Remove(target)
	raw, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`UPDATE runs SET status='queued' WHERE id='formal'`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if _, err = ConvertScenarioLifecycle(ctx, source, target, playbooks, targetRoot); err == nil || !strings.Contains(err.Error(), "active work") {
		t.Fatalf("active conversion=%v", err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("failed conversion left target: %v", err)
	}
}

func TestScenarioLifecycleConversionRejectsExistingWorkspaceManifestDrift(t *testing.T) {
	ctx := context.Background()
	source, root := lifecycleConversionFixture(t)
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "managed", "templates", "config.j2"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := fmt.Sprintf("%x", sum)
	for _, statement := range []string{
		`INSERT INTO components(id,slug,name,owner_id,created_at,updated_at) VALUES('component','component','Component','owner','2026-09-05','2026-09-05')`,
		`INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES('line','component','Main','2026-09-05')`,
		`INSERT INTO component_releases(id,component_id,line_id,version,status,compatibility,risk_level,created_at) VALUES('release','component','line','1.0.0','draft','not_applicable','low','2026-09-05')`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO component_playbook_files(release_id,relative_path,sha256,size_bytes,media_type,updated_at) VALUES('release','templates/config.j2',?,?,'text/plain','2026-09-05')`, digest, len(data)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE component_releases SET playbook_workspace_root='managed/',playbook_tree_sha256=? WHERE id='release'`, conversionTreeDigest(map[string]string{"templates/config.j2": digest})); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = os.WriteFile(filepath.Join(root, "managed", "templates", "config.j2"), []byte("out-of-band edit"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(source), "converted.db")
	targetRoot := filepath.Join(filepath.Dir(source), "converted-playbooks")
	if _, err = ConvertScenarioLifecycle(ctx, source, target, root, targetRoot); err == nil || !strings.Contains(err.Error(), "differs from manifest") {
		t.Fatalf("manifest drift accepted: %v", err)
	}
	for _, path := range []string{target, targetRoot} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("invalid conversion left output %s: %v", path, err)
		}
	}
}

func TestScenarioLifecycleCurrentContractCopyPreservesLineageAndPartialState(t *testing.T) {
	ctx := context.Background()
	old, oldRoot := lifecycleConversionFixture(t)
	root := filepath.Dir(old)
	source := filepath.Join(root, "current.db")
	sourceRoot := filepath.Join(root, "current-playbooks")
	if _, err := ConvertScenarioLifecycle(ctx, old, source, oldRoot, sourceRoot); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := db.GetScenarioRevision(ctx, "revision")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fork := domain.Scenario{ID: "fork", Slug: "fork", Name: "Fork", OwnerID: "owner", CreatedAt: now, UpdatedAt: now, ForkedFromScenarioID: "scenario", ForkedFromRevisionID: revision.ID, ForkedFromDigest: domain.ScenarioRevisionSpecDigest(revision)}
	first := domain.ScenarioRevision{ID: "fork-revision", ScenarioID: fork.ID, Revision: 1, Status: domain.RevisionDraft, DigestVersion: domain.ScenarioDigestVersion, CreatedAt: now, Graph: revision.Graph}
	if err = db.CreateScenario(ctx, fork, first); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB().Exec(`UPDATE scenario_installations SET state='partial',revision_id='revision',run_id='formal',mutating_run_id='formal',test_only=0 WHERE environment_id='environment'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB().Exec(`INSERT INTO scenario_execution_submissions(user_id,key,request_digest,run_id) VALUES('owner','saved-key','request-digest','formal')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	target := filepath.Join(root, "copied.db")
	targetRoot := filepath.Join(root, "copied-playbooks")
	if _, err = ConvertScenarioLifecycle(ctx, source, target, sourceRoot, targetRoot); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.GetScenario(ctx, fork.ID, false)
	if err != nil || got.ForkedFromDigest != fork.ForkedFromDigest {
		t.Fatalf("fork provenance=%+v %v", got, err)
	}
	baseline, err := db.GetScenarioInstallation(ctx, "environment", "scenario")
	if err != nil || baseline.State != "partial" || baseline.TestOnly || baseline.MutatingRunID != "formal" || baseline.RunID != "formal" {
		t.Fatalf("partial state changed: %+v %v", baseline, err)
	}
	run, err := db.GetScenarioSubmission(ctx, "owner", "saved-key", "request-digest")
	if err != nil || run.ID != "formal" {
		t.Fatalf("idempotency lost: %+v %v", run, err)
	}
}

func TestUserExperienceMigrationRetainsMissingDeclarationsAndHistoricalBytes(t *testing.T) {
	ctx := context.Background()
	source, root := lifecycleConversionFixture(t)
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO components(id,slug,name,owner_id,layer,created_at,updated_at) VALUES('legacy','legacy','Legacy','owner','host_foundation','2026-09-05','2026-09-05')`,
		`INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES('legacy-line','legacy','Legacy','2026-09-05')`,
		`INSERT INTO component_releases(id,component_id,line_id,version,status,compatibility,risk_level,created_at) VALUES('legacy-release','legacy','legacy-line','1.0.0','draft','not_applicable','low','2026-09-05')`,
		`INSERT INTO action_definitions(id,release_id,name,kind,playbook,host_group,timeout_seconds) VALUES('legacy-action','legacy-release','Install','install','tasks/install.yml','all',60)`,
		`ALTER TABLE action_definitions DROP COLUMN resource_contract_json`,
	} {
		if _, err = db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	var beforeSnapshot, beforeDigest string
	if err = db.QueryRow(`SELECT input_snapshot_json,artifact_digest FROM runs WHERE id='formal'`).Scan(&beforeSnapshot, &beforeDigest); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(source), "ux-migrated.db")
	targetRoot := filepath.Join(filepath.Dir(root), "ux-playbooks")
	if _, err = ConvertScenarioLifecycle(ctx, source, target, root, targetRoot); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(before) != string(after) {
		t.Fatal("migration changed source")
	}
	migrated, err := store.Open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	release, err := migrated.GetComponentRelease(ctx, "legacy-release")
	if err != nil || len(release.Actions) != 1 || release.Actions[0].ResourceContract != nil {
		t.Fatalf("legacy declaration was synthesized: %+v %v", release, err)
	}
	if err := domain.ValidateResourceContract(release.Actions[0].ResourceContract, nil, true); err == nil {
		t.Fatal("legacy action became ready for new execution")
	}
	var snapshot, digest string
	if err = migrated.DB().QueryRow(`SELECT input_snapshot_json,artifact_digest FROM runs WHERE id='formal'`).Scan(&snapshot, &digest); err != nil || snapshot != beforeSnapshot || digest != beforeDigest {
		t.Fatal("historical evidence changed", err)
	}
	var sessions int
	if err = migrated.DB().QueryRow(`SELECT COUNT(*) FROM workflow_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("durable workflows not initialized", err)
	}
}
