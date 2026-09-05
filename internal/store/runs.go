package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) UpdateRunDeliveryResults(ctx context.Context, runID string, results any) error {
	encoded, err := json.Marshal(results)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE runs SET input_snapshot_json=json_set(input_snapshot_json,'$.deliveryResults',json(?)) WHERE id=? AND status='running'`, string(encoded), runID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) CreateRun(ctx context.Context, r domain.Run, approval *domain.Approval) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r.Kind == domain.RunEnvironmentRollback {
		var active int
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM runs
  WHERE environment_id=? AND status IN ('awaiting_approval','queued','running')
)`, r.EnvironmentID).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			return fmt.Errorf("%w: environment has an active run", domain.ErrConflict)
		}
	}
	if err := validateNewRunReferences(ctx, tx, r); err != nil {
		return err
	}
	if err := validateRunAdaptationTx(ctx, tx, r); err != nil {
		return err
	}
	if err := validateRunReleaseLifecycle(ctx, tx, r); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,destructive,input_snapshot_json,artifact_digest,retry_of_run_id,retry_root_run_id,retry_attempt,retry_start_step,error_text,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Kind, r.Status, r.RequestedBy, r.EnvironmentID, r.EnvironmentRevisionID, nullString(r.ComponentReleaseID), nullString(r.ScenarioRevisionID), r.Action, r.Destructive, jsonText(r.InputSnapshot), r.ArtifactDigest, nullString(r.RetryOfRunID), nullString(r.RetryRootRunID), r.RetryAttempt, r.RetryStartStep, r.Error, timeText(r.CreatedAt), ptrTimeText(r.StartedAt), ptrTimeText(r.FinishedAt))
	if err != nil {
		return mapSQLError(err)
	}
	if approval != nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO approvals(id,run_id,status,requested_at,decided_by,decision,reason,decided_at) VALUES(?,?,?,?,?,?,?,?)`, approval.ID, r.ID, approval.Status, timeText(approval.RequestedAt), nullString(approval.DecidedBy), approval.Decision, approval.Reason, ptrTimeText(approval.DecidedAt))
		if err != nil {
			return mapSQLError(err)
		}
	}
	return tx.Commit()
}

func validateRunReleaseLifecycle(ctx context.Context, tx *sql.Tx, r domain.Run) error {
	if r.Status != domain.RunAwaitingApproval && r.Status != domain.RunQueued && r.Status != domain.RunRunning {
		return nil
	}
	if r.Kind == domain.RunComponentTest && r.ComponentReleaseID != "" {
		var withdrawn int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM component_releases WHERE id=? AND status='deprecated' AND released_at IS NULL)`, r.ComponentReleaseID).Scan(&withdrawn); err != nil {
			return err
		}
		if withdrawn != 0 {
			return fmt.Errorf("%w: component draft was deprecated before the run was created", domain.ErrConflict)
		}
	}
	if r.Kind != domain.RunScenarioTest && r.Kind != domain.RunScenario {
		return nil
	}
	var invalid int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1
  FROM json_each(?, '$.steps') AS step
  LEFT JOIN component_releases release
    ON release.id=json_extract(step.value, '$.releaseId')
  WHERE release.id IS NULL
     OR CASE
          WHEN ?='scenario_test' THEN NOT (
            (release.status='draft' AND release.candidate=1)
            OR (release.status IN ('released','deprecated') AND release.released_at IS NOT NULL)
          )
          ELSE NOT (
            release.status IN ('released','deprecated')
            AND release.released_at IS NOT NULL
          )
        END
)`, jsonText(r.InputSnapshot), r.Kind).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("%w: a locked component release was withdrawn before the run was created", domain.ErrConflict)
	}
	return nil
}

func scanRun(row scanner) (domain.Run, error) {
	var r domain.Run
	var component, scenario, retryOf, retryRoot sql.NullString
	var destructive int
	var snapshot, created string
	var started, finished sql.NullString
	err := row.Scan(&r.ID, &r.Kind, &r.Status, &r.RequestedBy, &r.EnvironmentID, &r.EnvironmentRevisionID, &component, &scenario, &r.Action, &destructive, &snapshot, &r.ArtifactDigest, &retryOf, &retryRoot, &r.RetryAttempt, &r.RetryStartStep, &r.Error, &created, &started, &finished)
	r.ComponentReleaseID = component.String
	r.ScenarioRevisionID = scenario.String
	r.RetryOfRunID = retryOf.String
	r.RetryRootRunID = retryRoot.String
	r.Destructive = destructive != 0
	r.InputSnapshot = decodeJSON(snapshot, map[string]any{})
	r.CreatedAt = parseTime(created)
	r.StartedAt = parseNullTime(started)
	r.FinishedAt = parseNullTime(finished)
	return r, err
}

const runSelect = `SELECT id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,destructive,input_snapshot_json,artifact_digest,retry_of_run_id,retry_root_run_id,retry_attempt,retry_start_step,error_text,created_at,started_at,finished_at FROM runs`

// CountScenarioRunsForComponentRelease counts scenario Runs whose immutable
// execution snapshot locked the exact component Release. The snapshot is the
// authority here: mutable scenario revisions may be edited after an earlier
// test, while the Run must continue to describe what was actually executed.
func (s *Store) CountScenarioRunsForComponentRelease(ctx context.Context, releaseID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(DISTINCT runs.id)
FROM retained_run_history runs
JOIN json_each(runs.input_snapshot_json, '$.steps') AS step
WHERE runs.scenario_revision_id IS NOT NULL
  AND runs.kind IN ('scenario_test','scenario_run')
  AND json_extract(step.value, '$.releaseId')=?`, releaseID).Scan(&count)
	return count, err
}

