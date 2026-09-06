package backup

import (
	"codex/platform-demo/internal/domain"
	"encoding/json"
	"fmt"
)

func validateCatalogBranchScopes(catalog Catalog) error {
	tables := map[string]TableDump{}
	for _, table := range catalog.Tables {
		tables[table.Name] = table
	}
	scope := func(table TableDump, row []DBCell) (map[string]any, error) {
		column := columnIndex(table.Columns, "environment_constraints_json")
		var out map[string]any
		if column < 0 || column >= len(row) || row[column].Kind != "text" {
			return nil, fmt.Errorf("%w: %s 缺少分支适配范围", domain.ErrInvalid, table.Name)
		}
		if err := json.Unmarshal([]byte(row[column].Text), &out); err != nil || out == nil {
			return nil, fmt.Errorf("%w: %s 适配范围无效", domain.ErrInvalid, table.Name)
		}
		for _, raw := range out {
			values, ok := raw.([]any)
			if !ok || len(values) == 0 {
				return nil, fmt.Errorf("%w: %s 适配范围必须使用非空选项数组", domain.ErrInvalid, table.Name)
			}
			for _, value := range values {
				if text, ok := value.(string); !ok || text == "" {
					return nil, fmt.Errorf("%w: %s 适配选项无效", domain.ErrInvalid, table.Name)
				}
			}
		}
		return out, nil
	}
	for _, relation := range []struct{ parent, child, key string }{{"component_release_lines", "component_releases", "line_id"}, {"scenarios", "scenario_revisions", "scenario_id"}} {
		parents := map[string]map[string]any{}
		table := tables[relation.parent]
		for _, row := range table.Rows {
			parsed, err := scope(table, row)
			if err != nil {
				return err
			}
			parents[row[columnIndex(table.Columns, "id")].Text] = parsed
		}
		table = tables[relation.child]
		for _, row := range table.Rows {
			parsed, err := scope(table, row)
			if err != nil {
				return err
			}
			id := row[columnIndex(table.Columns, "id")].Text
			parent, exists := parents[row[columnIndex(table.Columns, relation.key)].Text]
			if !exists || !domain.SameEnvironmentConstraints(parent, parsed) {
				return &domain.CodedError{Code: "catalog_relationship_invalid", Message: "版本与分支适配范围不同，请先处理分支冲突", Cause: domain.ErrInvalid, Details: map[string]any{"table": relation.child, "objectId": id, "relation": "environmentConstraints"}}
			}
		}
	}
	return nil
}
