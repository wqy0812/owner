package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

func TestPlaybookWorkspaceFilesAreGovernedAsOneDraftContract(t *testing.T) {
	platform, database, root := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	component := domain.Component{ID: "workspace-component", Slug: "workspace-runtime", Name: "Workspace runtime", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "workspace-release", ComponentID: component.ID, LineID: "workspace-line", LineName: "Stable 1.x", Version: "1.2.3 RC1", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	entry, err := platform.SaveReleaseActionPlaybook(ctx, owner, release.ID, domain.ActionInstall, []byte("---\n- hosts: all\n  tasks: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path != "managed/workspace-runtime/stable-1.x/1.2.3-rc1--workspace-release/install.yml" {
		t.Fatalf("entry path=%q", entry.Path)
	}
	if _, err := platform.SaveReleaseWorkspaceFile(ctx, owner, release.ID, "templates/config.j2", []byte("port={{ port }}\n")); err != nil {
		t.Fatal(err)
	}
	workspace, err := platform.ListReleasePlaybookWorkspace(ctx, owner, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Root != "managed/workspace-runtime/stable-1.x/1.2.3-rc1--workspace-release/" || len(workspace.Files) != 2 || workspace.TreeSHA256 == "" {
		t.Fatalf("workspace=%+v", workspace)
	}
	before := workspace.TreeSHA256
	if _, err := database.DB().ExecContext(ctx, `UPDATE component_releases SET candidate=1,review_status='approved',review_contract_digest='old' WHERE id=?`, release.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.SaveReleaseWorkspaceFile(ctx, owner, release.ID, "roles/helper/tasks/main.yml", []byte("---\n[]\n")); err != nil {
		t.Fatal(err)
	}
	updated, err := database.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PlaybookTreeSHA256 == before || updated.Candidate || updated.Review.Status != domain.ReleaseReviewNotSubmitted {
		t.Fatalf("workspace edit did not invalidate contract: %+v", updated)
	}
	if _, err := platform.RenameReleaseWorkspaceFile(ctx, owner, release.ID, "templates/config.j2", "templates/runtime.conf.j2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := platform.ReadReleaseWorkspaceFile(ctx, owner, release.ID, "templates/runtime.conf.j2"); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.DeleteReleaseWorkspaceFile(ctx, owner, release.ID, "templates/runtime.conf.j2"); err != nil {
		t.Fatal(err)
	}
	large := make([]byte, MaxPlaybookBytes+1)
	if _, err := platform.SaveReleaseWorkspaceFile(ctx, owner, release.ID, "files/helper.bin", large); err != nil {
		t.Fatal(err)
	}
	metadata, contents, err := platform.ReadReleaseWorkspaceFile(ctx, owner, release.ID, "files/helper.bin")
	if err != nil || metadata.Editable || len(contents) != len(large) {
		t.Fatalf("binary metadata=%+v bytes=%d err=%v", metadata, len(contents), err)
	}
	if _, err := platform.SaveReleaseWorkspaceFile(ctx, owner, release.ID, "../escape", []byte("x")); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := platform.SaveReleaseWorkspaceFile(ctx, owner, release.ID, "files/too-large.bin", make([]byte, MaxWorkspaceFileBytes+1)); err == nil {
		t.Fatal("oversized file accepted")
	}
	workspaceDirectory := filepath.Join(root, "managed", "workspace-runtime", "stable-1.x", "1.2.3-rc1--workspace-release")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspaceDirectory, "redirect")); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.SaveReleaseWorkspaceFile(ctx, owner, release.ID, "redirect/outside.txt", []byte("secret")); err == nil {
		t.Fatal("directory symlink accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "outside.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write escaped workspace: %v", err)
	}
}

func TestPlaybookWorkspaceRejectsStaleConditionalMutations(t *testing.T) {
	platform, database, _ := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	component := domain.Component{ID: "conditional-component", Slug: "conditional", Name: "Conditional", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "conditional-release", ComponentID: component.ID, LineID: "conditional-line", LineName: "Stable", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}

	mustNotExist := ""
	first, err := platform.SaveReleaseWorkspaceFileConditional(ctx, owner, release.ID, "templates/config.j2", []byte("first\n"), &mustNotExist)
	if err != nil {
		t.Fatal(err)
	}
	second, err := platform.SaveReleaseWorkspaceFileConditional(ctx, owner, release.ID, "templates/config.j2", []byte("second\n"), &first.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.SaveReleaseWorkspaceFileConditional(ctx, owner, release.ID, "templates/config.j2", []byte("stale\n"), &first.SHA256); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale save error=%v, want conflict", err)
	}
	staleTree := "stale-tree"
	if _, err := platform.SaveReleaseWorkspaceFileWithExpectation(ctx, owner, release.ID, "templates/config.j2", []byte("tree-stale\n"), &second.SHA256, &staleTree); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale workspace tree error=%v, want conflict", err)
	}
	workspaceBeforeAction, err := platform.ListReleasePlaybookWorkspace(ctx, owner, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.SaveReleaseWorkspaceFile(ctx, owner, release.ID, "roles/helper/tasks/main.yml", []byte("---\n[]\n")); err != nil {
		t.Fatal(err)
	}
	emptySHA := ""
	if _, err := platform.SaveReleaseActionPlaybookWithExpectation(ctx, owner, release.ID, domain.ActionInstall, []byte("---\n- hosts: all\n"), &emptySHA, &workspaceBeforeAction.TreeSHA256); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale action workspace tree error=%v, want conflict", err)
	}
	if _, err := platform.RenameReleaseWorkspaceFileConditional(ctx, owner, release.ID, "templates/config.j2", "templates/renamed.j2", &first.SHA256); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale rename error=%v, want conflict", err)
	}
	if _, err := platform.DeleteReleaseWorkspaceFileConditional(ctx, owner, release.ID, "templates/config.j2", &first.SHA256); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale delete error=%v, want conflict", err)
	}
	_, contents, err := platform.ReadReleaseWorkspaceFile(ctx, owner, release.ID, "templates/config.j2")
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "second\n" || second.SHA256 == first.SHA256 {
		t.Fatalf("stale edit changed current file: sha=%s contents=%q", second.SHA256, contents)
	}
}

