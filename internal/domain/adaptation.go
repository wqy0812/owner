package domain

import (
	"fmt"
	"sort"
	"strings"
)

// ScenarioAdaptationIssues compares declared sets, never inferring a missing
// scenario choice from a component. Empty component sets mean unrestricted.
func ScenarioAdaptationIssues(scenario, component map[string]any, nodeID, name string, complete bool) []ValidationIssue {
	issues := []ValidationIssue{}
	keys := make([]string, 0, len(component))
	for key := range component {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		allowed := ConstraintValues(component[key])
		if len(allowed) == 0 {
			continue
		}
		selected := ConstraintValues(scenario[key])
		if len(selected) == 0 {
			if complete {
				issues = append(issues, ValidationIssue{Code: "adaptation_missing", NodeID: nodeID, Message: fmt.Sprintf("%s 要求场景补选适配标签 %s（支持：%s）", name, key, strings.Join(allowed, "、"))})
			}
			continue
		}
		accepted := map[string]bool{}
		for _, v := range allowed {
			accepted[v] = true
		}
		for _, v := range selected {
			if !accepted[v] {
				issues = append(issues, ValidationIssue{Code: "adaptation_conflict", NodeID: nodeID, Message: fmt.Sprintf("%s 不支持场景适配标签 %s=%s（支持：%s）", name, key, v, strings.Join(allowed, "、"))})
			}
		}
	}
	return issues
}

func MatchEnvironment(constraints, facts map[string]any) error {
	keys := make([]string, 0, len(constraints))
	for k := range constraints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values := ConstraintValues(constraints[key])
		if len(values) == 0 {
			continue
		}
		actual, ok := facts[key].(string)
		matched := false
		for _, v := range values {
			if ok && actual == v {
				matched = true
			}
		}
		if !matched {
			if !ok {
				actual = "未填写"
			}
			return fmt.Errorf("%w: 适配标签 %s 要求 [%s]，实际为 %s", ErrInvalid, key, strings.Join(values, "、"), actual)
		}
	}
	return nil
}
