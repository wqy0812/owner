package store

import (
	"codex/platform-demo/internal/domain"
	"context"
	"database/sql"
	"fmt"
)

func validateBranchScopeTx(ctx context.Context, tx *sql.Tx, table, id string, scope map[string]any) error {
	var query string
	switch table {
	case "component_release_lines":
		query = `SELECT environment_constraints_json FROM component_release_lines WHERE id=?`
	case "scenarios":
		query = `SELECT environment_constraints_json FROM scenarios WHERE id=?`
	default:
		return fmt.Errorf("%w: unknown branch kind", domain.ErrInvalid)
	}
	var raw string
	if err := tx.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		return mapSQLError(err)
	}
	if !domain.SameEnvironmentConstraints(decodeJSON(raw, map[string]any{}), scope) {
		return fmt.Errorf("%w: 适配范围已固定，请新增分支", domain.ErrConflict)
	}
	return nil
}
