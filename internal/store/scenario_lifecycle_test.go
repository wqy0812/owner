package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func scenarioLifecycleFixture(t *testing.T) (*Store, domain.Scenario, domain.ScenarioRevision) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UTC()
	owner := domain.User{ID: "owner", Name: "Owner", Role: domain.RoleScenarioOwner, CreatedAt: now}
	if err = db.UpsertUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	revision := domain.ScenarioRevision{ID: "revision", ScenarioID: "scenario", Revision: 1, Status: domain.RevisionDraft, DigestVersion: domain.ScenarioDigestVersion, Graph: domain.ScenarioGraph{Nodes: []domain.ScenarioNode{}, Edges: []domain.ScenarioEdge{}}, AcceptanceJobs: []domain.ScenarioAcceptanceJob{{ID: "business", Name: "Business", HostGroup: "workers", Playbook: "acceptance.yml", PlaybookSHA256: "source-sha"}}, CreatedAt: now}
	scenario := domain.Scenario{ID: "scenario", Slug: "scenario", Name: "Scenario", OwnerID: owner.ID, CurrentRevisionID: revision.ID, CreatedAt: now, UpdatedAt: now}
	if err = db.CreateScenario(ctx, scenario, revision); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO environments(id,name,owner_id,created_at,updated_at) VALUES('env','Env','owner',?,?)`, timeText(now), timeText(now)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `INSERT INTO environment_revisions(id,environment_id,revision,created_at) VALUES('env-rev','env',1,?)`, timeText(now)); err != nil {
		t.Fatal(err)
	}
	return db, scenario, revision
}
func insertScenarioSourceRun(t *testing.T, db *Store, revision domain.ScenarioRevision, id, kind, status, mode string) {
	t.Helper()
	snapshot := map[string]any{"scenarioRevisionSpecDigest": domain.ScenarioRevisionSpecDigest(revision), "executionMode": mode, "acceptanceJobIds": []string{"business"}}
	_, err := db.db.Exec(`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,input_snapshot_json,created_at) VALUES(?,?,?,'owner','env','env-rev',?,?,?)`, id, kind, status, revision.ID, jsonText(snapshot), timeText(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
}

func scenarioSourceEvidenceFixture(t *testing.T) (*Store, domain.ScenarioRevision) {
	t.Helper()
	ctx := context.Background()
	db, _, revision := scenarioLifecycleFixture(t)
	if err := db.UpsertPlatformOptionCategory(ctx, domain.PlatformOptionCategory{ID: "category-hosts", Key: "hostGroup", Label: "Host groups", Kind: domain.PlatformOptionHostGroup, CreatedBy: "owner", CreatedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPlatformOption(ctx, domain.PlatformOption{ID: "workers", CategoryID: "category-hosts", Value: "workers", Label: "Workers", CreatedBy: "owner", CreatedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	component := componentFixture("component", "owner")
	if err := db.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := releaseFixture("release", component.ID, "1.0.0", domain.ReleaseReleased)
	release.EnvironmentConstraints = nil
	release.Actions[0].PreCheckActionID = "check"
	release.Actions[0].PostCheckActionID = "check"
	release.Actions = append(release.Actions,
		domain.ActionDefinition{ID: "check", Name: "Installed state", Kind: domain.ActionCheck, Playbook: "tasks/check.yml", HostGroup: "workers", TimeoutSeconds: 30, RiskLevel: domain.RiskLow},
		domain.ActionDefinition{ID: "rollback", Name: "Remove installation", Kind: domain.ActionRollback, Playbook: "tasks/rollback.yml", HostGroup: "workers", TimeoutSeconds: 60, RiskLevel: domain.RiskDestructive, PreCheckActionID: "check", PostCheckActionID: "check"},
	)
	if err := db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	revision.Status = domain.RevisionReleased
	revision.Graph.Nodes = []domain.ScenarioNode{{ID: "node", ReleaseID: release.ID, Action: domain.ActionInstall}}
	if _, err := db.DB().Exec(`UPDATE scenario_revisions SET status='released',graph_json=? WHERE id=?`, jsonText(revision.Graph), revision.ID); err != nil {
		t.Fatal(err)
	}
	return db, revision
}

func insertCompleteScenarioSourceRun(t *testing.T, db *Store, revision domain.ScenarioRevision, id, kind, status, mode string) {
	t.Helper()
	release, err := db.GetComponentRelease(context.Background(), revision.Graph.Nodes[0].ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	steps := []map[string]any{}
	for _, step := range []struct{ nodeID, actionID, action, stage string }{
		{"precheck:node", "check", "check", "change"},
		{"node", "release-install", "install", "change"},
		{"postcheck:node", "check", "check", "change"},
		{"target:node", "check", "check", "target_verify"},
	} {
		steps = append(steps, map[string]any{"nodeId": step.nodeID, "sourceNodeId": "node", "sourceType": "component_action", "componentId": release.ComponentID, "releaseId": release.ID, "releaseSpecDigest": domain.ComponentReleaseSpecDigest(release), "actionId": step.actionID, "action": step.action, "stage": step.stage})
	}
	steps = append(steps, map[string]any{"nodeId": "acceptance:business", "sourceType": "scenario_acceptance", "scenarioRevisionId": revision.ID, "acceptanceJobId": "business", "stage": "acceptance"})
	snapshot := map[string]any{"scenarioContractVersion": 2, "scenarioRevisionSpecDigest": domain.ScenarioRevisionSpecDigest(revision), "executionMode": mode, "acceptanceJobIds": []string{"business"}, "steps": steps}
	if _, err = db.DB().Exec(`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,input_snapshot_json,created_at) VALUES(?,?,?,'owner','env','env-rev',?,?,?)`, id, kind, status, revision.ID, jsonText(snapshot), timeText(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if _, err = db.DB().Exec(`INSERT INTO run_steps(id,run_id,node_id,name,status,exit_code) VALUES(?,?,?,?,'succeeded',0)`, id+":"+step["nodeId"].(string), id, step["nodeId"], step["nodeId"]); err != nil {
			t.Fatal(err)
		}
	}
}
func TestScenarioDefinitionCASAndActiveRunGuard(t *testing.T) {
	ctx := context.Background()
	db, _, revision := scenarioLifecycleFixture(t)
	digest := domain.ScenarioRevisionSpecDigest(revision)
	revision.AcceptanceJobs[0].Purpose = "business reaches its endpoint"
	if err := db.SaveScenarioRevisionDefinition(ctx, revision, digest); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetScenarioRevision(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.AcceptanceJobs[0].Purpose != revision.AcceptanceJobs[0].Purpose || saved.DigestVersion != 2 {
		t.Fatalf("lifecycle lost: %+v", saved)
	}
	if err = db.SaveScenarioRevisionDefinition(ctx, revision, digest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale save=%v", err)
	}
	for _, status := range []string{"awaiting_approval", "queued", "running"} {
		insertScenarioSourceRun(t, db, saved, status, "scenario_test", status, "install")
		if err = db.SaveScenarioRevisionDefinition(ctx, saved, domain.ScenarioRevisionSpecDigest(saved)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("%s edit=%v", status, err)
		}
		if _, err = db.db.Exec(`UPDATE runs SET status='failed' WHERE id=?`, status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.db.Exec(`UPDATE scenario_revisions SET status='released' WHERE id=?`, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveScenarioRevisionDefinition(ctx, saved, domain.ScenarioRevisionSpecDigest(saved)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("released edit=%v", err)
	}
}
func TestScenarioSourceRequiresSuccessfulFormalAcceptance(t *testing.T) {
	ctx := context.Background()
	db, revision := scenarioSourceEvidenceFixture(t)
	for _, fixture := range []struct{ id, kind, status, mode string }{{"test", "scenario_test", "succeeded", "install"}, {"failed", "scenario_run", "failed", "install"}, {"baseline", "scenario_run", "succeeded", "baseline_verify"}} {
		insertCompleteScenarioSourceRun(t, db, revision, fixture.id, fixture.kind, fixture.status, fixture.mode)
	}
	insertScenarioSourceRun(t, db, revision, "partial", "scenario_run", "succeeded", "install")
	if _, err := db.SuccessfulScenarioSourceRun(ctx, revision, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ineligible source accepted: %v", err)
	}
	insertCompleteScenarioSourceRun(t, db, revision, "formal", "scenario_run", "succeeded", "upgrade")
	run, err := db.SuccessfulScenarioSourceRun(ctx, revision, "")
	if err != nil || run.ID != "formal" {
		t.Fatalf("formal run=%+v err=%v", run, err)
	}
	next := revision
	next.ID = "next"
	next.Revision = 2
	next.Status = domain.RevisionDraft
	next.SourceRevisionID = revision.ID
	next.SourceRunID = run.ID
	if err = db.CreateScenarioRevisionFromSource(ctx, revision.ID, next); err != nil {
		t.Fatal(err)
	}
	duplicate := next
	duplicate.ID = "duplicate"
	duplicate.Revision = 3
	if err = db.CreateScenarioRevisionFromSource(ctx, revision.ID, duplicate); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("multiple active drafts: %v", err)
	}
	preserved, _ := db.GetScenarioRevision(ctx, revision.ID)
	if preserved.Status != domain.RevisionReleased {
		t.Fatalf("immutable source changed: %s", preserved.Status)
	}
}

func TestScenarioSourceRejectsIncompleteOrStaleFormalEvidence(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"missing whole target verification", `UPDATE runs SET input_snapshot_json=json_remove(input_snapshot_json,'$.steps[3]') WHERE id='formal'`},
		{"failed component step", `UPDATE run_steps SET status='failed',exit_code=1 WHERE node_id='node'`},
		{"missing acceptance result", `DELETE FROM run_steps WHERE node_id='acceptance:business'`},
		{"missing contract version", `UPDATE runs SET input_snapshot_json=json_remove(input_snapshot_json,'$.scenarioContractVersion') WHERE id='formal'`},
		{"missing release digest", `UPDATE runs SET input_snapshot_json=json_remove(input_snapshot_json,'$.steps[0].releaseSpecDigest') WHERE id='formal'`},
		{"stale release contract", `UPDATE action_definitions SET timeout_seconds=timeout_seconds+1 WHERE id='check'`},
		{"wrong target component", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.steps[3].componentId','unrelated') WHERE id='formal'`},
		{"wrong target node", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.steps[3].sourceNodeId','unrelated') WHERE id='formal'`},
		{"graph release uncovered", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.steps[0].releaseId','missing','$.steps[1].releaseId','missing','$.steps[2].releaseId','missing','$.steps[3].releaseId','missing') WHERE id='formal'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, revision := scenarioSourceEvidenceFixture(t)
			insertCompleteScenarioSourceRun(t, db, revision, "formal", "scenario_run", "succeeded", "install")
			if _, err := db.SuccessfulScenarioSourceRun(context.Background(), revision, "formal"); err != nil {
				t.Fatalf("complete formal evidence rejected: %v", err)
			}
			if _, err := db.DB().Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SuccessfulScenarioSourceRun(context.Background(), revision, "formal"); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("incomplete or stale source accepted: %v", err)
			}
		})
	}
}
func TestScenarioBranchPublishedSourceAndProvenance(t *testing.T) {
	ctx := context.Background()
	db, source, revision := scenarioLifecycleFixture(t)
	revision.Status = domain.RevisionReleased
	if _, err := db.db.Exec(`UPDATE scenario_revisions SET status='released' WHERE id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	branch := source
	branch.ID = "fork"
	branch.Slug = "fork"
	branch.ForkedFromScenarioID = source.ID
	branch.ForkedFromRevisionID = revision.ID
	branch.ForkedFromDigest = domain.ScenarioRevisionSpecDigest(revision)
	draft := revision
	draft.ID = "fork-r1"
	draft.ScenarioID = branch.ID
	draft.Status = domain.RevisionDraft
	if err := db.CreateScenario(ctx, branch, draft); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetScenario(ctx, branch.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ForkedFromRevisionID != revision.ID || got.ForkedFromDigest != branch.ForkedFromDigest || got.Revisions[0].SourceRevisionID != "" {
		t.Fatalf("provenance=%+v", got)
	}
	if _, err = db.db.Exec(`UPDATE scenario_revisions SET status='deprecated' WHERE id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	branch.ID = "stale"
	branch.Slug = "stale"
	draft.ID = "stale-r1"
	draft.ScenarioID = branch.ID
	if err = db.CreateScenario(ctx, branch, draft); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("deprecated branch source=%v", err)
	}
}
func TestScenarioInstallationCASPreservesTestIdentity(t *testing.T) {
	ctx := context.Background()
	db, scenario, revision := scenarioLifecycleFixture(t)
	initial := domain.ScenarioInstallation{EnvironmentID: "env", ScenarioID: scenario.ID, RevisionID: revision.ID, RunID: "test-run", State: "test", TestOnly: true, InstallationDigest: "installed", UpdatedAt: time.Now().UTC()}
	if err := db.SaveScenarioInstallation(ctx, initial, 0); err != nil {
		t.Fatal(err)
	}
	saved, err := db.GetScenarioInstallation(ctx, "env", scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved.State = "partial"
	saved.MutatingRunID = "upgrade"
	if err = db.SaveScenarioInstallation(ctx, saved, saved.Generation); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveScenarioInstallation(ctx, saved, saved.Generation); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale baseline=%v", err)
	}
	got, err := db.GetScenarioInstallation(ctx, "env", scenario.ID)
	if err != nil || !got.TestOnly || got.RevisionID != revision.ID || got.Generation != 2 {
		t.Fatalf("partial=%+v err=%v", got, err)
	}
}