// ListRunsForComponentRelease returns every immutable Run that directly tested
// the Release or locked it as part of a Scenario execution. The execution
// snapshot is authoritative for Scenario Runs because the mutable Scenario
// graph may have changed since an older Run was created.
func (s *Store) ListRunsForComponentRelease(ctx context.Context, releaseID string) ([]domain.Run, error) {
	rows, err := s.db.QueryContext(ctx, runSelect+`
WHERE (runs.kind='component_test' AND runs.component_release_id=?)
   OR (runs.kind IN ('scenario_test','scenario_run') AND EXISTS (
     SELECT 1
     FROM json_each(runs.input_snapshot_json, '$.steps') AS locked_step
     WHERE json_extract(locked_step.value, '$.releaseId')=?
   ))
ORDER BY runs.created_at DESC`, releaseID, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Run
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) HasActiveRetry(ctx context.Context, retryRootRunID string) (bool, error) {
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE retry_root_run_id=? AND status IN ('awaiting_approval','queued','running'))`, retryRootRunID).Scan(&active)
	return active != 0, err
}

func (s *Store) NextRetryAttempt(ctx context.Context, retryRootRunID string) (int, error) {
	var attempt int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(retry_attempt),0)+1 FROM runs WHERE retry_root_run_id=?`, retryRootRunID).Scan(&attempt)
	return attempt, err
}

func (s *Store) GetRun(ctx context.Context, id string) (domain.Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, runSelect+` WHERE id=?`, id))
	if err != nil {
		return r, mapSQLError(err)
	}
	r.Steps, _ = s.ListRunSteps(ctx, id)
	if a, x := s.GetApprovalByRun(ctx, id); x == nil {
		r.Approval = &a
	}
	return r, nil
}

