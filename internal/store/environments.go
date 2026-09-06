package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"codex/platform-demo/internal/domain"
)

type ActiveEnvironmentRun struct {
	ID     string
	Status domain.RunStatus
}

type EnvironmentLifecycleImpact struct {
	RevisionCount         int `json:"revisionCount"`
	RunCount              int `json:"runCount"`
	ActiveRunCount        int `json:"activeRunCount"`
	ImageBuildCount       int `json:"imageBuildCount"`
	ActiveImageBuildCount int `json:"activeImageBuildCount"`
	InstallationCount     int `json:"installationCount"`
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

func (s *Store) CreateEnvironment(ctx context.Context, e domain.Environment, r domain.EnvironmentRevision, writes ...EnvironmentRevisionWrite) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertEnvironmentTx(ctx, tx, e, r, writes...); err != nil {
		return err
	}
	return tx.Commit()
}

func environmentLifecycleImpact(ctx context.Context, q queryer, environmentID string) (EnvironmentLifecycleImpact, error) {
	var impact EnvironmentLifecycleImpact
	var exists int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM environments WHERE id=?`, environmentID).Scan(&exists); err != nil {
		return impact, err
	}
	if exists == 0 {
		return impact, domain.ErrNotFound
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM environment_revisions WHERE environment_id=?`, environmentID).Scan(&impact.RevisionCount); err != nil {
		return impact, err
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status IN ('running','awaiting_approval','queued') THEN 1 ELSE 0 END),0) FROM retained_run_history WHERE environment_id=?`, environmentID).Scan(&impact.RunCount, &impact.ActiveRunCount); err != nil {
		return impact, err
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status IN ('queued','running') THEN 1 ELSE 0 END),0) FROM component_image_builds WHERE environment_id=?`, environmentID).Scan(&impact.ImageBuildCount, &impact.ActiveImageBuildCount); err != nil {
		return impact, err
	}
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM environment_component_installations WHERE environment_id=?`, environmentID).Scan(&impact.InstallationCount); err != nil {
		return impact, err
	}
	return impact, nil
}

func (s *Store) EnvironmentLifecycleImpact(ctx context.Context, environmentID string) (EnvironmentLifecycleImpact, error) {
	return environmentLifecycleImpact(ctx, s.db, environmentID)
}

func insertEnvironmentLifecycleAudit(ctx context.Context, tx *sql.Tx, audit domain.AuditEvent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, audit.ID, audit.ActorID, audit.Action, audit.ResourceType, audit.ResourceID, jsonText(audit.Metadata), timeText(audit.CreatedAt))
	return mapSQLError(err)
}

// DeleteEnvironment permanently removes only environments that have never
// been used by a Run or image build and have no current installation baseline.
func (s *Store) DeleteEnvironment(ctx context.Context, environmentID string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	impact, err := environmentLifecycleImpact(ctx, tx, environmentID)
	if err != nil {
		return err
	}
	if impact.RunCount > 0 {
		return fmt.Errorf("%w: environment run history must be retained", domain.ErrConflict)
	}
	if impact.ImageBuildCount > 0 {
		return fmt.Errorf("%w: environment image build history must be retained", domain.ErrConflict)
	}
	if impact.InstallationCount > 0 {
		return fmt.Errorf("%w: environment installations must be rolled back first", domain.ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM environments WHERE id=?`, environmentID)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return domain.ErrNotFound
	}
	if err := insertEnvironmentLifecycleAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ArchiveEnvironment(ctx context.Context, environmentID string, archivedAt time.Time, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	impact, err := environmentLifecycleImpact(ctx, tx, environmentID)
	if err != nil {
		return err
	}
	if impact.ActiveRunCount > 0 {
		return fmt.Errorf("%w: active environment runs must finish first", domain.ErrConflict)
	}
	if impact.ActiveImageBuildCount > 0 {
		return fmt.Errorf("%w: active environment image builds must finish first", domain.ErrConflict)
	}
	if impact.InstallationCount > 0 {
		return fmt.Errorf("%w: environment installations must be rolled back first", domain.ErrConflict)
	}
	result, err := tx.ExecContext(ctx, `UPDATE environments SET archived_at=?,updated_at=? WHERE id=? AND archived_at IS NULL`, timeText(archivedAt), timeText(archivedAt), environmentID)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: environment is already archived", domain.ErrConflict)
	}
	if err := insertEnvironmentLifecycleAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UnarchiveEnvironment(ctx context.Context, environmentID string, restoredAt time.Time, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM environments WHERE id=?`, environmentID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return domain.ErrNotFound
	}
	result, err := tx.ExecContext(ctx, `UPDATE environments SET archived_at=NULL,updated_at=? WHERE id=? AND archived_at IS NOT NULL`, timeText(restoredAt), environmentID)
	if err != nil {
		return mapSQLError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: environment is not archived", domain.ErrConflict)
	}
	if err := insertEnvironmentLifecycleAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func insertEnvironmentRevision(ctx context.Context, tx *sql.Tx, r domain.EnvironmentRevision) error {
	inventory := string(r.Inventory)
	if inventory == "" {
		inventory = "{}"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO environment_revisions(id,environment_id,revision,facts_json,inventory_json,variables_json,parameters_json,credential_refs_json,created_by,change_reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, r.ID, r.EnvironmentID, r.Revision, jsonText(r.Facts), inventory, jsonText(r.Variables), jsonText(r.Parameters), jsonText(r.CredentialRefs), r.CreatedBy, r.ChangeReason, timeText(r.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) CreateEnvironmentRevision(ctx context.Context, r domain.EnvironmentRevision, writes ...EnvironmentRevisionWrite) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentID string
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(current_revision_id,'') FROM environments WHERE id=?", r.EnvironmentID).Scan(&currentID); err != nil {
		return mapSQLError(err)
	}
	var write EnvironmentRevisionWrite
	if len(writes) > 0 {
		write = writes[0]
	}
	if len(writes) > 0 && write.ExpectedCurrentRevisionID != currentID {
		return fmt.Errorf("%w: environment revision changed; refresh and retry", domain.ErrConflict)
	}
	var previous domain.EnvironmentRevision
	if currentID != "" {
		previous, err = environmentRevisionTx(ctx, tx, currentID)
		if err != nil {
			return err
		}
	}
	if write.RestoreSourceRevisionID != "" {
		source, err := environmentRevisionTx(ctx, tx, write.RestoreSourceRevisionID)
		if err != nil {
			return err
		}
		if source.EnvironmentID != r.EnvironmentID || !sameEnvironmentSnapshot(source, r) {
			return fmt.Errorf("%w: restore must reproduce the retained environment snapshot", domain.ErrConflict)
		}
	}
	if err := validateEnvironmentCatalogTx(ctx, tx, r, previous, write.RestoreSourceRevisionID != "", write.ValidateAllValues, write.RequireCompleteFacts); err != nil {
		return err
	}
	if err = insertEnvironmentRevision(ctx, tx, r); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE environments SET current_revision_id=?,updated_at=? WHERE id=? AND archived_at IS NULL`, r.ID, timeText(r.CreatedAt), r.EnvironmentID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var exists int
		if queryErr := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM environments WHERE id=?`, r.EnvironmentID).Scan(&exists); queryErr != nil {
			return queryErr
		}
		if exists == 0 {
			return domain.ErrNotFound
		}
		return fmt.Errorf("%w: environment is archived", domain.ErrConflict)
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
	var current, archived sql.NullString
	var cr, up string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,description,owner_id,current_revision_id,archived_at,created_at,updated_at FROM environments WHERE id=?`, id).Scan(&e.ID, &e.Name, &e.Description, &e.OwnerID, &current, &archived, &cr, &up)
	if err != nil {
		return e, mapSQLError(err)
	}
	e.CurrentRevisionID = current.String
	if archived.Valid {
		value := parseTime(archived.String)
		e.ArchivedAt = &value
	}
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