func TestActionAndEntrypointAreCommittedAndDeletedAtomically(t *testing.T) {
	platform, database, root := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	seedAtomicActionHostGroup(t, ctx, database, now)
	component := domain.Component{ID: "atomic-action-component", Slug: "atomic-action", Name: "Atomic action", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "atomic-action-release", ComponentID: component.ID, LineID: "atomic-action-line", LineName: "Stable", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	empty := ""
	action := domain.ActionDefinition{Name: "Install", Kind: domain.ActionInstall, HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}
	saved, err := platform.SaveReleaseActionAtomic(ctx, owner, release.ID, action, []byte("---\n- hosts: all\n  tasks: []\n"), &empty, &empty)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Action == nil || saved.Action.ID == "" || saved.Action.PlaybookSHA256 != saved.SHA256 {
		t.Fatalf("saved action=%+v file=%+v", saved.Action, saved)
	}
	persisted, err := database.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Actions) != 1 || len(persisted.PlaybookFiles) != 1 || persisted.Actions[0].ID != saved.Action.ID {
		t.Fatalf("persisted release=%+v", persisted)
	}
	if pending, err := database.ListPendingActionFileMutations(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("successful save left pending mutation=%+v err=%v", pending, err)
	}
	workspace, err := platform.ListReleasePlaybookWorkspace(ctx, owner, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `CREATE TRIGGER reject_atomic_action_delete BEFORE DELETE ON action_definitions BEGIN SELECT RAISE(ABORT, 'injected action delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.DeleteReleaseActionAtomic(ctx, owner, release.ID, domain.ActionInstall, &saved.SHA256, &workspace.TreeSHA256); err == nil {
		t.Fatal("injected delete failure was accepted")
	}
	persisted, err = database.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Actions) != 1 || len(persisted.PlaybookFiles) != 1 {
		t.Fatalf("failed delete changed catalog: actions=%+v files=%+v", persisted.Actions, persisted.PlaybookFiles)
	}
	entrypoint := filepath.Join(root, "managed", "atomic-action", "stable", "1.0.0--atomic-action-release", "install.yml")
	if _, err := os.Stat(entrypoint); err != nil {
		t.Fatalf("failed delete did not restore entrypoint: %v", err)
	}
	if _, err := database.DB().ExecContext(ctx, `DROP TRIGGER reject_atomic_action_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := platform.DeleteReleaseActionAtomic(ctx, owner, release.ID, domain.ActionInstall, &saved.SHA256, &workspace.TreeSHA256); err != nil {
		t.Fatal(err)
	}
	persisted, err = database.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Actions) != 0 || len(persisted.PlaybookFiles) != 0 {
		t.Fatalf("atomic delete left metadata: actions=%+v files=%+v", persisted.Actions, persisted.PlaybookFiles)
	}
	if _, err := os.Stat(entrypoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("atomic delete left entrypoint: %v", err)
	}
}

