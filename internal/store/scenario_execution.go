package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"codex/platform-demo/internal/domain"
)

func ScenarioComponentInstallationDigest(values []domain.EnvironmentComponentInstallation) string {
	type identity struct {
		NodeID, ComponentID, ReleaseID, InstallRunID, BackupRef string
		TestOnly                                                bool
		Backup                                                  domain.BackupMetadata
	}
	items := []identity{}
	for _, v := range values {
		items = append(items, identity{v.NodeID, v.ComponentID, v.ReleaseID, v.InstallRunID, v.BackupRef, v.TestOnly, v.Backup})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ComponentID == items[j].ComponentID {
			return items[i].NodeID < items[j].NodeID
		}
		return items[i].ComponentID < items[j].ComponentID
	})
	raw, _ := json.Marshal(items)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func scenarioInstallationsTx(ctx context.Context, q queryer, environmentID string) ([]domain.EnvironmentComponentInstallation, error) {
	rows, err := q.QueryContext(ctx, `SELECT source_node_id,component_id,release_id,install_run_id,backup_ref,test_only,backup_metadata_json FROM environment_component_installations WHERE environment_id=?`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.EnvironmentComponentInstallation{}
	for rows.Next() {
		v := domain.EnvironmentComponentInstallation{EnvironmentID: environmentID}
		var backup string
		if err := rows.Scan(&v.NodeID, &v.ComponentID, &v.ReleaseID, &v.InstallRunID, &v.BackupRef, &v.TestOnly, &backup); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(backup), &v.Backup); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func scenarioSnapshotNumber(snapshot map[string]any, key string) int64 {
	switch v := snapshot[key].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}
func isScenarioLifecycleRun(run domain.Run) bool {
	return run.ScenarioRevisionID != "" && scenarioSnapshotNumber(run.InputSnapshot, "scenarioContractVersion") == 2
}
func (s *Store) GetScenarioSubmission(ctx context.Context, userID, key, digest string) (domain.Run, error) {
	var id, current string
	err := s.db.QueryRowContext(ctx, `SELECT run_id,request_digest FROM scenario_execution_submissions WHERE user_id=? AND key=?`, userID, key).Scan(&id, &current)
	if err != nil {
		return domain.Run{}, mapSQLError(err)
	}
	if current != digest {
		return domain.Run{}, fmt.Errorf("%w: 幂等标识已用于其他请求", domain.ErrConflict)
	}
	return s.GetRun(ctx, id)
}

func (s *Store) ScenarioEnvironmentStateCount(ctx context.Context, id string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_installations WHERE environment_id=?`, id).Scan(&count)
	return count, err
}
func validateScenarioExecutionBaselineTx(ctx context.Context, q queryer, run domain.Run) error {
	if !isScenarioLifecycleRun(run) {
		return nil
	}
	revision, err := getScenarioRevision(ctx, q, run.ScenarioRevisionID)
	if err != nil {
		return err
	}
	if run.InputSnapshot["scenarioRevisionSpecDigest"] != domain.ScenarioRevisionSpecDigest(revision) {
		return fmt.Errorf("%w: 场景定义已变化", domain.ErrConflict)
	}
	var currentEnvironment string
	if err = q.QueryRowContext(ctx, `SELECT current_revision_id FROM environments WHERE id=? AND archived_at IS NULL`, run.EnvironmentID).Scan(&currentEnvironment); err != nil {
		return mapSQLError(err)
	}
	if currentEnvironment != run.EnvironmentRevisionID {
		return fmt.Errorf("%w: 环境 Revision 已变化", domain.ErrConflict)
	}
	baseline, err := getScenarioInstallation(ctx, q, run.EnvironmentID, revision.ScenarioID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if run.RetryOfRunID != "" {
		if baseline.Generation != scenarioSnapshotNumber(run.InputSnapshot, "retryBaselineGeneration") || baseline.MutatingRunID != run.InputSnapshot["retryBaselineMutatingRunId"] {
			return fmt.Errorf("%w: 场景续跑基线已变化", domain.ErrConflict)
		}
		if baseline.MutatingRunID != "" {
			mutation, err := getRunRecord(ctx, q, baseline.MutatingRunID)
			if err != nil {
				return err
			}
			if mutation.ID != run.RetryRootRunID && mutation.RetryRootRunID != run.RetryRootRunID {
				return fmt.Errorf("%w: 场景被其他运行修改", domain.ErrConflict)
			}
		}
		return validateRetryState(ctx, q, run)
	}
	if baseline.Generation != scenarioSnapshotNumber(run.InputSnapshot, "baselineGeneration") || baseline.RunID != run.InputSnapshot["baselineRunId"] {
		return fmt.Errorf("%w: 场景安装基线已变化", domain.ErrConflict)
	}
	installed, err := scenarioInstallationsTx(ctx, q, run.EnvironmentID)
	if err != nil {
		return err
	}
	if ScenarioComponentInstallationDigest(installed) != run.InputSnapshot["baselineInstallationDigest"] {
		return fmt.Errorf("%w: 当前组件安装与预览不一致", domain.ErrConflict)
	}
	mode, _ := run.InputSnapshot["executionMode"].(string)
	switch mode {
	case "install":
		var count int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_installations WHERE environment_id=?`, run.EnvironmentID).Scan(&count); err != nil {
			return err
		}
		if len(installed) > 0 || count > 0 {
			return fmt.Errorf("%w: 安装要求干净环境", domain.ErrConflict)
		}
		pending, err := scenarioRecoveryReceiptsTx(ctx, q, run.EnvironmentID)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			return fmt.Errorf("%w: 环境存在未恢复的组件变更", domain.ErrConflict)
		}
	case "upgrade":
		if baseline.State != "complete" || baseline.TestOnly || baseline.RevisionID != revision.SourceRevisionID {
			return fmt.Errorf("%w: 升级来源基线不完整", domain.ErrConflict)
		}
		source, e := getScenarioRevision(ctx, q, revision.SourceRevisionID)
		if e != nil {
			return e
		}
		if source.ScenarioID != revision.ScenarioID {
			return domain.ErrConflict
		}
		if _, e = successfulScenarioSourceRun(ctx, q, source, revision.SourceRunID); e != nil {
			return e
		}
		if _, e = successfulScenarioSourceRun(ctx, q, source, baseline.RunID); e != nil {
			return e
		}
	case "baseline_verify":
		if err := validateScenarioRecoveryReceipts(ctx, q, run); err != nil {
			return err
		}
		if historicalID, _ := run.InputSnapshot["historicalBaselineRunId"].(string); historicalID != "" {
			if _, err := validateHistoricalBaselineSnapshot(ctx, q, run, revision); err != nil {
				return err
			}
		} else if baseline.RunID == "" || baseline.RevisionID != revision.ID {
			return fmt.Errorf("%w: 没有可恢复的完整历史基线", domain.ErrConflict)
		}
	default:
		return domain.ErrInvalid
	}
	return nil
}