func TestResetClearsScenarioForkBaselineAndSubmissionInForeignKeyOrder(t *testing.T) {
	ctx := context.Background()
	db, source, revision := scenarioLifecycleFixture(t)
	if _, err := db.DB().Exec(`UPDATE scenario_revisions SET status='released' WHERE id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	revision.Status = domain.RevisionReleased
	fork := source
	fork.ID = "fork"
	fork.Slug = "fork"
	fork.ForkedFromScenarioID = source.ID
	fork.ForkedFromRevisionID = revision.ID
	fork.ForkedFromDigest = domain.ScenarioRevisionSpecDigest(revision)
	draft := revision
	draft.ID = "fork-r1"
	draft.ScenarioID = fork.ID
	draft.Status = domain.RevisionDraft
	if err := db.CreateScenario(ctx, fork, draft); err != nil {
		t.Fatal(err)
	}
	insertScenarioSourceRun(t, db, revision, "failed", "scenario_run", "failed", "upgrade")
	if err := db.SaveScenarioInstallation(ctx, domain.ScenarioInstallation{EnvironmentID: "env", ScenarioID: source.ID, RevisionID: revision.ID, RunID: "failed", MutatingRunID: "failed", State: "partial", UpdatedAt: time.Now().UTC()}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(`INSERT INTO scenario_execution_submissions(user_id,key,request_digest,run_id) VALUES('owner','key','digest','failed')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"scenario_installations", "scenario_execution_submissions", "scenarios", "scenario_revisions", "runs"} {
		var count int
		if err := db.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("Reset retained %s count=%d err=%v", table, count, err)
		}
	}
	rows, err := db.DB().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("Reset left dangling references")
	}
}
