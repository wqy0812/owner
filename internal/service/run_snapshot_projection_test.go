package service

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"codex/platform-demo/internal/domain"
)

// Golden files cover the current direct-execution plan and Native Job v4 execution structure with current delivery metadata.
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
	if err := domain.ValidateRunSnapshot(domain.Run{Kind: domain.RunComponentTest, Status: domain.RunQueued, Snapshot: snapshot}); err != nil {
		t.Fatalf("current golden must be an executable snapshot: %v", err)
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
		t.Fatalf("Native Job v4 projection changed: %s", actual)
	}
}