// EnvironmentArchived reads only lifecycle state. Long-running checks use it
// to translate a final-write trigger conflict without loading a second
// Environment Revision and accidentally changing the checked snapshot.
func (s *Store) EnvironmentArchived(ctx context.Context, environmentID string) (bool, error) {
	var archivedAt sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT archived_at FROM environments WHERE id=?`, environmentID).Scan(&archivedAt); err != nil {
		return false, mapSQLError(err)
	}
	return archivedAt.Valid, nil
}

func (s *Store) ListEnvironments(ctx context.Context, includeArchived ...bool) ([]domain.Environment, error) {
	query := `SELECT id,name,description,owner_id,current_revision_id,archived_at,created_at,updated_at FROM environments`
	if len(includeArchived) == 0 || !includeArchived[0] {
		query += ` WHERE archived_at IS NULL`
	}
	query += ` ORDER BY archived_at IS NOT NULL,name`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Environment
	for rows.Next() {
		var e domain.Environment
		var current, archived sql.NullString
		var cr, up string
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.OwnerID, &current, &archived, &cr, &up); err != nil {
			return nil, err
		}
		e.CurrentRevisionID = current.String
		if archived.Valid {
			value := parseTime(archived.String)
			e.ArchivedAt = &value
		}
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
		ssh, sshErr := s.LatestEnvironmentSSHCheck(ctx, out[i].ID)
		if sshErr == nil {
			out[i].SSHCheck = &ssh
		} else if sshErr != domain.ErrNotFound {
			return nil, sshErr
		}
	}
	return out, nil
}

func scanEnvironmentRevision(row scanner) (domain.EnvironmentRevision, error) {
	var r domain.EnvironmentRevision
	var facts, inventory, variables, parameters, refs, created string
	err := row.Scan(&r.ID, &r.EnvironmentID, &r.Revision, &facts, &inventory, &variables, &parameters, &refs, &r.CreatedBy, &r.ChangeReason, &created)
	r.Facts = decodeJSON(facts, map[string]any{})
	r.Inventory = []byte(inventory)
	r.Variables = decodeJSON(variables, map[string]string{})
	r.Parameters = decodeJSON(parameters, map[string]any{})
	r.CredentialRefs = decodeJSON(refs, []domain.CredentialRef{})
	r.CreatedAt = parseTime(created)
	return r, err
}

func (s *Store) GetEnvironmentRevision(ctx context.Context, id string) (domain.EnvironmentRevision, error) {
	r, err := scanEnvironmentRevision(s.db.QueryRowContext(ctx, `SELECT id,environment_id,revision,facts_json,inventory_json,variables_json,parameters_json,credential_refs_json,created_by,change_reason,created_at FROM environment_revisions WHERE id=?`, id))
	return r, mapSQLError(err)
}

func (s *Store) ListEnvironmentRevisions(ctx context.Context, environmentID string) ([]domain.EnvironmentRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,environment_id,revision,facts_json,inventory_json,variables_json,parameters_json,credential_refs_json,created_by,change_reason,created_at FROM environment_revisions WHERE environment_id=? ORDER BY revision DESC`, environmentID)
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

