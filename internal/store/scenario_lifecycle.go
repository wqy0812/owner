package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex/platform-demo/internal/domain"
)

// HasActiveScenarioRevisionRun is the read-side edit gate; mutations retain
// their transaction-level check in validateScenarioEditableTx.
func (s *Store) HasActiveScenarioRevisionRun(ctx context.Context, revisionID string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE scenario_revision_id=? AND status IN ('awaiting_approval','queued','running'))`, revisionID).Scan(&active)
	return active, err
}

// Keep the legacy revision columns unchanged so historic digests and records
// survive conversion. The versioned lifecycle definition lives alongside them.
func scenarioLifecycleJSON(revision domain.ScenarioRevision) string {
	raw, _ := json.Marshal(revision)
	values := map[string]any{}
	_ = json.Unmarshal(raw, &values)
	for _, key := range []string{"id", "scenarioId", "revision", "status", "graph", "environmentConstraints", "createdAt", "testPassedAt", "releasedAt", "deprecatedAt", "abandonedAt"} {
		delete(values, key)
	}
	return jsonText(values)
}
func decodeScenarioLifecycle(raw string, revision domain.ScenarioRevision) domain.ScenarioRevision {
	_ = json.Unmarshal([]byte(raw), &revision)
	return revision
}

func validateScenarioEditableTx(ctx context.Context, tx *sql.Tx, revision domain.ScenarioRevision) error {
	if revision.Status != domain.RevisionDraft && revision.Status != domain.RevisionTesting && revision.Status != domain.RevisionTestPassed {
		return fmt.Errorf("%w: published scenario revisions are immutable", domain.ErrConflict)
	}
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT current_revision_id FROM scenarios WHERE id=?`, revision.ScenarioID).Scan(&current); err != nil {
		return mapSQLError(err)
	}
	if current != revision.ID {
		return fmt.Errorf("%w: only the current revision may be edited", domain.ErrConflict)
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE scenario_revision_id=? AND status IN ('awaiting_approval','queued','running'))`, revision.ID).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return fmt.Errorf("%w: scenario revision has an active run", domain.ErrConflict)
	}
	return nil
}

// SaveScenarioRevisionDefinition atomically protects every editor against Run
// creation, stale workspaces, and stale graph/acceptance contracts.
func (s *Store) SaveScenarioRevisionDefinition(ctx context.Context, revision domain.ScenarioRevision, expectedDigest string) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prior, err := getScenarioRevision(ctx, tx, revision.ID)
	if err != nil {
		return err
	}
	if err = validateScenarioEditableTx(ctx, tx, prior); err != nil {
		return err
	}
	if expectedDigest == "" || expectedDigest != domain.ScenarioRevisionSpecDigest(prior) {
		return fmt.Errorf("%w: scenario definition changed; reload before saving", domain.ErrConflict)
	}
	if revision.ScenarioID != prior.ScenarioID || revision.SourceRevisionID != prior.SourceRevisionID || revision.SourceRunID != prior.SourceRunID {
		return fmt.Errorf("%w: scenario source identity is immutable", domain.ErrConflict)
	}
	if err = validateScenarioAdaptationTx(ctx, tx, revision, prior.EnvironmentConstraints, false); err != nil {
		return err
	}
	if err = validateScenarioCatalogTx(ctx, tx, revision.Graph, prior.Graph); err != nil {
		return err
	}
	revision.DigestVersion = domain.ScenarioDigestVersion
	_, err = tx.ExecContext(ctx, `UPDATE scenario_revisions SET graph_json=?,environment_constraints_json=?,lifecycle_json=?,status='draft',test_passed_at=NULL,publication_generation=publication_generation+1 WHERE id=?`, jsonText(revision.Graph), jsonText(domain.NormalizeEnvironmentConstraints(revision.EnvironmentConstraints)), scenarioLifecycleJSON(revision), revision.ID)
	if err != nil {
		return mapSQLError(err)
	}
	return tx.Commit()
}

func (s *Store) SuccessfulScenarioSourceRun(ctx context.Context, revision domain.ScenarioRevision, runID string) (domain.Run, error) {
	return successfulScenarioSourceRun(ctx, s.db, revision, runID)
}
func successfulScenarioSourceRun(ctx context.Context, q queryer, revision domain.ScenarioRevision, runID string) (domain.Run, error) {
	if revision.Status != domain.RevisionReleased || len(revision.Graph.Nodes) == 0 || len(revision.AcceptanceJobs) == 0 || revision.DigestVersion < domain.ScenarioDigestVersion {
		return domain.Run{}, fmt.Errorf("%w: source needs a released version with business acceptance", domain.ErrConflict)
	}
	query := strings.Replace(runSelect, "FROM runs", "FROM retained_run_history runs", 1) + ` WHERE scenario_revision_id=? AND kind='scenario_run' AND status='succeeded' AND json_extract(input_snapshot_json,'$.executionMode') IN ('install','upgrade') AND json_extract(input_snapshot_json,'$.scenarioRevisionSpecDigest')=? AND json_array_length(input_snapshot_json,'$.acceptanceJobIds')=?`
	args := []any{revision.ID, domain.ScenarioRevisionSpecDigest(revision), len(revision.AcceptanceJobs)}
	if runID != "" {
		query += ` AND id=?`
		args = append(args, runID)
	}
	query += ` ORDER BY created_at DESC,id DESC`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return domain.Run{}, err
	}
	var candidates []domain.Run
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			rows.Close()
			return domain.Run{}, scanErr
		}
		candidates = append(candidates, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return domain.Run{}, err
	}
	for _, run := range candidates {
		ids, _ := run.InputSnapshot["acceptanceJobIds"].([]any)
		locked := map[string]bool{}
		for _, id := range ids {
			if value, ok := id.(string); ok {
				locked[value] = true
			}
		}
		complete := len(locked) == len(revision.AcceptanceJobs)
		for _, job := range revision.AcceptanceJobs {
			if !locked[job.ID] {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		if err = validateScenarioSourceEvidence(ctx, q, run, revision); err != nil {
			if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrNotFound) {
				continue
			}
			return domain.Run{}, err
		}
		return run, nil
	}
	return domain.Run{}, fmt.Errorf("%w: source needs a successful formal install or upgrade including every business acceptance job", domain.ErrConflict)
}

// A successful status is insufficient provenance: retain the same complete
// component and acceptance evidence required when completing the Run.
func validateScenarioSourceEvidence(ctx context.Context, q queryer, run domain.Run, revision domain.ScenarioRevision) error {
	if err := validateScenarioAcceptanceEvidence(ctx, q, run, revision); err != nil {
		return err
	}
	locks, err := releaseLocksFromRunSnapshot(run.InputSnapshot)
	if err != nil {
		return err
	}
	releases := make(map[string]domain.ComponentRelease, len(locks))
	for releaseID, digest := range locks {
		release, getErr := getComponentRelease(ctx, q, releaseID)
		if getErr != nil {
			return getErr
		}
		if domain.ComponentReleaseSpecDigest(release) != digest {
			return fmt.Errorf("%w: source Run locked a different definition of release %s", domain.ErrConflict, releaseID)
		}
		releases[releaseID] = release
	}
	nodes := make(map[string]domain.ScenarioNode, len(revision.Graph.Nodes))
	for _, node := range revision.Graph.Nodes {
		if _, covered := releases[node.ReleaseID]; !covered {
			return fmt.Errorf("%w: source Run does not cover target release %s", domain.ErrConflict, node.ReleaseID)
		}
		nodes[node.ID] = node
	}
	steps, _ := run.InputSnapshot["steps"].([]any)
	for _, raw := range steps {
		step := raw.(map[string]any) // validated by validateScenarioAcceptanceEvidence
		if step["stage"] != "target_verify" {
			continue
		}
		nodeID, _ := step["sourceNodeId"].(string)
		node, exists := nodes[nodeID]
		if !exists || step["sourceType"] == "scenario_acceptance" || step["releaseId"] != node.ReleaseID || step["componentId"] != releases[node.ReleaseID].ComponentID {
			return fmt.Errorf("%w: source Run target check does not match node %s", domain.ErrConflict, nodeID)
		}
	}
	return nil
}

func (s *Store) GetScenarioInstallation(ctx context.Context, environmentID, scenarioID string) (domain.ScenarioInstallation, error) {
	return getScenarioInstallation(ctx, s.db, environmentID, scenarioID)
}
func getScenarioInstallation(ctx context.Context, q queryer, environmentID, scenarioID string) (domain.ScenarioInstallation, error) {
	var v domain.ScenarioInstallation
	var updated string
	err := q.QueryRowContext(ctx, `SELECT environment_id,scenario_id,COALESCE(revision_id,''),COALESCE(run_id,''),COALESCE(mutating_run_id,''),state,test_only,installation_digest,generation,updated_at FROM scenario_installations WHERE environment_id=? AND scenario_id=?`, environmentID, scenarioID).Scan(&v.EnvironmentID, &v.ScenarioID, &v.RevisionID, &v.RunID, &v.MutatingRunID, &v.State, &v.TestOnly, &v.InstallationDigest, &v.Generation, &updated)
	v.UpdatedAt = parseTime(updated)
	return v, mapSQLError(err)
}
func (s *Store) SaveScenarioInstallation(ctx context.Context, v domain.ScenarioInstallation, expectedGeneration int64) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var result sql.Result
	if expectedGeneration == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO scenario_installations(environment_id,scenario_id,revision_id,run_id,mutating_run_id,state,test_only,installation_digest,generation,updated_at) VALUES(?,?,?,?,?,?,?,?,1,?) ON CONFLICT(environment_id,scenario_id) DO NOTHING`, v.EnvironmentID, v.ScenarioID, nullString(v.RevisionID), nullString(v.RunID), nullString(v.MutatingRunID), v.State, v.TestOnly, v.InstallationDigest, timeText(v.UpdatedAt))
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE scenario_installations SET revision_id=?,run_id=?,mutating_run_id=?,state=?,test_only=?,installation_digest=?,generation=generation+1,updated_at=? WHERE environment_id=? AND scenario_id=? AND generation=?`, nullString(v.RevisionID), nullString(v.RunID), nullString(v.MutatingRunID), v.State, v.TestOnly, v.InstallationDigest, timeText(v.UpdatedAt), v.EnvironmentID, v.ScenarioID, expectedGeneration)
	}
	if err != nil {
		return mapSQLError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: scenario installation baseline changed", domain.ErrConflict)
	}
	return tx.Commit()
}
