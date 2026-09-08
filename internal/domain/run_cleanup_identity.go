package domain

import (
	"encoding/json"
	"fmt"
)

// RunCleanupIdentity retains permission/evidence identity after failed Run
// detail is deleted. It deliberately has no executable plan or mutable state.
type RunCleanupIdentity struct {
	ComponentReleaseSpecDigest string           `json:"componentReleaseSpecDigest,omitempty"`
	ScenarioRevisionSpecDigest string           `json:"scenarioRevisionSpecDigest,omitempty"`
	ReleaseLocks               []RunReleaseLock `json:"releaseLocks"`
}
type RunReleaseLock struct {
	ReleaseID         string `json:"releaseId"`
	ReleaseSpecDigest string `json:"releaseSpecDigest"`
}

// DecodeRunCleanupIdentity reads only retained identities. Damaged execution
// controls must not prevent cleanup, but ambiguous identity fields still do.
func DecodeRunCleanupIdentity(raw []byte) (out RunCleanupIdentity, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: cleanup identity: %w", ErrInvalid, err)
		}
	}()
	var projection struct {
		Subject *struct {
			ComponentReleaseSpecDigest json.RawMessage `json:"componentReleaseSpecDigest"`
			ScenarioRevisionSpecDigest json.RawMessage `json:"scenarioRevisionSpecDigest"`
		} `json:"subject"`
		Plan *struct {
			Steps       json.RawMessage `json:"steps"`
			ParentSteps json.RawMessage `json:"parentSteps"`
		} `json:"plan"`
	}
	if err = json.Unmarshal(raw, &projection); err != nil {
		return out, err
	}
	if projection.Subject == nil || projection.Plan == nil {
		return out, fmt.Errorf("subject and plan are required")
	}
	out.ReleaseLocks = []RunReleaseLock{}
	for _, field := range []struct {
		raw json.RawMessage
		dst *string
	}{
		{projection.Subject.ComponentReleaseSpecDigest, &out.ComponentReleaseSpecDigest},
		{projection.Subject.ScenarioRevisionSpecDigest, &out.ScenarioRevisionSpecDigest},
	} {
		if len(field.raw) == 0 {
			continue
		}
		var value *string
		if err = json.Unmarshal(field.raw, &value); err != nil {
			return out, err
		}
		if value == nil {
			return out, fmt.Errorf("subject digest must be a string")
		}
		*field.dst = *value
	}
	seen := map[string]string{}
	for _, rawSteps := range []json.RawMessage{projection.Plan.Steps, projection.Plan.ParentSteps} {
		var steps []json.RawMessage
		if err = json.Unmarshal(rawSteps, &steps); err != nil {
			return out, err
		}
		for _, rawStep := range steps {
			var step struct {
				ReleaseID         *string `json:"releaseId"`
				ReleaseSpecDigest *string `json:"releaseSpecDigest"`
			}
			if err = json.Unmarshal(rawStep, &step); err != nil {
				return out, err
			}
			if step.ReleaseID == nil || step.ReleaseSpecDigest == nil {
				return out, fmt.Errorf("step release identity is missing")
			}
			id, digest := *step.ReleaseID, *step.ReleaseSpecDigest
			if id == "" {
				continue
			}
			if old, ok := seen[id]; ok {
				if old != digest {
					return out, fmt.Errorf("conflicting release identity %s", id)
				}
				continue
			}
			seen[id] = digest
			out.ReleaseLocks = append(out.ReleaseLocks, RunReleaseLock{ReleaseID: id, ReleaseSpecDigest: digest})
		}
	}
	return out, nil
}
