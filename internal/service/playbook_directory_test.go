package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func directoryDeletionFixture(t *testing.T) (*Platform, *store.Store, string, domain.User, domain.ComponentRelease) {
	t.Helper()
	p, db, root := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	seedAtomicActionHostGroup(t, ctx, db, now)
	c := domain.Component{ID: "directory-component", Slug: "directory-component", Name: "Directory", Layer: domain.LayerRuntimeState, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateComponent(ctx, c); err != nil {
		t.Fatal(err)
	}
	r := domain.ComponentRelease{ID: "directory-release", ComponentID: c.ID, LineID: "directory-line", LineName: "Main", Version: "1.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
	if err := db.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := p.catalog.SaveActionAtomic(ctx, owner, r.ID, domain.ActionDefinition{Kind: domain.ActionInstall, Name: "Install", HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}, []byte("- assert:\n    that: true\n"), nil, nil); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"files/nested/a.txt": "a", "files/nested/deeper/b.txt": "b", "files/empty/.gitkeep": "", "files/keep.txt": "keep"} {
		if _, err := p.catalog.SavePlaybookWorkspaceFile(ctx, owner, r.ID, name, []byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := db.GetComponentRelease(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p, db, filepath.Join(root, r.PlaybookWorkspaceRoot), owner, r
}

func TestWorkspaceDirectoryDeletionPreservesSiblingsAndInvalidatesOnce(t *testing.T) {
	p, db, root, owner, release := directoryDeletionFixture(t)
	ctx := context.Background()
	for _, directory := range []string{"files/nested", "files/empty"} {
		before, err := p.catalog.ListPlaybookWorkspace(ctx, owner, release.ID)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := p.catalog.DeletePlaybookWorkspaceDirectory(ctx, owner, release.ID, directory, before.TreeSHA256)
		if err != nil {
			t.Fatal(err)
		}
		if updated.TreeSHA256 == before.TreeSHA256 {
			t.Fatal("manifest did not change")
		}
		if _, err := os.Stat(filepath.Join(root, directory)); !os.IsNotExist(err) {
			t.Fatalf("directory remained: %v", err)
		}
		if data, err := os.ReadFile(filepath.Join(root, "files/keep.txt")); err != nil || string(data) != "keep" {
			t.Fatalf("sibling changed: %v", err)
		}
		pending, err := db.ListPendingActionFileMutations(ctx)
		if err != nil || len(pending) != 0 {
			t.Fatalf("successful deletion left recovery records: %v %v", pending, err)
		}
	}
}

func TestWorkspaceDirectoryDeletionRejectsStaleUnsafeAndProtectedRequests(t *testing.T) {
	p, db, root, owner, release := directoryDeletionFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		path, digest string
		user         domain.User
		expected     error
	}{
		{"files/nested", "stale", owner, domain.ErrConflict},
		{"files/nested", "", owner, domain.ErrInvalid},
		{"../outside", release.PlaybookTreeSHA256, owner, domain.ErrInvalid},
		{".", release.PlaybookTreeSHA256, owner, domain.ErrInvalid},
		{"tasks", release.PlaybookTreeSHA256, owner, domain.ErrConflict},
		{"files/nested", release.PlaybookTreeSHA256, domain.User{ID: "someone-else", Role: domain.RoleComponentOwner}, domain.ErrForbidden},
	} {
		if _, err := p.catalog.DeletePlaybookWorkspaceDirectory(ctx, tc.user, release.ID, tc.path, tc.digest); !errors.Is(err, tc.expected) {
			t.Fatalf("%s: %v", tc.path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "files/nested/concurrent.txt"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.catalog.DeletePlaybookWorkspaceDirectory(ctx, owner, release.ID, "files/nested", release.PlaybookTreeSHA256); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("concurrent file ignored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "files/nested/a.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `UPDATE component_releases SET status='released' WHERE id=?`, release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.catalog.DeletePlaybookWorkspaceDirectory(ctx, owner, release.ID, "files/nested", release.PlaybookTreeSHA256); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("released workspace deletion: %v", err)
	}
}

func TestWorkspaceDirectoryDeletionRestoresAllFilesOnCatalogFailure(t *testing.T) {
	p, db, root, owner, release := directoryDeletionFixture(t)
	ctx := context.Background()
	if _, err := db.DB().ExecContext(ctx, `CREATE TRIGGER fail_directory_manifest BEFORE DELETE ON component_playbook_files BEGIN SELECT RAISE(ABORT,'injected metadata failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.catalog.DeletePlaybookWorkspaceDirectory(ctx, owner, release.ID, "files/nested", release.PlaybookTreeSHA256); err == nil || !strings.Contains(err.Error(), "injected metadata failure") {
		t.Fatalf("expected metadata failure, got %v", err)
	}
	for name, content := range map[string]string{"files/nested/a.txt": "a", "files/nested/deeper/b.txt": "b"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(data) != content {
			t.Fatalf("rollback lost %s: %v", name, err)
		}
	}
	current, err := db.GetComponentRelease(ctx, release.ID)
	if err != nil || current.PlaybookTreeSHA256 != release.PlaybookTreeSHA256 {
		t.Fatalf("failed deletion changed manifest: %v", err)
	}
	pending, err := db.ListPendingActionFileMutations(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("compensation left pending records: %v %v", pending, err)
	}
}

func TestWorkspaceDirectoryRecoveryAfterInterruptedDeletion(t *testing.T) {
	p, db, root, _, release := directoryDeletionFixture(t)
	ctx := context.Background()
	payload, err := json.Marshal([]directoryOriginalFile{{Path: "files/nested/a.txt", Contents: []byte("a")}, {Path: "files/nested/deeper/b.txt", Contents: []byte("b")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreatePendingActionFileMutation(ctx, store.PendingActionFileMutation{ID: "directory-recovery", Kind: "directory", ReleaseID: release.ID, WorkspaceRoot: release.PlaybookWorkspaceRoot, RelativePath: "files/nested", BeforeExists: true, BeforeContents: payload, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "files/nested/a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := p.catalog.RecoverActionFileMutations(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"files/nested/a.txt", "files/nested/deeper/b.txt"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := db.ListPendingActionFileMutations(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("startup recovery incomplete: %v %v", pending, err)
	}
}
