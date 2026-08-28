package api

import "net/http"

func (h *Handler) workbench(w http.ResponseWriter, r *http.Request) {
	workbench, err := h.platform.ReadModel().Workbench(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, workbench)
}
