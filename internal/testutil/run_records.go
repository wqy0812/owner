package testutil

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"codex/platform-demo/internal/domain"
)

// InsertRunRecord arranges stored records for read, deletion and recovery tests.
// Execution tests must use the service submission protocol instead.
func InsertRunRecord(ctx context.Context, db *sql.DB, run domain.Run, approvals ...*domain.Approval) error {
	snapshot, err := json.Marshal(run.Snapshot)
	if err != nil {
		return err
	}
	nullText := func(value string) any {
		if value == "" {
			return nil
		}
		return value
	}
	nullTime := func(value *time.Time) any {
		if value == nil {
			return nil
		}
		return value.UTC().Format(time.RFC3339Nano)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	results, err := json.Marshal(run.DeliveryResults)
	if err != nil {
		return err
	}
	if run.DeliveryResults == nil {
		results = []byte("[]")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,destructive,execution_snapshot_json,delivery_results_json,artifact_digest,retry_of_run_id,retry_root_run_id,retry_attempt,retry_start_step,error_text,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.Kind, run.Status, run.RequestedBy, run.EnvironmentID, run.EnvironmentRevisionID, nullText(run.ComponentReleaseID), nullText(run.ScenarioRevisionID), run.Action, run.Destructive, string(snapshot), string(results), run.ArtifactDigest, nullText(run.RetryOfRunID), nullText(run.RetryRootRunID), run.RetryAttempt, run.RetryStartStep, run.Error, run.CreatedAt.UTC().Format(time.RFC3339Nano), nullTime(run.StartedAt), nullTime(run.FinishedAt))
	if err != nil {
		return err
	}
	for _, id := range run.Snapshot.ReferencedRunIDs() {
		if id == run.ID {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO run_snapshot_references(run_id,referenced_run_id) VALUES(?,?)`, run.ID, id); err != nil {
			return err
		}
	}
	for _, a := range approvals {
		if a == nil {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO approvals(id,run_id,status,requested_at,decided_by,decision,reason,decided_at) VALUES(?,?,?,?,?,?,?,?)`, a.ID, run.ID, a.Status, a.RequestedAt.UTC().Format(time.RFC3339Nano), nullText(a.DecidedBy), a.Decision, a.Reason, nullTime(a.DecidedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
