package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"codex/platform-demo/internal/domain"
)

type ScenarioHistoricalNode struct {
	NodeID      string         `json:"nodeId"`
	ComponentID string         `json:"componentId"`
	ReleaseID   string         `json:"releaseId"`
	Variables   map[string]any `json:"variables"`
}
type ScenarioHistoricalBaseline struct {
	Run                domain.Run               `json:"run"`
	Nodes              []ScenarioHistoricalNode `json:"nodes"`
	TestOnly           bool                     `json:"testOnly"`
	InstallationDigest string                   `json:"installationDigest"`
}

// FindScenarioHistoricalBaseline finds a verifiable candidate; it never marks
// an environment complete or manufactures execution/acceptance evidence.
func (s *Store) FindScenarioHistoricalBaseline(ctx context.Context, environmentID, scenarioID, revisionID string) (ScenarioHistoricalBaseline, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ScenarioHistoricalBaseline{}, err
	}
	defer tx.Rollback()
	return findScenarioHistoricalBaseline(ctx, tx, environmentID, scenarioID, revisionID)
}
func findScenarioHistoricalBaseline(ctx context.Context, q queryer, environmentID, scenarioID, revisionID string) (ScenarioHistoricalBaseline, error) {
	fail := func(message string) (ScenarioHistoricalBaseline, error) {
		return ScenarioHistoricalBaseline{}, fmt.Errorf("%w: %s", domain.ErrConflict, message)
	}
	revision, err := getScenarioRevision(ctx, q, revisionID)
	if err != nil {
		return ScenarioHistoricalBaseline{}, err
	}
	if revision.ScenarioID != scenarioID {
		return fail("historical baseline revision does not belong to scenario")
	}
	rows, err := q.QueryContext(ctx, `SELECT source_node_id,environment_id,component_id,release_id,install_run_id,backup_ref,backup_metadata_json,test_only,installed_at FROM environment_component_installations WHERE environment_id=? ORDER BY source_node_id`, environmentID)
	if err != nil {
		return ScenarioHistoricalBaseline{}, err
	}
	var installed []domain.EnvironmentComponentInstallation
	for rows.Next() {
		var value domain.EnvironmentComponentInstallation
		var raw, at string
		if err = rows.Scan(&value.NodeID, &value.EnvironmentID, &value.ComponentID, &value.ReleaseID, &value.InstallRunID, &value.BackupRef, &raw, &value.TestOnly, &at); err != nil {
			rows.Close()
			return ScenarioHistoricalBaseline{}, err
		}
		if err = json.Unmarshal([]byte(raw), &value.Backup); err != nil {
			rows.Close()
			return fail("installed backup metadata is invalid")
		}
		value.InstalledAt = parseTime(at)
		installed = append(installed, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ScenarioHistoricalBaseline{}, err
	}
	if len(revision.Graph.Nodes) == 0 || len(installed) != len(revision.Graph.Nodes) {
		return fail("current installations do not cover the complete historical scenario")
	}
	byNode := map[string]domain.EnvironmentComponentInstallation{}
	seenComponents := map[string]bool{}
	for _, item := range installed {
		if item.NodeID == "" || byNode[item.NodeID].NodeID != "" || seenComponents[item.ComponentID] {
			return fail("historical installation node identity is missing or ambiguous")
		}
		if item.InstallRunID == "" || item.BackupRef == "" || item.Backup.InstallRunID != item.InstallRunID || item.Backup.EnvironmentID != environmentID || item.Backup.ComponentID != item.ComponentID || item.Backup.ReleaseID != item.ReleaseID || item.Backup.NodeID != item.NodeID {
			return fail("historical installation and recovery identities do not match")
		}
		byNode[item.NodeID] = item
		seenComponents[item.ComponentID] = true
	}
	for _, node := range revision.Graph.Nodes {
		installed, ok := byNode[node.ID]
		if !ok || installed.ReleaseID != node.ReleaseID {
			return fail("current node releases differ from historical scenario")
		}
		var componentID string
		if err = q.QueryRowContext(ctx, `SELECT component_id FROM component_releases WHERE id=?`, node.ReleaseID).Scan(&componentID); err != nil {
			return ScenarioHistoricalBaseline{}, mapSQLError(err)
		}
		if componentID != installed.ComponentID {
			return fail("historical node component identity does not match")
		}
	}
	query := strings.Replace(runSelect, "FROM runs", "FROM retained_run_history runs", 1) + ` WHERE environment_id=? AND scenario_revision_id=? AND kind IN ('scenario_run','scenario_test') AND status='succeeded' AND COALESCE(json_extract(input_snapshot_json,'$.executionMode'),'install') IN ('install','upgrade') ORDER BY CASE kind WHEN 'scenario_run' THEN 0 ELSE 1 END,created_at DESC,id DESC`
	rows, err = q.QueryContext(ctx, query, environmentID, revisionID)
	if err != nil {
		return ScenarioHistoricalBaseline{}, err
	}
	var runs []domain.Run
	for rows.Next() {
		run, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return ScenarioHistoricalBaseline{}, e
		}
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ScenarioHistoricalBaseline{}, err
	}
	var candidates []ScenarioHistoricalBaseline
	for _, run := range runs {
		if run.InputSnapshot["scenarioRevisionSpecDigest"] != domain.ScenarioRevisionSpecDigest(revision) {
			continue
		}
		nodes, valid, e := matchingHistoricalScenarioNodes(ctx, q, run, revision, byNode)
		if e != nil {
			return ScenarioHistoricalBaseline{}, e
		}
		if !valid {
			continue
		}
		candidates = append(candidates, ScenarioHistoricalBaseline{Run: run, Nodes: nodes, TestOnly: run.Kind == domain.RunScenarioTest, InstallationDigest: ScenarioComponentInstallationDigest(installed)})
	}
	if len(candidates) == 0 {
		return fail("no successful historical Run exactly matches current installations and frozen parameters")
	}
	if len(candidates) > 1 {
		return fail("multiple historical Runs match current installations; baseline cannot be chosen uniquely")
	}
	return candidates[0], nil
}

