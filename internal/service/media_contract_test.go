package service

import (
	"codex/platform-demo/internal/domain"
	"encoding/json"
	"os"
	"testing"
)

// Current locked plans round-trip all media identities and decisions.
// Test direct serialization too: map normalization can hide field-order drift.
func TestLockedMediaPlanJSONRoundTrip(t *testing.T) {
	for _, name := range []string{"nil", "empty", "mixed"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/media-plan-current-" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var plan domain.RunExecutionPlan
			if err := json.Unmarshal(data, &plan); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(plan)
			if err != nil || string(encoded) != string(data) {
				t.Fatalf("locked JSON changed: %s error=%v", encoded, err)
			}
		})
	}
}
