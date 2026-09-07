package api

import "net/http"

func (h *Handler) environmentRevisionDeletionImpact(w http.ResponseWriter, r *http.Request) {
	impact, err := h.platform.Environments().RevisionDeletionImpact(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("revisionId"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, impact)
}

func (h *Handler) deleteEnvironmentRevision(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.Environments().DeleteRevision(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("revisionId")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": true})
}
