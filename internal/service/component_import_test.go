package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
)

func componentImportRecoveryPlatform(t *testing.T) (*Platform, *store.Store, string) {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	owner := domain.User{ID: "component-import-owner", Name: "Owner", Role: domain.RoleComponentOwner, CreatedAt: time.Now().UTC()}
	if err := database.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	platform := newTestPlatform(t, database, nil, nil)
	platform.ConfigurePlaybookRoot(root)
	return platform, database, root
}

func recoveryImportFile(componentID, releaseID, slug string) componentImportFile {
	component := domain.Component{ID: componentID, Slug: slug}
	release := domain.ComponentRelease{ID: releaseID, ComponentID: componentID, LineName: "Baseline", Version: "1.0.0"}
	return componentImportFile{
		Component: component, Release: release,
		RelativePath: managedReleasePrefix(component, release) + "tasks/install.yml",
		Content:      "---\n- ansible.builtin.assert:\n    that: true\n",
	}
}

func TestRecoverComponentImportFilesRemovesUncommittedPromotion(t *testing.T) {
	platform, _, root := componentImportRecoveryPlatform(t)
	file := recoveryImportFile("component-orphan", "release-orphan", "orphan")
	manifest, err := platform.catalog.stageComponentImportFiles([]componentImportFile{file})
	if err != nil {
		t.Fatal(err)
	}
	if err := platform.catalog.promoteComponentImportFiles(manifest); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, filepath.FromSlash(file.RelativePath))
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
	if err := platform.catalog.RecoverComponentImportFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("uncommitted promoted file still exists: %v", err)
	}
}

func TestRecoverComponentImportFilesKeepsCommittedPromotion(t *testing.T) {
	platform, database, root := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	now := time.Now().UTC()
	file := recoveryImportFile("component-committed", "release-committed", "committed")
	manifest, err := platform.catalog.stageComponentImportFiles([]componentImportFile{file})
	if err != nil {
		t.Fatal(err)
	}
	if err := platform.catalog.promoteComponentImportFiles(manifest); err != nil {
		t.Fatal(err)
	}
	component := domain.Component{ID: file.Component.ID, Slug: file.Component.Slug, Name: "Committed", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: "component-import-owner", CreatedAt: now, UpdatedAt: now}
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: file.Release.ID, ComponentID: component.ID, Version: "1.0.0", Status: domain.ReleaseDraft, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	if err := platform.catalog.RecoverComponentImportFiles(ctx); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, filepath.FromSlash(file.RelativePath))
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("committed promoted file was removed: %v", err)
	}
	if _, err := os.Stat(manifest.BatchDir); !os.IsNotExist(err) {
		t.Fatalf("committed manifest was not removed: %v", err)
	}
}

func TestComponentImportPromotionRejectsManagedDirectorySymlink(t *testing.T) {
	platform, _, root := componentImportRecoveryPlatform(t)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "managed"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "managed", "redirected")); err != nil {
		t.Fatal(err)
	}
	file := recoveryImportFile("component-redirected", "release-redirected", "redirected")
	manifest, err := platform.catalog.stageComponentImportFiles([]componentImportFile{file})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(manifest.BatchDir)
	if err := platform.catalog.promoteComponentImportFiles(manifest); err == nil {
		t.Fatal("promotion through a managed-directory symlink succeeded")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("promotion created files outside playbook root: %#v", entries)
	}
}

