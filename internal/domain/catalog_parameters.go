package domain

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var imageRegistryPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
var EnvironmentVariablePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func MatchesParameterType(value any, expected string) bool {
	if value == nil {
		return expected == "null"
	}
	kind := reflect.TypeOf(value).Kind()
	switch expected {
	case "string":
		return kind == reflect.String
	case "boolean":
		return kind == reflect.Bool
	case "object":
		return kind == reflect.Map
	case "array":
		return kind == reflect.Array || kind == reflect.Slice
	case "number":
		return isNumericKind(kind)
	case "integer":
		if !isNumericKind(kind) {
			return false
		}
		switch number := value.(type) {
		case float32:
			return math.Trunc(float64(number)) == float64(number)
		case float64:
			return math.Trunc(number) == number
		default:
			return true
		}
	case "null":
		return false
	default:
		// Parameter types are validated when the release contract is saved.
		return true
	}
}

func isNumericKind(kind reflect.Kind) bool {
	return kind >= reflect.Int && kind <= reflect.Float64
}

func ContainsParameterValue(values []any, value any) bool {
	for _, candidate := range values {
		if ParameterValuesEqual(candidate, value) {
			return true
		}
	}
	return false
}

func ParameterValuesEqual(left, right any) bool {
	if left != nil && right != nil && isNumericKind(reflect.TypeOf(left).Kind()) && isNumericKind(reflect.TypeOf(right).Kind()) {
		return numericValue(left) == numericValue(right)
	}
	return reflect.DeepEqual(left, right)
}

func numericValue(value any) float64 {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(reflected.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(reflected.Uint())
	default:
		return reflected.Float()
	}
}

