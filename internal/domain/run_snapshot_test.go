package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func snapshotRun(kind RunKind) Run {
	r := Run{ID: "run", Kind: kind, Snapshot: SnapshotFromExecutionPlan(RunExecutionPlan{Steps: []RunPlanStep{{ID: "step", NodeID: "node", ReleaseID: "release", Variables: map[string]any{"runId": "user-value", "nested": map[string]any{"future": true}}}}})}
	if kind == RunScenario || kind == RunScenarioTest {
		r.ScenarioRevisionID = "revision"
		r.Snapshot.Subject = RunSubject{ScenarioID: "scenario", ScenarioRevisionSpecDigest: "definition"}
		r.Snapshot.ScenarioExecution = ScenarioRunExecution{Mode: ScenarioExecutionInstall, TargetNodes: []ScenarioTargetNode{}, AcceptanceJobIDs: []string{}, Baseline: ScenarioRunBaseline{InstallationDigest: "empty-baseline"}}
		r.Snapshot.Submission = RunSubmission{Key: "key", Digest: "request", PlanDigest: "preview"}
	}
	return r
}
func TestRunSnapshotKindsModesAndRoundTrip(t *testing.T) {
	for _, kind := range []RunKind{RunComponentTest, RunEnvironmentRollback, RunScenario, RunScenarioTest} {
		t.Run(string(kind), func(t *testing.T) {
			r := snapshotRun(kind)
			if kind == RunEnvironmentRollback {
				r.ScenarioRevisionID = "associated-only"
				r.Snapshot.Subject.ScenarioID = "scenario"
			}
			if err := ValidateRunSnapshot(r); err != nil {
				t.Fatal(err)
			}
			raw, err := EncodeRunSnapshot(r.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeRunSnapshot(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, r.Snapshot) {
				t.Fatalf("round trip changed inputs: %s", raw)
			}
		})
	}
	for _, mode := range []ScenarioExecutionMode{ScenarioExecutionInstall, ScenarioExecutionUpgrade, ScenarioExecutionBaselineVerify} {
		r := snapshotRun(RunScenario)
		r.Snapshot.ScenarioExecution.Mode = mode
		if mode == ScenarioExecutionUpgrade {
			r.Snapshot.ScenarioExecution.SourceRevisionID = "source"
		}
		if err := ValidateRunSnapshot(r); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}
func TestRunSnapshotRejectsMissingNullTypesAndUnknownControls(t *testing.T) {
	raw, err := EncodeRunSnapshot(snapshotRun(RunScenario).Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, from, to string }{
		{"contract", `"clusterforge-run-v1"`, `"clusterforge-run-v2"`},
		{"unknown", `"plan":{`, `"plan":{"future":true,`},
		{"wrong-case", `"contract":`, `"Contract":`},
		{"generation missing", `"generation":0,`, ``},
		{"false missing", `"testOnly":false`, ``},
		{"integer string", `"generation":0`, `"generation":"0"`},
		{"integer fractional", `"generation":0`, `"generation":0.5`},
		{"integer decimal", `"generation":0`, `"generation":0.0`},
		{"overflow", `"generation":0`, `"generation":9223372036854775808`},
		{"null scalar", `"generation":0`, `"generation":null`},
		{"missing step bool", `"become":false,`, ``},
		{"null step bool", `"become":false`, `"become":null`},
		{"missing plan runtime", `"runtime":{"ansibleCore":"","python":""},`, ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(string(raw), tc.from, tc.to, 1)
			if changed == string(raw) {
				t.Fatal("fixture replacement did not apply")
			}
			if _, e := DecodeRunSnapshot([]byte(changed)); e == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
	for _, suffix := range []string{`{}`, ` true`} {
		if _, e := DecodeRunSnapshot(append(append([]byte{}, raw...), suffix...)); e == nil {
			t.Fatal("trailing JSON accepted")
		}
	}
}
func TestRunSnapshotReferenceOwnershipAndClone(t *testing.T) {
	r := snapshotRun(RunScenario)
	r.Snapshot.Plan.Steps[0].Backup = &BackupMetadata{InstallRunID: "backup", Previous: &EnvironmentComponentInstallation{InstallRunID: "ancestor"}}
	r.Snapshot.Plan.ParentSteps = []RunPlanStep{{Backup: &BackupMetadata{InstallRunID: "parent"}}}
	r.Snapshot.ScenarioExecution.Baseline.RunID = "baseline"
	r.Snapshot.Retry.BaselineMutatingRunID = "mutation"
	r.Snapshot.Recovery.InstallationBaseline = []RunInstallationBaseline{{InstallRunID: "installation"}}
	want := []string{"ancestor", "backup", "baseline", "installation", "mutation", "parent"}
	if got := r.Snapshot.ReferencedRunIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("refs=%v", got)
	}
	copy, err := r.Snapshot.Clone()
	if err != nil {
		t.Fatal(err)
	}
	copy.Plan.Steps[0].Variables["runId"] = "changed"
	if r.Snapshot.Plan.Steps[0].Variables["runId"] != "user-value" {
		t.Fatal("clone shares dynamic inputs")
	}
	// Separating observations cannot change the Native Job / preview projection.
	before, _ := json.Marshal(r.Snapshot.ExecutionPlan([]RunDeliveryResult{{Status: "delivered"}}))
	copy.Plan.Steps[0].Variables["runId"] = "user-value"
	after, _ := json.Marshal(copy.ExecutionPlan([]RunDeliveryResult{{Status: "delivered"}}))
	if string(before) != string(after) {
		t.Fatal("storage round trip changed flat plan projection")
	}
}
func TestRunSnapshotRejectsContradictoryGroups(t *testing.T) {
	r := snapshotRun(RunEnvironmentRollback)
	r.ScenarioRevisionID = "associated"
	r.Snapshot.ScenarioExecution.Baseline.Generation = 1
	if ValidateRunSnapshot(r) == nil {
		t.Fatal("rollback acquired scenario execution semantics")
	}
	r = snapshotRun(RunScenario)
	r.Snapshot.ScenarioExecution.Mode = "new-mode"
	if ValidateRunSnapshot(r) == nil {
		t.Fatal("unknown mode accepted")
	}
	r = snapshotRun(RunScenario)
	r.Snapshot.ScenarioExecution.Mode = ScenarioExecutionBaselineVerify
	r.Snapshot.Delivery.ArtifactTransfers = []RunArtifactTransfer{{}}
	if ValidateRunSnapshot(r) == nil {
		t.Fatal("baseline verification permitted media transfer")
	}
}

func TestRunDeliveryResultsDecodeSeparatelyAndStrictly(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `[{"requirementId":"image","mode":"direct"}]`, `[{"requirementId":"image","mode":"direct","status":"pending","unknown":false}]`, `[{"requirementId":2,"mode":"direct","status":"pending"}]`} {
		if _, err := DecodeRunDeliveryResults([]byte(raw)); err == nil {
			t.Fatal("invalid observation accepted", raw)
		}
	}
	for _, raw := range []string{`[]`, `[{"requirementId":"image","mode":"direct","status":"pending"}]`} {
		if _, err := DecodeRunDeliveryResults([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
}
