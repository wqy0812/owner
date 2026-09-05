package domain

import (
	"reflect"
	"testing"
)

func TestScenarioAdaptationSets(t *testing.T) {
	for _, tc := range []struct {
		name                string
		scenario, component map[string]any
		complete            bool
		want                string
	}{
		{"subset", map[string]any{"os": []string{"ubuntu"}}, map[string]any{"os": []string{"ubuntu", "suse"}}, true, ""},
		{"unrestricted", map[string]any{"os": []string{"ubuntu"}}, nil, true, ""},
		{"missing", nil, map[string]any{"os": []string{"ubuntu"}}, true, "adaptation_missing"},
		{"incomplete draft", nil, map[string]any{"os": []string{"ubuntu"}}, false, ""},
		{"conflict draft", map[string]any{"os": []string{"ubuntu", "suse"}}, map[string]any{"os": []string{"ubuntu"}}, false, "adaptation_conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues := ScenarioAdaptationIssues(tc.scenario, tc.component, "node", "component", tc.complete)
			if tc.want == "" {
				if len(issues) != 0 {
					t.Fatal(issues)
				}
			} else if len(issues) != 1 || issues[0].Code != tc.want {
				t.Fatal(issues)
			}
		})
	}
}
func TestAdaptationEnvironmentAndEvidenceIdentity(t *testing.T) {
	constraints := map[string]any{"os": []string{"ubuntu", "suse"}, "arch": []string{"amd64"}}
	for _, tc := range []struct {
		facts map[string]any
		valid bool
	}{{map[string]any{"os": "ubuntu", "arch": "amd64"}, true}, {map[string]any{"os": "ubuntu"}, false}, {map[string]any{"os": "other", "arch": "amd64"}, false}, {map[string]any{"os": []string{"ubuntu"}, "arch": "amd64"}, false}} {
		if (MatchEnvironment(constraints, tc.facts) == nil) != tc.valid {
			t.Fatal(tc)
		}
	}
	r := ScenarioRevision{}
	before := ScenarioRevisionSpecDigest(r)
	r.EnvironmentConstraints = constraints
	if before == ScenarioRevisionSpecDigest(r) {
		t.Fatal("labels not included in evidence identity")
	}
	if !reflect.DeepEqual(constraints, r.EnvironmentConstraints) {
		t.Fatal("matching mutated contract")
	}
}