func ValidateResolvedParameters(parameters []ParameterDefinition, resolved map[string]any) error {
	for _, parameter := range parameters {
		value, exists := resolved[parameter.Name]
		global := parameter.ValueProvider == ParameterProviderEnvironmentOwner && parameter.EnvironmentBinding != nil && parameter.EnvironmentBinding.Kind == EnvironmentBindingGlobal
		if !exists {
			if parameter.Required || global {
				return fmt.Errorf("%w: required parameter %q has no resolved value", ErrInvalid, parameter.Name)
			}
			continue
		}
		if global && MissingEnvironmentParameterValue(value) {
			return fmt.Errorf("%w: environment parameter %q must be filled by the Environment Owner", ErrInvalid, parameter.Name)
		}
		if !MatchesParameterType(value, string(parameter.Type)) {
			return fmt.Errorf("%w: parameter %q must be of type %s", ErrInvalid, parameter.Name, parameter.Type)
		}
		if parameter.MinLength > 0 {
			text, isString := value.(string)
			if isString && len([]rune(text)) < parameter.MinLength {
				return fmt.Errorf("%w: parameter %q must contain at least %d characters", ErrInvalid, parameter.Name, parameter.MinLength)
			}
		}
		if len(parameter.Enum) > 0 && !ContainsParameterValue(parameter.Enum, value) {
			return fmt.Errorf("%w: parameter %q is not one of the allowed values", ErrInvalid, parameter.Name)
		}
	}
	return nil
}

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|private[_-]?key|encryption[_-]?key|credential)`)

func IsSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	if strings.HasSuffix(normalized, "_version") {
		return false
	}
	return sensitiveKey.MatchString(key)
}

func FindSensitiveValue(value any, prefix string) (string, bool) {
	switch values := value.(type) {
	case map[string]any:
		for key, child := range values {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			if IsSensitiveKey(key) {
				return path, true
			}
			if nested, found := FindSensitiveValue(child, path); found {
				return nested, true
			}
		}
	case []any:
		for index, child := range values {
			path := fmt.Sprintf("%s[%d]", prefix, index)
			if nested, found := FindSensitiveValue(child, path); found {
				return nested, true
			}
		}
	}
	return "", false
}

func NormalizeImageRegistry(value string) (string, error) {
	registry := strings.TrimSuffix(strings.TrimSpace(value), "/")
	if registry == "" || strings.Contains(registry, "://") || strings.ContainsAny(registry, "@?#") || !imageRegistryPattern.MatchString(registry) {
		return "", fmt.Errorf("%w: %s must be a Docker registry prefix without a URL scheme, tag or digest", ErrInvalid, "IMAGE_REGISTRY")
	}
	host := registry
	if slash := strings.IndexByte(host, '/'); slash >= 0 {
		host = host[:slash]
	}
	if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		port, err := strconv.Atoi(host[colon+1:])
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("%w: %s contains an invalid registry port", ErrInvalid, "IMAGE_REGISTRY")
		}
	}
	return registry, nil
}

func NormalizeEnvironmentVariables(variables map[string]string, refs []CredentialRef) (map[string]string, error) {
	names := map[string]bool{}
	for _, ref := range refs {
		names[ref.Name] = true
	}
	out := make(map[string]string, len(variables))
	for name, value := range variables {
		if !EnvironmentVariablePattern.MatchString(name) {
			return nil, fmt.Errorf("%w: environment variable %q must be an uppercase identifier", ErrInvalid, name)
		}
		if IsSensitiveKey(name) {
			return nil, fmt.Errorf("%w: sensitive environment variable %q must use a CredentialRef", ErrInvalid, name)
		}
		if names[name] {
			return nil, fmt.Errorf("%w: environment variable %q conflicts with a CredentialRef", ErrInvalid, name)
		}
		if name == "IMAGE_REGISTRY" {
			var err error
			value, err = NormalizeImageRegistry(value)
			if err != nil {
				return nil, err
			}
		}
		out[name] = value
	}
	return out, nil
}

// ValidateEnvironmentValues validates supplied values only, not run-time completeness.
// Each binding is checked so a global field cannot pick an arbitrary first consumer.
func ValidateEnvironmentValues(values map[string]any, variables map[string]string, refs []CredentialRef, releases []ComponentRelease, definitions []EnvironmentParameterDefinition, variableDefinitions []EnvironmentVariableDefinition) error {
	fields := map[string][]ParameterDefinition{}
	globals := map[string]EnvironmentParameterDefinition{}
	for _, d := range definitions {
		globals[d.ID] = d
	}
	for _, r := range releases {
		if r.Status == ReleaseDeprecated && r.ReleasedAt == nil {
			continue
		}
		for _, p := range r.Parameters {
			if p.ValueProvider != ParameterProviderEnvironmentOwner || !p.Modifiable {
				continue
			}
			if p.EnvironmentBinding != nil && p.EnvironmentBinding.Kind == EnvironmentBindingGlobal {
				if _, ok := globals[p.EnvironmentBinding.DefinitionID]; !ok {
					continue
				}
			}
			key := EnvironmentParameterValueKey(r.ID, p)
			p.Name, p.Required = key, false
			fields[key] = append(fields[key], p)
		}
	}
	for key, value := range values {
		field, ok := fields[key]
		if !ok {
			return fmt.Errorf("%w: unknown environment parameter field %q", ErrInvalid, key)
		}
		if err := ValidateResolvedParameters(field, map[string]any{key: value}); err != nil {
			return err
		}
	}
	if path, found := FindSensitiveValue(values, ""); found {
		return fmt.Errorf("%w: sensitive environment parameter %q must use a CredentialRef", ErrInvalid, path)
	}
	allowed := map[string]bool{}
	for _, d := range variableDefinitions {
		allowed[d.Name] = true
	}
	for name := range variables {
		if !allowed[name] {
			return fmt.Errorf("%w: environment variable %q is not defined by the platform administrator", ErrInvalid, name)
		}
	}
	_, err := NormalizeEnvironmentVariables(variables, refs)
	return err
}

func ValidateGlobalParameterBindings(parameters []ParameterDefinition, definitions []EnvironmentParameterDefinition) error {
	byID := map[string]EnvironmentParameterDefinition{}
	for _, d := range definitions {
		byID[d.ID] = d
	}
	for _, p := range parameters {
		if p.ValueProvider != ParameterProviderEnvironmentOwner || p.EnvironmentBinding == nil || p.EnvironmentBinding.Kind != EnvironmentBindingGlobal {
			continue
		}
		d, ok := byID[p.EnvironmentBinding.DefinitionID]
		if !ok {
			return fmt.Errorf("%w: environment parameter %q references an unknown global definition", ErrInvalid, p.Name)
		}
		matches := len(d.Enum) == len(p.Enum)
		for i := 0; matches && i < len(d.Enum); i++ {
			matches = ParameterValuesEqual(d.Enum[i], p.Enum[i])
		}
		if d.Type != p.Type || d.MinLength != p.MinLength || !matches {
			return fmt.Errorf("%w: environment parameter %q does not match its global definition", ErrInvalid, p.Name)
		}
	}
	return nil
}

func (r ComponentRelease) IsApprovedCandidate() bool {
	return r.Status == ReleaseDraft && r.Candidate && r.Review.Status == ReleaseReviewApproved && r.Review.ContractDigest == ComponentReleaseSpecDigest(r)
}
