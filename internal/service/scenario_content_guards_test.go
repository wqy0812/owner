package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestScenarioPublishRejectsUnrecordedAcceptanceWorkspaceDrift(t *testing.T) {
	p, db, owner, revision, env, _ := scenarioExecutionFixture(t)
	ctx := context.Background()
	runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	revision, err := db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.catalog.workspace.root, filepath.FromSlash(revision.AcceptanceJobs[0].Playbook))
	if err = os.WriteFile(path, []byte("- ansible.builtin.fail:\n    msg: changed after testing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = p.releases.PublishScenario(ctx, owner, revision.ID); err == nil {
		t.Fatal("published evidence for different workspace bytes")
	}
	stored, err := db.GetScenarioRevision(ctx, revision.ID)
	if err != nil || stored.Status == domain.RevisionReleased {
		t.Fatalf("publication changed state: %s %v", stored.Status, err)
	}
}

func TestScenarioSnapshotsAllEffectiveEnvironmentValues(t *testing.T) {
	p, _, owner, revision, env, _ := scenarioExecutionFixture(t)
	if _, err := testDatabase(p).DB().Exec("UPDATE environment_revisions SET variables_json=? WHERE id=?", `{"site_setting":"frozen-old-value"}`, env.CurrentRevisionID); err != nil {
		t.Fatal(err)
	}
	run := runScenarioProtocol(t, p, owner, revision.ID, env.ID, domain.ScenarioExecutionInstall, domain.RunScenarioTest)
	nodes, err := scenarioSnapshotNodes(run.InputSnapshot)
	if err != nil || len(nodes) != 1 || nodes[0].Variables["site_setting"] != "frozen-old-value" {
		t.Fatalf("snapshot dropped effective environment values: %+v %v", nodes, err)
	}
	steps := []lockedStep{{SourceType: "component_action", Stage: "source_verify", Action: domain.ActionCheck, Variables: cloneMap(nodes[0].Variables)}, {SourceType: "component_action", Stage: "change", Action: domain.ActionUninstall, Variables: cloneMap(nodes[0].Variables)}}
	if err = injectEnvironmentVariables(domain.EnvironmentRevision{Variables: map[string]string{"site_setting": "new-target-value"}}, steps); err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if step.Variables["site_setting"] != "frozen-old-value" {
			t.Fatalf("target environment replaced source parameters: %+v", step)
		}
	}
}
