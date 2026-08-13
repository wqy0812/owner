package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

func (s *Store) CreateRun(ctx context.Context, r domain.Run, approval *domain.Approval) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO runs(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,destructive,input_snapshot_json,artifact_digest,error_text,created_at,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Kind, r.Status, r.RequestedBy, r.EnvironmentID, r.EnvironmentRevisionID, nullString(r.ComponentReleaseID), nullString(r.ScenarioRevisionID), r.Action, r.Destructive, jsonText(r.InputSnapshot), r.ArtifactDigest, r.Error, timeText(r.CreatedAt), ptrTimeText(r.StartedAt), ptrTimeText(r.FinishedAt))
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

func scanRun(row scanner) (domain.Run, error) {
	var r domain.Run
	var component, scenario sql.NullString
	var destructive int
	var snapshot, created string
	var started, finished sql.NullString
	err := row.Scan(&r.ID, &r.Kind, &r.Status, &r.RequestedBy, &r.EnvironmentID, &r.EnvironmentRevisionID, &component, &scenario, &r.Action, &destructive, &snapshot, &r.ArtifactDigest, &r.Error, &created, &started, &finished)
	r.ComponentReleaseID = component.String
	r.ScenarioRevisionID = scenario.String
	r.Destructive = destructive != 0
	r.InputSnapshot = decodeJSON(snapshot, map[string]any{})
	r.CreatedAt = parseTime(created)
	r.StartedAt = parseNullTime(started)
	r.FinishedAt = parseNullTime(finished)
	return r, err
}

const runSelect = `SELECT id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,destructive,input_snapshot_json,artifact_digest,error_text,created_at,started_at,finished_at FROM runs`

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
  SELECT 1 FROM runs r
  WHERE r.id=? AND (
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
)`, runID, viewer.ID,
		viewer.Role, viewer.ID,
		viewer.Role, viewer.ID, viewer.ID,
		viewer.Role, viewer.ID,
	).Scan(&visible)
	return visible != 0, err
}

func (s *Store) ListRuns(ctx context.Context, viewer domain.User) ([]domain.Run, error) {
	q := runSelect
	args := []any{}
	switch viewer.Role {
	case domain.RoleEnvironmentOwner:
		q += ` WHERE EXISTS (SELECT 1 FROM environments e WHERE e.id=runs.environment_id AND e.owner_id=?)`
		args = append(args, viewer.ID)
	case domain.RoleComponentOwner:
		q += ` WHERE requested_by=?
OR EXISTS (
  SELECT 1 FROM component_releases cr JOIN components c ON c.id=cr.component_id
  WHERE cr.id=runs.component_release_id AND c.owner_id=?
)
OR EXISTS (
  SELECT 1 FROM json_each(runs.input_snapshot_json, '$.steps') locked_step
  JOIN component_releases cr ON cr.id=json_extract(locked_step.value, '$.releaseId')
  JOIN components c ON c.id=cr.component_id
  WHERE c.owner_id=?
)`
		args = append(args, viewer.ID, viewer.ID, viewer.ID)
	case domain.RoleScenarioOwner:
		q += ` WHERE requested_by=? OR EXISTS (SELECT 1 FROM scenario_revisions sr JOIN scenarios s ON s.id=sr.scenario_id WHERE sr.id=runs.scenario_revision_id AND s.owner_id=?)`
		args = append(args, viewer.ID, viewer.ID)
	}
	q += ` ORDER BY created_at DESC`
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
	res, err := s.db.ExecContext(ctx, q, args...)
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_steps(id,run_id,node_id,name,status,exit_code,summary,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?)`, st.ID, st.RunID, st.NodeID, st.Name, st.Status, st.ExitCode, st.Summary, ptrTimeText(st.StartedAt), ptrTimeText(st.FinishedAt))
	return mapSQLError(err)
}
func (s *Store) UpdateRunStep(ctx context.Context, st domain.RunStep) error {
	res, err := s.db.ExecContext(ctx, `UPDATE run_steps SET status=?,exit_code=?,summary=?,started_at=?,finished_at=? WHERE id=?`, st.Status, st.ExitCode, st.Summary, ptrTimeText(st.StartedAt), ptrTimeText(st.FinishedAt), st.ID)
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
	if decision != "approved" && decision != "rejected" {
		return domain.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
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
	runResult, err := tx.ExecContext(ctx, `UPDATE runs SET status=?,finished_at=CASE WHEN ?='rejected' THEN ? ELSE finished_at END WHERE id=? AND status='awaiting_approval'`, runStatus, decision, timeText(at), runID)
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
