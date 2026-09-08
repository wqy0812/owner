package domain

import (
	"encoding/json"
	"math"
	"testing"
)

func TestParameterNumberTypes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   any
		integer bool
	}{
		{"integer", json.Number("3"), true},
		{"decimal integer", json.Number("3.0"), true},
		{"exponent integer", json.Number("3e0"), true},
		{"signed exponent", json.Number("3E+00"), true},
		{"fraction", json.Number("3.5"), false},
		{"scaled integer", json.Number("350e-1"), true},
		{"scaled fraction", json.Number("35e-1"), false},
		{"negative zero", json.Number("-0.00e-999"), true},
		{"large integer", json.Number("9007199254740993"), true},
		{"tiny fraction", json.Number("1e-100000000000000000000"), false},
		{"huge integer", json.Number("1e100000000000000000000"), true},
		{"native integer", int64(9007199254740993), true},
		{"native unsigned", uint64(math.MaxUint64), true},
		{"native float", float64(3), true},
		{"native fraction", float32(3.5), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !MatchesParameterType(tc.value, "number") || MatchesParameterType(tc.value, "integer") != tc.integer {
				t.Fatalf("incorrect numeric type for %T(%v), want integer=%v", tc.value, tc.value, tc.integer)
			}
			if MatchesParameterType(tc.value, "string") {
				t.Fatalf("number %v matched string", tc.value)
			}
		})
	}
	for _, value := range []any{"3", true, nil, json.Number(""), json.Number("+3"), json.Number("03"), json.Number(".3"), json.Number("3."), json.Number("3e"), json.Number("NaN"), json.Number(" 3"), math.NaN(), math.Inf(1)} {
		if MatchesParameterType(value, "number") || MatchesParameterType(value, "integer") {
			t.Errorf("invalid number accepted: %T(%v)", value, value)
		}
	}
	if !MatchesParameterType("3", "string") {
		t.Fatal("ordinary string lost its type")
	}
}

func TestParameterValuesEqualNumbersAndContainers(t *testing.T) {
	for _, tc := range []struct {
		name        string
		left, right any
		equal       bool
	}{
		{"decimal", json.Number("3.0"), 3, true},
		{"snapshot decimal", json.Number("3"), json.Number("3.0"), true},
		{"snapshot exponent", json.Number("3.0"), json.Number("3e0"), true},
		{"exponent", json.Number("3e0"), float64(3), true},
		{"fraction", json.Number("3.5"), float32(3.5), true},
		{"signed zero", json.Number("-0.00"), 0, true},
		{"string", json.Number("3"), "3", false},
		{"fraction differs", json.Number("3.5"), 3, false},
		{"large integer", json.Number("9007199254740993"), int64(9007199254740993), true},
		{"adjacent integers", int64(9007199254740992), int64(9007199254740993), false},
		{"adjacent snapshot integers", json.Number("9007199254740992"), json.Number("9007199254740993"), false},
		{"rounded float", json.Number("9007199254740993"), float64(9007199254740992), false},
		{"unsigned integer", json.Number("18446744073709551615"), uint64(math.MaxUint64), true},
		{"negative integer", json.Number("-9007199254740993"), int64(-9007199254740993), true},
		{"exact decimal", json.Number("9007199254740993.1"), json.Number("9007199254740993.2"), false},
		{"huge exponent", json.Number("10e99999999999999999999"), json.Number("1e100000000000000000000"), true},
		{"tiny exponent", json.Number("1e-99999999999999999999"), json.Number("10e-100000000000000000000"), true},
		{"invalid number", json.Number("invalid"), json.Number("invalid"), false},
		{"nested numbers", map[string]any{"values": []any{json.Number("3e0"), map[string]any{"large": json.Number("9007199254740993")}}}, map[string]any{"values": []any{float64(3), map[string]any{"large": int64(9007199254740993)}}}, true},
		{"nested difference", []any{map[string]any{"large": json.Number("9007199254740993")}}, []any{map[string]any{"large": int64(9007199254740992)}}, false},
		{"nested string", map[string]any{"value": json.Number("3")}, map[string]any{"value": "3"}, false},
		{"array order", []any{1, 2}, []any{2, 1}, false},
		{"array length", []any{1}, []any{1, 2}, false},
		{"object keys", map[string]any{"a": nil}, map[string]any{"b": nil}, false},
		{"missing key", map[string]any{"a": nil}, map[string]any{}, false},
		{"nulls", nil, nil, true},
		{"null and object", nil, map[string]any{}, false},
		{"null and array", nil, []any{}, false},
		{"nil and empty object", map[string]any(nil), map[string]any{}, false},
		{"nil and empty array", []any(nil), []any{}, false},
		{"empty objects", map[string]any{}, map[string]any{}, true},
		{"empty arrays", []any{}, []any{}, true},
		{"container kinds", map[string]any{}, []any{}, false},
		{"typed arrays", [2]int{3, 4}, [2]json.Number{"3.0", "4e0"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ParameterValuesEqual(tc.left, tc.right) != tc.equal || ParameterValuesEqual(tc.right, tc.left) != tc.equal {
				t.Fatalf("equality of %#v and %#v: want %v in both directions", tc.left, tc.right, tc.equal)
			}
		})
	}
}

func TestParameterEnumsUseNumericEquality(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    ParameterType
		allowed any
		value   any
		valid   bool
	}{
		{"integer", ParameterTypeInteger, float64(3), json.Number("3e0"), true},
		{"fraction", ParameterTypeInteger, float64(3), json.Number("3.5"), false},
		{"string", ParameterTypeInteger, float64(3), "3", false},
		{"outside enum", ParameterTypeInteger, float64(3), json.Number("4"), false},
		{"large integer", ParameterTypeNumber, int64(9007199254740993), json.Number("9007199254740993"), true},
		{"adjacent integer", ParameterTypeNumber, int64(9007199254740993), json.Number("9007199254740992"), false},
		{"object", ParameterTypeObject, map[string]any{"values": []any{3}}, map[string]any{"values": []any{json.Number("3.0")}}, true},
		{"array", ParameterTypeArray, []any{3, 4}, []any{json.Number("3e0"), json.Number("4.0")}, true},
		{"array order", ParameterTypeArray, []any{3, 4}, []any{json.Number("4"), json.Number("3")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parameters := []ParameterDefinition{{Name: "value", Type: tc.kind, Required: true, Enum: []any{tc.allowed}}}
			err := ValidateResolvedParameters(parameters, map[string]any{"value": tc.value})
			if (err == nil) != tc.valid {
				t.Fatalf("validation of %#v against %#v: %v, want valid=%v", tc.value, tc.allowed, err, tc.valid)
			}
		})
	}
}
