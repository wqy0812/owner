package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/testutil"
)

// Opt-in desktop browser fixture. Evidence is produced through the in-memory
// job protocol and exported only to a new temporary destination.
func TestScenarioBrowserFixture(t *testing.T) {
	destination := os.Getenv("CLUSTERFORGE_SCENARIO_BROWSER_FIXTURE")
	if destination == "" {
		t.Skip("set CLUSTERFORGE_SCENARIO_BROWSER_FIXTURE for desktop browser QA")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	p, db, owner, first, env, runner := scenarioExecutionFixture(t)
	ctx := context.Background()
	runScenarioProtocol(t, p, owner, first.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	first, err := p.releases.PublishScenario(ctx, owner, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	upgradeEnv := copyScenarioTestEnvironment(t, p, env, "desktop-upgrade-test-source")
	formalEnv := copyScenarioTestEnvironment(t, p, env, "desktop-formal-source")
	formal := runScenarioProtocol(t, p, owner, first.ID, upgradeEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenario)
	runScenarioProtocol(t, p, owner, first.ID, formalEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenario)
	clone := ScenarioCloneRequest{SourceRevisionID: first.ID, SourceRunID: formal.ID}
	preview, err := p.scenarios.PreviewClone(ctx, owner, first.ScenarioID, clone)
	if err != nil {
		t.Fatal(err)
	}
	clone.ExpectedPlanDigest = preview.PlanDigest
	next, err := p.scenarios.CloneRevision(ctx, owner, first.ScenarioID, clone)
	if err != nil {
		t.Fatal(err)
	}
	old, err := db.GetComponentRelease(ctx, first.Graph.Nodes[0].ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	release := old
	release.ID, release.Version, release.ParentReleaseID = "desktop-release-v2", "2.0", old.ID
	release.PlaybookWorkspaceRoot = "managed/component-1/scenario-line/desktop-release-v2/"
	release.PlaybookFiles = nil
	release.PlaybookTreeSHA256 = ""
	release.Actions = []domain.ActionDefinition{{ID: "desktop-v2-install", Kind: domain.ActionInstall, Name: "Install", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow}, {ID: "desktop-v2-upgrade", Kind: domain.ActionUpgrade, Name: "Upgrade", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, FromReleaseID: old.ID, ToReleaseID: release.ID}, {ID: "desktop-v2-rollback", Kind: domain.ActionRollback, Name: "Restore", HostGroup: "test_nodes", TimeoutSeconds: 60, RiskLevel: domain.RiskLow, FromReleaseID: release.ID, ToReleaseID: old.ID}}
	if err = db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	testutil.Workspaces(t, db, runner.root, release.ID)
	graph := next.Graph
	graph.Nodes = append([]domain.ScenarioNode(nil), graph.Nodes...)
	graph.Nodes[0].ReleaseID = release.ID
	next, err = p.scenarios.SaveGraph(ctx, owner, next.ID, graph)
	if err != nil {
		t.Fatal(err)
	}
	runScenarioProtocol(t, p, owner, next.ID, upgradeEnv.ID, domain.ScenarioExecutionUpgrade, domain.RunScenarioTest)
	clean := copyScenarioTestEnvironment(t, p, env, "desktop-clean-install")
	runScenarioProtocol(t, p, owner, next.ID, clean.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)

	// A second published scene with no active Draft exposes the new-version flow.
	forkInput := ScenarioForkRequest{SourceRevisionID: first.ID, Name: "Desktop source for new version", Slug: "desktop-source"}
	forkPreview, err := p.scenarios.PreviewFork(ctx, owner, forkInput)
	if err != nil {
		t.Fatal(err)
	}
	forkInput.ExpectedPlanDigest = forkPreview.PlanDigest
	fork, err := p.scenarios.Fork(ctx, owner, forkInput)
	if err != nil {
		t.Fatal(err)
	}
	forkTestEnv := copyScenarioTestEnvironment(t, p, env, "desktop-fork-install-test")
	forkFormalEnv := copyScenarioTestEnvironment(t, p, env, "desktop-fork-formal-source")
	runScenarioProtocol(t, p, owner, fork.CurrentRevisionID, forkTestEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	if _, err = p.releases.PublishScenario(ctx, owner, fork.CurrentRevisionID); err != nil {
		t.Fatal(err)
	}
	runScenarioProtocol(t, p, owner, fork.CurrentRevisionID, forkFormalEnv.ID, domain.ScenarioExecutionInstall, domain.RunScenario)
	copyScenarioTestEnvironment(t, p, env, "desktop-clean-unused")
	if err = os.CopyFS(filepath.Join(destination, "playbooks"), os.DirFS(runner.root)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB().ExecContext(ctx, "VACUUM INTO ?", filepath.Join(destination, "platform.db")); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.MarshalIndent(map[string]any{"ownerId": owner.ID, "scenarioId": first.ScenarioID, "sourceRevisionId": first.ID, "dualTestRevisionId": next.ID, "newVersionScenarioId": fork.ID, "formalSourceEnvironmentId": formalEnv.ID, "runPolicy": "do not submit Runs in browser; evidence was created by isolated protocol fixtures"}, "", "  ")
	if err = os.WriteFile(filepath.Join(destination, "fixture.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
}