func validateHistoricalBaselineSnapshot(ctx context.Context, q queryer, run domain.Run, revision domain.ScenarioRevision) (ScenarioHistoricalBaseline, error) {
	candidate, err := findScenarioHistoricalBaseline(ctx, q, run.EnvironmentID, revision.ScenarioID, revision.ID)
	if err != nil {
		return candidate, err
	}
	if candidate.Run.ID != run.InputSnapshot["historicalBaselineRunId"] || candidate.TestOnly != run.InputSnapshot["historicalBaselineTestOnly"] {
		return candidate, fmt.Errorf("%w: 历史基线候选发生变化，请重新预览", domain.ErrConflict)
	}
	var locked []ScenarioHistoricalNode
	raw, err := json.Marshal(run.InputSnapshot["targetNodes"])
	if err != nil {
		return candidate, err
	}
	if err = json.Unmarshal(raw, &locked); err != nil {
		return candidate, err
	}
	left, _ := json.Marshal(locked)
	right, _ := json.Marshal(candidate.Nodes)
	if string(left) != string(right) {
		return candidate, fmt.Errorf("%w: 历史冻结参数不匹配", domain.ErrConflict)
	}
	return candidate, nil
}
func (s *Store) ValidateScenarioExecutionBaseline(ctx context.Context, run domain.Run) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return validateScenarioExecutionBaselineTx(ctx, tx, run)
}
func validateScenarioRunCreationTx(ctx context.Context, tx *sql.Tx, run domain.Run) error {
	if !isScenarioLifecycleRun(run) {
		return nil
	}
	if err := validateScenarioExecutionBaselineTx(ctx, tx, run); err != nil {
		return err
	}
	revision, err := getScenarioRevision(ctx, tx, run.ScenarioRevisionID)
	if err != nil {
		return err
	}
	if len(revision.AcceptanceJobs) == 0 && run.InputSnapshot["executionMode"] != "baseline_verify" {
		return fmt.Errorf("%w: 业务验收作业必填", domain.ErrConflict)
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE scenario_revision_id=? AND status IN ('awaiting_approval','queued','running')`, revision.ID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return fmt.Errorf("%w: 当前版本有活动运行", domain.ErrConflict)
	}
	if run.Kind == domain.RunScenarioTest {
		if err = validateScenarioEditableTx(ctx, tx, revision); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE scenario_revisions SET status='testing' WHERE id=?`, revision.ID); err != nil {
			return err
		}
	} else if revision.Status != domain.RevisionReleased && run.InputSnapshot["executionMode"] != "baseline_verify" {
		return fmt.Errorf("%w: 正式运行要求已发布版本", domain.ErrConflict)
	}
	key, _ := run.InputSnapshot["submissionKey"].(string)
	digest, _ := run.InputSnapshot["submissionDigest"].(string)
	if key == "" || digest == "" {
		return domain.ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scenario_execution_submissions(user_id,key,request_digest,run_id) VALUES(?,?,?,?)`, run.RequestedBy, key, digest, run.ID)
	return mapSQLError(err)
}

// Mark before asking Ansible to begin a potentially mutating stage. A crash
// cannot leave the environment advertised as a complete old or new version.
func (s *Store) MarkScenarioMutation(ctx context.Context, run domain.Run) error {
	if !isScenarioLifecycleRun(run) {
		return nil
	}
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	revision, err := getScenarioRevision(ctx, tx, run.ScenarioRevisionID)
	if err != nil {
		return err
	}
	baseline, err := getScenarioInstallation(ctx, tx, run.EnvironmentID, revision.ScenarioID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if baseline.MutatingRunID == run.ID {
		return nil
	}
	if baseline.Generation != scenarioExpectedGeneration(run) {
		return fmt.Errorf("%w: 变更前的基线已变化", domain.ErrConflict)
	}
	if baseline.Generation == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO scenario_installations(environment_id,scenario_id,revision_id,run_id,mutating_run_id,state,test_only,installation_digest,generation,updated_at) VALUES(?,?,NULL,NULL,?,'partial',?,'',1,?)`, run.EnvironmentID, revision.ScenarioID, run.ID, run.Kind == domain.RunScenarioTest, timeText(time.Now().UTC()))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE scenario_installations SET state='partial',mutating_run_id=?,generation=generation+1,updated_at=? WHERE environment_id=? AND scenario_id=?`, run.ID, timeText(time.Now().UTC()), run.EnvironmentID, revision.ScenarioID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func validateScenarioAcceptanceEvidence(ctx context.Context, q queryer, run domain.Run, revision domain.ScenarioRevision) error {
	if (len(revision.AcceptanceJobs) == 0 && run.InputSnapshot["executionMode"] != "baseline_verify") || !isScenarioLifecycleRun(run) {
		return fmt.Errorf("%w: 缺少场景业务验收证据", domain.ErrConflict)
	}
	if run.InputSnapshot["scenarioRevisionSpecDigest"] != domain.ScenarioRevisionSpecDigest(revision) {
		return fmt.Errorf("%w: 业务验收对应其他版本定义", domain.ErrConflict)
	}
	steps, err := scenarioRetryEvidence(ctx, q, run)
	if err != nil {
		return err
	}
	jobs := map[string]bool{}
	targetChecks := map[string]bool{}
	for _, evidence := range steps {
		step := evidence.step
		nodeID, _ := step["nodeId"].(string)
		var count int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_steps WHERE run_id=? AND node_id=? AND status='succeeded'`, evidence.runID, nodeID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("%w: 执行步骤 %s 缺少完整成功证据", domain.ErrConflict, nodeID)
		}
		if step["sourceType"] == "scenario_acceptance" {
			id, _ := step["acceptanceJobId"].(string)
			if step["scenarioRevisionId"] != revision.ID || jobs[id] {
				return domain.ErrConflict
			}
			jobs[id] = true
		}
		if step["stage"] == "target_verify" {
			id, _ := step["sourceNodeId"].(string)
			targetChecks[id] = true
		}
	}
	if len(jobs) != len(revision.AcceptanceJobs) {
		return fmt.Errorf("%w: 业务验收数量不匹配", domain.ErrConflict)
	}
	for _, job := range revision.AcceptanceJobs {
		if !jobs[job.ID] {
			return fmt.Errorf("%w: 缺少业务验收 %s", domain.ErrConflict, job.Name)
		}
	}
	for _, node := range revision.Graph.Nodes {
		if !targetChecks[node.ID] {
			return fmt.Errorf("%w: 缺少目标节点整体验证 %s", domain.ErrConflict, node.ID)
		}
	}
	return nil
}