type historicalScenarioStep struct {
	NodeID       string                 `json:"nodeId"`
	SourceNodeID string                 `json:"sourceNodeId"`
	ComponentID  string                 `json:"componentId"`
	ReleaseID    string                 `json:"releaseId"`
	Action       domain.ActionKind      `json:"action"`
	Phase        string                 `json:"phase"`
	Variables    map[string]any         `json:"variables"`
	BackupRef    string                 `json:"backupRef"`
	Backup       *domain.BackupMetadata `json:"backup"`
}

func matchingHistoricalScenarioNodes(ctx context.Context, q queryer, run domain.Run, revision domain.ScenarioRevision, installed map[string]domain.EnvironmentComponentInstallation) ([]ScenarioHistoricalNode, bool, error) {
	raw, err := json.Marshal(run.InputSnapshot["steps"])
	if err != nil {
		return nil, false, err
	}
	var steps []historicalScenarioStep
	if err = json.Unmarshal(raw, &steps); err != nil || len(steps) == 0 {
		return nil, false, nil
	}
	resolved := map[string]ScenarioHistoricalNode{}
	for _, step := range steps {
		if step.NodeID == "" {
			return nil, false, nil
		}
		var succeeded int
		if err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_steps WHERE run_id=? AND node_id=? AND status='succeeded'`, run.ID, step.NodeID).Scan(&succeeded); err != nil {
			return nil, false, err
		}
		if succeeded != 1 {
			return nil, false, nil
		}
		if step.Action != domain.ActionInstall && step.Action != domain.ActionConfigure && step.Action != domain.ActionUpgrade {
			continue
		}
		nodeID := step.SourceNodeID
		if nodeID == "" {
			nodeID = step.NodeID
		}
		current, ok := installed[nodeID]
		if !ok || current.ReleaseID != step.ReleaseID || current.ComponentID != step.ComponentID {
			return nil, false, nil
		}
		if current.TestOnly != (run.Kind == domain.RunScenarioTest) {
			return nil, false, nil
		}
		if step.Backup == nil || step.BackupRef != current.BackupRef || step.Backup.InstallRunID != current.InstallRunID || step.Backup.InstallRunID != run.ID || !reflect.DeepEqual(*step.Backup, current.Backup) {
			return nil, false, nil
		}
		if step.Variables == nil {
			return nil, false, nil
		}
		if _, duplicate := resolved[nodeID]; duplicate {
			return nil, false, nil
		}
		resolved[nodeID] = ScenarioHistoricalNode{NodeID: nodeID, ComponentID: step.ComponentID, ReleaseID: step.ReleaseID, Variables: step.Variables}
	}
	if len(resolved) != len(revision.Graph.Nodes) {
		return nil, false, nil
	}
	out := make([]ScenarioHistoricalNode, 0, len(revision.Graph.Nodes))
	for _, node := range revision.Graph.Nodes {
		value, ok := resolved[node.ID]
		if !ok {
			return nil, false, nil
		}
		out = append(out, value)
	}
	return out, true, nil
}
