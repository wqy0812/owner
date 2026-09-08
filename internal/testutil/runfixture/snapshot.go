package runfixture

import (
	"bytes"
	"encoding/json"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/runmigration/legacy"
)

// RunSnapshotFixture preserves the authored contents of existing record fixtures
// during the storage transition. It is NOT a valid execution-plan factory: tests
// of execution must submit through the Service. The source representation is
// owned exclusively by the offline migration package.
func Snapshot(value any) domain.RunSnapshot {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var old legacy.Snapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&old); err != nil {
		panic(err)
	}
	return old.Convert()
}

func StepMaps(steps []domain.RunPlanStep) []any {
	raw, err := json.Marshal(steps)
	if err != nil {
		panic(err)
	}
	var out []any
	if err = json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}
