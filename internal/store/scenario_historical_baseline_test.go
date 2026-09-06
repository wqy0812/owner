package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func historicalBaselineFixture(t *testing.T, testOnly bool) (*Store, domain.ScenarioRevision) {
	t.Helper()
	ctx := context.Background()
	db, _, revision := scenarioLifecycleFixture(t)
	now := time.Now().UTC()
	component := domain.Component{ID: "component", Slug: "component", Name: "Component", OwnerID: "owner", Layer: domain.LayerRuntimeState, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateComponent(ctx, component); err != nil {
		t.Fatal(err)
	}
	release := domain.ComponentRelease{ID: "release", ComponentID: component.ID, Version: "1.0.0", LineName: "Main", Status: domain.ReleaseDraft, Compatibility: domain.CompatibilityNotApplicable, RiskLevel: domain.RiskLow, CreatedAt: now}
	if err := db.CreateComponentRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	revision.DigestVersion = 0
	revision.AcceptanceJobs = nil
	revision.Graph = domain.ScenarioGraph{Nodes: []domain.ScenarioNode{{ID: "node", ReleaseID: "release", Action: domain.ActionInstall}}, Edges: []domain.ScenarioEdge{}}
	if _, err := db.DB().Exec(`UPDATE scenario_revisions SET graph_json=?,lifecycle_json='{}' WHERE id=?`, jsonText(revision.Graph), revision.ID); err != nil {
		t.Fatal(err)
	}
	kind := domain.RunScenario
	if testOnly {
		kind = domain.RunScenarioTest
	}
	backup := domain.BackupMetadata{NodeID: "node", EnvironmentID: "env", ComponentID: "component", ReleaseID: "release", ActionID: "install", InstallRunID: "historical", CapturedAt: now, PlaybookSHA256: "locked-sha"}
	snapshot := map[string]any{"scenarioRevisionSpecDigest": domain.ScenarioRevisionSpecDigest(revision), "steps": []any{map[string]any{"nodeId": "node", "sourceNodeId": "node", "componentId": "component", "releaseId": "release", "action": "install", "variables": map[string]any{"port": 8080}, "backupRef": "backup-identity", "backup": backup}}}
	if _, err := db.DB().Exec(`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,input_snapshot_json,created_at) VALUES('historical',?,'succeeded','owner','env','env-rev',?,?,'2026-09-05')`, kind, revision.ID, jsonText(snapshot)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(`INSERT INTO run_steps(id,run_id,node_id,name,status,exit_code) VALUES('historical-step','historical','node','Install','succeeded',0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertEnvironmentComponentInstallation(ctx, domain.EnvironmentComponentInstallation{NodeID: "node", EnvironmentID: "env", ComponentID: "component", ReleaseID: "release", InstallRunID: "historical", BackupRef: "backup-identity", Backup: backup, TestOnly: testOnly, InstalledAt: now}); err != nil {
		t.Fatal(err)
	}
	return db, revision
}
func TestHistoricalScenarioBaselineCandidateIsReadOnlyAndRetainsTestIdentity(t *testing.T) {
	for _, testOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "formal", true: "test"}[testOnly], func(t *testing.T) {
			ctx := context.Background()
			db, revision := historicalBaselineFixture(t, testOnly)
			candidate, err := db.FindScenarioHistoricalBaseline(ctx, "env", revision.ScenarioID, revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			if candidate.Run.ID != "historical" || candidate.TestOnly != testOnly || len(candidate.Nodes) != 1 || candidate.Nodes[0].Variables["port"] != float64(8080) || candidate.InstallationDigest == "" {
				t.Fatalf("candidate=%+v", candidate)
			}
			if _, err = db.GetScenarioInstallation(ctx, "env", revision.ScenarioID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("discovery wrote baseline: %v", err)
			}
			persisted, err := db.GetRun(ctx, "historical")
			if err != nil || persisted.InputSnapshot["acceptanceJobIds"] != nil {
				t.Fatal("historical acceptance evidence was invented")
			}
			if _, err = db.DB().Exec(`UPDATE environment_component_installations SET test_only=?`, !testOnly); err != nil {
				t.Fatal(err)
			}
			if _, err = db.FindScenarioHistoricalBaseline(ctx, "env", revision.ScenarioID, revision.ID); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("test identity mismatch accepted: %v", err)
			}
		})
	}
}
func TestHistoricalScenarioBaselineRejectsIdentityAndEvidenceDrift(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"missing node identity", `UPDATE environment_component_installations SET source_node_id=''`},
		{"backup replaced", `UPDATE environment_component_installations SET backup_ref='other'`},
		{"metadata changed", `UPDATE environment_component_installations SET backup_metadata_json=json_set(backup_metadata_json,'$.playbookSha256','other')`},
		{"missing frozen values", `UPDATE runs SET input_snapshot_json=json_remove(input_snapshot_json,'$.steps[0].variables') WHERE id='historical'`},
		{"revision changed", `UPDATE scenario_revisions SET graph_json=json_set(graph_json,'$.nodes[0].name','changed')`},
		{"step failed", `UPDATE run_steps SET status='failed',exit_code=1`},
		{"missing installation", `DELETE FROM environment_component_installations`},
		{"baseline verification is not install", `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.executionMode','baseline_verify') WHERE id='historical'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, revision := historicalBaselineFixture(t, false)
			if _, err := db.DB().Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := db.FindScenarioHistoricalBaseline(context.Background(), "env", revision.ScenarioID, revision.ID); !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("unsafe candidate accepted: %v", err)
			}
		})
	}
}
