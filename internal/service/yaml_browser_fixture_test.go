package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/domain"
)

// This opt-in fixture never executes Ansible or connects to a host.
func TestYAMLBrowserFixture(t *testing.T) {
	destination := os.Getenv("CLUSTERFORGE_YAML_BROWSER_FIXTURE")
	if destination == "" {
		t.Skip("set CLUSTERFORGE_YAML_BROWSER_FIXTURE for isolated browser QA")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	p, db, owner, revision, env, runner := scenarioExecutionFixture(t)
	ctx := context.Background()
	if _, err := db.DB().Exec(`UPDATE action_definitions SET pre_check_action_id='',post_check_action_id='' WHERE release_id=? AND kind='rollback'`, "scenario-release"); err != nil {
		t.Fatal(err)
	}
	runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	componentOwner := domain.User{ID: "component-owner", Role: domain.RoleComponentOwner}
	input := ReleaseDraftRequest{Mode: ReleaseDraftNewLine, LineName: "YAML", TemplateSourceReleaseID: "scenario-release", Version: "2.0", ReleaseNotes: "独立 YAML 迁入验证"}
	preview, err := p.catalog.PreviewReleaseDraft(ctx, componentOwner, "component-1", input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedPlanDigest = preview.PlanDigest
	draft, err := p.catalog.createReleaseDraft(ctx, componentOwner, "component-1", input)
	if err != nil {
		t.Fatal(err)
	}
	install, _ := findAction(draft, domain.ActionInstall)
	contract := *install.ResourceContract
	contract.Checks = []domain.RuntimeCheck{{ID: "legacy-sh", Kind: "command", Target: "sh"}}
	encoded, _ := json.Marshal(contract)
	if _, err := db.DB().Exec(`UPDATE action_definitions SET gather_facts=1,resource_contract_json=? WHERE id=?`, string(encoded), install.ID); err != nil {
		t.Fatal(err)
	}
	revision, err = db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	digest := domain.ScenarioRevisionSpecDigest(revision)
	revision.AcceptanceJobs[0].GatherFacts = true
	revision.AcceptanceJobs[0].RuntimeChecks = []domain.RuntimeCheck{{ID: "legacy-sh", Kind: "command", Target: "sh"}}
	if err := db.SaveScenarioRevisionDefinition(ctx, revision, digest); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(destination, "playbooks"), os.DirFS(runner.root)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, "VACUUM INTO ?", filepath.Join(destination, "platform.db")); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.MarshalIndent(map[string]any{"componentId": "component-1", "releaseId": draft.ID, "actionId": install.ID, "scenarioId": revision.ScenarioID, "revisionId": revision.ID, "environmentId": env.ID, "ownerId": owner.ID, "policy": "No browser Run submissions; protocol fixture only."}, "", "  ")
	if err := os.WriteFile(filepath.Join(destination, "fixture.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
}
