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
func InsertRunRecord(ctx context.Context, db *sql.DB, run domain.Run) error {
	snapshot, err := json.Marshal(run.InputSnapshot)
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
	_, err = db.ExecContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,destructive,input_snapshot_json,artifact_digest,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.Kind, run.Status, run.RequestedBy, run.EnvironmentID, run.EnvironmentRevisionID, nullText(run.ComponentReleaseID), nullText(run.ScenarioRevisionID), run.Action, run.Destructive, string(snapshot), run.ArtifactDigest, run.CreatedAt.UTC().Format(time.RFC3339Nano), nullTime(run.StartedAt), nullTime(run.FinishedAt))
	return err
}
