package api

import (
	"net/http"

	"codex/platform-demo/internal/service"
)

func (h *Handler) previewComponentImport(w http.ResponseWriter, r *http.Request) {
	var input service.ComponentImportRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	plan, err := h.platform.Catalog().PreviewImport(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}

func (h *Handler) importComponents(w http.ResponseWriter, r *http.Request) {
	var input service.ComponentImportRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	result, err := h.platform.Catalog().Import(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, result)
}
