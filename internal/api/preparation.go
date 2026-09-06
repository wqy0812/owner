package api

import (
	"codex/platform-demo/internal/service"
	"net/http"
)

func (h *Handler) executorHealth(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, h.platform.Preparations().Health(r.Context()))
}
func (h *Handler) createPreparation(w http.ResponseWriter, r *http.Request) {
	var input service.PreparationInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	value, err := h.platform.Preparations().Create(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, value)
}
func (h *Handler) getPreparation(w http.ResponseWriter, r *http.Request) {
	value, err := h.platform.Preparations().Get(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, value)
}
func (h *Handler) listPreparations(w http.ResponseWriter, r *http.Request) {
	value, err := h.platform.Preparations().List(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, value)
}
func (h *Handler) cancelPreparation(w http.ResponseWriter, r *http.Request) {
	value, err := h.platform.Preparations().Cancel(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, value)
}
