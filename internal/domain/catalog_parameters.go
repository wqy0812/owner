package domain

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var imageRegistryPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
var EnvironmentVariablePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
var parameterNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

func MatchesParameterType(value any, expected string) bool {
	if value == nil {
		return expected == "null"
	}
	kind := reflect.TypeOf(value).Kind()
	switch expected {
	case "string":
		_, number := value.(json.Number)
		return kind == reflect.String && !number
	case "boolean":
		return kind == reflect.Bool
	case "object":
		return kind == reflect.Map
	case "array":
		return kind == reflect.Array || kind == reflect.Slice
	case "number", "integer":
		text, numeric := parameterNumberText(value)
		if !numeric {
			return false
		}
		_, exponent, valid := normalizeParameterNumber(text)
		return valid && (expected == "number" || exponent.Sign() >= 0)
	case "null":
		return false
	default:
		// Parameter types are validated when the release contract is saved.
		return true
	}
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
	leftText, leftNumeric := parameterNumberText(left)
	rightText, rightNumeric := parameterNumberText(right)
	if leftNumeric || rightNumeric {
		if !leftNumeric || !rightNumeric {
			return false
		}
		leftDigits, leftExponent, leftValid := normalizeParameterNumber(leftText)
		rightDigits, rightExponent, rightValid := normalizeParameterNumber(rightText)
		return leftValid && rightValid && leftDigits == rightDigits && leftExponent.Cmp(rightExponent) == 0
	}
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	l, r := reflect.ValueOf(left), reflect.ValueOf(right)
	if l.Kind() != r.Kind() {
		return false
	}
	// Parameter containers are JSON trees. Compare their children using the same
	// numeric semantics as scalars while preserving keys, order and nil containers.
	switch l.Kind() {
	case reflect.Map:
		if l.Type().Key() != r.Type().Key() || l.IsNil() != r.IsNil() || l.Len() != r.Len() {
			return false
		}
		entries := l.MapRange()
		for entries.Next() {
			other := r.MapIndex(entries.Key())
			if !other.IsValid() || !ParameterValuesEqual(entries.Value().Interface(), other.Interface()) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if l.Len() != r.Len() || (l.Kind() == reflect.Slice && l.IsNil() != r.IsNil()) {
			return false
		}
		for i := 0; i < l.Len(); i++ {
			if !ParameterValuesEqual(l.Index(i).Interface(), r.Index(i).Interface()) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(left, right)
}

func parameterNumberText(value any) (string, bool) {
	if number, ok := value.(json.Number); ok {
		return string(number), true
	}
	if value == nil {
		return "", false
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(reflected.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(reflected.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(reflected.Float(), 'g', -1, reflected.Type().Bits()), true
	default:
		return "", false
	}
}

// normalizeParameterNumber represents a number as signed significant digits
// times 10^exponent. Keeping the exponent separate avoids float rounding and
// expanding potentially enormous JSON exponents into equally enormous integers.
func normalizeParameterNumber(text string) (string, *big.Int, bool) {
	if !parameterNumberPattern.MatchString(text) {
		return "", nil, false
	}
	exponent := new(big.Int)
	if index := strings.IndexAny(text, "eE"); index >= 0 {
		exponent.SetString(text[index+1:], 10)
		text = text[:index]
	}
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	fractionDigits := 0
	if index := strings.IndexByte(text, '.'); index >= 0 {
		fractionDigits = len(text) - index - 1
		text = text[:index] + text[index+1:]
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		return "0", new(big.Int), true
	}
	digits := strings.TrimRight(text, "0")
	exponent.Add(exponent, big.NewInt(int64(len(text)-len(digits)-fractionDigits)))
	if negative {
		digits = "-" + digits
	}
	return digits, exponent, true
}

func ValidateResolvedParameters(parameters []ParameterDefinition, resolved map[string]any) error {
	for _, parameter := range parameters {
		value, exists := resolved[parameter.Name]
		if !exists {
			if parameter.Required {
				return fmt.Errorf("%w: required parameter %q has no resolved value", ErrInvalid, parameter.Name)
			}
			continue
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
// Values belong to the exact Release parameter that defines them.
func ValidateEnvironmentValues(values map[string]any, variables map[string]string, refs []CredentialRef, releases []ComponentRelease, variableDefinitions []EnvironmentVariableDefinition) error {
	fields := map[string][]ParameterDefinition{}
	for _, r := range releases {
		if r.Status == ReleaseDeprecated && r.ReleasedAt == nil {
			continue
		}
		for _, p := range r.Parameters {
			if p.ValueProvider != ParameterProviderEnvironmentOwner || !p.Modifiable {
				continue
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

func (r ComponentRelease) IsApprovedCandidate() bool {
	return r.Status == ReleaseDraft && r.Candidate && r.Review.Status == ReleaseReviewApproved && r.Review.ContractDigest == ComponentReleaseSpecDigest(r)
}
