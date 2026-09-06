package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"database/sql"
	"strings"
)

// Scoped, batched read: work items need the digest but not the full Run snapshot.
func (s *Store) ComponentEvidenceReads(ctx context.Context, user domain.User, componentID string) ([]domain.Run, map[string]string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	where, args := runVisibility(user)
	where += ` AND runs.kind='component_test' AND runs.component_release_id IN (SELECT id FROM component_releases WHERE component_id=?)`
	args = append(args, componentID)
	selection := strings.Replace(runSelect, "input_snapshot_json", `json_object(
  'componentReleaseSpecDigest',component_spec_digest,
  'componentTestEvidence',component_evidence_kind,
  'parentSteps',json((SELECT json_group_array(json_object(
    'actionId',json_extract(value,'$.actionId'),'sourceNodeId',json_extract(value,'$.sourceNodeId'),
    'action',json_extract(value,'$.action'),'phase',json_extract(value,'$.phase')
  )) FROM json_each(runs.input_snapshot_json,'$.parentSteps'))),
  'steps',json((SELECT json_group_array(json_object(
    'parentActionId',json_extract(value,'$.parentActionId'),'sourceNodeId',json_extract(value,'$.sourceNodeId'),
    'phase',json_extract(value,'$.phase')
  )) FROM json_each(runs.input_snapshot_json,'$.steps')))
)`, 1)
	rows, err := tx.QueryContext(ctx, selection+` WHERE `+where+` ORDER BY COALESCE(finished_at,created_at) DESC,created_at DESC,id DESC`, args...)
	if err != nil {
		return nil, nil, err
	}
	runs := []domain.Run{}
	byID := map[string]int{}
	for rows.Next() {
		run, e := scanRun(rows)
		if e != nil {
			rows.Close()
			return nil, nil, e
		}
		byID[run.ID] = len(runs)
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT run_id,name FROM run_steps WHERE run_id IN (SELECT runs.id FROM runs WHERE `+where+`) ORDER BY rowid`, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		i, ok := byID[id]
		if ok {
			runs[i].Steps = append(runs[i].Steps, domain.RunStep{Name: name})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,name FROM environments WHERE id IN (SELECT environment_id FROM runs WHERE `+where+`)`, args...)
	if err != nil {
		return nil, nil, err
	}
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		names[id] = name
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	return runs, names, tx.Commit()
}
