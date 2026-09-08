package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"codex/platform-demo/internal/domain"
)

// Diagnostic reads never produce an executable Run. Persisted identity, actual
// steps and approval remain readable even when execution inputs are damaged.
func (s *Store) GetRunDiagnosticRecord(ctx context.Context, id string) (domain.RunReadModel, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.RunReadModel{}, err
	}
	defer tx.Rollback()
	return getRunDiagnosticRecord(ctx, tx, id)
}

func getRunIdentity(ctx context.Context, q queryer, id string) (domain.RunReadModel, error) {
	r, err := scanRunReadModel(q.QueryRowContext(ctx, `SELECT `+runReadColumns+`,'[]','[]' FROM retained_run_history WHERE id=? AND cleaned=0`, id))
	return r, mapSQLError(err)
}

func getRunDiagnosticRecord(ctx context.Context, q queryer, id string) (domain.RunReadModel, error) {
	r, err := getRunIdentity(ctx, q, id)
	if err != nil {
		return r, err
	}
	r.Steps, err = listRunSteps(ctx, q, id)
	if err != nil {
		return r, err
	}
	a, err := scanApproval(q.QueryRowContext(ctx, `SELECT id,run_id,status,requested_at,decided_by,decision,reason,decided_at FROM approvals WHERE run_id=?`, id))
	if err == nil {
		r.Approval = &a
	} else if !errors.Is(err, domain.ErrNotFound) {
		return r, err
	}
	var raw sql.NullString
	if err = q.QueryRowContext(ctx, `SELECT json_extract(execution_snapshot_json,`+runSnapshotStepsPath+`) FROM runs WHERE id=?`, id).Scan(&raw); err != nil {
		return r, err
	}
	// Locked names/phase are optional diagnostic decoration. An invalid entry
	// must not hide actual step results or the failure recorded by the scheduler.
	var steps []json.RawMessage
	if json.Unmarshal([]byte(raw.String), &steps) == nil {
		for _, rawStep := range steps {
			var step domain.RunReadStep
			if json.Unmarshal(rawStep, &step) == nil && step.NodeID != "" {
				r.LockedSteps = append(r.LockedSteps, step)
			}
		}
	}
	return r, nil
}
