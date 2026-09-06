package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"fmt"
	"time"
)

type ActionExecutionReceipt struct {
	RunID         string                `json:"runId"`
	StepID        string                `json:"stepId"`
	EnvironmentID string                `json:"environmentId"`
	ComponentID   string                `json:"componentId"`
	ReleaseID     string                `json:"releaseId"`
	ActionID      string                `json:"actionId"`
	SourceNodeID  string                `json:"sourceNodeId"`
	Status        string                `json:"status"`
	BackupRef     string                `json:"backupRef"`
	Backup        domain.BackupMetadata `json:"backup"`
	StartedAt     time.Time             `json:"startedAt"`
	UpdatedAt     time.Time             `json:"updatedAt"`
}

func (s *Store) RecordActionExecution(ctx context.Context, r ActionExecutionReceipt) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO action_execution_receipts(run_id,step_id,environment_id,component_id,release_id,action_id,source_node_id,status,backup_ref,backup_json,started_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id,step_id) DO UPDATE SET status=excluded.status,updated_at=excluded.updated_at WHERE action_execution_receipts.backup_ref=excluded.backup_ref AND action_execution_receipts.backup_json=excluded.backup_json AND (action_execution_receipts.status=excluded.status OR (action_execution_receipts.status='started' AND excluded.status IN ('main_succeeded','verified')) OR (action_execution_receipts.status='main_succeeded' AND excluded.status='verified'))`, r.RunID, r.StepID, r.EnvironmentID, r.ComponentID, r.ReleaseID, r.ActionID, r.SourceNodeID, r.Status, r.BackupRef, jsonText(r.Backup), timeText(r.StartedAt), timeText(r.UpdatedAt))
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return fmt.Errorf("%w: execution receipt identity or state changed", domain.ErrConflict)
	}
	return nil
}
func (s *Store) LatestActionReceipt(ctx context.Context, environmentID, componentID, releaseID string) (ActionExecutionReceipt, error) {
	var r ActionExecutionReceipt
	var backup, started, updated string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,step_id,environment_id,component_id,release_id,action_id,source_node_id,status,backup_ref,backup_json,started_at,updated_at FROM action_execution_receipts WHERE environment_id=? AND component_id=? AND release_id=? ORDER BY updated_at DESC LIMIT 1`, environmentID, componentID, releaseID).Scan(&r.RunID, &r.StepID, &r.EnvironmentID, &r.ComponentID, &r.ReleaseID, &r.ActionID, &r.SourceNodeID, &r.Status, &r.BackupRef, &backup, &started, &updated)
	if err != nil {
		return r, mapSQLError(err)
	}
	r.Backup = decodeJSON(backup, domain.BackupMetadata{})
	r.StartedAt = parseTime(started)
	r.UpdatedAt = parseTime(updated)
	return r, nil
}
func (s *Store) SaveRunJob(ctx context.Context, runID, digest string, bundle []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_jobs(run_id,digest,bundle) VALUES(?,?,?)`, runID, digest, bundle)
	return err
}
func (s *Store) GetRunJob(ctx context.Context, runID string) (string, []byte, *int, error) {
	var digest string
	var content []byte
	var code *int
	err := s.db.QueryRowContext(ctx, `SELECT digest,bundle,exit_code FROM run_jobs WHERE run_id=?`, runID).Scan(&digest, &content, &code)
	return digest, content, code, mapSQLError(err)
}
func (s *Store) SetRunJobExitCode(ctx context.Context, runID string, code *int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE run_jobs SET exit_code=? WHERE run_id=?`, code, runID)
	return err
}

func (s *Store) LatestActionReceiptForNode(ctx context.Context, environmentID, componentID, releaseID, nodeID string) (ActionExecutionReceipt, error) {
	var r ActionExecutionReceipt
	var backup, started, updated string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,step_id,environment_id,component_id,release_id,action_id,source_node_id,status,backup_ref,backup_json,started_at,updated_at FROM action_execution_receipts WHERE environment_id=? AND component_id=? AND (?='' OR release_id=?) AND source_node_id=? ORDER BY updated_at DESC LIMIT 1`, environmentID, componentID, releaseID, releaseID, nodeID).Scan(&r.RunID, &r.StepID, &r.EnvironmentID, &r.ComponentID, &r.ReleaseID, &r.ActionID, &r.SourceNodeID, &r.Status, &r.BackupRef, &backup, &started, &updated)
	if err != nil {
		return r, mapSQLError(err)
	}
	r.Backup = decodeJSON(backup, domain.BackupMetadata{})
	r.StartedAt = parseTime(started)
	r.UpdatedAt = parseTime(updated)
	return r, nil
}
func (s *Store) RunJobSummary(ctx context.Context, id string) (string, *int, error) {
	var digest string
	var code *int
	err := s.db.QueryRowContext(ctx, `SELECT digest,exit_code FROM run_jobs WHERE run_id=?`, id).Scan(&digest, &code)
	return digest, code, mapSQLError(err)
}

func (s *Store) HasSuccessfulActionTest(ctx context.Context, releaseID, spec, actionID, core, python string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs r WHERE r.kind='component_test' AND r.status='succeeded' AND r.component_release_id=? AND json_extract(r.input_snapshot_json,'$.componentReleaseSpecDigest')=? AND COALESCE(json_extract(r.input_snapshot_json,'$.runtime.ansibleCore'),'')=? AND COALESCE(json_extract(r.input_snapshot_json,'$.runtime.python'),'')=? AND EXISTS(SELECT 1 FROM json_each(r.input_snapshot_json,'$.parentSteps') j WHERE json_extract(j.value,'$.actionId')=? AND json_extract(j.value,'$.phase')='execute'))`, releaseID, spec, core, python, actionID).Scan(&found)
	return found != 0, err
}

// LatestEnvironmentActionReceipts returns the current operation for each component instance.
func (s *Store) LatestEnvironmentActionReceipts(ctx context.Context, environmentID string) ([]ActionExecutionReceipt, error) {
	return latestEnvironmentActionReceipts(ctx, s.db, environmentID)
}
func latestEnvironmentActionReceipts(ctx context.Context, q queryer, environmentID string) ([]ActionExecutionReceipt, error) {
	rows, err := q.QueryContext(ctx, `SELECT run_id,step_id,environment_id,component_id,release_id,action_id,source_node_id,status,backup_ref,backup_json,started_at,updated_at FROM action_execution_receipts r WHERE environment_id=? AND NOT EXISTS (SELECT 1 FROM action_execution_receipts newer WHERE newer.environment_id=r.environment_id AND newer.component_id=r.component_id AND newer.source_node_id=r.source_node_id AND (newer.updated_at>r.updated_at OR (newer.updated_at=r.updated_at AND newer.rowid>r.rowid))) ORDER BY component_id,source_node_id`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActionExecutionReceipt{}
	for rows.Next() {
		var r ActionExecutionReceipt
		var backup, started, updated string
		if err := rows.Scan(&r.RunID, &r.StepID, &r.EnvironmentID, &r.ComponentID, &r.ReleaseID, &r.ActionID, &r.SourceNodeID, &r.Status, &r.BackupRef, &backup, &started, &updated); err != nil {
			return nil, err
		}
		r.Backup = decodeJSON(backup, domain.BackupMetadata{})
		r.StartedAt = parseTime(started)
		r.UpdatedAt = parseTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}
