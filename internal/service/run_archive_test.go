package service

import (
	"archive/tar"
	"codex/platform-demo/internal/backup"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/runarchive"
	"codex/platform-demo/internal/store"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunArchiveRoundTripRedactionAndRecovery(t *testing.T) {
	p, db := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := domain.User{ID: "archive-env-owner", Name: "Env", Role: domain.RoleEnvironmentOwner, CreatedAt: now}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	env, err := p.CreateEnvironment(ctx, owner, domain.Environment{Name: "Archive test"}, completeServiceTestFacts())
	if err != nil {
		t.Fatal(err)
	}
	r := domain.Run{ID: "archive-run", Kind: domain.RunScenario, Status: domain.RunSucceeded, RequestedBy: owner.ID, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, CreatedAt: now, FinishedAt: &now, InputSnapshot: map[string]any{"password": "DO-NOT-EXPORT", "steps": []any{}}}
	if err = db.CreateRun(ctx, r, nil); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"first\nline", "last"} {
		if _, err = db.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "stdout", Message: message, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	if err = p.ConfigureRunArchives(root); err != nil {
		t.Fatal(err)
	}
	admin := domain.User{ID: "admin", Role: domain.RolePlatformAdmin}
	if _, err = p.Execution().ArchiveRuns(ctx, owner, []string{r.ID}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = p.Execution().ArchiveRuns(ctx, admin, []string{r.ID}); err != nil {
		t.Fatal(err)
	}
	task, err := db.ClaimRunArchive(ctx, "test-lease", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.archiveOne(ctx, task); err != nil {
		t.Fatal(err)
	}
	a, err := db.GetRunArchive(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, a.RelativePath)
	if err = runarchive.Verify(path, r.ID); err != nil {
		t.Fatal(err)
	}
	f, _, err := p.Execution().ArchiveDownload(ctx, owner, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	packed, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Close()
	gz, err := gzip.NewReader(packed)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		content, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(content), "DO-NOT-EXPORT") {
			t.Fatal("secret exported in", header.Name)
		}
	}
	if logs, _ := db.ListRunLogs(ctx, r.ID, 0, 100); len(logs) != 0 {
		t.Fatal(logs)
	}
	// Produce a filesystem snapshot of the in-memory fixture, then exercise the
	// Git-independent backup/restore path, including the actual archive bytes.
	database := filepath.Join(t.TempDir(), "source.db")
	if _, err = db.DB().ExecContext(ctx, `VACUUM INTO ?`, database); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "saved")
	if err = backup.SnapshotRunHistory(ctx, database, root, saved); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err = backup.RestoreRunHistory(ctx, saved, restored); err != nil {
		t.Fatal(err)
	}
	if err = backup.VerifyRunHistory(ctx, restored); err != nil {
		t.Fatal(err)
	}
	restoredDB, err := store.Open(ctx, filepath.Join(restored, "platform.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restoredDB.Close()
	restoredPlatform := NewPlatform(restoredDB, nil, nil)
	defer restoredPlatform.Close()
	if err = restoredPlatform.ConfigureRunArchives(filepath.Join(restored, "archives")); err != nil {
		t.Fatal(err)
	}
	download, _, err := restoredPlatform.Execution().ArchiveDownload(ctx, owner, r.ID)
	if err != nil {
		t.Fatal("restored download failed", err)
	}
	download.Close()
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.Execution().ArchiveDownload(ctx, owner, r.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("corruption not rejected", err)
	}
}

func TestArchiveStorageAndCancellationPreserveOnlineData(t *testing.T) {
	for _, check := range []struct {
		available uint64
		needed    int64
		ok        bool
	}{{0, 0, false}, {16 << 20, 0, true}, {16 << 20, 1, false}, {(16 << 20) + 200, 100, true}, {(16 << 20) + 199, 100, false}, {^uint64(0), -1, false}} {
		if archiveSpaceSufficient(check.available, check.needed) != check.ok {
			t.Fatal(check)
		}
	}
	if err := checkArchiveStorage(filepath.Join(t.TempDir(), "missing"), 0); err == nil {
		t.Fatal("missing storage accepted")
	}
	p, db := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := domain.User{ID: "storage-owner", Name: "Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: now}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	env, err := p.CreateEnvironment(ctx, owner, domain.Environment{Name: "Storage test"}, completeServiceTestFacts())
	if err != nil {
		t.Fatal(err)
	}
	r := domain.Run{ID: "cancel-archive", Kind: domain.RunScenario, Status: domain.RunSucceeded, RequestedBy: owner.ID, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, CreatedAt: now, FinishedAt: &now, InputSnapshot: map[string]any{}}
	if err = db.CreateRun(ctx, r, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AppendRunLog(ctx, domain.RunLog{RunID: r.ID, Stream: "stdout", Message: "keep this", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = p.ConfigureRunArchives(root); err != nil {
		t.Fatal(err)
	}
	if _, err = db.EnqueueRunArchives(ctx, []string{r.ID}, "manual", owner.ID, now); err != nil {
		t.Fatal(err)
	}
	task, err := db.ClaimRunArchive(ctx, "cancel-token", now)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = p.archiveOne(cancelled, task); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if logs, err := db.ListRunLogs(ctx, r.ID, 0, 100); err != nil || len(logs) != 1 {
		t.Fatal(logs, err)
	}
	if err = os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Execution().ArchiveRuns(ctx, domain.User{Role: domain.RolePlatformAdmin}, []string{r.ID}); err == nil {
		t.Fatal("new archive accepted without storage")
	}
}

func TestAutomaticRetentionProgressesPastProtectedRecords(t *testing.T) {
	p, db := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-91 * 24 * time.Hour)
	owner := domain.User{ID: "retention-owner", Name: "Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: now}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	env, err := p.CreateEnvironment(ctx, owner, domain.Environment{Name: "Retention cursor test"}, completeServiceTestFacts())
	if err != nil {
		t.Fatal(err)
	}
	refs := []any{}
	for i := 0; i < 101; i++ {
		id := fmt.Sprintf("retention-%03d", i)
		r := domain.Run{ID: id, Kind: domain.RunScenario, Status: domain.RunFailed, RequestedBy: owner.ID, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, CreatedAt: old, FinishedAt: &old, InputSnapshot: map[string]any{}}
		if err = db.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
		if i < 100 {
			refs = append(refs, map[string]any{"runId": id})
		}
	}
	holder := domain.Run{ID: "retention-holder", Kind: domain.RunScenario, Status: domain.RunSucceeded, RequestedBy: owner.ID, EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, CreatedAt: old, FinishedAt: &old, InputSnapshot: map[string]any{"sources": refs}}
	if err = db.CreateRun(ctx, holder, nil); err != nil {
		t.Fatal(err)
	}
	if err = p.ConfigureRunArchives(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveRunRetentionPolicy(ctx, domain.RunRetentionPolicy{AutoArchive: true, AutoCleanup: true, ArchiveDays: 90, CleanupDays: 90}, "system", now); err != nil {
		t.Fatal(err)
	}
	p.scanRunRetention(ctx)
	var skipped int
	if err = db.DB().QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='run.cleanup_skipped'`).Scan(&skipped); err != nil || skipped != 50 {
		t.Fatal("shared scan budget", skipped, err)
	}
	p.scanRunRetention(ctx)
	p.scanRunRetention(ctx)
	if _, err = db.GetRun(ctx, "retention-100"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("protected records starved later candidate", err)
	}
	if _, err = db.GetRun(ctx, "retention-000"); err != nil {
		t.Fatal("protected record lost", err)
	}
	health, err := db.RunArchiveHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, result := range health.CleanupResults {
		if result.RunID == "retention-100" && result.Status == "cleaned" {
			found = true
		}
	}
	if !found {
		t.Fatal("cleanup outcome absent", health.CleanupResults)
	}
}

func TestCleanupDoesNotResurfaceAnOlderWorkbenchFailure(t *testing.T) {
	p, db := readinessTestPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := domain.User{ID: "history-env-owner", Name: "Owner", Role: domain.RoleEnvironmentOwner, CreatedAt: now}
	if err := db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	env, err := p.CreateEnvironment(ctx, owner, domain.Environment{Name: "History workbench"}, completeServiceTestFacts())
	if err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "history-draft", ComponentID: "component-1", Version: "1.0", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, CreatedAt: now, EnvironmentConstraints: map[string]any{}}
	if err = db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	release, err = db.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"workbench-older-failure", "workbench-latest-failure"} {
		at := now.Add(time.Duration(-100+i) * 24 * time.Hour)
		r := domain.Run{ID: id, Kind: domain.RunComponentTest, Status: domain.RunFailed, ComponentReleaseID: release.ID, RequestedBy: "component-owner", EnvironmentID: env.ID, EnvironmentRevisionID: env.CurrentRevisionID, CreatedAt: at, FinishedAt: &at, InputSnapshot: map[string]any{"componentReleaseSpecDigest": domain.ComponentReleaseSpecDigest(release)}}
		if err = db.CreateRun(ctx, r, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.CleanupRuns(ctx, []string{"workbench-latest-failure"}, "system", "manual", now); err != nil {
		t.Fatal(err)
	}
	work, err := p.Workbench(ctx, domain.User{ID: "component-owner", Role: domain.RoleComponentOwner})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(work)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "workbench-older-failure") || strings.Contains(string(encoded), "workbench-latest-failure") {
		t.Fatal("cleaned or superseded failure resurfaced", string(encoded))
	}
}
