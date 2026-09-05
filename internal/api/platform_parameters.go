package api

import (
	"net/http"

	"codex/platform-demo/internal/domain"
)

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