func TestAtomicActionRestoresEntrypointWhenCatalogTransactionFails(t *testing.T) {
	platform, database, root := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	seedAtomicActionHostGroup(t, ctx, database, now)
	component := domain.Component{ID: "atomic-failure-component", Slug: "atomic-failure", Name: "Atomic failure", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "atomic-failure-release", ComponentID: component.ID, LineID: "atomic-failure-line", LineName: "Stable", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `CREATE TRIGGER reject_atomic_action BEFORE INSERT ON action_definitions BEGIN SELECT RAISE(ABORT, 'injected action failure'); END`); err != nil {
		t.Fatal(err)
	}
	empty := ""
	action := domain.ActionDefinition{Name: "Install", Kind: domain.ActionInstall, HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}
	if _, err := platform.SaveReleaseActionAtomic(ctx, owner, release.ID, action, []byte("---\n- hosts: all\n"), &empty, &empty); err == nil {
		t.Fatal("injected catalog failure was accepted")
	}
	persisted, err := database.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Actions) != 0 || len(persisted.PlaybookFiles) != 0 || persisted.PlaybookTreeSHA256 != "" {
		t.Fatalf("failed transaction changed catalog: %+v", persisted)
	}
	entrypoint := filepath.Join(root, "managed", "atomic-failure", "stable", "1.0.0--atomic-failure-release", "install.yml")
	if _, err := os.Stat(entrypoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed transaction left entrypoint: %v", err)
	}
	if pending, err := database.ListPendingActionFileMutations(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("compensated failure left pending mutation=%+v err=%v", pending, err)
	}
}

