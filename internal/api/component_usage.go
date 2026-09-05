package api

import (
	"codex/platform-demo/internal/domain"
	"fmt"
	"net/http"
	"strconv"
)

func (h *Handler) componentUsage(w http.ResponseWriter, r *http.Request) {
	history := false
	if raw := r.URL.Query().Get("includeHistory"); raw != "" {
		var err error
		history, err = strconv.ParseBool(raw)
		if err != nil {
			writeError(w, fmt.Errorf("%w: invalid includeHistory", domain.ErrInvalid))
			return
		}
	}
	result, err := h.platform.Catalog().Usage(r.Context(), currentUser(r), r.PathValue("id"), r.URL.Query().Get("releaseId"), history)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, result)
}
