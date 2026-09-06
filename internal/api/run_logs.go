package api

import (
	"fmt"
	"mime"
	"net/http"
	"strconv"

	"codex/platform-demo/internal/domain"
)

func (h *Handler) runActivity(w http.ResponseWriter, r *http.Request) {
	var afterID *int64
	query := r.URL.Query()
	if query.Has("afterId") {
		value, err := strconv.ParseInt(query.Get("afterId"), 10, 64)
		if err != nil || value < 0 {
			writeError(w, fmt.Errorf("%w: invalid afterId", domain.ErrInvalid))
			return
		}
		afterID = &value
	}
	limit := 500
	if query.Has("limit") {
		value, err := strconv.Atoi(query.Get("limit"))
		if err != nil || value < 1 || value > 2000 {
			writeError(w, fmt.Errorf("%w: invalid limit", domain.ErrInvalid))
			return
		}
		limit = value
	}
	out, err := h.platform.Execution().RunActivity(r.Context(), currentUser(r), r.PathValue("id"), afterID, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, out)
}

func (h *Handler) runDiagnostics(w http.ResponseWriter, r *http.Request) {
	out, err := h.platform.Execution().RunDiagnostics(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, out)
}
func (h *Handler) downloadRunLogs(w http.ResponseWriter, r *http.Request) {
	bundle, err := h.platform.Execution().RunLogBundle(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer bundle.Close()
	info, err := bundle.File.Stat()
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": bundle.Filename}))
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, bundle.Filename, info.ModTime(), bundle.File)
}
