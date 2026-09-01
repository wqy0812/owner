package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func TestSnapshotAndBothRestorePaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	artifactContents := strings.Repeat("x", 42)
	artifactDigest := sha256.Sum256([]byte(artifactContents))
	var externalArtifactValid atomic.Bool
	externalArtifactValid.Store(true)
	artifactServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		contents := artifactContents
		if !externalArtifactValid.Load() {
			contents = strings.Repeat("y", len(artifactContents))
		}
		_, _ = response.Write([]byte(contents))
	}))
	t.Cleanup(artifactServer.Close)
	dockerDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dockerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dockerDir, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	databasePath := filepath.Join(root, "source.db")
	playbookRoot := filepath.Join(root, "jobs")
	playbookPath := filepath.Join(playbookRoot, "managed", "runtime", "release-runtime-1", "install.yml")
	if err := os.MkdirAll(filepath.Dir(playbookPath), 0o700); err != nil {
		t.Fatal(err)
	}
	playbook := []byte("---\n- name: Install runtime\n  hosts: all\n  tasks: []\n")
	if err := os.WriteFile(playbookPath, playbook, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(playbook)
	playbookSHA := hex.EncodeToString(digest[:])
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)`, []any{"component-owner", "Component Owner", "component_owner", now}},
		{`INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)`, []any{"scenario-owner", "Scenario Owner", "scenario_owner", now}},
		{`INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)`, []any{"environment-owner", "Environment Owner", "environment_owner", now}},
		{`INSERT INTO components(id,slug,name,description,owner_id,created_at,updated_at,layer,tags_json) VALUES(?,?,?,?,?,?,?,?,?)`, []any{"component-runtime", "runtime", "Runtime", "runtime", "component-owner", now, now, "runtime_state", `["runtime"]`}},
		{`INSERT INTO components(id,slug,name,description,owner_id,created_at,updated_at,layer,tags_json) VALUES(?,?,?,?,?,?,?,?,?)`, []any{"component-host", "host", "Host", "host", "component-owner", now, now, "host_foundation", `["host"]`}},
		{`INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES(?,?,?,?)`, []any{"line-host-1", "component-host", "Host 1.0", now}},
		{`INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES(?,?,?,?)`, []any{"line-runtime-1", "component-runtime", "Runtime 1.0", now}},
		{`INSERT INTO component_releases(id,component_id,line_id,version,status,release_notes,compatibility,candidate,publication_generation,risk_level,environment_constraints_json,parameters_json,created_at,released_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{"release-host-1", "component-host", "line-host-1", "1.0.0", "released", "stable", "not_applicable", 0, 2, "low", `{}`, `[]`, now, now}},
		{`INSERT INTO component_releases(id,component_id,line_id,version,status,release_notes,compatibility,candidate,publication_generation,risk_level,environment_constraints_json,parameters_json,created_at,released_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{"release-runtime-1", "component-runtime", "line-runtime-1", "1.0.0", "released", "stable", "not_applicable", 0, 3, "low", `{}`, `[]`, now, now}},
		{`INSERT INTO component_dependencies(id,release_id,upstream_component_id,upstream_release_id,purpose,parameter_mappings_json) VALUES(?,?,?,?,?,?)`, []any{"dependency-runtime-host", "release-runtime-1", "component-host", "release-host-1", "prepared host", `[]`}},
		{`INSERT INTO action_definitions(id,release_id,name,kind,playbook,playbook_sha256,tags_json,limit_pattern,host_group,allowed_parameters_json,required_credentials_json,timeout_seconds,risk_level,destructive,idempotent) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, []any{"action-install", "release-runtime-1", "install", "install", "managed/runtime/release-runtime-1/install.yml", playbookSHA, `[]`, "", "runtime", `[]`, `[]`, 1800, "low", 0, 1}},
		{`INSERT INTO component_release_artifacts(id,release_id,alias,filename,sha256,size_bytes,source_url,source_updated_by,source_updated_at,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, []any{"artifact-runtime", "release-runtime-1", "runtime_media", "runtime.tgz", hex.EncodeToString(artifactDigest[:]), int64(len(artifactContents)), artifactServer.URL + "/runtime.tgz", "component-owner", now, "component-owner", now}},
		{`INSERT INTO component_release_images(id,release_id,logical_name,digest,source_ref,source_updated_by,source_updated_at,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, []any{"image-runtime", "release-runtime-1", "main", "sha256:" + strings.Repeat("b", 64), "registry.example.invalid/runtime:1.0.0", "component-owner", now, "component-owner", now}},
		{`INSERT INTO scenarios(id,slug,name,description,owner_id,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, []any{"scenario-runtime", "runtime-scenario", "Runtime Scenario", "scenario", "scenario-owner", "scenario-runtime-r1", now, now}},
		{`INSERT INTO scenario_revisions(id,scenario_id,revision,status,publication_generation,graph_json,created_at,test_passed_at,released_at) VALUES(?,?,?,?,?,?,?,?,?)`, []any{"scenario-runtime-r1", "scenario-runtime", 1, "released", 2, `{"nodes":[{"id":"runtime","name":"Runtime","releaseId":"release-runtime-1","action":"install","hostGroup":"runtime","values":{},"runInputs":[],"position":{"x":0,"y":0}}],"edges":[]}`, now, now, now}},
		{`INSERT INTO environments(id,name,description,owner_id,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, []any{"environment-secret", "Secret Environment", "not a Catalog asset", "environment-owner", "environment-secret-r1", now, now}},
		{`INSERT INTO environment_revisions(id,environment_id,revision,facts_json,inventory_json,variables_json,credential_refs_json,created_by,change_reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, []any{"environment-secret-r1", "environment-secret", 1, `{}`, `{"hosts":[]}`, `{"SECRET":"do-not-export"}`, `[{"name":"token","reference":"REAL_SECRET_REF"}]`, "environment-owner", "secret fixture", now}},
		{`UPDATE publication_state SET generation=9 WHERE id=1`, nil},
	}
	for _, statement := range statements {
		if _, err := database.DB().ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("fixture statement %q: %v", statement.query, err)
		}
	}

	remote := filepath.Join(root, "catalog.git")
	repo := filepath.Join(root, "catalog")
	if _, err := runCommand(ctx, root, "git", "init", "--bare", remote); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, root, "git", "clone", remote, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, repo, "git", "checkout", "--orphan", "catalog"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitkeep"), []byte("catalog\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, repo, "git", "add", ".gitkeep"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, repo, "git", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initialize catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, repo, "git", "push", "-u", "origin", "catalog"); err != nil {
		t.Fatal(err)
	}

	manager, err := NewManager(Config{
		DatabasePath: databasePath, PlaybookRoot: playbookRoot,
		BackupDir: filepath.Join(root, "backups"), CatalogRepo: repo,
		CatalogRemote: "origin", CatalogBranch: "catalog",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := manager.Snapshot(ctx, "test-publication")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != StatusSuccess || manifest.PublicationGeneration != 9 || manifest.Counts["component_releases"] != 2 || manifest.Counts["scenario_revisions"] != 1 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if _, err := manager.Verify(ctx, manifest.BackupID, true); err != nil {
		t.Fatal(err)
	}
	if err := manager.VerifyExternal(ctx, manifest.BackupID); err != nil {
		t.Fatalf("external Catalog verification failed: %v", err)
	}
	externalArtifactValid.Store(false)
	if err := manager.VerifyExternal(ctx, manifest.BackupID); err == nil || !strings.Contains(err.Error(), "does not match its Catalog identity") {
		t.Fatalf("tampered external artifact verification error=%v", err)
	}
	catalogContents, err := manager.gitShow(ctx, manifest.GitCommit, "catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"do-not-export", "REAL_SECRET_REF", "environment-owner", "environment-secret"} {
		if strings.Contains(string(catalogContents), forbidden) {
			t.Fatalf("Catalog leaked excluded environment data %q", forbidden)
		}
	}
	remoteTag, err := runCommand(ctx, repo, "git", "ls-remote", "--tags", "origin", "refs/tags/"+manifest.GitTag)
	if err != nil || !strings.Contains(remoteTag, "refs/tags/"+manifest.GitTag) {
		t.Fatalf("remote backup tag missing: %q err=%v", remoteTag, err)
	}

	fullDatabase := filepath.Join(root, "full-restore.db")
	fullPlaybooks := filepath.Join(root, "full-jobs")
	if _, err := manager.RestoreDatabase(ctx, manifest.BackupID, fullDatabase, fullPlaybooks); err != nil {
		t.Fatal(err)
	}
	assertRestoredCatalog(t, fullDatabase, filepath.Join(fullPlaybooks, "managed", "runtime", "release-runtime-1", "install.yml"), playbookSHA)

	catalogDatabase := filepath.Join(root, "catalog-restore.db")
	catalogPlaybooks := filepath.Join(root, "catalog-jobs")
	if _, err := manager.RestoreCatalog(ctx, manifest.GitTag, catalogDatabase, catalogPlaybooks); err != nil {
		t.Fatal(err)
	}
	assertRestoredCatalog(t, catalogDatabase, filepath.Join(catalogPlaybooks, "managed", "runtime", "release-runtime-1", "install.yml"), playbookSHA)
	restored, err := openReadOnly(catalogDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var runs int
	if err := restored.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("Catalog-only restore invented Runs: count=%d err=%v", runs, err)
	}

	liveDatabasePath := filepath.Join(root, "live-empty.db")
	liveDatabase, err := store.Open(ctx, liveDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, identity := range []struct{ id, name, role string }{{"component-owner", "Component Owner", "component_owner"}, {"scenario-owner", "Scenario Owner", "scenario_owner"}} {
		if _, err := liveDatabase.DB().ExecContext(ctx, `INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)`, identity.id, identity.name, identity.role, now); err != nil {
			t.Fatal(err)
		}
	}
	livePlan, err := manager.CurrentDatabaseRestorePlan(ctx, manifest.GitTag, liveDatabase.DB())
	if err != nil || livePlan.PlanDigest == "" {
		t.Fatalf("live restore plan=%+v err=%v", livePlan, err)
	}
	if _, err := manager.RestoreCurrentDatabase(ctx, manifest.GitTag, livePlan.PlanDigest, liveDatabase.DB(), filepath.Join(root, "live-jobs")); err != nil {
		t.Fatal(err)
	}
	if err := liveDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	assertRestoredCatalog(t, liveDatabasePath, filepath.Join(root, "live-jobs", "managed", "runtime", "release-runtime-1", "install.yml"), playbookSHA)

	nonEmptyDatabase, err := store.Open(ctx, liveDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.CurrentDatabaseRestorePlan(ctx, manifest.GitTag, nonEmptyDatabase.DB())
	var coded *domain.CodedError
	if !errors.As(err, &coded) || coded.Code != "target_catalog_not_empty" {
		t.Fatalf("non-empty restore error=%v", err)
	}
	nonEmptyDatabase.Close()

	conflictDatabasePath := filepath.Join(root, "playbook-conflict.db")
	conflictDatabase, err := store.Open(ctx, conflictDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	conflictPlan, err := manager.CurrentDatabaseRestorePlan(ctx, manifest.GitTag, conflictDatabase.DB())
	if err != nil {
		t.Fatal(err)
	}
	conflictPlaybook := filepath.Join(root, "conflict-jobs", "managed", "runtime", "release-runtime-1", "install.yml")
	if err := os.MkdirAll(filepath.Dir(conflictPlaybook), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflictPlaybook, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = manager.RestoreCurrentDatabase(ctx, manifest.GitTag, conflictPlan.PlanDigest, conflictDatabase.DB(), filepath.Join(root, "conflict-jobs"))
	if !errors.As(err, &coded) || coded.Code != "playbook_conflict" {
		t.Fatalf("Playbook conflict error=%v", err)
	}
	var components int
	if err := conflictDatabase.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM components`).Scan(&components); err != nil || components != 0 {
		t.Fatalf("failed restore left components=%d err=%v", components, err)
	}
	conflictDatabase.Close()

	userConflictDatabase, err := store.Open(ctx, filepath.Join(root, "user-conflict.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := userConflictDatabase.DB().ExecContext(ctx, `INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)`, "component-owner", "Different Owner", "component_owner", now); err != nil {
		t.Fatal(err)
	}
	userConflictPlan, err := manager.CurrentDatabaseRestorePlan(ctx, manifest.GitTag, userConflictDatabase.DB())
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.RestoreCurrentDatabase(ctx, manifest.GitTag, userConflictPlan.PlanDigest, userConflictDatabase.DB(), filepath.Join(root, "user-conflict-jobs"))
	if !errors.As(err, &coded) || coded.Code != "catalog_user_conflict" {
		t.Fatalf("user conflict error=%v", err)
	}
	if err := userConflictDatabase.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM components`).Scan(&components); err != nil || components != 0 {
		t.Fatalf("user conflict left components=%d err=%v", components, err)
	}
	userConflictDatabase.Close()

	staleDatabase, err := store.Open(ctx, filepath.Join(root, "stale-plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	stalePlan, err := manager.CurrentDatabaseRestorePlan(ctx, manifest.GitTag, staleDatabase.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staleDatabase.DB().ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_, err = manager.RestoreCurrentDatabase(ctx, manifest.GitTag, stalePlan.PlanDigest, staleDatabase.DB(), filepath.Join(root, "stale-jobs"))
	if !errors.As(err, &coded) || coded.Code != "restore_plan_changed" {
		t.Fatalf("stale plan error=%v", err)
	}
	staleDatabase.Close()

	catalog, _, err := manager.catalogAt(ctx, manifest.GitTag)
	if err != nil {
		t.Fatal(err)
	}
	objectConflictDatabase, err := store.Open(ctx, filepath.Join(root, "object-conflict.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := objectConflictDatabase.DB().ExecContext(ctx, `INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)`, "component-owner", "Component Owner", "component_owner", now); err != nil {
		t.Fatal(err)
	}
	if _, err := objectConflictDatabase.DB().ExecContext(ctx, `INSERT INTO components(id,slug,name,description,owner_id,created_at,updated_at,layer,tags_json) VALUES(?,?,?,?,?,?,?,?,?)`, "different-id", "runtime", "Conflict", "", "component-owner", now, now, "runtime_state", `[]`); err != nil {
		t.Fatal(err)
	}
	tx, err := objectConflictDatabase.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = checkCatalogObjectConflicts(ctx, tx, catalog)
	_ = tx.Rollback()
	if !errors.As(err, &coded) || coded.Code != "catalog_conflict" {
		t.Fatalf("object conflict error=%v", err)
	}
	objectConflictDatabase.Close()
}

func TestRejectsUnsafeConfigurationAndRecoveryIdentifiers(t *testing.T) {
	if _, err := NewManager(Config{DatabasePath: "db", PlaybookRoot: "jobs", BackupDir: "backups", CatalogRepo: "repo", CatalogRemote: "--upload-pack", CatalogBranch: "catalog"}); err == nil {
		t.Fatal("unsafe Git remote was accepted")
	}
	if err := validateBackupID("../backup"); err == nil {
		t.Fatal("path-like backup ID was accepted")
	}
	if safeGitName("--exec") {
		t.Fatal("option-like Git ref was accepted")
	}
}

func TestPrivateRepositoryControllerConfinesPathsAndCreatesBareRepository(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	base := Config{DatabasePath: filepath.Join(root, "platform.db"), PlaybookRoot: filepath.Join(root, "jobs"), BackupDir: filepath.Join(root, "backups"), CatalogRepo: filepath.Join(root, "unused-clone"), CatalogRemote: "origin", CatalogBranch: "catalog"}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewRepositoryController(base, allowed, NewScheduler(manager, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	status, err := controller.Create(ctx, "private/catalog.git")
	if err != nil {
		t.Fatal(err)
	}
	expectedPath, err := filepath.EvalSymlinks(filepath.Dir(filepath.Join(allowed, "private", "catalog.git")))
	if err != nil {
		t.Fatal(err)
	}
	expectedPath = filepath.Join(expectedPath, "catalog.git")
	if !status.Configured || status.Path != expectedPath {
		t.Fatalf("status=%+v", status)
	}
	selected, found, err := SelectedRepositoryConfig(base)
	if err != nil || !found || selected.CatalogRepo == base.CatalogRepo {
		t.Fatalf("selected config=%+v found=%t err=%v", selected, found, err)
	}
	if _, err := os.Stat(filepath.Join(selected.CatalogRepo, ".git")); err != nil {
		t.Fatalf("selected clone is unavailable: %v", err)
	}
	info, err := os.Stat(status.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("repository permissions=%o", info.Mode().Perm())
	}
	if _, err := runCommand(ctx, status.Path, "git", "-C", status.Path, "rev-parse", "--verify", "refs/heads/catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base.BackupDir, "repository.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Create(ctx, "../escape.git"); err == nil {
		t.Fatal("path traversal was accepted")
	}

	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(allowed, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Create(ctx, "linked/escape.git"); err == nil {
		t.Fatal("symlink escape was accepted")
	}

	const recoveryTag = "backup/repository-test"
	if _, err := runCommand(ctx, selected.CatalogRepo, "git", "-C", selected.CatalogRepo,
		"-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"tag", "-a", recoveryTag, "-m", "test recovery point", "refs/remotes/origin/catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommand(ctx, selected.CatalogRepo, "git", "-C", selected.CatalogRepo, "push", "origin", "refs/tags/"+recoveryTag); err != nil {
		t.Fatal(err)
	}
	withRecoveryPoint, err := controller.Status(ctx)
	if err != nil || len(withRecoveryPoint.RecoveryPoints) != 1 || withRecoveryPoint.RecoveryPoints[0].Ref != recoveryTag {
		t.Fatalf("remote recovery points=%+v err=%v", withRecoveryPoint.RecoveryPoints, err)
	}
	if _, err := runCommand(ctx, selected.CatalogRepo, "git", "-C", selected.CatalogRepo, "push", "origin", ":refs/tags/"+recoveryTag); err != nil {
		t.Fatal(err)
	}
	withoutDeletedRemoteTag, err := controller.Status(ctx)
	if err != nil || len(withoutDeletedRemoteTag.RecoveryPoints) != 0 {
		t.Fatalf("deleted remote tag remained available: points=%+v err=%v", withoutDeletedRemoteTag.RecoveryPoints, err)
	}

	missingBranch := filepath.Join(allowed, "missing.git")
	if _, err := runCommand(ctx, allowed, "git", "init", "--bare", missingBranch); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Connect(ctx, missingBranch); err == nil || !strings.Contains(err.Error(), "no catalog branch") {
		t.Fatalf("missing branch error=%v", err)
	}

	unavailablePath := status.Path + ".unavailable"
	if err := os.Rename(status.Path, unavailablePath); err != nil {
		t.Fatal(err)
	}
	degraded, err := controller.Status(ctx)
	if err != nil {
		t.Fatalf("repository failure must remain a readable status: %v", err)
	}
	expectedAllowedRoot, err := filepath.EvalSymlinks(allowed)
	if err != nil {
		t.Fatal(err)
	}
	if !degraded.Configured || degraded.AllowedRoot != expectedAllowedRoot || !strings.Contains(degraded.LastError, "同步所选私有仓库失败") {
		t.Fatalf("degraded repository status=%+v", degraded)
	}
}

func TestPrivateRepositoryControllerCreatesImmediateRecoveryPoint(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	base := Config{
		DatabasePath: filepath.Join(root, "platform.db"), PlaybookRoot: filepath.Join(root, "jobs"),
		BackupDir: filepath.Join(root, "backups"), CatalogRepo: filepath.Join(root, "unused-clone"),
		CatalogRemote: "origin", CatalogBranch: "catalog",
	}
	database, err := store.Open(ctx, base.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := os.MkdirAll(base.PlaybookRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(manager, time.Second)
	controller, err := NewRepositoryController(base, filepath.Join(root, "allowed"), scheduler, database.DB())
	if err != nil {
		t.Fatal(err)
	}
	status, err := controller.Create(ctx, "catalog.git")
	if err != nil {
		t.Fatal(err)
	}
	if !status.RestoreTargetKnown || !status.TargetCatalogEmpty || status.TargetComponentCount != 0 || status.TargetScenarioCount != 0 {
		t.Fatalf("empty restore target status=%+v", status)
	}
	manifest, err := controller.Snapshot(ctx, "manual-ui")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != StatusSuccess || manifest.Reason != "manual-ui" || manifest.GitCommit == "" || !strings.HasPrefix(manifest.GitTag, "backup/") {
		t.Fatalf("immediate backup manifest=%+v", manifest)
	}
	if status := scheduler.Status(); status.LastSuccessAt == nil || status.LastError != "" {
		t.Fatalf("scheduler status after immediate backup=%+v", status)
	}
	if _, err := controller.Snapshot(ctx, ""); err != nil {
		t.Fatalf("second immediate backup should create a new recovery point: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := database.DB().ExecContext(ctx, `INSERT INTO users(id,name,role,created_at) VALUES(?,?,?,?)`, "component-owner", "Component Owner", "component_owner", now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `INSERT INTO components(id,slug,name,description,owner_id,created_at,updated_at,layer,tags_json) VALUES(?,?,?,?,?,?,?,?,?)`, "component-test", "test", "Test", "", "component-owner", now, now, "runtime_state", `[]`); err != nil {
		t.Fatal(err)
	}
	status, err = controller.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.RestoreTargetKnown || status.TargetCatalogEmpty || status.TargetComponentCount != 1 || status.TargetScenarioCount != 0 {
		t.Fatalf("non-empty restore target status=%+v", status)
	}
}

func TestPrivateRepositoryControllerUsesCompatibleOrphanCheckoutAndClassifiesExistingTarget(t *testing.T) {
	ctx := context.Background()
	controller, _, _ := newPrivateRepositoryController(t)
	originalRunner := controller.runCommand
	var usedCompatibleCheckout bool
	controller.runCommand = func(ctx context.Context, directory, command string, args ...string) (string, error) {
		if command == "git" && len(args) > 0 && args[0] == "switch" {
			t.Fatalf("repository initialization used incompatible git switch: %v", args)
		}
		if command == "git" && len(args) >= 2 && args[0] == "checkout" && args[1] == "--orphan" {
			usedCompatibleCheckout = true
		}
		return originalRunner(ctx, directory, command, args...)
	}

	status, err := controller.Create(ctx, "catalog.git")
	if err != nil {
		t.Fatal(err)
	}
	if !usedCompatibleCheckout {
		t.Fatal("repository initialization did not use git checkout --orphan")
	}
	if _, err := runCommand(ctx, status.Path, "git", "-C", status.Path, "rev-parse", "--verify", "refs/heads/catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Create(ctx, "catalog.git"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("existing repository error=%v", err)
	}
}

func TestPrivateRepositoryControllerRejectsRootLikeRelativePath(t *testing.T) {
	controller, _, allowed := newPrivateRepositoryController(t)

	shortRelative, err := controller.resolveCreatePath("nested/catalog.git")
	if err != nil || shortRelative != filepath.Join(allowed, "nested", "catalog.git") {
		t.Fatalf("short relative path=%q err=%v", shortRelative, err)
	}
	absolute := filepath.Join(allowed, "absolute.git")
	resolvedAbsolute, err := controller.resolveCreatePath(absolute)
	if err != nil || resolvedAbsolute != absolute {
		t.Fatalf("absolute path=%q err=%v", resolvedAbsolute, err)
	}
	rootLikeRelative := strings.TrimPrefix(allowed, string(filepath.Separator)) + string(filepath.Separator) + "catalog.git"
	if _, err := controller.resolveCreatePath(rootLikeRelative); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "must start with") {
		t.Fatalf("root-like relative path error=%v", err)
	}
}

func TestPrivateRepositoryControllerCleansOnlyParentsCreatedByFailedAttempt(t *testing.T) {
	ctx := context.Background()
	controller, _, allowed := newPrivateRepositoryController(t)
	preexisting := filepath.Join(allowed, "preexisting")
	if err := os.MkdirAll(preexisting, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(preexisting, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalRunner := controller.runCommand
	controller.runCommand = func(ctx context.Context, directory, command string, args ...string) (string, error) {
		if command == "git" && len(args) >= 2 && args[0] == "checkout" && args[1] == "--orphan" {
			return "", errors.New("simulated orphan checkout failure")
		}
		return originalRunner(ctx, directory, command, args...)
	}

	target := filepath.Join(preexisting, "created", "nested", "catalog.git")
	if _, err := controller.Create(ctx, target); err == nil || !strings.Contains(err.Error(), "simulated orphan checkout failure") {
		t.Fatalf("create error=%v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed repository target remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(preexisting, "created")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created parent directories remain: %v", err)
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "keep" {
		t.Fatalf("pre-existing content changed: contents=%q err=%v", contents, err)
	}
}

func newPrivateRepositoryController(t *testing.T) (*RepositoryController, Config, string) {
	t.Helper()
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	base := Config{
		DatabasePath:  filepath.Join(root, "platform.db"),
		PlaybookRoot:  filepath.Join(root, "jobs"),
		BackupDir:     filepath.Join(root, "backups"),
		CatalogRepo:   filepath.Join(root, "unused-clone"),
		CatalogRemote: "origin",
		CatalogBranch: "catalog",
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewRepositoryController(base, allowed, NewScheduler(manager, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return controller, base, controller.allowedRoot
}

func TestLatestSuccessfulIsScopedToCatalogRepository(t *testing.T) {
	root := t.TempDir()
	base := Config{DatabasePath: filepath.Join(root, "platform.db"), PlaybookRoot: filepath.Join(root, "jobs"), BackupDir: filepath.Join(root, "backups"), CatalogRemote: "origin", CatalogBranch: "catalog"}
	firstConfig, secondConfig := base, base
	firstConfig.CatalogRepo, secondConfig.CatalogRepo = filepath.Join(root, "first"), filepath.Join(root, "second")
	first, err := NewManager(firstConfig)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewManager(secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	completed := time.Now().UTC()
	manifest := Manifest{FormatVersion: CatalogFormatVersion, BackupID: "first", Status: StatusSuccess, CreatedAt: completed, CompletedAt: &completed, DatabaseFile: "platform.db", PublicationGeneration: 9}
	if err := first.writeLatestSuccessful(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := second.LatestSuccessful(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second repository reused first repository recovery point: %v", err)
	}
}

func TestNeedsSnapshotWhenPublicationGenerationMovesBackward(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "platform.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	manager, err := NewManager(Config{
		DatabasePath: databasePath, PlaybookRoot: filepath.Join(root, "jobs"),
		BackupDir: filepath.Join(root, "backups"), CatalogRepo: filepath.Join(root, "repo"),
		CatalogRemote: "origin", CatalogBranch: "catalog",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := time.Now().UTC()
	if err := manager.writeLatestSuccessful(Manifest{
		FormatVersion: CatalogFormatVersion, BackupID: "older-database", Status: StatusSuccess,
		CreatedAt: completed, CompletedAt: &completed, DatabaseFile: "platform.db", PublicationGeneration: 9,
	}); err != nil {
		t.Fatal(err)
	}
	needed, generation, err := manager.NeedsSnapshot(ctx)
	if err != nil || !needed || generation != 1 {
		t.Fatalf("needed=%t generation=%d err=%v", needed, generation, err)
	}
}

func TestNeedsSnapshotWhenSchemaContractChangesWithoutGenerationChange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "platform.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	manager, err := NewManager(Config{
		DatabasePath: databasePath, PlaybookRoot: filepath.Join(root, "jobs"),
		BackupDir: filepath.Join(root, "backups"), CatalogRepo: filepath.Join(root, "repo"),
		CatalogRemote: "origin", CatalogBranch: "catalog",
	})
	if err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := database.DB().QueryRowContext(ctx, `SELECT generation FROM publication_state WHERE id=1`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	completed := time.Now().UTC()
	if err := manager.writeLatestSuccessful(Manifest{
		FormatVersion: CatalogFormatVersion, BackupID: "pre-migration", Status: StatusSuccess,
		CreatedAt: completed, CompletedAt: &completed, DatabaseFile: "platform.db",
		PublicationGeneration: generation, SchemaContract: "clusterforge-v1-20260828-environment-lifecycle",
	}); err != nil {
		t.Fatal(err)
	}
	needed, currentGeneration, err := manager.NeedsSnapshot(ctx)
	if err != nil || !needed || currentGeneration != generation {
		t.Fatalf("needed=%t generation=%d want=%d err=%v", needed, currentGeneration, generation, err)
	}
}

func TestSchedulerNotifiesWhenBackupHealthChanges(t *testing.T) {
	manager, err := NewManager(Config{DatabasePath: "platform.db", PlaybookRoot: "jobs", BackupDir: "backups", CatalogRepo: "repo", CatalogRemote: "origin", CatalogBranch: "catalog"})
	if err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(manager, time.Second)
	var notifications atomic.Int32
	scheduler.SetStatusChangeHandler(func() { notifications.Add(1) })
	scheduler.recordFailure(errors.New("git push failed"))
	if status := scheduler.Status(); status.LastError != "git push failed" || notifications.Load() != 1 {
		t.Fatalf("failure status=%+v notifications=%d", status, notifications.Load())
	}
	scheduler.recordSuccess()
	if status := scheduler.Status(); status.LastError != "" || status.LastSuccessAt == nil || notifications.Load() != 2 {
		t.Fatalf("success status=%+v notifications=%d", status, notifications.Load())
	}
}

func assertRestoredCatalog(t *testing.T, databasePath, playbookPath, expectedPlaybookSHA string) {
	t.Helper()
	ctx := context.Background()
	database, err := openReadOnly(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for table, expected := range map[string]int{"components": 2, "component_releases": 2, "component_dependencies": 1, "component_release_artifacts": 1, "component_release_images": 1, "scenarios": 1, "scenario_revisions": 1} {
		var count int
		if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != expected {
			t.Fatalf("restored %s count=%d want=%d err=%v", table, count, expected, err)
		}
	}
	digest, err := sha256File(playbookPath)
	if err != nil || digest != expectedPlaybookSHA {
		t.Fatalf("restored Playbook digest=%s want=%s err=%v", digest, expectedPlaybookSHA, err)
	}
	if err := verifySQLite(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
}
