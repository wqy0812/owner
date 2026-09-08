package store

import (
	"codex/platform-demo/internal/testutil/runfixture"
	"context"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func componentInstallationFixture(t *testing.T, testOnly bool) (*Store, domain.ScenarioRevision) {
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
	snapshot := map[string]any{"scenarioContractVersion": 2, "executionMode": "install", "scenarioRevisionSpecDigest": domain.ScenarioRevisionSpecDigest(revision), "steps": []any{map[string]any{"nodeId": "node", "sourceNodeId": "node", "componentId": "component", "releaseId": "release", "action": "install", "variables": map[string]any{"port": 8080}, "backupRef": "backup-identity", "backup": backup}}}
	if _, err := db.DB().Exec(`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,execution_snapshot_json,created_at) VALUES('historical',?,'succeeded','owner','env','env-rev',?,?,'2026-09-05')`, kind, revision.ID, jsonText(runfixture.Snapshot(snapshot))); err != nil {
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
