package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"codex/platform-demo/internal/domain"
)

// Read models retain legacy fields for history; authoring uses a separate
// decoder so a client cannot silently reintroduce the removed controls.
type actionSourceInput domain.ActionDefinition
type acceptanceJobInput domain.ScenarioAcceptanceJob

func rejectLegacyAuthoringJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for key, value := range raw {
		if strings.EqualFold(key, "gatherFacts") || strings.EqualFold(key, "runtimeChecks") {
			return fmt.Errorf("%w: %s 已移除，请在 YAML 中编写采集与检查", domain.ErrInvalid, key)
		}
		if !strings.EqualFold(key, "resourceContract") {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(value, &fields); err != nil {
			return err
		}
		for field := range fields {
			if strings.EqualFold(field, "checks") {
				return fmt.Errorf("%w: resourceContract.checks 已移除，请使用前置检查 YAML", domain.ErrInvalid)
			}
		}
	}
	return nil
}

func (a *actionSourceInput) UnmarshalJSON(data []byte) error {
	if err := rejectLegacyAuthoringJSON(data); err != nil {
		return err
	}
	type plain actionSourceInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(a))
}

func (j *acceptanceJobInput) UnmarshalJSON(data []byte) error {
	if err := rejectLegacyAuthoringJSON(data); err != nil {
		return err
	}
	type plain acceptanceJobInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(j))
}
