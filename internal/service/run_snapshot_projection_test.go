package service

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"codex/platform-demo/internal/domain"
)

// Golden files were emitted by lockedPlan and jobPlanFromLocked from the source
// captured before this refactor (2026-09-08, old database contract). Do not refresh
// them from current types: their byte order and fields are old digest inputs.
func TestRunSnapshotPreservesOriginalPlanAndNativeJobProjection(t *testing.T) {
	original, err := os.ReadFile("testdata/run-snapshot/original-run-plan.json")
	if err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile("testdata/run-snapshot/original-native-job-v4.json")
	if err != nil {
		t.Fatal(err)
	}
	var p domain.RunExecutionPlan
	if err = json.Unmarshal(original, &p); err != nil {
		t.Fatal(err)
	}
	snapshot, err := domain.SnapshotFromExecutionPlan(p).Clone()
	if err != nil {
		t.Fatal(err)
	}
	projected := snapshot.ExecutionPlan(p.DeliveryResults)
	actual, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, original) {
		t.Fatal("original flat plan digest projection changed")
	}
	actual, err = json.Marshal(jobPlanFromLocked("environment", projected, []byte("[all]\nhost\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, native) {
		t.Fatal("original Native Job v4 projection changed")
	}
}
