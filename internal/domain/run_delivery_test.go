package domain

import "testing"

func TestRunSnapshotRejectsInvalidAutomaticDelivery(t *testing.T) {
	for _, status := range []RunStatus{RunQueued, RunRunning} {
		for _, policy := range []string{"", "direct", "unknown", DeliveryPolicyTransferToTarget} {
			r := snapshotRun(RunComponentTest)
			r.Status = status
			r.Snapshot.Delivery.Requirements = []RunDeliveryRequirement{{ID: "image", Kind: "image", Target: "target/image"}}
			r.Snapshot.Delivery.Decisions = []RunDeliveryDecision{{RequirementID: "image", Mode: "transfer", Policy: policy}}
			err := ValidateRunSnapshot(r)
			if (err == nil) != (policy == DeliveryPolicyTransferToTarget) {
				t.Fatalf("status=%s policy=%q: %v", status, policy, err)
			}
		}
	}
}

func TestRunPlanRiskIncludesReusedDeliveryAndOnlyRemainingSteps(t *testing.T) {
	plan := RunExecutionPlan{Steps: []RunPlanStep{{HighRisk: true}, {}}}
	if !plan.HighRisk() {
		t.Fatal("destructive step lost its risk")
	}
	plan.Steps = plan.Steps[1:]
	if plan.HighRisk() {
		t.Fatal("completed destructive step inflated continuation risk")
	}
	plan.DeliveryRequirements = []RunDeliveryRequirement{{ID: "reused-target"}}
	if !plan.HighRisk() {
		t.Fatal("reused delivery lost its risk without a transfer")
	}
}
