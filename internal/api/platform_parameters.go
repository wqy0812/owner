package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"codex/platform-demo/internal/domain"
)

func (h *Handler) listEnvironmentParameterDefinitions(w http.ResponseWriter, r *http.Request) {
	items, err := h.platform.PlatformOptions().ListEnvironmentParameterDefinitions(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, items)
}

func (h *Handler) createEnvironmentParameterDefinition(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Label        string               `json:"label"`
		Description  string               `json:"description"`
		Type         domain.ParameterType `json:"type"`
		Enum         []any                `json:"enum"`
		MinLength    int                  `json:"minLength"`
		DefaultValue any                  `json:"defaultValue"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	item, err := h.platform.PlatformOptions().CreateEnvironmentParameterDefinition(r.Context(), currentUser(r), domain.EnvironmentParameterDefinition{Label: input.Label, Description: input.Description, Type: input.Type, Enum: input.Enum, MinLength: input.MinLength, DefaultValue: input.DefaultValue})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, item)
}

func (h *Handler) updateEnvironmentParameterDefault(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DefaultValue json.RawMessage `json:"defaultValue"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if len(input.DefaultValue) == 0 {
		writeError(w, fmt.Errorf("%w: defaultValue is required; use null to remove the default", domain.ErrInvalid))
		return
	}
	var value any
	if err := json.Unmarshal(input.DefaultValue, &value); err != nil {
		writeError(w, fmt.Errorf("%w: invalid defaultValue", domain.ErrInvalid))
		return
	}
	item, err := h.platform.PlatformOptions().UpdateEnvironmentParameterDefault(r.Context(), currentUser(r), r.PathValue("id"), value)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, item)
}

func (h *Handler) deleteEnvironmentParameterDefinition(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.PlatformOptions().DeleteEnvironmentParameterDefinition(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) listEnvironmentVariableDefinitions(w http.ResponseWriter, r *http.Request) {
	items, err := h.platform.PlatformOptions().ListEnvironmentVariableDefinitions(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, items)
}

func (h *Handler) createEnvironmentVariableDefinition(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Label       string `json:"label"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	item, err := h.platform.PlatformOptions().CreateEnvironmentVariableDefinition(r.Context(), currentUser(r), domain.EnvironmentVariableDefinition{Name: input.Name, Label: input.Label, Description: input.Description})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, item)
}

func (h *Handler) deleteEnvironmentVariableDefinition(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.PlatformOptions().DeleteEnvironmentVariableDefinition(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": true})
}