func TestPendingActionFileMutationIsRecoveredAfterProcessInterruption(t *testing.T) {
	platform, database, root := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	seedAtomicActionHostGroup(t, ctx, database, now)
	component := domain.Component{ID: "atomic-recovery-component", Slug: "atomic-recovery", Name: "Atomic recovery", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "atomic-recovery-release", ComponentID: component.ID, LineID: "atomic-recovery-line", LineName: "Stable", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	empty := ""
	before := []byte("---\n- hosts: before\n")
	action := domain.ActionDefinition{Name: "Install", Kind: domain.ActionInstall, HostGroup: "all", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}
	if _, err := platform.SaveReleaseActionAtomic(ctx, owner, release.ID, action, before, &empty, &empty); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := "managed/atomic-recovery/stable/1.0.0--atomic-recovery-release/"
	entrypoint := filepath.Join(root, filepath.FromSlash(workspaceRoot), "install.yml")
	mutation := store.PendingActionFileMutation{ID: "pending-action-recovery", ReleaseID: release.ID, WorkspaceRoot: workspaceRoot, RelativePath: "install.yml", BeforeExists: true, BeforeContents: before, CreatedAt: now.Add(time.Second)}
	if err := database.CreatePendingActionFileMutation(ctx, mutation); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entrypoint, []byte("---\n- hosts: interrupted\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := platform.RecoverActionFileMutations(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := os.ReadFile(entrypoint)
	if err != nil || string(recovered) != string(before) {
		t.Fatalf("recovered entrypoint=%q err=%v", recovered, err)
	}
	pending, err := database.ListPendingActionFileMutations(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending mutations after recovery=%+v err=%v", pending, err)
	}
}

func seedAtomicActionHostGroup(t *testing.T, ctx context.Context, database interface{ DB() *sql.DB }, now time.Time) {
	t.Helper()
	if _, err := database.DB().ExecContext(ctx, `INSERT INTO platform_option_categories(id,technical_key,label,category_type,environment_required,sort_order,created_by,created_at) VALUES('atomic-host-group-category','hostGroup','Host group','host_group',0,0,'component-import-owner',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `INSERT INTO platform_options(id,category_id,technical_value,label,sort_order,created_by,created_at) VALUES('atomic-host-group-all','atomic-host-group-category','all','all',0,'component-import-owner',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
}

func TestSavedActionKindIsImmutable(t *testing.T) {
	platform, database, _ := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	component := domain.Component{ID: "immutable-action-component", Slug: "immutable-action", Name: "Immutable action", Layer: domain.LayerRuntimeState, Tags: []string{"runtime"}, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{
		ID: "immutable-action-release", ComponentID: component.ID, LineID: "immutable-action-line", LineName: "Stable", Version: "1.0.0",
		Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow,
		EnvironmentConstraints: map[string]any{}, Parameters: []domain.ParameterDefinition{}, CreatedAt: now,
		Actions: []domain.ActionDefinition{{ID: "immutable-action-id", Kind: domain.ActionInstall, Name: "Install", Playbook: "install.yml", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}},
	}
	if _, err := database.DB().ExecContext(ctx, `INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES(?,?,?,?)`, release.LineID, component.ID, release.LineName, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `INSERT INTO component_releases(id,component_id,line_id,version,status,compatibility,risk_level,environment_constraints_json,parameters_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, release.ID, component.ID, release.LineID, release.Version, release.Status, release.Compatibility, release.RiskLevel, `{}`, `[]`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.DB().ExecContext(ctx, `INSERT INTO action_definitions(id,release_id,name,kind,playbook,tags_json,host_group,required_credentials_json,timeout_seconds,risk_level,destructive,idempotent) VALUES(?,?,?,?,?,?,?,?,?,?,0,0)`, release.Actions[0].ID, release.ID, release.Actions[0].Name, release.Actions[0].Kind, release.Actions[0].Playbook, `[]`, release.Actions[0].HostGroup, `[]`, 60, release.Actions[0].RiskLevel); err != nil {
		t.Fatal(err)
	}
	patch := release
	patch.Actions = append([]domain.ActionDefinition(nil), release.Actions...)
	patch.Actions[0].Kind = domain.ActionConfigure
	if _, err := platform.UpdateRelease(ctx, owner, release.ID, patch); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("action kind mutation error=%v, want conflict", err)
	}
}

func TestReleaseActionsRequireCurrentWorkspaceManifest(t *testing.T) {
	platform, _, root := componentImportRecoveryPlatform(t)
	if err := os.WriteFile(filepath.Join(root, "install.yml"), []byte("---\n- hosts: all\n  tasks: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.ReleaseStatus{domain.ReleaseDraft, domain.ReleaseReleased, domain.ReleaseDeprecated} {
		release := domain.ComponentRelease{ID: "unmanaged", Status: status, Actions: []domain.ActionDefinition{{Kind: domain.ActionInstall, Playbook: "install.yml"}}}
		if err := platform.validateWorkspaceManifest(context.Background(), release); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("status %s accepted an entrypoint without a manifest: %v", status, err)
		}
	}
}

func TestDraftVersionChangePreservesReadableActionWorkspace(t *testing.T) {
	platform, database, _ := componentImportRecoveryPlatform(t)
	ctx := context.Background()
	owner := domain.User{ID: "component-import-owner", Role: domain.RoleComponentOwner}
	now := time.Now().UTC()
	seedAtomicActionHostGroup(t, ctx, database, now)
	component := domain.Component{ID: "rename-component", Slug: "rename", Name: "Rename", Layer: domain.LayerRuntimeState, OwnerID: owner.ID, CreatedAt: now, UpdatedAt: now}
	if err := database.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "rename-release", ComponentID: component.ID, LineID: "rename-line", LineName: "Stable", Version: "1.0.0", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, EnvironmentConstraints: map[string]any{}, CreatedAt: now}
	if err := database.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	empty := ""
	content := "---\n- hosts: all\n  tasks: []\n"
	entry, err := platform.SaveReleaseActionAtomic(ctx, owner, release.ID, domain.ActionDefinition{Name: "Install", Kind: domain.ActionInstall, HostGroup: "all"}, []byte(content), &empty, &empty)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := database.GetComponentRelease(ctx, release.ID)
	if err != nil {
		t.Fatal(err)
	}
	patch.Version = "1.0.1"
	updated, err := platform.UpdateRelease(ctx, owner, release.ID, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Actions) != 1 || updated.Actions[0].ID != entry.Action.ID {
		t.Fatalf("Action identity changed: %+v", updated.Actions)
	}
	read, err := platform.ReadReleasePlaybook(ctx, owner, release.ID, updated.Actions[0].Playbook)
	if err != nil || read.Content != content {
		t.Fatalf("renamed Draft entrypoint is unreadable: %v", err)
	}
	if err := platform.validateWorkspaceManifest(ctx, updated); err != nil {
		t.Fatalf("renamed Draft manifest is invalid: %v", err)
	}
}
