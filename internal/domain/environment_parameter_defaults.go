package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// A missing value is different from false or zero. Empty strings are not a
// usable global environment setting, even when its definition has no minLength.
func MissingEnvironmentParameterValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func ValidateEnvironmentParameterDefault(d EnvironmentParameterDefinition) error {
	if d.DefaultValue == nil {
		return nil
	}
	if !d.Type.Valid() {
		return fmt.Errorf("%w: invalid type for environment default %q", ErrInvalid, d.Label)
	}
	if MissingEnvironmentParameterValue(d.DefaultValue) {
		return fmt.Errorf("%w: default value for %q cannot be blank; remove the default instead", ErrInvalid, d.Label)
	}
	parameter := ParameterDefinition{Name: d.Label, Type: d.Type, Enum: d.Enum, MinLength: d.MinLength}
	if err := ValidateResolvedParameters([]ParameterDefinition{parameter}, map[string]any{d.Label: d.DefaultValue}); err != nil {
		return err
	}
	if path, found := FindSensitiveValue(d.DefaultValue, d.Label); found {
		return fmt.Errorf("%w: sensitive default %q must use a CredentialRef", ErrInvalid, path)
	}
	return nil
}

// MaterializeEnvironmentParameterDefaults is called only while saving an
// environment, never while reading or executing a retained revision.
func MaterializeEnvironmentParameterDefaults(values map[string]any, releases []ComponentRelease, definitions []EnvironmentParameterDefinition, requireComplete bool) (map[string]any, error) {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	used := map[string]bool{}
	for _, release := range releases {
		if release.Status == ReleaseDeprecated && release.ReleasedAt == nil {
			continue
		}
		for _, parameter := range release.Parameters {
			if parameter.ValueProvider == ParameterProviderEnvironmentOwner && parameter.EnvironmentBinding != nil && parameter.EnvironmentBinding.Kind == EnvironmentBindingGlobal {
				used[parameter.EnvironmentBinding.DefinitionID] = true
			}
		}
	}
	for _, d := range definitions {
		if !used[d.ID] {
			continue
		}
		key := "global:" + d.ID
		value, exists := out[key]
		if !exists && d.DefaultValue != nil {
			if err := ValidateEnvironmentParameterDefault(d); err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(d.DefaultValue)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(encoded, &value); err != nil {
				return nil, err
			}
			out[key] = value
		}
		if requireComplete && MissingEnvironmentParameterValue(value) {
			return nil, fmt.Errorf("%w: environment parameter %q must be filled by the Environment Owner", ErrInvalid, d.Label)
		}
	}
	return out, nil
}
