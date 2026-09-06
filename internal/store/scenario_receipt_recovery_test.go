package store

import (
	"context"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
)

func TestScenarioReceiptRecoveryRequiresCurrentExactVerifiedBaseline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Store, map[string]any, *ActionExecutionReceipt, time.Time)
		want   bool
	}{
		{name: "restored formal baseline", want: true},
		{name: "mutating acceptance generation", want: true, mutate: func(db *Store, _ map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			_, _ = db.db.Exec(`UPDATE scenario_installations SET generation=3`)
		}},
		{name: "historical baseline", want: true, mutate: func(_ *Store, s map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			s["historicalBaselineRunId"] = "baseline"
			s["baselineRunId"] = ""
			s["historicalBaselineTestOnly"] = false
		}},
		{name: "test remains test", want: true, mutate: func(db *Store, s map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			s["baselineTestOnly"] = true
			_, _ = db.db.Exec(`UPDATE scenario_installations SET state='test',test_only=1`)
		}},
		{name: "no explicit coverage", mutate: func(_ *Store, s map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			delete(s, "recoveredReceipts")
		}},
		{name: "coverage other operation", mutate: func(_ *Store, s map[string]any, r *ActionExecutionReceipt, _ time.Time) {
			other := *r
			other.StepID = "another-step"
			s["recoveredReceipts"] = []ActionExecutionReceipt{other}
		}},
		{name: "verification predates operation", mutate: func(db *Store, _ map[string]any, r *ActionExecutionReceipt, _ time.Time) {
			_, _ = db.db.Exec(`UPDATE runs SET finished_at=? WHERE id='verify'`, timeText(r.UpdatedAt.Add(-time.Second)))
		}},
		{name: "failed verification", mutate: func(db *Store, _ map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			_, _ = db.db.Exec(`UPDATE runs SET status='failed' WHERE id='verify'`)
		}},
		{name: "ordinary install is not recovery", mutate: func(_ *Store, s map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			s["executionMode"] = "install"
		}},
		{name: "later baseline update", mutate: func(db *Store, _ map[string]any, _ *ActionExecutionReceipt, at time.Time) {
			_, _ = db.db.Exec(`UPDATE scenario_installations SET updated_at=?`, timeText(at.Add(time.Second)))
		}},
		{name: "partial state", mutate: func(db *Store, _ map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			_, _ = db.db.Exec(`UPDATE scenario_installations SET state='partial',mutating_run_id='failed'`)
		}},
		{name: "baseline identity changed", mutate: func(db *Store, _ map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			_, _ = db.db.Exec(`UPDATE scenario_installations SET run_id='other'`)
		}},
		{name: "actual installation drift", mutate: func(db *Store, _ map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			_, _ = db.db.Exec(`UPDATE environment_component_installations SET backup_ref='replaced'`)
		}},
		{name: "later operation", mutate: func(db *Store, _ map[string]any, r *ActionExecutionReceipt, at time.Time) {
			later := *r
			later.StepID = "later"
			later.UpdatedAt = at.Add(time.Second)
			if err := db.RecordActionExecution(context.Background(), later); err != nil {
				panic(err)
			}
		}},
		{name: "receipt bytes changed", mutate: func(db *Store, _ map[string]any, _ *ActionExecutionReceipt, _ time.Time) {
			_, _ = db.db.Exec(`UPDATE action_execution_receipts SET backup_ref='other-backup'`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, scenario, revision := scenarioLifecycleFixture(t)
			for _, query := range []string{`INSERT INTO components(id,slug,name,owner_id,created_at,updated_at) VALUES('component','component','Component','owner','2026-01-01','2026-01-01')`, `INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES('line','component','Line','2026-01-01')`, `INSERT INTO component_releases(id,component_id,line_id,version,status,compatibility,created_at) VALUES('release','component','line','1.0','released','not_applicable','2026-01-01')`} {
				if _, err := db.db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			for _, run := range []struct{ id, status, mode string }{{"baseline", "succeeded", "install"}, {"failed", "failed", "upgrade"}, {"verify", "succeeded", "baseline_verify"}} {
				insertScenarioSourceRun(t, db, revision, run.id, "scenario_run", run.status, run.mode)
			}
			at := time.Now().UTC()
			receipt := ActionExecutionReceipt{RunID: "failed", StepID: "change", EnvironmentID: "env", ComponentID: "component", ReleaseID: "release", ActionID: "upgrade", SourceNodeID: "runtime", Status: "main_succeeded", BackupRef: "failed-backup", StartedAt: at.Add(-2 * time.Minute), UpdatedAt: at.Add(-time.Minute), Backup: domain.BackupMetadata{InstallRunID: "failed", NodeID: "runtime", CapturedAt: at.Add(-2 * time.Minute)}}
			if err := db.RecordActionExecution(ctx, receipt); err != nil {
				t.Fatal(err)
			}
			installation := domain.EnvironmentComponentInstallation{NodeID: "runtime", EnvironmentID: "env", ComponentID: "component", ReleaseID: "release", InstallRunID: "baseline", BackupRef: "baseline-backup", InstalledAt: at.Add(-time.Hour)}
			if err := db.UpsertEnvironmentComponentInstallation(ctx, installation); err != nil {
				t.Fatal(err)
			}
			digest := ScenarioComponentInstallationDigest([]domain.EnvironmentComponentInstallation{installation})
			baseline := domain.ScenarioInstallation{EnvironmentID: "env", ScenarioID: scenario.ID, RevisionID: revision.ID, RunID: "baseline", State: "complete", InstallationDigest: digest, UpdatedAt: at}
			if err := db.SaveScenarioInstallation(ctx, baseline, 0); err != nil {
				t.Fatal(err)
			}
			snapshot := map[string]any{"scenarioContractVersion": 2, "executionMode": "baseline_verify", "scenarioId": scenario.ID, "baselineRunId": "baseline", "baselineInstallationDigest": digest, "baselineTestOnly": false, "recoveredReceipts": []ActionExecutionReceipt{receipt}}
			if _, err := db.db.Exec(`UPDATE runs SET finished_at=? WHERE id='verify'`, timeText(at)); err != nil {
				t.Fatal(err)
			}
			if tc.mutate != nil {
				tc.mutate(db, snapshot, &receipt, at)
			}
			if _, err := db.db.Exec(`UPDATE runs SET input_snapshot_json=? WHERE id='verify'`, jsonText(snapshot)); err != nil {
				t.Fatal(err)
			}
			recovered, err := db.IsActionReceiptRecovered(ctx, receipt)
			if err != nil || recovered != tc.want {
				t.Fatalf("recovered=%v want=%v err=%v", recovered, tc.want, err)
			}
			failed, err := db.GetRun(ctx, "failed")
			if err != nil || failed.Status != domain.RunFailed {
				t.Fatalf("recovery changed failed history: %+v %v", failed, err)
			}
			var status string
			if err := db.db.QueryRow(`SELECT status FROM action_execution_receipts WHERE run_id='failed' AND step_id='change'`).Scan(&status); err != nil || status != "main_succeeded" {
				t.Fatalf("recovery forged post-check: %s %v", status, err)
			}
		})
	}
}
