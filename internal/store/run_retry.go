package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func retryRecoveryStateDigest(ctx context.Context, q queryer, environmentID string) (string, error) {
	receipts, err := latestEnvironmentActionReceipts(ctx, q, environmentID)
	if err != nil {
		return "", err
	}
	installed, err := scenarioInstallationsTx(ctx, q, environmentID)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Receipts      []ActionExecutionReceipt
		Installations string
	}{receipts, ScenarioComponentInstallationDigest(installed)})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func (s *Store) RetryRecoveryStateDigest(ctx context.Context, environmentID string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	return retryRecoveryStateDigest(ctx, tx, environmentID)
}

// This check runs in the queue transaction and immediately before execution.
// It binds recovery to all current component instances, including partial ones.
func validateRetryState(ctx context.Context, q queryer, run domain.Run) error {
	if run.RetryOfRunID == "" {
		return nil
	}
	var currentRevision string
	if err := q.QueryRowContext(ctx, `SELECT current_revision_id FROM environments WHERE id=? AND archived_at IS NULL`, run.EnvironmentID).Scan(&currentRevision); err != nil {
		return mapSQLError(err)
	}
	if currentRevision != run.EnvironmentRevisionID {
		return fmt.Errorf("%w: retry environment revision changed", domain.ErrConflict)
	}
	current, err := retryRecoveryStateDigest(ctx, q, run.EnvironmentID)
	if err != nil {
		return err
	}
	if current != run.InputSnapshot["retryRecoveryStateDigest"] {
		return fmt.Errorf("%w: recovery state changed after retry preview", domain.ErrConflict)
	}
	source, err := getRunRecord(ctx, q, run.RetryOfRunID)
	if err != nil {
		return err
	}
	root := source.RetryRootRunID
	if root == "" {
		root = source.ID
	}
	if run.RetryRootRunID != root || run.RetryAttempt != source.RetryAttempt+1 || source.EnvironmentRevisionID != run.EnvironmentRevisionID || source.RequestedBy != run.RequestedBy || source.Kind != run.Kind || source.ScenarioRevisionID != run.ScenarioRevisionID || source.ComponentReleaseID != run.ComponentReleaseID || source.ArtifactDigest != run.ArtifactDigest || (source.Status != domain.RunFailed && source.Status != domain.RunInterrupted) {
		return fmt.Errorf("%w: invalid retry lineage", domain.ErrConflict)
	}
	var newer int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE retry_root_run_id=? AND retry_attempt>=? AND id<>?`, root, run.RetryAttempt, run.ID).Scan(&newer); err != nil {
		return err
	}
	if newer > 0 {
		return fmt.Errorf("%w: a newer retry already exists", domain.ErrConflict)
	}
	return nil
}

func (s *Store) ValidateRetryState(ctx context.Context, run domain.Run) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return validateRetryState(ctx, tx, run)
}

type scenarioStageEvidence struct {
	runID string
	step  map[string]any
}

// A continuation has its own actual stages. Complete scenario evidence joins
// those stages to successful stages of its immutable source attempts.
func scenarioRetryEvidence(ctx context.Context, q queryer, run domain.Run) ([]scenarioStageEvidence, error) {
	chain := []domain.Run{run}
	seen := map[string]bool{run.ID: true}
	for current := run; current.RetryOfRunID != ""; {
		source, err := getRunRecord(ctx, q, current.RetryOfRunID)
		if err != nil {
			return nil, err
		}
		if seen[source.ID] || source.ScenarioRevisionID != run.ScenarioRevisionID || source.EnvironmentRevisionID != run.EnvironmentRevisionID || source.ArtifactDigest != run.ArtifactDigest || source.InputSnapshot["scenarioRevisionSpecDigest"] != run.InputSnapshot["scenarioRevisionSpecDigest"] || source.RetryAttempt+1 != current.RetryAttempt {
			return nil, fmt.Errorf("%w: scenario retry evidence differs from its source", domain.ErrConflict)
		}
		seen[source.ID] = true
		chain = append(chain, source)
		current = source
	}
	stages := []scenarioStageEvidence{}
	index := map[string]int{}
	for i := len(chain) - 1; i >= 0; i-- {
		steps, ok := chain[i].InputSnapshot["steps"].([]any)
		if !ok || len(steps) == 0 {
			return nil, domain.ErrInvalid
		}
		for _, raw := range steps {
			step, ok := raw.(map[string]any)
			if !ok {
				return nil, domain.ErrInvalid
			}
			id, _ := step["nodeId"].(string)
			if id == "" {
				return nil, domain.ErrInvalid
			}
			item := scenarioStageEvidence{chain[i].ID, step}
			if position, exists := index[id]; exists {
				stages[position] = item
			} else {
				index[id] = len(stages)
				stages = append(stages, item)
			}
		}
	}
	return stages, nil
}
