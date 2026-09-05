package api

import (
	"codex/platform-demo/internal/domain"
	"net/http"
)

type runIDsInput struct {
	RunIDs []string `json:"runIds"`
}

func (h *Handler) archiveRuns(w http.ResponseWriter, r *http.Request) {
	var in runIDsInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	out, err := h.platform.Execution().ArchiveRuns(r.Context(), currentUser(r), in.RunIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, out)
}
func (h *Handler) previewRunCleanup(w http.ResponseWriter, r *http.Request) {
	var in runIDsInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	out, err := h.platform.Execution().CleanupPreview(r.Context(), currentUser(r), in.RunIDs)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, out)
}
func (h *Handler) cleanupRuns(w http.ResponseWriter, r *http.Request) {
	var in runIDsInput
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := h.platform.Execution().CleanupRuns(r.Context(), currentUser(r), in.RunIDs); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": len(in.RunIDs)})
}
func (h *Handler) downloadRunArchive(w http.ResponseWriter, r *http.Request) {
	f, a, err := h.platform.Execution().ArchiveDownload(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="run-archive.tar.gz"`)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "run-archive.tar.gz", a.UpdatedAt, f)
}
func (h *Handler) runArchiveHealth(w http.ResponseWriter, r *http.Request) {
	out, err := h.platform.Execution().ArchiveHealth(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, out)
}
func (h *Handler) saveRunRetention(w http.ResponseWriter, r *http.Request) {
	var in domain.RunRetentionPolicy
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, err)
		return
	}
	if err := h.platform.Execution().SaveRetention(r.Context(), currentUser(r), in); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, in)
}
