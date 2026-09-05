package domain

import (
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
