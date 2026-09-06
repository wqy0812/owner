package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// JSON references are explicit identity fields, not substring matches against
// messages, parameters or backup paths. The same predicate fences all writers.
const runReferenceKeySQL = `('installRunId','backupInstallRunId','sourceRunId','baselineRunId','historicalBaselineRunId','retryOfRunId','retryRootRunId','install_run_id','runId')`

// Locked step variables and resolved parameter values are user data. Even a
// nested field named runId in those values is not a platform history reference.
// Authoritative rollback identities are retained separately in step.backup and
// installationBaseline. Use this same predicate for reads and final writes.
const runSnapshotReferenceSQL = `j.key IN ` + runReferenceKeySQL + ` AND j.type='text'
 AND j.fullkey NOT LIKE '$.steps[%].variables.%'
 AND j.fullkey NOT LIKE '$.resolvedParametersByNode.%'`

func cleanupPreview(ctx context.Context, q queryer, id string, days int, now time.Time) (domain.RunCleanupPreview, error) {
	p := domain.RunCleanupPreview{RunID: id, Reasons: []string{}}
	var status string
	var finished sql.NullString
	err := q.QueryRowContext(ctx, `SELECT status,finished_at FROM runs WHERE id=?`, id).Scan(&status, &finished)
	if err == sql.ErrNoRows {
		p.Reasons = append(p.Reasons, "记录不存在或已清理")
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if status != "failed" {
		p.Reasons = append(p.Reasons, "仅失败记录可清理")
	}
	if !finished.Valid {
		p.Reasons = append(p.Reasons, "缺少结束时间")
	} else if !parseTime(finished.String).Before(now.Add(-time.Duration(days) * 24 * time.Hour)) {
		p.Reasons = append(p.Reasons, fmt.Sprintf("结束尚未超过 %d 天", days))
	}
	checks := []struct{ query, reason string }{
		{`SELECT EXISTS(SELECT 1 FROM environment_component_installations WHERE install_run_id=? OR EXISTS(SELECT 1 FROM json_tree(backup_metadata_json) j WHERE j.key IN ` + runReferenceKeySQL + ` AND j.type='text' AND j.value=?))`, "被环境安装记录或安装备份引用"},
		{`SELECT EXISTS(SELECT 1 FROM runs WHERE id<>? AND (retry_of_run_id=? OR retry_root_run_id=?))`, "被其他 Run 的续跑链引用"},
		{`SELECT EXISTS(SELECT 1 FROM action_execution_receipts WHERE run_id=? OR EXISTS(SELECT 1 FROM json_tree(backup_json) j WHERE j.key IN ` + runReferenceKeySQL + ` AND j.type='text' AND j.value=?))`, "被操作及恢复记录引用"},
		{`SELECT EXISTS(SELECT 1 FROM runs WHERE id<>? AND EXISTS(SELECT 1 FROM json_tree(input_snapshot_json) j WHERE ` + runSnapshotReferenceSQL + ` AND j.value=?))`, "被执行快照中的安装来源或回滚备份引用"},
		{`SELECT EXISTS(SELECT 1 FROM scenario_installations WHERE run_id=? OR mutating_run_id=?)`, "被场景完整版本或部分变更引用"},
		{`SELECT EXISTS(SELECT 1 FROM scenario_execution_submissions WHERE run_id=? AND run_id=?)`, "被场景幂等提交记录引用"},
	}
	for i, c := range checks {
		args := []any{id, id}
		if i == 1 {
			args = append(args, id)
		}
		var found int
		if err = q.QueryRowContext(ctx, c.query, args...).Scan(&found); err != nil {
			return p, err
		}
		if found != 0 {
			p.Reasons = append(p.Reasons, c.reason)
		}
	}
	p.Eligible = len(p.Reasons) == 0
	return p, nil
}
func (s *Store) PreviewRunCleanup(ctx context.Context, ids []string, now time.Time) ([]domain.RunCleanupPreview, error) {
	if err := validateRunIDs(ids); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var days int
	if err = tx.QueryRowContext(ctx, `SELECT cleanup_days FROM run_retention_policy WHERE id=1`).Scan(&days); err != nil {
		return nil, err
	}
	out := []domain.RunCleanupPreview{}
	for _, id := range ids {
		p, err := cleanupPreview(ctx, tx, id, days, now)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Store) CleanupRuns(ctx context.Context, ids []string, actor, source string, now time.Time) error {
	if err := validateRunIDs(ids); err != nil {
		return err
	}
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var days int
	if err = tx.QueryRowContext(ctx, `SELECT cleanup_days FROM run_retention_policy WHERE id=1`).Scan(&days); err != nil {
		return err
	}
	for _, id := range ids {
		p, e := cleanupPreview(ctx, tx, id, days, now)
		if e != nil {
			return e
		}
		if !p.Eligible {
			return &domain.CodedError{Code: "run.cleanup_blocked", Message: fmt.Sprintf("%s: %v", id, p.Reasons), Cause: domain.ErrConflict}
		}
	}
	for _, id := range ids {
		r, e := getRunRecord(ctx, tx, id)
		if e != nil {
			return e
		}
		identity := map[string]any{"cleaned": true}
		for _, key := range []string{"componentReleaseSpecDigest", "scenarioRevisionSpecDigest", "environmentRevisionId"} {
			if v, ok := r.InputSnapshot[key].(string); ok {
				identity[key] = v
			}
		}
		// Only Release identities remain for visibility, lifecycle guards and workload
		// ordering. No parameters, error output, locked plan or credential data remain.
		steps := []map[string]any{}
		seen := map[string]bool{}
		if rows, ok := r.InputSnapshot["steps"].([]any); ok {
			for _, v := range rows {
				if row, ok := v.(map[string]any); ok {
					if release, ok := row["releaseId"].(string); ok && release != "" && !seen[release] {
						steps = append(steps, map[string]any{"releaseId": release, "releaseSpecDigest": row["releaseSpecDigest"]})
						seen[release] = true
					}
				}
			}
		}
		identity["steps"] = steps
		_, err = tx.ExecContext(ctx, `INSERT INTO run_cleanup_history(id,kind,status,requested_by,environment_id,environment_revision_id,component_release_id,scenario_revision_id,action_kind,created_at,finished_at,cleaned_at,actor_id,reason,identity_json) VALUES(?,?,'failed',?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.Kind, r.RequestedBy, r.EnvironmentID, r.EnvironmentRevisionID, nullString(r.ComponentReleaseID), nullString(r.ScenarioRevisionID), r.Action, timeText(r.CreatedAt), ptrTimeText(r.FinishedAt), timeText(now), actor, source, jsonText(identity))
		if err != nil {
			return err
		}
		for _, table := range []string{"run_logs", "run_steps", "approvals"} {
			if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE run_id=?", id); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM notifications WHERE EXISTS(SELECT 1 FROM json_tree(notifications.payload_json) j WHERE j.key IN ('runId','evidenceRunId') AND j.value=?)`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM runs WHERE id=? AND status='failed'`, id); err != nil {
			return err
		}
		if err = archiveAudit(ctx, tx, actor, "run.cleaned", id, now, map[string]any{"status": r.Status, "environmentId": r.EnvironmentID, "componentReleaseId": r.ComponentReleaseID, "scenarioRevisionId": r.ScenarioRevisionID, "finishedAt": r.FinishedAt, "source": source}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) RunWasCleaned(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_cleanup_history WHERE id=?`, id).Scan(&count)
	return count > 0, err
}

// Final reference validation is performed after acquiring the writer slot. It
// also protects references prepared in memory before a concurrent cleanup.
func validateNewRunReferences(ctx context.Context, tx *sql.Tx, r domain.Run) error {
	refs := map[string]bool{}
	if r.RetryOfRunID != "" {
		refs[r.RetryOfRunID] = true
	}
	if r.RetryRootRunID != "" {
		refs[r.RetryRootRunID] = true
	}
	b, err := json.Marshal(r.InputSnapshot)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT j.value FROM json_tree(?) j WHERE `+runSnapshotReferenceSQL+` AND j.value<>''`, string(b))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if id != r.ID {
			refs[id] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id := range refs {
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE id=?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 {
			return fmt.Errorf("%w: 引用的 Run %s 不存在或已清理", domain.ErrConflict, id)
		}
	}
	return nil
}
