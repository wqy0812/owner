package domain

import "testing"

func TestBranchScopeSemanticEquality(t *testing.T) {
	a := map[string]any{"architecture": []string{"arm64", "amd64", "arm64"}}
	b := map[string]any{"architecture": []any{"amd64", "arm64"}}
	if !SameEnvironmentConstraints(a, b) {
		t.Fatal("order and repeated options changed scope")
	}
	if SameEnvironmentConstraints(a, map[string]any{}) {
		t.Fatal("broadened scope was accepted")
	}
	if SameEnvironmentConstraints(a, map[string]any{"architecture": []string{"amd64"}}) {
		t.Fatal("narrowed scope was accepted")
	}
	if a["architecture"].([]string)[0] != "arm64" {
		t.Fatal("normalization mutated input")
	}
}
