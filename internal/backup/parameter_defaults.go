package backup

import (
	"encoding/json"
	"fmt"

	"codex/platform-demo/internal/domain"
)

func validateCatalogParameterDefaults(catalog Catalog) error {
	tables := catalogTableMap(catalog)
	definitions := tables["environment_parameter_definitions"]
	byID := map[string]domain.EnvironmentParameterDefinition{}
	for _, row := range definitions.Rows {
		text := func(column string) string { return row[columnIndex(definitions.Columns, column)].Text }
		d := domain.EnvironmentParameterDefinition{ID: text("id"), Label: text("label"), Type: domain.ParameterType(text("parameter_type")), MinLength: int(row[columnIndex(definitions.Columns, "min_length")].Int)}
		if err := json.Unmarshal([]byte(text("enum_json")), &d.Enum); err != nil {
			return err
		}
		byID[d.ID] = d
	}
	defaults := tables["environment_parameter_defaults"]
	for _, row := range defaults.Rows {
		id := row[columnIndex(defaults.Columns, "definition_id")].Text
		d, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: default references unknown environment parameter %q", domain.ErrInvalid, id)
		}
		if err := json.Unmarshal([]byte(row[columnIndex(defaults.Columns, "value_json")].Text), &d.DefaultValue); err != nil {
			return err
		}
		if d.DefaultValue == nil {
			return fmt.Errorf("%w: stored environment default cannot be null", domain.ErrInvalid)
		}
		if err := domain.ValidateEnvironmentParameterDefault(d); err != nil {
			return err
		}
	}
	return nil
}
