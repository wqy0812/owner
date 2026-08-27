package api

import (
	"net/http"

	"codex/platform-demo/internal/domain"
)

func (h *Handler) listRunInputPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := h.platform.ListRunInputPresets(r.Context(), currentUser(r), r.URL.Query().Get("resourceType"), r.URL.Query().Get("resourceId"), r.URL.Query().Get("context"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, presets)
}

func (h *Handler) saveRunInputPreset(w http.ResponseWriter, r *http.Request) {
	var input domain.RunInputPreset
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if id := r.PathValue("id"); id != "" {
		input.ID = id
	}
	preset, err := h.platform.SaveRunInputPreset(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if r.Method == http.MethodPut {
		status = http.StatusOK
	}
	writeData(w, status, preset)
}

func (h *Handler) deleteRunInputPreset(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.DeleteRunInputPreset(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
