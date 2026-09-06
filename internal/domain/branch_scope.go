package domain

import (
	"encoding/json"
	"slices"
)

// NormalizeEnvironmentConstraints gives branch scopes one representation without
// changing the meaning of an unrestricted dimension.
func NormalizeEnvironmentConstraints(scope map[string]any) map[string]any {
	out := map[string]any{}
	for key, raw := range scope {
		values := ConstraintValues(raw)
		slices.Sort(values)
		values = slices.Compact(values)
		if len(values) > 0 {
			out[key] = values
		}
	}
	return out
}

func SameEnvironmentConstraints(a, b map[string]any) bool {
	left, _ := json.Marshal(NormalizeEnvironmentConstraints(a))
	right, _ := json.Marshal(NormalizeEnvironmentConstraints(b))
	return string(left) == string(right)
}