func finishScenarioLifecycleTx(ctx context.Context, tx *sql.Tx, run domain.Run, at time.Time) error {
	if !isScenarioLifecycleRun(run) {
		return nil
	}
	revision, err := getScenarioRevision(ctx, tx, run.ScenarioRevisionID)
	if err != nil {
		return err
	}
	if err = validateScenarioAcceptanceEvidence(ctx, tx, run, revision); err != nil {
		return err
	}
	installed, err := scenarioInstallationsTx(ctx, tx, run.EnvironmentID)
	if err != nil {
		return err
	}
	var target []struct {
		NodeID      string `json:"nodeId"`
		ComponentID string `json:"componentId"`
		ReleaseID   string `json:"releaseId"`
	}
	raw, _ := json.Marshal(run.InputSnapshot["targetNodes"])
	if err = json.Unmarshal(raw, &target); err != nil {
		return err
	}
	if len(target) == 0 || len(target) != len(installed) || len(target) != len(revision.Graph.Nodes) {
		return fmt.Errorf("%w: 目标集群组件数量不完整", domain.ErrConflict)
	}
	for _, node := range target {
		count := 0
		for _, item := range installed {
			if item.NodeID == node.NodeID && item.ComponentID == node.ComponentID && item.ReleaseID == node.ReleaseID {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("%w: 目标节点 %s 安装状态不匹配", domain.ErrConflict, node.NodeID)
		}
	}
	baseline, err := getScenarioInstallation(ctx, tx, run.EnvironmentID, revision.ScenarioID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if baseline.MutatingRunID != run.ID && baseline.Generation != scenarioExpectedGeneration(run) {
		return fmt.Errorf("%w: 完成时场景基线已变化", domain.ErrConflict)
	}
	testOnly := run.Kind == domain.RunScenarioTest
	state := "complete"
	baseRunID := run.ID
	if run.InputSnapshot["executionMode"] == "baseline_verify" {
		if err := validateScenarioRecoveryReceipts(ctx, tx, run); err != nil {
			return err
		}
		testOnly = baseline.TestOnly
		baseRunID = baseline.RunID
		if historicalID, _ := run.InputSnapshot["historicalBaselineRunId"].(string); historicalID != "" {
			candidate, err := validateHistoricalBaselineSnapshot(ctx, tx, run, revision)
			if err != nil {
				return err
			}
			testOnly, baseRunID = candidate.TestOnly, candidate.Run.ID
		}
	}
	if testOnly {
		state = "test"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scenario_installations(environment_id,scenario_id,revision_id,run_id,mutating_run_id,state,test_only,installation_digest,generation,updated_at) VALUES(?,?,?,?,NULL,?,?,?,1,?) ON CONFLICT(environment_id,scenario_id) DO UPDATE SET revision_id=excluded.revision_id,run_id=excluded.run_id,mutating_run_id=NULL,state=excluded.state,test_only=excluded.test_only,installation_digest=excluded.installation_digest,generation=scenario_installations.generation+1,updated_at=excluded.updated_at`, run.EnvironmentID, revision.ScenarioID, revision.ID, baseRunID, state, testOnly, ScenarioComponentInstallationDigest(installed), timeText(at))
	return err
}

func scenarioExpectedGeneration(run domain.Run) int64 {
	if run.RetryOfRunID != "" {
		return scenarioSnapshotNumber(run.InputSnapshot, "retryBaselineGeneration")
	}
	return scenarioSnapshotNumber(run.InputSnapshot, "baselineGeneration")
}

func scenarioRecoveryReceiptsTx(ctx context.Context, q queryer, environmentID string) ([]ActionExecutionReceipt, error) {
	rows, err := q.QueryContext(ctx, `SELECT run_id,step_id,environment_id,component_id,release_id,action_id,source_node_id,status,backup_ref,backup_json,started_at,updated_at FROM action_execution_receipts r WHERE environment_id=? AND status<>'verified' AND NOT EXISTS (SELECT 1 FROM action_execution_receipts newer WHERE newer.environment_id=r.environment_id AND newer.component_id=r.component_id AND newer.source_node_id=r.source_node_id AND (newer.updated_at>r.updated_at OR (newer.updated_at=r.updated_at AND newer.rowid>r.rowid))) ORDER BY component_id,source_node_id`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var receipts []ActionExecutionReceipt
	for rows.Next() {
		var receipt ActionExecutionReceipt
		var backup, started, updated string
		if err := rows.Scan(&receipt.RunID, &receipt.StepID, &receipt.EnvironmentID, &receipt.ComponentID, &receipt.ReleaseID, &receipt.ActionID, &receipt.SourceNodeID, &receipt.Status, &receipt.BackupRef, &backup, &started, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(backup), &receipt.Backup); err != nil {
			return nil, err
		}
		receipt.StartedAt, receipt.UpdatedAt = parseTime(started), parseTime(updated)
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}
func validateScenarioRecoveryReceipts(ctx context.Context, q queryer, run domain.Run) error {
	current, err := scenarioRecoveryReceiptsTx(ctx, q, run.EnvironmentID)
	if err != nil {
		return err
	}
	var expected []ActionExecutionReceipt
	raw, err := json.Marshal(run.InputSnapshot["recoveredReceipts"])
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &expected); err != nil {
		return err
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(expected)
	if string(a) != string(b) {
		return fmt.Errorf("%w: 待恢复操作已变化，请重新预览基线复核", domain.ErrConflict)
	}
	var targets []ScenarioHistoricalNode
	raw, err = json.Marshal(run.InputSnapshot["targetNodes"])
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &targets); err != nil {
		return err
	}
	for _, receipt := range current {
		covered := false
		for _, node := range targets {
			if receipt.SourceNodeID == node.NodeID && receipt.ComponentID == node.ComponentID {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("%w: 不能用旧集群基线检查替代新增节点的卸载验证", domain.ErrConflict)
		}
	}
	return nil
}

func (s *Store) MarkScenarioBaselineUnverified(ctx context.Context, run domain.Run) error {
	if !isScenarioLifecycleRun(run) {
		return nil
	}
	scenarioID, _ := run.InputSnapshot["scenarioId"].(string)
	_, err := s.db.ExecContext(ctx, `UPDATE scenario_installations SET state='unverified',generation=generation+1,updated_at=? WHERE environment_id=? AND scenario_id=? AND generation=? AND mutating_run_id IS NULL`, timeText(time.Now().UTC()), run.EnvironmentID, scenarioID, scenarioSnapshotNumber(run.InputSnapshot, "baselineGeneration"))
	return err
}

func scenarioRequiredEvidenceTx(ctx context.Context, q queryer, revision domain.ScenarioRevision) (map[string]string, error) {
	if len(revision.AcceptanceJobs) == 0 {
		return nil, fmt.Errorf("%w: 场景业务验收作业必填", domain.ErrConflict)
	}
	rows, err := q.QueryContext(ctx, runSelect+` WHERE scenario_revision_id=? AND kind='scenario_test' AND status='succeeded' AND json_extract(input_snapshot_json,'$.scenarioRevisionSpecDigest')=? ORDER BY created_at DESC`, revision.ID, domain.ScenarioRevisionSpecDigest(revision))
	if err != nil {
		return nil, err
	}
	runs := []domain.Run{}
	for rows.Next() {
		run, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	found := map[string]string{}
	for _, run := range runs {
		mode, _ := run.InputSnapshot["executionMode"].(string)
		if mode != "install" && mode != "upgrade" {
			continue
		}
		if found[mode] != "" {
			continue
		}
		if _, err := scenarioTestEvidenceMatches(ctx, q, run, revision); err == nil {
			found[mode] = run.ID
		}
	}
	if found["install"] == "" || (revision.SourceRevisionID != "" && found["upgrade"] == "") {
		return found, fmt.Errorf("%w: 当前版本尚未通过所需的安装和升级测试", domain.ErrConflict)
	}
	return found, nil
}
func (s *Store) ScenarioTestEvidence(ctx context.Context, id string) (map[string]string, error) {
	revision, err := s.GetScenarioRevision(ctx, id)
	if err != nil {
		return nil, err
	}
	return scenarioRequiredEvidenceTx(ctx, s.db, revision)
}
