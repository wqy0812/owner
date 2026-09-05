package domain

import "fmt"

// ValidateComponentParameterAuthoring accepts only component-owned bindings.
func ValidateComponentParameterAuthoring(parameters []ParameterDefinition) error {
	for _, p := range parameters {
		if p.EnvironmentBinding != nil && !p.EnvironmentBinding.Valid() {
			return fmt.Errorf("%w: environment parameter %s must use a component-owned binding", ErrInvalid, p.Name)
		}
	}
	return nil
}
