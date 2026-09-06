package service

import (
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
	"codex/platform-demo/internal/testutil"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBranchScopeBrowserFixture(t *testing.T) {
	destination := os.Getenv("CLUSTERFORGE_BRANCH_BROWSER_FIXTURE")
	if destination == "" {
		t.Skip("set CLUSTERFORGE_BRANCH_BROWSER_FIXTURE for isolated desktop QA")
	}
	t.Setenv("CLUSTERFORGE_SCENARIO_BROWSER_FIXTURE", destination)
	TestScenarioBrowserFixture(t)
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(destination, "platform.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	// Explicit fixture relationships, unrelated to any user's existing catalog.
	categories, err := db.ListPlatformOptionCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	osID := ""
	ubuntuID := ""
	for _, category := range categories {
		if category.Key == "operatingSystem" {
			osID = category.ID
			for _, option := range category.Options {
				if option.Value == "Ubuntu" {
					ubuntuID = option.ID
				}
			}
		}
	}
	if osID != "" && ubuntuID != "" {
		for _, category := range categories {
			if category.Key == "operatingSystemVersion" {
				if _, err = db.DB().Exec(`UPDATE platform_option_categories SET parent_category_id=? WHERE id=?`, osID, category.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = db.DB().Exec(`UPDATE platform_options SET parent_option_id=? WHERE category_id=?`, ubuntuID, category.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	r := domain.ComponentRelease{ID: "provenance-release", ComponentID: "component-1", LineID: "provenance-line", LineName: "Ubuntu 24.04 · 基础准备", Version: "3.0.0", Status: domain.ReleaseReleased, ReleasedAt: &now, RiskLevel: domain.RiskLow, Compatibility: domain.CompatibilityNotApplicable, EnvironmentConstraints: map[string]any{"architecture": []string{"amd64"}, "operatingSystem": []string{"Ubuntu"}, "operatingSystemVersion": []string{"24.04"}, "ipFamily": []string{"IPv4"}}, CreatedAt: now,
		Parameters: []domain.ParameterDefinition{{Name: "shared_root", Description: "本组件准备的共享目录；下游通过公开参数引用。", Type: domain.ParameterTypeString, Visibility: domain.ParameterPublic, ValueProvider: domain.ParameterProviderEnvironmentOwner, Modifiable: true, Required: true, EnvironmentBinding: &domain.EnvironmentParameterBinding{Kind: domain.EnvironmentBindingPrivate}, SuggestedValue: "/tmp/clusterforge-foundation-example-fixture"}},
		Actions:    []domain.ActionDefinition{{ID: "provenance-install", Name: "准备共享目录", Kind: domain.ActionInstall, HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, RequiredCredentials: []string{"shared_token"}}, {ID: "provenance-rollback", Name: "目录回退检查", Kind: domain.ActionRollback, HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, RequiredCredentials: []string{"shared_token"}}}}
	if err = db.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, filepath.Join(destination, "playbooks"), r.ID)
	r.ID, r.LineID, r.LineName, r.Version, r.Status, r.ReleasedAt = "provenance-draft", "provenance-draft-line", "Ubuntu 开发分支", "4.0.0", domain.ReleaseDraft, nil
	for i := range r.Actions {
		r.Actions[i].ID += "-draft"
	}
	if err = db.CreateComponentRelease(ctx, r); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, filepath.Join(destination, "playbooks"), r.ID)
	if _, err = db.DB().Exec(`UPDATE environment_revisions SET credential_refs_json='[{"name":"shared_token","kind":"envVarRef","reference":"CLUSTERFORGE_EXAMPLE_TOKEN"},{"name":"ssh_private_key","kind":"sshKeyPath","reference":"/tmp/example-key"},{"name":"unrelated","kind":"envVarRef","reference":""}]'`); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.MarshalIndent(map[string]any{"componentId": "component-1", "releaseId": "provenance-draft", "sourceReleaseId": "provenance-release", "policy": "Read-only browser QA except disposable draft and directory edits; no Run submissions."}, "", "  ")
	if err = os.WriteFile(filepath.Join(destination, "branch-fixture.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
}