// CanViewRun answers the run authorization question without loading steps or
// approval details. Keep this query aligned with ListRuns so SSE filtering and
// the runs API enforce the same ownership rules.
func (s *Store) CanViewRun(ctx context.Context, viewer domain.User, runID string) (bool, error) {
	var visible int
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1 FROM retained_run_history r
  WHERE r.id=? AND (
	?='platform_admin'
	OR
    r.requested_by=?
    OR (?='environment_owner' AND EXISTS (
      SELECT 1 FROM environments e WHERE e.id=r.environment_id AND e.owner_id=?
    ))
    OR (?='component_owner' AND (
      EXISTS (
        SELECT 1 FROM component_releases cr JOIN components c ON c.id=cr.component_id
        WHERE cr.id=r.component_release_id AND c.owner_id=?
      )
      OR EXISTS (
        SELECT 1 FROM json_each(r.input_snapshot_json, '$.steps') locked_step
        JOIN component_releases cr ON cr.id=json_extract(locked_step.value, '$.releaseId')
        JOIN components c ON c.id=cr.component_id
        WHERE c.owner_id=?
      )
    ))
    OR (?='scenario_owner' AND EXISTS (
      SELECT 1 FROM scenario_revisions sr JOIN scenarios s ON s.id=sr.scenario_id
      WHERE sr.id=r.scenario_revision_id AND s.owner_id=?
    ))
  )
)`, runID, viewer.Role, viewer.ID,
		viewer.Role, viewer.ID,
		viewer.Role, viewer.ID, viewer.ID,
		viewer.Role, viewer.ID,
	).Scan(&visible)
	return visible != 0, err
}

func (s *Store) ListRuns(ctx context.Context, viewer domain.User) ([]domain.Run, error) {
	where, args := runVisibility(viewer)
	q := strings.Replace(runSelect, "FROM runs", "FROM retained_run_history runs", 1) + ` WHERE ` + where + ` ORDER BY created_at DESC,id DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueuedRunPosition returns the one-based FIFO position of a queued Run in its
// environment without exposing the runs table or timestamp encoding to API.
func (s *Store) QueuedRunPosition(ctx context.Context, environmentID string, createdAt time.Time) (int, error) {
	var position int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE environment_id=? AND status='queued' AND created_at<=?`, environmentID, timeText(createdAt)).Scan(&position)
	return position, err
}

func (s *Store) UpdateRunStatus(ctx context.Context, id string, from []domain.RunStatus, to domain.RunStatus, errText string, at time.Time) error {
	if len(from) == 0 {
		return domain.ErrInvalid
	}
	ph := ""
	started, finished := any(nil), any(nil)
	if to == domain.RunRunning {
		started = timeText(at)
	}
	switch to {
	case domain.RunSucceeded, domain.RunFailed, domain.RunCancelled, domain.RunRejected, domain.RunInterrupted:
		finished = timeText(at)
	}
	args := []any{to, errText, started, finished, id}
	for i, v := range from {
		if i > 0 {
			ph += ","
		}
		ph += "?"
		args = append(args, v)
	}
	q := fmt.Sprintf(`UPDATE runs SET status=?,error_text=?,started_at=COALESCE(?,started_at),finished_at=COALESCE(?,finished_at) WHERE id=? AND status IN (%s)`, ph)
	// Publish Run success and its scenario evidence in one write boundary.
	// Observers must never see success while the revision is still Testing.
	if to == domain.RunSucceeded {
		tx, err := s.beginCatalogWrite(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("%w: invalid run transition", domain.ErrConflict)
		}
		run, err := getRunRecord(ctx, tx, id)
		if err != nil {
			return err
		}
		if run.Kind == domain.RunScenarioTest {
			revision, err := getScenarioRevision(ctx, tx, run.ScenarioRevisionID)
			if err != nil {
				return err
			}
			if revision.Status == domain.RevisionTesting {
				if _, evidenceErr := scenarioTestEvidenceMatches(ctx, tx, run, revision); evidenceErr != nil {
					_, err = tx.ExecContext(ctx, `UPDATE scenario_revisions SET status='draft',test_passed_at=NULL WHERE id=?`, revision.ID)
				} else {
					_, err = tx.ExecContext(ctx, `UPDATE scenario_revisions SET status='test_passed',test_passed_at=? WHERE id=?`, timeText(at), revision.ID)
				}
				if err != nil {
					return err
				}
			}
		}
		return tx.Commit()
	}
	res, err := s.execWithBusyRetry(ctx, q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: invalid run transition", domain.ErrConflict)
	}
	return nil
}

func (s *Store) MarkRunningInterrupted(ctx context.Context, at time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// A scenario test owns the Testing state while it is queued/running. A
	// process restart makes that test inconclusive, so release the revision
	// back to Draft before marking the run Interrupted.
	if _, err := tx.ExecContext(ctx, `
UPDATE scenario_revisions
SET status='draft',test_passed_at=NULL
WHERE status='testing' AND id IN (
  SELECT scenario_revision_id FROM runs
  WHERE status='running' AND kind='scenario_test' AND scenario_revision_id IS NOT NULL
)`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE run_steps
SET status='interrupted',summary='server restarted while step was active',finished_at=?
WHERE status='running' AND run_id IN (SELECT id FROM runs WHERE status='running')`, timeText(at)); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE runs SET status='interrupted',error_text='server restarted while run was active',finished_at=? WHERE status='running'`, timeText(at))
	if err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

const invalidActiveRunPredicate = `
runs.status IN ('awaiting_approval','queued') AND (
  NOT EXISTS (SELECT 1 FROM users u WHERE u.id=runs.requested_by)
  OR NOT EXISTS (SELECT 1 FROM environments e WHERE e.id=runs.environment_id)
  OR NOT EXISTS (
    SELECT 1 FROM environment_revisions er
    WHERE er.id=runs.environment_revision_id AND er.environment_id=runs.environment_id
  )
  OR (runs.component_release_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM component_releases cr WHERE cr.id=runs.component_release_id
  ))
  OR (runs.scenario_revision_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM scenario_revisions sr WHERE sr.id=runs.scenario_revision_id
  ))
  OR json_type(runs.input_snapshot_json, '$.steps') IS NOT 'array'
  OR json_array_length(runs.input_snapshot_json, '$.steps')=0
  OR EXISTS (
    SELECT 1 FROM json_each(runs.input_snapshot_json, '$.steps') locked_step
    WHERE COALESCE(json_extract(locked_step.value, '$.releaseId'), '')=''
       OR NOT EXISTS (
         SELECT 1 FROM component_releases cr
         WHERE cr.id=json_extract(locked_step.value, '$.releaseId')
       )
  )
  OR (runs.status='awaiting_approval' AND NOT EXISTS (
    SELECT 1 FROM approvals a WHERE a.run_id=runs.id AND a.status='pending'
  ))
  OR (runs.status='queued' AND runs.destructive=1 AND NOT EXISTS (
    SELECT 1 FROM approvals a WHERE a.run_id=runs.id AND a.status='approved'
  ))
)`

// FailInvalidActiveRuns removes corrupt entries from the active environment
// queue without deleting their audit trail. It protects current first-version
// data from partial writes or operator tampering.
func (s *Store) FailInvalidActiveRuns(ctx context.Context, at time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
UPDATE scenario_revisions
SET status='draft',test_passed_at=NULL
WHERE status='testing' AND id IN (
  SELECT scenario_revision_id FROM runs
  WHERE scenario_revision_id IS NOT NULL AND `+invalidActiveRunPredicate+`
)`); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `
UPDATE runs
SET status='failed',
    error_text='invalid active run: referenced entity, approval, or locked plan is missing or inconsistent',
    finished_at=?
WHERE `+invalidActiveRunPredicate, timeText(at))
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) HasRunningRun(ctx context.Context, environmentID string) (bool, error) {
	var running int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE environment_id=? AND status='running')`, environmentID).Scan(&running)
	return running != 0, err
}

func (s *Store) HasActiveEnvironmentRun(ctx context.Context, environmentID string) (bool, error) {
	var active int
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM runs
  WHERE environment_id=? AND status IN ('awaiting_approval','queued','running')
)`, environmentID).Scan(&active)
	return active != 0, err
}

func (s *Store) ListQueuedEnvironmentIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT environment_id FROM runs WHERE status='queued' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) HasActiveComponentTest(ctx context.Context, releaseID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM runs
WHERE kind='component_test' AND component_release_id=?
  AND status IN ('awaiting_approval','queued','running')`, releaseID).Scan(&count)
	return count > 0, err
}

func (s *Store) ClaimNextRun(ctx context.Context, environmentID string, at time.Time) (domain.Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE environment_id=? AND status='running'`, environmentID).Scan(&active); err != nil {
		return domain.Run{}, err
	}
	if active > 0 {
		return domain.Run{}, domain.ErrConflict
	}
	r, err := scanRun(tx.QueryRowContext(ctx, runSelect+` WHERE environment_id=? AND status='queued' ORDER BY created_at LIMIT 1`, environmentID))
	if err != nil {
		return r, mapSQLError(err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE runs SET status='running',started_at=? WHERE id=? AND status='queued'`, timeText(at), r.ID)
	if err != nil {
		return r, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return r, domain.ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return r, err
	}
	r.Status = domain.RunRunning
	r.StartedAt = &at
	return r, nil
}

func (s *Store) CreateRunStep(ctx context.Context, st domain.RunStep) error {
	_, err := s.execWithBusyRetry(ctx, `INSERT INTO run_steps(id,run_id,node_id,name,status,exit_code,summary,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?)`, st.ID, st.RunID, st.NodeID, st.Name, st.Status, st.ExitCode, st.Summary, ptrTimeText(st.StartedAt), ptrTimeText(st.FinishedAt))
	return mapSQLError(err)
}
func (s *Store) UpdateRunStep(ctx context.Context, st domain.RunStep) error {
	res, err := s.execWithBusyRetry(ctx, `UPDATE run_steps SET status=?,exit_code=?,summary=?,started_at=?,finished_at=? WHERE id=?`, st.Status, st.ExitCode, st.Summary, ptrTimeText(st.StartedAt), ptrTimeText(st.FinishedAt), st.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
func (s *Store) ListRunSteps(ctx context.Context, runID string) ([]domain.RunStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,run_id,node_id,name,status,exit_code,summary,started_at,finished_at FROM run_steps WHERE run_id=? ORDER BY rowid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RunStep
	for rows.Next() {
		var st domain.RunStep
		var exit sql.NullInt64
		var started, finished sql.NullString
		if err := rows.Scan(&st.ID, &st.RunID, &st.NodeID, &st.Name, &st.Status, &exit, &st.Summary, &started, &finished); err != nil {
			return nil, err
		}
		if exit.Valid {
			x := int(exit.Int64)
			st.ExitCode = &x
		}
		st.StartedAt = parseNullTime(started)
		st.FinishedAt = parseNullTime(finished)
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) AppendRunLog(ctx context.Context, l domain.RunLog) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO run_logs(run_id,step_id,stream,message,created_at) VALUES(?,?,?,?,?)`, l.RunID, l.StepID, l.Stream, l.Message, timeText(l.CreatedAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (s *Store) ListRunLogs(ctx context.Context, runID string, afterID int64, limit int) ([]domain.RunLog, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,run_id,step_id,stream,message,created_at FROM run_logs WHERE run_id=? AND id>? ORDER BY id LIMIT ?`, runID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RunLog
	for rows.Next() {
		var l domain.RunLog
		var created string
		if err := rows.Scan(&l.ID, &l.RunID, &l.StepID, &l.Stream, &l.Message, &created); err != nil {
			return nil, err
		}
		l.CreatedAt = parseTime(created)
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) ListRunLogTail(ctx context.Context, runID string, limit int) ([]domain.RunLog, error) {
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,run_id,step_id,stream,message,created_at FROM (SELECT id,run_id,step_id,stream,message,created_at FROM run_logs WHERE run_id=? ORDER BY id DESC LIMIT ?) ORDER BY id`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RunLog, 0)
	for rows.Next() {
		var l domain.RunLog
		var created string
		if err := rows.Scan(&l.ID, &l.RunID, &l.StepID, &l.Stream, &l.Message, &created); err != nil {
			return nil, err
		}
		l.CreatedAt = parseTime(created)
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) GetApproval(ctx context.Context, id string) (domain.Approval, error) {
	return scanApproval(s.db.QueryRowContext(ctx, `SELECT id,run_id,status,requested_at,decided_by,decision,reason,decided_at FROM approvals WHERE id=?`, id))
}
func (s *Store) GetApprovalByRun(ctx context.Context, runID string) (domain.Approval, error) {
	return scanApproval(s.db.QueryRowContext(ctx, `SELECT id,run_id,status,requested_at,decided_by,decision,reason,decided_at FROM approvals WHERE run_id=?`, runID))
}
func scanApproval(row scanner) (domain.Approval, error) {
	var a domain.Approval
	var requested string
	var decidedBy, decidedAt sql.NullString
	err := row.Scan(&a.ID, &a.RunID, &a.Status, &requested, &decidedBy, &a.Decision, &a.Reason, &decidedAt)
	a.RequestedAt = parseTime(requested)
	a.DecidedBy = decidedBy.String
	a.DecidedAt = parseNullTime(decidedAt)
	return a, mapSQLError(err)
}

func (s *Store) DecideApproval(ctx context.Context, id, userID, decision, reason string, at time.Time) error {
	return s.DecideApprovalWithSnapshot(ctx, id, userID, decision, reason, nil, at)
}

func (s *Store) DecideApprovalWithSnapshot(ctx context.Context, id, userID, decision, reason string, snapshot map[string]any, at time.Time) error {
	if decision != "approved" && decision != "rejected" {
		return domain.ErrInvalid
	}
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var runID string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM approvals WHERE id=? AND status='pending'`, id).Scan(&runID)
	if err != nil {
		return mapSQLError(err)
	}
	approvalResult, err := tx.ExecContext(ctx, `UPDATE approvals SET status=?,decided_by=?,decision=?,reason=?,decided_at=? WHERE id=? AND status='pending'`, decision, userID, decision, reason, timeText(at), id)
	if err != nil {
		return err
	}
	if updated, rowsErr := approvalResult.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if updated != 1 {
		return fmt.Errorf("%w: approval is no longer pending", domain.ErrConflict)
	}
	runStatus := domain.RunQueued
	if decision == "rejected" {
		runStatus = domain.RunRejected
	}
	query := `UPDATE runs SET status=?,finished_at=CASE WHEN ?='rejected' THEN ? ELSE finished_at END WHERE id=? AND status='awaiting_approval'`
	arguments := []any{runStatus, decision, timeText(at), runID}
	if snapshot != nil {
		if err := validateNewRunReferences(ctx, tx, domain.Run{ID: runID, InputSnapshot: snapshot}); err != nil {
			return err
		}
		query = `UPDATE runs SET status=?,input_snapshot_json=?,finished_at=CASE WHEN ?='rejected' THEN ? ELSE finished_at END WHERE id=? AND status='awaiting_approval'`
		arguments = []any{runStatus, jsonText(snapshot), decision, timeText(at), runID}
	}
	runResult, err := tx.ExecContext(ctx, query, arguments...)
	if err != nil {
		return err
	}
	if updated, rowsErr := runResult.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if updated != 1 {
		return fmt.Errorf("%w: run is no longer awaiting approval", domain.ErrConflict)
	}
	return tx.Commit()
}

// BatchDecideApprovals validates and consumes the complete selection in one
// transaction. A stale, foreign, or already-decided item aborts the batch.
func (s *Store) BatchDecideApprovals(ctx context.Context, ids []string, ownerID, decision, reason string, at time.Time) ([]domain.Run, error) {
	if len(ids) == 0 || len(ids) > 100 || (decision != "approved" && decision != "rejected") {
		return nil, domain.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	runIDs := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil, fmt.Errorf("%w: approval ids must be non-empty and unique", domain.ErrInvalid)
		}
		seen[id] = true
		var runID string
		if err := tx.QueryRowContext(ctx, `
SELECT a.run_id FROM approvals a
JOIN runs r ON r.id=a.run_id
JOIN environments e ON e.id=r.environment_id
WHERE a.id=? AND a.status='pending' AND r.status='awaiting_approval' AND e.owner_id=?`, id, ownerID).Scan(&runID); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("%w: approval %s is stale or belongs to another environment owner", domain.ErrConflict, id)
			}
			return nil, err
		}
		runIDs = append(runIDs, runID)
	}
	toStatus := domain.RunRejected
	if decision == "approved" {
		toStatus = domain.RunQueued
	}
	for index, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE approvals SET status=?,decided_by=?,decision=?,reason=?,decided_at=? WHERE id=? AND status='pending'`, decision, ownerID, decision, reason, timeText(at), id); err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE runs SET status=?,finished_at=CASE WHEN ?='rejected' THEN ? ELSE NULL END WHERE id=? AND status='awaiting_approval'`, toStatus, decision, timeText(at), runIDs[index])
		if err != nil {
			return nil, err
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return nil, fmt.Errorf("%w: run %s changed while approving batch", domain.ErrConflict, runIDs[index])
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	runs := make([]domain.Run, 0, len(runIDs))
	for _, runID := range runIDs {
		run, getErr := s.GetRun(ctx, runID)
		if getErr != nil {
			return nil, getErr
		}
		runs = append(runs, run)
	}
	return runs, nil
}
