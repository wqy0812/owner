package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"codex/platform-demo/internal/domain"
)

// IsActionReceiptRecovered recognizes a subsequent complete baseline verification
// without changing the failed operation or claiming its post-check succeeded.
// The verification is valid only while its exact restored installation is current.
func (s *Store) IsActionReceiptRecovered(ctx context.Context, receipt ActionExecutionReceipt) (bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var latest ActionExecutionReceipt
	var backup, started, updated string
	err = tx.QueryRowContext(ctx, `SELECT run_id,step_id,environment_id,component_id,release_id,action_id,source_node_id,status,backup_ref,backup_json,started_at,updated_at FROM action_execution_receipts WHERE environment_id=? AND component_id=? AND source_node_id=? ORDER BY updated_at DESC,rowid DESC LIMIT 1`, receipt.EnvironmentID, receipt.ComponentID, receipt.SourceNodeID).Scan(&latest.RunID, &latest.StepID, &latest.EnvironmentID, &latest.ComponentID, &latest.ReleaseID, &latest.ActionID, &latest.SourceNodeID, &latest.Status, &latest.BackupRef, &backup, &started, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	latest.Backup = decodeJSON(backup, domain.BackupMetadata{})
	latest.StartedAt, latest.UpdatedAt = parseTime(started), parseTime(updated)
	if jsonText(latest) != jsonText(receipt) {
		return false, nil
	}
	rows, err := tx.QueryContext(ctx, runSelect+` WHERE environment_id=? AND kind='scenario_run' AND status='succeeded' AND json_extract(input_snapshot_json,'$.scenarioContractVersion')=2 AND json_extract(input_snapshot_json,'$.executionMode')='baseline_verify' AND finished_at IS NOT NULL ORDER BY finished_at DESC`, receipt.EnvironmentID)
	if err != nil {
		return false, err
	}
	runs := []domain.Run{}
	for rows.Next() {
		run, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return false, e
		}
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	installed, err := scenarioInstallationsTx(ctx, tx, receipt.EnvironmentID)
	if err != nil {
		return false, err
	}
	actualDigest := ScenarioComponentInstallationDigest(installed)
	for _, run := range runs {
		if run.FinishedAt == nil || !run.FinishedAt.After(receipt.UpdatedAt) {
			continue
		}
		scenarioID, _ := run.InputSnapshot["scenarioId"].(string)
		baseline, err := getScenarioInstallation(ctx, tx, receipt.EnvironmentID, scenarioID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if (baseline.State != "complete" && baseline.State != "test") || baseline.MutatingRunID != "" || baseline.RevisionID != run.ScenarioRevisionID || !baseline.UpdatedAt.Equal(*run.FinishedAt) {
			continue
		}
		if baseline.InstallationDigest != actualDigest || run.InputSnapshot["baselineInstallationDigest"] != actualDigest {
			continue
		}
		baselineRunID, _ := run.InputSnapshot["baselineRunId"].(string)
		if historicalID, _ := run.InputSnapshot["historicalBaselineRunId"].(string); historicalID != "" {
			baselineRunID = historicalID
		}
		if baselineRunID == "" || baseline.RunID != baselineRunID {
			continue
		}
		testOnly, _ := run.InputSnapshot["baselineTestOnly"].(bool)
		if _, historical := run.InputSnapshot["historicalBaselineRunId"]; historical {
			testOnly, _ = run.InputSnapshot["historicalBaselineTestOnly"].(bool)
		}
		if baseline.TestOnly != testOnly || (baseline.State == "test") != testOnly {
			continue
		}
		raw, err := json.Marshal(run.InputSnapshot["recoveredReceipts"])
		if err != nil {
			return false, err
		}
		var recovered []ActionExecutionReceipt
		if json.Unmarshal(raw, &recovered) != nil {
			continue
		}
		for _, locked := range recovered {
			if jsonText(locked) == jsonText(receipt) {
				return true, nil
			}
		}
	}
	return false, nil
}
