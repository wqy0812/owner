package store

import (
	"context"
	"database/sql"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func (s *Store) ListEnvironmentVariableDefinitions(ctx context.Context) ([]domain.EnvironmentVariableDefinition, error) {
	return listEnvironmentVariableDefinitions(ctx, s.db, true)
}

func listEnvironmentVariableDefinitions(ctx context.Context, q queryer, includeUsage bool) ([]domain.EnvironmentVariableDefinition, error) {
	usage := `0`
	if includeUsage {
		usage = `(SELECT COUNT(*) FROM environment_revisions r,json_each(r.variables_json) v WHERE v.key=d.variable_name)`
	}
	rows, err := q.QueryContext(ctx, `
SELECT d.id,d.variable_name,d.label,d.description,d.created_by,d.created_at,
  `+usage+`
FROM environment_variable_definitions d ORDER BY d.variable_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.EnvironmentVariableDefinition{}
	for rows.Next() {
		var item domain.EnvironmentVariableDefinition
		var created string
		if err := rows.Scan(&item.ID, &item.Name, &item.Label, &item.Description, &item.CreatedBy, &created, &item.Usage); err != nil {
			return nil, err
		}
		item.CreatedAt = parseTime(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateEnvironmentVariableDefinition(ctx context.Context, item domain.EnvironmentVariableDefinition, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO environment_variable_definitions(id,variable_name,label,description,created_by,created_at) VALUES(?,?,?,?,?,?)`, item.ID, item.Name, item.Label, item.Description, item.CreatedBy, timeText(item.CreatedAt)); err != nil {
		return mapSQLError(err)
	}
	if err = insertParameterRegistryAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpsertEnvironmentVariableDefinition(ctx context.Context, item domain.EnvironmentVariableDefinition) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO environment_variable_definitions(id,variable_name,label,description,created_by,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET variable_name=excluded.variable_name,label=excluded.label,description=excluded.description`, item.ID, item.Name, item.Label, item.Description, item.CreatedBy, timeText(item.CreatedAt))
	return mapSQLError(err)
}

func (s *Store) DeleteEnvironmentVariableDefinition(ctx context.Context, id string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var name string
	if err = tx.QueryRowContext(ctx, `SELECT variable_name FROM environment_variable_definitions WHERE id=?`, id).Scan(&name); err != nil {
		return mapSQLError(err)
	}
	var usage int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM environment_revisions r,json_each(r.variables_json) v WHERE v.key=?`, name).Scan(&usage); err != nil {
		return err
	}
	if usage > 0 {
		return fmt.Errorf("%w: environment variable definition is used by %d environment revision(s)", domain.ErrConflict, usage)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM environment_variable_definitions WHERE id=?`, id); err != nil {
		return mapSQLError(err)
	}
	if err = insertParameterRegistryAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func insertParameterRegistryAudit(ctx context.Context, tx *sql.Tx, audit domain.AuditEvent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_id,action,resource_type,resource_id,metadata_json,created_at) VALUES(?,?,?,?,?,?,?)`, audit.ID, audit.ActorID, audit.Action, audit.ResourceType, audit.ResourceID, jsonText(audit.Metadata), timeText(audit.CreatedAt))
	return mapSQLError(err)
}