func TestReleaseCloneCopyRejectsManagedTargetSymlink(t *testing.T) {
	platform, db, root := componentImportRecoveryPlatform(t)
	component := domain.Component{ID: "component-clone-symlink", Slug: "clone-symlink"}
	source := domain.ComponentRelease{ID: "release-clone-source", ComponentID: component.ID, LineName: "Baseline", Version: "1.0.0"}
	target := domain.ComponentRelease{ID: "release-clone-target", ComponentID: component.ID, LineName: "Baseline", Version: "2.0.0", Actions: []domain.ActionDefinition{
		{Playbook: managedReleasePrefix(component, source) + "tasks/install.yml"},
	}}
	component.Name, component.OwnerID, component.Layer, component.CreatedAt, component.UpdatedAt = component.Slug, "component-import-owner", domain.LayerRuntimeState, time.Now().UTC(), time.Now().UTC()
	if err := db.CreateComponent(context.Background(), component); err != nil {
		t.Fatal(err)
	}
	source.Status, source.RiskLevel, source.CreatedAt = domain.ReleaseDraft, domain.RiskLow, time.Now().UTC()
	source.Actions = []domain.ActionDefinition{{ID: source.ID + "-install", ReleaseID: source.ID, Kind: domain.ActionInstall, Name: "Install", Playbook: managedReleasePrefix(component, source) + "tasks/install.yml"}}
	if err := db.CreateComponentRelease(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, root, source.ID)
	source, _ = db.GetComponentRelease(context.Background(), source.ID)
	target.Actions = []domain.ActionDefinition{{ID: target.ID + "-install", ReleaseID: target.ID, Kind: domain.ActionInstall, Name: "Install", Playbook: source.Actions[0].Playbook}}
	sourceDirectory := filepath.Join(root, filepath.FromSlash(managedReleasePrefix(component, source)))
	if err := os.MkdirAll(sourceDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDirectory, "tasks", "install.yml"), []byte(testutil.Playbook), 0o640); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(filepath.Join(root, filepath.FromSlash(managedReleasePrefix(component, target))))), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Clean(filepath.Join(root, filepath.FromSlash(managedReleasePrefix(component, target))))); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.catalog.copyManagedPlaybooksForClone(component, source.ID, &target); err == nil {
		t.Fatal("release clone copied a Playbook through a managed target symlink")
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("release clone created files outside playbook root: %#v", entries)
	}
}

func TestReleaseCloneCopyRecoveryRemovesUncommittedPromotion(t *testing.T) {
	platform, db, root := componentImportRecoveryPlatform(t)
	component := domain.Component{ID: "component-clone-recovery", Slug: "clone-recovery"}
	source := domain.ComponentRelease{ID: "release-clone-recovery-source", ComponentID: component.ID, LineName: "Baseline", Version: "1.0.0"}
	target := domain.ComponentRelease{ID: "release-clone-recovery-target", ComponentID: component.ID, LineName: "Baseline", Version: "2.0.0", Actions: []domain.ActionDefinition{
		{Playbook: managedReleasePrefix(component, source) + "tasks/install.yml"},
	}}
	component.Name, component.OwnerID, component.Layer, component.CreatedAt, component.UpdatedAt = component.Slug, "component-import-owner", domain.LayerRuntimeState, time.Now().UTC(), time.Now().UTC()
	if err := db.CreateComponent(context.Background(), component); err != nil {
		t.Fatal(err)
	}
	source.Status, source.RiskLevel, source.CreatedAt = domain.ReleaseDraft, domain.RiskLow, time.Now().UTC()
	source.Actions = []domain.ActionDefinition{{ID: source.ID + "-install", ReleaseID: source.ID, Kind: domain.ActionInstall, Name: "Install", Playbook: managedReleasePrefix(component, source) + "tasks/install.yml"}}
	if err := db.CreateComponentRelease(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, root, source.ID)
	source, _ = db.GetComponentRelease(context.Background(), source.ID)
	target.Actions = []domain.ActionDefinition{{ID: target.ID + "-install", ReleaseID: target.ID, Kind: domain.ActionInstall, Name: "Install", Playbook: source.Actions[0].Playbook}}
	sourceDirectory := filepath.Join(root, filepath.FromSlash(managedReleasePrefix(component, source)))
	if err := os.MkdirAll(sourceDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDirectory, "tasks", "install.yml"), []byte(testutil.Playbook), 0o640); err != nil {
		t.Fatal(err)
	}
	manifest, err := platform.catalog.copyManagedPlaybooksForClone(component, source.ID, &target)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(root, filepath.FromSlash(target.Actions[0].Playbook))
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("cloned Playbook was not promoted: %v", err)
	}
	if err := platform.catalog.RecoverComponentImportFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("uncommitted cloned Playbook still exists: %v", err)
	}
	if _, err := os.Stat(manifest.BatchDir); !os.IsNotExist(err) {
		t.Fatalf("clone recovery manifest still exists: %v", err)
	}
}