func (s *Store) SaveEnvironmentSSHCheck(ctx context.Context, check domain.EnvironmentSSHCheck) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO environment_ssh_checks(id,environment_id,environment_revision_id,status,duration_ms,results_json,checked_at) VALUES(?,?,?,?,?,?,?)`, check.ID, check.EnvironmentID, check.EnvironmentRevisionID, check.Status, check.DurationMS, jsonText(check.Results), timeText(check.CheckedAt))
	return mapSQLError(err)
}

func (s *Store) LatestEnvironmentSSHCheck(ctx context.Context, environmentID string) (domain.EnvironmentSSHCheck, error) {
	var check domain.EnvironmentSSHCheck
	var results, checkedAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,environment_id,environment_revision_id,status,duration_ms,results_json,checked_at FROM environment_ssh_checks WHERE environment_id=? ORDER BY checked_at DESC LIMIT 1`, environmentID).Scan(&check.ID, &check.EnvironmentID, &check.EnvironmentRevisionID, &check.Status, &check.DurationMS, &results, &checkedAt)
	if err != nil {
		return check, mapSQLError(err)
	}
	check.Results = decodeJSON(results, []domain.EnvironmentSSHHostCheck{})
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

func insertEnvironmentTx(ctx context.Context, tx *sql.Tx, e domain.Environment, r domain.EnvironmentRevision, writes ...EnvironmentRevisionWrite) error {
	requireComplete := len(writes) > 0 && writes[0].RequireCompleteFacts
	if len(writes) > 0 {
	}
	if err := validateEnvironmentCatalogTx(ctx, tx, r, domain.EnvironmentRevision{}, false, true, requireComplete); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO environments(id,name,description,owner_id,current_revision_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, e.ID, e.Name, e.Description, e.OwnerID, r.ID, timeText(e.CreatedAt), timeText(e.UpdatedAt))
	if err != nil {
		return mapSQLError(err)
	}
	if err := insertEnvironmentRevision(ctx, tx, r); err != nil {
		return err
	}
	return nil
}
