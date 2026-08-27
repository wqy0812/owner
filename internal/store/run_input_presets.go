package store

import (
	"context"

	"codex/platform-demo/internal/domain"
)

func scanRunInputPreset(row scanner) (domain.RunInputPreset, error) {
	var preset domain.RunInputPreset
	var values, created, updated string
	err := row.Scan(&preset.ID, &preset.CreatedBy, &preset.ResourceType, &preset.ResourceID, &preset.Context, &preset.Name, &values, &preset.DefinitionDigest, &created, &updated)
	preset.Values = decodeJSON(values, map[string]any{})
	preset.CreatedAt, preset.UpdatedAt = parseTime(created), parseTime(updated)
	return preset, err
}

const runInputPresetSelect = `SELECT id,created_by,resource_type,resource_id,context,name,values_json,definition_digest,created_at,updated_at FROM run_input_presets`

func (s *Store) ListRunInputPresets(ctx context.Context, createdBy, resourceType, resourceID, presetContext string) ([]domain.RunInputPreset, error) {
	rows, err := s.db.QueryContext(ctx, runInputPresetSelect+` WHERE created_by=? AND resource_type=? AND resource_id=? AND context=? ORDER BY name`, createdBy, resourceType, resourceID, presetContext)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	output := make([]domain.RunInputPreset, 0)
	for rows.Next() {
		preset, scanErr := scanRunInputPreset(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		output = append(output, preset)
	}
	return output, rows.Err()
}

func (s *Store) GetRunInputPreset(ctx context.Context, id string) (domain.RunInputPreset, error) {
	preset, err := scanRunInputPreset(s.db.QueryRowContext(ctx, runInputPresetSelect+` WHERE id=?`, id))
	return preset, mapSQLError(err)
}

func (s *Store) SaveRunInputPreset(ctx context.Context, preset domain.RunInputPreset) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_input_presets(id,created_by,resource_type,resource_id,context,name,values_json,definition_digest,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,values_json=excluded.values_json,definition_digest=excluded.definition_digest,updated_at=excluded.updated_at WHERE created_by=excluded.created_by`, preset.ID, preset.CreatedBy, preset.ResourceType, preset.ResourceID, preset.Context, preset.Name, jsonText(preset.Values), preset.DefinitionDigest, timeText(preset.CreatedAt), timeText(preset.UpdatedAt))
	return mapSQLError(err)
}

func (s *Store) DeleteRunInputPreset(ctx context.Context, id, createdBy string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM run_input_presets WHERE id=? AND created_by=?`, id, createdBy)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return domain.ErrNotFound
	}
	return nil
}
