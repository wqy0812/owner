package service

import (
	"encoding/json"
	"os"
	"testing"
)

// Fixtures were serialized using the pre-extraction lockedPlan and media types.
// Test direct serialization too: map normalization can hide field-order drift.
func TestMediaTypeExtractionPreservesLockedJSON(t *testing.T) {
	for _, name := range []string{"nil", "empty", "mixed"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/media-plan-before-" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var plan lockedPlan
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
