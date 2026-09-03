package store

import (
	"context"
	"database/sql"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func (s *Store) ListEnvironmentParameterDefinitions(ctx context.Context) ([]domain.EnvironmentParameterDefinition, error) {
	return listEnvironmentParameterDefinitions(ctx, s.db)
}

func listEnvironmentParameterDefinitions(ctx context.Context, q queryer) ([]domain.EnvironmentParameterDefinition, error) {
	rows, err := q.QueryContext(ctx, `
SELECT d.id,d.technical_key,d.label,d.description,d.parameter_type,d.enum_json,d.min_length,d.created_by,d.created_at,
  COALESCE((SELECT value_json FROM environment_parameter_defaults WHERE definition_id=d.id),'null'),
  (SELECT COUNT(*) FROM component_releases r,json_each(r.parameters_json) p
   WHERE json_extract(p.value,'$.environmentBinding.definitionId')=d.id)
FROM environment_parameter_definitions d ORDER BY d.label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.EnvironmentParameterDefinition{}
	for rows.Next() {
		var item domain.EnvironmentParameterDefinition
		var enumJSON, created, defaultJSON string
		if err := rows.Scan(&item.ID, &item.Key, &item.Label, &item.Description, &item.Type, &enumJSON, &item.MinLength, &item.CreatedBy, &created, &defaultJSON, &item.Usage); err != nil {
			return nil, err
		}
		item.Enum = decodeJSON(enumJSON, []any{})
		item.DefaultValue = decodeJSON[any](defaultJSON, nil)
		item.CreatedAt = parseTime(created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetEnvironmentParameterDefinition(ctx context.Context, id string) (domain.EnvironmentParameterDefinition, error) {
	return getEnvironmentParameterDefinition(ctx, s.db, id)
}

func getEnvironmentParameterDefinition(ctx context.Context, q queryer, id string) (domain.EnvironmentParameterDefinition, error) {
	var item domain.EnvironmentParameterDefinition
	var enumJSON, created, defaultJSON string
	err := q.QueryRowContext(ctx, `SELECT id,technical_key,label,description,parameter_type,enum_json,min_length,created_by,created_at,COALESCE((SELECT value_json FROM environment_parameter_defaults WHERE definition_id=d.id),'null') FROM environment_parameter_definitions d WHERE id=?`, id).
		Scan(&item.ID, &item.Key, &item.Label, &item.Description, &item.Type, &enumJSON, &item.MinLength, &item.CreatedBy, &created, &defaultJSON)
	item.Enum = decodeJSON(enumJSON, []any{})
	item.DefaultValue = decodeJSON[any](defaultJSON, nil)
	item.CreatedAt = parseTime(created)
	return item, mapSQLError(err)
}

func (s *Store) CreateEnvironmentParameterDefinition(ctx context.Context, item domain.EnvironmentParameterDefinition, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := domain.ValidateEnvironmentParameterDefault(item); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO environment_parameter_definitions(id,technical_key,label,description,parameter_type,enum_json,min_length,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, item.ID, item.Key, item.Label, item.Description, item.Type, jsonText(item.Enum), item.MinLength, item.CreatedBy, timeText(item.CreatedAt)); err != nil {
		return mapSQLError(err)
	}
	if err = insertParameterRegistryAudit(ctx, tx, audit); err != nil {
		return err
	}
	if err := saveEnvironmentParameterDefault(ctx, tx, item.ID, item.DefaultValue); err != nil {
		return err
	}
	return tx.Commit()
}

func saveEnvironmentParameterDefault(ctx context.Context, tx *sql.Tx, id string, value any) error {
	if value == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM environment_parameter_defaults WHERE definition_id=?`, id)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO environment_parameter_defaults(definition_id,value_json) VALUES(?,?) ON CONFLICT(definition_id) DO UPDATE SET value_json=excluded.value_json`, id, jsonText(value))
	return mapSQLError(err)
}

func (s *Store) UpdateEnvironmentParameterDefault(ctx context.Context, id string, value any, audit domain.AuditEvent) (domain.EnvironmentParameterDefinition, error) {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return domain.EnvironmentParameterDefinition{}, err
	}
	defer tx.Rollback()
	item, err := getEnvironmentParameterDefinition(ctx, tx, id)
	if err != nil {
		return item, err
	}
	item.DefaultValue = value
	if err := domain.ValidateEnvironmentParameterDefault(item); err != nil {
		return item, err
	}
	if err := saveEnvironmentParameterDefault(ctx, tx, id, value); err != nil {
		return item, err
	}
	if err := insertParameterRegistryAudit(ctx, tx, audit); err != nil {
		return item, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1`); err != nil {
		return item, err
	}
	return item, tx.Commit()
}

func (s *Store) UpsertEnvironmentParameterDefinition(ctx context.Context, item domain.EnvironmentParameterDefinition) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := domain.ValidateEnvironmentParameterDefault(item); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO environment_parameter_definitions(id,technical_key,label,description,parameter_type,enum_json,min_length,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET technical_key=excluded.technical_key,label=excluded.label,description=excluded.description,parameter_type=excluded.parameter_type,enum_json=excluded.enum_json,min_length=excluded.min_length`, item.ID, item.Key, item.Label, item.Description, item.Type, jsonText(item.Enum), item.MinLength, item.CreatedBy, timeText(item.CreatedAt))
	if err != nil {
		return mapSQLError(err)
	}
	if err := saveEnvironmentParameterDefault(ctx, tx, item.ID, item.DefaultValue); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteEnvironmentParameterDefinition(ctx context.Context, id string, audit domain.AuditEvent) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var releaseUsage, revisionUsage int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM component_releases r,json_each(r.parameters_json) p WHERE json_extract(p.value,'$.environmentBinding.definitionId')=?`, id).Scan(&releaseUsage); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM environment_revisions r,json_each(r.parameters_json) p WHERE p.key=?`, "global:"+id).Scan(&revisionUsage); err != nil {
		return err
	}
	if releaseUsage+revisionUsage > 0 {
		return fmt.Errorf("%w: environment parameter definition is used by %d release(s) and %d environment revision(s)", domain.ErrConflict, releaseUsage, revisionUsage)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM environment_parameter_definitions WHERE id=?`, id)
	if err != nil {
		return mapSQLError(err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return domain.ErrNotFound
	}
	if err = insertParameterRegistryAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListEnvironmentVariableDefinitions(ctx context.Context) ([]domain.EnvironmentVariableDefinition, error) {
	return listEnvironmentVariableDefinitions(ctx, s.db)
}

func listEnvironmentVariableDefinitions(ctx context.Context, q queryer) ([]domain.EnvironmentVariableDefinition, error) {
	rows, err := q.QueryContext(ctx, `
SELECT d.id,d.variable_name,d.label,d.description,d.created_by,d.created_at,
  (SELECT COUNT(*) FROM environment_revisions r,json_each(r.variables_json) v WHERE v.key=d.variable_name)
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
