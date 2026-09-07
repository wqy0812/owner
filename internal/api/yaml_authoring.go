package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"codex/platform-demo/internal/domain"
)

// Multipart JSON inputs use the same strict current contract as JSON requests.
type actionSourceInput domain.ActionDefinition
type acceptanceJobInput domain.ScenarioAcceptanceJob

func validateWorkspaceUploadFields(r *http.Request, fileField string, valueFields ...string) error {
	for name, values := range r.MultipartForm.Value {
		if !slices.Contains(valueFields, name) || len(values) != 1 {
			return fmt.Errorf("%w: unknown or repeated upload field %q", domain.ErrInvalid, name)
		}
	}
	for name, files := range r.MultipartForm.File {
		if name != fileField || len(files) != 1 {
			return fmt.Errorf("%w: unknown or repeated upload file %q", domain.ErrInvalid, name)
		}
	}
	return nil
}

func (a *actionSourceInput) UnmarshalJSON(data []byte) error {
	type plain actionSourceInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(a))
}

func (j *acceptanceJobInput) UnmarshalJSON(data []byte) error {
	type plain acceptanceJobInput
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(j))
}
