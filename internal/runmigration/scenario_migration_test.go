package runmigration

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

// The migration must retain recovery state independently of how current business
// constructors would build a new plan. These are terminal source-contract rows.
func TestOfflineMigrationPreservesScenarioRecoveryAndAssociatedRollback(t *testing.T) {
	o, db := sourceFixture(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO components(id,slug,name,owner_id,created_at,updated_at) VALUES('component','component','Component','owner','2026-09-01','2026-09-01')`)
	exec(`INSERT INTO component_release_lines(id,component_id,name,created_at) VALUES('line','component','main','2026-09-01')`)
	exec(`INSERT INTO component_releases(id,component_id,line_id,version,status,compatibility,created_at) VALUES('release','component','line','1.0','released','compatible','2026-09-01')`)
	exec(`INSERT INTO scenarios(id,slug,name,owner_id,created_at,updated_at) VALUES('scenario','scenario','Scenario','owner','2026-09-01','2026-09-01')`)
	for i, id := range []string{"scenario-rev-1", "scenario-rev-2"} {
		exec(`INSERT INTO scenario_revisions(id,scenario_id,revision,status,created_at) VALUES(?,'scenario',?,'released','2026-09-01')`, id, i+1)
	}
	at := time.Date(2026, 9, 1, 2, 3, 4, 0, time.UTC)
	backup := domain.BackupMetadata{EnvironmentID: "env", ComponentID: "component", ReleaseID: "release", NodeID: "node", ActionID: "install", InstallRunID: "s-upgrade", CapturedAt: at, PlaybookSHA256: "locked-sha", DependencySnapshot: map[string]any{"runId": "user-value"}, Previous: &domain.EnvironmentComponentInstallation{NodeID: "node", EnvironmentID: "env", ComponentID: "component", ReleaseID: "release", InstallRunID: "run-0", BackupRef: "older-backup", Backup: domain.BackupMetadata{InstallRunID: "run-0"}, InstalledAt: at}}
	receipt := domain.ActionExecutionReceipt{RunID: "s-upgrade", StepID: "step", EnvironmentID: "env", ComponentID: "component", ReleaseID: "release", ActionID: "install", SourceNodeID: "node", Status: "main_succeeded", BackupRef: "backup", Backup: backup, StartedAt: at, UpdatedAt: at}
	plan := domain.RunExecutionPlan{Runtime: domain.RunRuntime{AnsibleCore: "2.8.8", Python: "3.6.9"}, TreeDigest: "original-job-tree", Steps: []domain.RunPlanStep{{ID: "step", NodeID: "node", ComponentID: "component", ReleaseID: "release", ReleaseSpecDigest: "original-release-digest", Variables: map[string]any{"large": json.Number("9007199254740993"), "false": false}}}, ParentSteps: []domain.RunPlanStep{{ID: "parent", NodeID: "node", ComponentID: "component", ReleaseID: "release", ReleaseSpecDigest: "original-release-digest", Backup: &backup}}}
	rawPlan, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := map[string]string{}
	for _, c := range []struct {
		id   string
		mode domain.ScenarioExecutionMode
		kind domain.RunKind
	}{
		{"s-install", domain.ScenarioExecutionInstall, domain.RunScenarioTest},
		{"s-upgrade", domain.ScenarioExecutionUpgrade, domain.RunScenario},
		{"s-verify", domain.ScenarioExecutionBaselineVerify, domain.RunScenario},
		{"s-retry", domain.ScenarioExecutionBaselineVerify, domain.RunScenario},
	} {
		var values map[string]any
		decoder := json.NewDecoder(bytes.NewReader(rawPlan))
		decoder.UseNumber()
		if err = decoder.Decode(&values); err != nil {
			t.Fatal(err)
		}
		values["scenarioContractVersion"] = 2
		values["executionMode"] = c.mode
		values["scenarioId"] = "scenario"
		values["scenarioRevisionSpecDigest"] = "original-scenario-digest"
		values["environmentRevisionId"] = "revision"
		values["targetNodes"] = []domain.ScenarioTargetNode{{NodeID: "node", ComponentID: "component", ReleaseID: "release", Variables: map[string]any{"runId": "user-value"}}}
		values["sourceRevisionId"] = "scenario-rev-1"
		values["baselineRunId"] = "run-0"
		values["baselineGeneration"] = 7
		values["baselineInstallationDigest"] = "generation-seven-digest"
		values["baselineTestOnly"] = false
		values["acceptanceJobIds"] = []string{"acceptance"}
		values["submissionKey"] = c.id + "-key"
		values["submissionDigest"] = "original-request-digest"
		values["planDigest"] = "original-preview-digest"
		if c.mode == domain.ScenarioExecutionBaselineVerify {
			values["recoveredReceipts"] = []domain.ActionExecutionReceipt{receipt}
		}
		if c.id == "s-retry" {
			values["retryRecoveryStateDigest"] = "original-retry-recovery"
			values["retryBaselineGeneration"] = 9
			values["retryBaselineMutatingRunId"] = "s-upgrade"
		}
		raw, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		snapshots[c.id] = string(raw)
		var retryOf, retryRoot any
		attempt := 0
		if c.id == "s-retry" {
			retryOf = "s-verify"
			retryRoot = "s-verify"
			attempt = 1
		}
		exec(`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,input_snapshot_json,artifact_digest,retry_of_run_id,retry_root_run_id,retry_attempt,created_at,finished_at) VALUES(?,?,'succeeded','owner','env','revision','scenario-rev-2',?,'original-job-tree',?,?,?,'2026-09-01','2026-09-02')`, c.id, c.kind, string(raw), retryOf, retryRoot, attempt)
		exec(`INSERT INTO scenario_execution_submissions(user_id,key,request_digest,run_id) VALUES('owner',?,'original-request-digest',?)`, c.id+"-key", c.id)
	}
	// A rollback carries a related revision and recovery inputs, but no scenario
	// execution mode or baseline. It must remain an environment rollback.
	recovery := plan
	recovery.InstallationBaseline = []domain.RunInstallationBaseline{{NodeID: "node", ComponentID: "component", ReleaseID: "release", InstallRunID: "s-upgrade", BackupRef: "backup", PlaybookSHA256: "locked-sha"}}
	recovery.InstallationBaselineDigest = "original-recovery-baseline"
	recovery.ResetTargets = []domain.RunInventoryHost{{Name: "host", Address: "192.0.2.1", Groups: []string{"all"}}}
	recovery.ResetBoundaryDigest = "original-boundary"
	recovery.RecoveryEnvironmentDigest = "original-environment"
	raw, err := json.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	var rollback map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err = dec.Decode(&rollback); err != nil {
		t.Fatal(err)
	}
	rollback["scenarioRevisionSpecDigest"] = "original-scenario-digest"
	raw, err = json.Marshal(rollback)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,scenario_revision_id,input_snapshot_json,artifact_digest,created_at,finished_at) VALUES('rollback','environment_rollback','succeeded','owner','env','revision','scenario-rev-2',?,'original-job-tree','2026-09-01','2026-09-02')`, string(raw))
	backupRaw, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO environment_component_installations(environment_id,component_id,source_node_id,release_id,install_run_id,backup_ref,backup_metadata_json,installed_at) VALUES('env','component','node','release','s-upgrade','backup',?,'2026-09-01')`, string(backupRaw))
	exec(`INSERT INTO action_execution_receipts(run_id,step_id,environment_id,component_id,release_id,action_id,source_node_id,status,backup_ref,backup_json,started_at,updated_at) VALUES('s-upgrade','step','env','component','release','install','node','main_succeeded','backup',?,'2026-09-01','2026-09-01')`, string(backupRaw))
	exec(`INSERT INTO scenario_installations(environment_id,scenario_id,revision_id,run_id,mutating_run_id,state,test_only,installation_digest,generation,updated_at) VALUES('env','scenario','scenario-rev-2','s-verify','s-upgrade','complete',0,'generation-nine-digest',9,'2026-09-02')`)
	db.Close()
	o.ExpectedSourceDigest, err = SourceDigest(o.Source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Migrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	target, err := store.Open(context.Background(), o.Target)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	for id, original := range snapshots {
		r, err := target.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if err = domain.ValidateRunSnapshot(r); err != nil {
			t.Fatal(err)
		}
		if r.Snapshot.ScenarioExecution.Baseline.Generation != 7 || r.Snapshot.Submission.PlanDigest != "original-preview-digest" || r.Snapshot.Subject.ScenarioRevisionSpecDigest != "original-scenario-digest" {
			t.Fatalf("scenario identity changed: %+v", r)
		}
		if got := r.Snapshot.Plan.Steps[0].Variables["large"]; got != json.Number("9007199254740993") {
			t.Fatal("numeric parameter changed", got)
		}
		if r.Snapshot.ScenarioExecution.Mode == domain.ScenarioExecutionBaselineVerify && !reflect.DeepEqual(r.Snapshot.ScenarioExecution.RecoveredReceipts, []domain.ActionExecutionReceipt{receipt}) {
			t.Fatal("recovery receipt changed", id)
		}
		var originalFields map[string]json.RawMessage
		if err = json.Unmarshal([]byte(original), &originalFields); err != nil {
			t.Fatal(err)
		}
		projected, err := json.Marshal(r.Snapshot.ExecutionPlan(r.DeliveryResults))
		if err != nil {
			t.Fatal(err)
		}
		var projectedFields map[string]json.RawMessage
		if err = json.Unmarshal(projected, &projectedFields); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"runtime", "steps", "parentSteps", "treeDigest"} {
			var oldValue, newValue any
			// Compare structural JSON; precise large-number preservation is checked above.
			if err = json.Unmarshal(originalFields[field], &oldValue); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(projectedFields[field], &newValue); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldValue, newValue) {
				t.Fatal("native projection changed", id, field)
			}
		}
	}
	baseline, err := target.GetScenarioInstallation(context.Background(), "env", "scenario")
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Generation != 9 || baseline.RunID != "s-verify" || baseline.MutatingRunID != "s-upgrade" || baseline.InstallationDigest != "generation-nine-digest" {
		t.Fatal("baseline changed", baseline)
	}
	var storedBackup, storedReceipt string
	if err = target.DB().QueryRow(`SELECT backup_metadata_json FROM environment_component_installations`).Scan(&storedBackup); err != nil {
		t.Fatal(err)
	}
	if err = target.DB().QueryRow(`SELECT backup_json FROM action_execution_receipts`).Scan(&storedReceipt); err != nil {
		t.Fatal(err)
	}
	if storedBackup != string(backupRaw) || storedReceipt != string(backupRaw) {
		t.Fatal("installation or receipt bytes changed")
	}
	retry, err := target.GetRun(context.Background(), "s-retry")
	if err != nil {
		t.Fatal(err)
	}
	if retry.RetryOfRunID != "s-verify" || retry.Snapshot.Retry.BaselineGeneration != 9 || retry.Snapshot.Retry.BaselineMutatingRunID != "s-upgrade" {
		t.Fatal("retry identity changed")
	}
	r, err := target.GetRun(context.Background(), "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != domain.RunEnvironmentRollback || !reflect.DeepEqual(r.Snapshot.ScenarioExecution, domain.ScenarioRunExecution{}) || r.Snapshot.Recovery.InstallationBaselineDigest != "original-recovery-baseline" || r.Snapshot.Recovery.ResetBoundaryDigest != "original-boundary" {
		t.Fatal("rollback reinterpreted", r)
	}
	var refs int
	if err = target.DB().QueryRow(`SELECT COUNT(*) FROM run_snapshot_references WHERE run_id='s-retry' AND referenced_run_id IN ('s-upgrade','run-0')`).Scan(&refs); err != nil || refs != 2 {
		t.Fatal("recovery references missing", refs, err)
	}
}
