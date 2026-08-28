package store

import (
	"context"
	"database/sql"

	"codex/platform-demo/internal/domain"
)

type ActiveEnvironmentRun struct {
	ID     string
	Status domain.RunStatus
}

// GetActiveEnvironmentRun keeps serialized-Run selection behind the
// Environment Store port instead of duplicating SQL in the HTTP layer.
func (s *Store) GetActiveEnvironmentRun(ctx context.Context, environmentID string) (ActiveEnvironmentRun, error) {
	var result ActiveEnvironmentRun
	err := s.db.QueryRowContext(ctx, `SELECT id,status FROM runs WHERE environment_id=? AND status IN ('running','awaiting_approval','queued') ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'awaiting_approval' THEN 1 ELSE 2 END, created_at LIMIT 1`, environmentID).Scan(&result.ID, &result.Status)
	if err == sql.ErrNoRows {
		return result, domain.ErrNotFound
	}
	return result, err
}

func (s *Store) CreateEnvironment(ctx context.Context, e domain.Environment, r domain.EnvironmentRevision) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO environments(id,name,description,owner_id,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, e.ID, e.Name, e.Description, e.OwnerID, r.ID, timeText(e.CreatedAt), timeText(e.UpdatedAt))
	if err != nil {
		return mapSQLError(err)
	}
	if err = insertEnvironmentRevision(ctx, tx, r); err != nil {
		return err
	}
	return tx.Commit()
}

func insertEnvironmentRevision(ctx context.Context, tx *sql.Tx, r domain.EnvironmentRevision) error {
	inventory := string(r.Inventory)
	if inventory == "" {
		inventory = "{}"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO environment_revisions(id,environment_id,revision,facts_json,inventory_json,variables_json,credential_refs_json,created_by,change_reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, r.ID, r.EnvironmentID, r.Revision, jsonText(r.Facts), inventory, jsonText(r.Variables), jsonText(r.CredentialRefs), r.CreatedBy, r.ChangeReason, timeText(r.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) CreateEnvironmentRevision(ctx context.Context, r domain.EnvironmentRevision) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = insertEnvironmentRevision(ctx, tx, r); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE environments SET current_revision_id=?,updated_at=? WHERE id=?`, r.ID, timeText(r.CreatedAt), r.EnvironmentID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) UpdateEnvironmentMetadata(ctx context.Context, e domain.Environment) error {
	res, err := s.db.ExecContext(ctx, `UPDATE environments SET name=?,description=?,updated_at=? WHERE id=?`, e.Name, e.Description, timeText(e.UpdatedAt), e.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) GetEnvironment(ctx context.Context, id string, includeRevisions bool) (domain.Environment, error) {
	var e domain.Environment
	var current sql.NullString
	var cr, up string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,description,owner_id,current_revision_id,created_at,updated_at FROM environments WHERE id=?`, id).Scan(&e.ID, &e.Name, &e.Description, &e.OwnerID, &current, &cr, &up)
	if err != nil {
		return e, mapSQLError(err)
	}
	e.CurrentRevisionID = current.String
	e.CreatedAt = parseTime(cr)
	e.UpdatedAt = parseTime(up)
	if current.Valid {
		r, x := s.GetEnvironmentRevision(ctx, current.String)
		if x != nil {
			return e, x
		}
		e.Revision = &r
	}
	if includeRevisions {
		e.Revisions, err = s.ListEnvironmentRevisions(ctx, id)
	}
	return e, err
}

func (s *Store) ListEnvironments(ctx context.Context) ([]domain.Environment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,owner_id,current_revision_id,created_at,updated_at FROM environments ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Environment
	for rows.Next() {
		var e domain.Environment
		var current sql.NullString
		var cr, up string
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.OwnerID, &current, &cr, &up); err != nil {
			return nil, err
		}
		e.CurrentRevisionID = current.String
		e.CreatedAt = parseTime(cr)
		e.UpdatedAt = parseTime(up)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].CurrentRevisionID != "" {
			r, x := s.GetEnvironmentRevision(ctx, out[i].CurrentRevisionID)
			if x != nil {
				return nil, x
			}
			out[i].Revision = &r
		}
		out[i].Revisions, err = s.ListEnvironmentRevisions(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		health, healthErr := s.LatestEnvironmentHealthCheck(ctx, out[i].ID)
		if healthErr == nil {
			out[i].HealthCheck = &health
		} else if healthErr != domain.ErrNotFound {
			return nil, healthErr
		}
	}
	return out, nil
}

func scanEnvironmentRevision(row scanner) (domain.EnvironmentRevision, error) {
	var r domain.EnvironmentRevision
	var facts, inventory, variables, refs, created string
	err := row.Scan(&r.ID, &r.EnvironmentID, &r.Revision, &facts, &inventory, &variables, &refs, &r.CreatedBy, &r.ChangeReason, &created)
	r.Facts = decodeJSON(facts, map[string]any{})
	r.Inventory = []byte(inventory)
	r.Variables = decodeJSON(variables, map[string]string{})
	r.CredentialRefs = decodeJSON(refs, []domain.CredentialRef{})
	r.CreatedAt = parseTime(created)
	return r, err
}

func (s *Store) GetEnvironmentRevision(ctx context.Context, id string) (domain.EnvironmentRevision, error) {
	r, err := scanEnvironmentRevision(s.db.QueryRowContext(ctx, `SELECT id,environment_id,revision,facts_json,inventory_json,variables_json,credential_refs_json,created_by,change_reason,created_at FROM environment_revisions WHERE id=?`, id))
	return r, mapSQLError(err)
}

func (s *Store) ListEnvironmentRevisions(ctx context.Context, environmentID string) ([]domain.EnvironmentRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,environment_id,revision,facts_json,inventory_json,variables_json,credential_refs_json,created_by,change_reason,created_at FROM environment_revisions WHERE environment_id=? ORDER BY revision DESC`, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.EnvironmentRevision
	for rows.Next() {
		r, err := scanEnvironmentRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SaveEnvironmentHealthCheck(ctx context.Context, check domain.EnvironmentHealthCheck) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO environment_health_checks(id,environment_id,environment_revision_id,status,results_json,checked_at) VALUES(?,?,?,?,?,?)`, check.ID, check.EnvironmentID, check.EnvironmentRevisionID, check.Status, jsonText(check.Results), timeText(check.CheckedAt))
	return mapSQLError(err)
}

func (s *Store) LatestEnvironmentHealthCheck(ctx context.Context, environmentID string) (domain.EnvironmentHealthCheck, error) {
	var check domain.EnvironmentHealthCheck
	var results, checkedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,environment_id,environment_revision_id,status,results_json,checked_at FROM environment_health_checks WHERE environment_id=? ORDER BY checked_at DESC LIMIT 1`, environmentID).Scan(&check.ID, &check.EnvironmentID, &check.EnvironmentRevisionID, &check.Status, &results, &checkedAt)
	if err != nil {
		return check, mapSQLError(err)
	}
	check.Results = decodeJSON(results, []domain.EnvironmentEndpointCheck{})
	check.CheckedAt = parseTime(checkedAt)
	return check, nil
}

func (s *Store) NextEnvironmentRevision(ctx context.Context, environmentID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM environment_revisions WHERE environment_id=?`, environmentID).Scan(&n)
	return n, err
}
func (s *Store) NextScenarioRevision(ctx context.Context, scenarioID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM scenario_revisions WHERE scenario_id=?`, scenarioID).Scan(&n)
	return n, err
}
