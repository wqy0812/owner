package api

import (
	"encoding/json"
	"testing"
)

func TestYAMLAuthoringInputsRejectRemovedControls(t *testing.T) {
	for _, raw := range []string{`{"gatherFacts":false}`, `{"gatherFacts":true}`, `{"resourceContract":{"checks":[]}}`, `{"runtimeChecks":[]}`, `{"GatherFacts":true}`, `{"ResourceContract":{"Checks":[]},"resourceContract":{}}`} {
		var action actionSourceInput
		if err := json.Unmarshal([]byte(raw), &action); err == nil {
			t.Fatalf("removed action input accepted: %s", raw)
		}
		var job acceptanceJobInput
		if err := json.Unmarshal([]byte(raw), &job); err == nil {
			t.Fatalf("removed acceptance input accepted: %s", raw)
		}
	}
	var action actionSourceInput
	if err := json.Unmarshal([]byte(`{"kind":"rollback","preCheckActionId":"","postCheckActionId":"","become":false}`), &action); err != nil {
		t.Fatal(err)
	}
}
