package service

import (
	"codex/platform-demo/internal/testutil/runfixture"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
)

func TestStartupRecoveryFailureKeepsQueuedRunsStoppedAfterReopen(t *testing.T) {
	for _, kind := range []string{"unsafe marker", "corrupt directory payload"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "platform.db")
			p, db, _, owner, release := directoryDeletionFixtureAt(t, path)
			root := p.workspace.root
			now := time.Now().UTC()
			env := domain.Environment{ID: "recovery-env", Name: "Recovery", OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
			revision := domain.EnvironmentRevision{ID: "recovery-revision", EnvironmentID: env.ID, Revision: 1, Facts: map[string]any{}, Inventory: json.RawMessage(`{"hosts":[]}`), CreatedAt: now}
			if err := db.CreateEnvironment(ctx, env, revision); err != nil {
				t.Fatal(err)
			}
			run := domain.Run{ID: "queued-before-interruption", Kind: domain.RunComponentTest, Status: domain.RunQueued, RequestedBy: owner.ID, EnvironmentID: env.ID, EnvironmentRevisionID: revision.ID, ComponentReleaseID: release.ID, Snapshot: runfixture.Snapshot(map[string]any{}), CreatedAt: now}
			if err := testutil.InsertRunRecord(ctx, db.DB(), run); err != nil {
				t.Fatal(err)
			}
			marker := store.PendingActionFileMutation{ID: "interrupted-write", ReleaseID: release.ID, WorkspaceRoot: "../unsafe", RelativePath: "tasks/install.yml", BeforeExists: true, BeforeContents: []byte("before"), CreatedAt: now}
			if kind == "corrupt directory payload" {
				marker.Kind = "directory"
				marker.WorkspaceRoot = release.PlaybookWorkspaceRoot
				marker.RelativePath = "files/nested"
				marker.BeforeContents = []byte("invalid directory backup")
			}
			if err := db.CreatePendingActionFileMutation(ctx, marker); err != nil {
				t.Fatal(err)
			}
			p.Close()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			p = newTestPlatform(t, reopened, nil, nil)
			defer p.Close()
			p.ConfigurePlaybookRoot(root)
			if err := p.Start(ctx); err == nil || !strings.Contains(err.Error(), "recover Action file mutations") {
				t.Fatalf("startup=%v", err)
			}
			retained, err := reopened.GetRun(ctx, run.ID)
			if err != nil || retained.Status != domain.RunQueued {
				t.Fatalf("recovery failure started queued work: %+v %v", retained, err)
			}
			p.scheduler.mu.Lock()
			workers := len(p.scheduler.workers)
			p.scheduler.mu.Unlock()
			if workers != 0 {
				t.Fatal("scheduler started before recovery completed")
			}
			pending, err := reopened.ListPendingActionFileMutations(ctx)
			if err != nil || len(pending) != 1 {
				t.Fatalf("failed marker discarded: %+v %v", pending, err)
			}
		})
	}
}
