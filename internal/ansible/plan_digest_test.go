package ansible

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// All current plan fields, including media and extension metadata, are sealed.
func TestPlanDigestSealsAllPlanFields(t *testing.T) {
	data, err := os.ReadFile("testdata/media-job-current.json")
	if err != nil {
		t.Fatal(err)
	}

	var plan JobPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(encoded))
	if got := PlanDigest(plan); got != expected {
		t.Fatalf("digest=%s want=%s", got, expected)
	}
	plan.Metadata["unrelated_contract_field"] = "must remain in the full digest"
	if PlanDigest(plan) == expected {
		t.Fatal("full digest dropped non-media metadata")
	}
}
