package deploydb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func verifyExistingBranchScopes(ctx context.Context, db *sql.DB) error {
	for _, pair := range []struct{ parent, child, key string }{{"component_release_lines", "component_releases", "line_id"}, {"scenarios", "scenario_revisions", "scenario_id"}} {
		var present int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='environment_constraints_json'`, pair.parent).Scan(&present); err != nil {
			return err
		}
		if present != 1 {
			return fmt.Errorf("branch scope requires a separate reviewed assignment plan: %s has no fixed scope; this converter does not infer labels", pair.parent)
		}
		rows, err := db.QueryContext(ctx, `SELECT p.id,p.environment_constraints_json,c.environment_constraints_json FROM `+pair.parent+` p JOIN `+pair.child+` c ON c.`+pair.key+`=p.id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, parentRaw, childRaw string
			if err = rows.Scan(&id, &parentRaw, &childRaw); err != nil {
				rows.Close()
				return err
			}
			var parent, child map[string][]string
			parentErr, childErr := json.Unmarshal([]byte(parentRaw), &parent), json.Unmarshal([]byte(childRaw), &child)
			asScope := func(in map[string][]string) map[string]any {
				out := map[string]any{}
				for key, values := range in {
					out[key] = values
				}
				return out
			}
			if parentErr != nil || childErr != nil || parent == nil || child == nil || !domain.SameEnvironmentConstraints(asScope(parent), asScope(child)) {
				rows.Close()
				return fmt.Errorf("branch scope conflict for %s %s; preserve history and resolve with a separate plan", pair.parent, id)
			}
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
	}
	return nil
}
