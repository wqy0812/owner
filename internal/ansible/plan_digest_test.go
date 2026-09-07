package ansible

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The expected digest was captured with service.DigestStandalonePlan's original
// JSON + SHA-256 implementation before extraction, including both media kinds.
func TestPlanDigestMatchesPreExtractionJob(t *testing.T) {
	data, err := os.ReadFile("testdata/media-job-before.json")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/media-job-before.sha256")
	if err != nil {
		t.Fatal(err)
	}
	var plan JobPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if got := PlanDigest(plan); got != strings.TrimSpace(string(expected)) {
		t.Fatalf("digest=%s want=%s", got, expected)
	}
	plan.Metadata["unrelated_contract_field"] = "must remain in the full digest"
	if PlanDigest(plan) == strings.TrimSpace(string(expected)) {
		t.Fatal("full digest dropped non-media metadata")
	}
}
