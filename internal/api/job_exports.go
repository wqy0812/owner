package api

import (
	"bytes"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
	"fmt"
	"net/http"
)

func (h *Handler) previewScenarioJob(w http.ResponseWriter, r *http.Request) {
	var input service.ScenarioJobRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	plan, err := h.platform.Execution().PreviewScenarioJob(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}
func (h *Handler) exportScenarioJob(w http.ResponseWriter, r *http.Request) {
	var input service.ScenarioJobRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	bundle, err := h.platform.Execution().ExportScenarioJob(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	defer bundle.Close()
	var content bytes.Buffer
	if err := bundle.WriteArchive(&content); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="scenario-job.tar.gz"`)
	_, _ = w.Write(content.Bytes())
}
func (h *Handler) downloadRunJob(w http.ResponseWriter, r *http.Request) {
	var content bytes.Buffer
	var err error
	verified := r.URL.Query().Get("verified")
	if verified != "" && verified != "true" && verified != "false" {
		writeError(w, fmt.Errorf("%w: verified must be true or false", domain.ErrInvalid))
		return
	}
	if verified == "true" {
		err = h.platform.Execution().DownloadVerifiedRunJob(r.Context(), currentUser(r), r.PathValue("id"), &content)
	} else {
		err = h.platform.Execution().DownloadRunJob(r.Context(), currentUser(r), r.PathValue("id"), &content)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	filename := "run-job.tar.gz"
	if verified == "true" {
		filename = "verified-cluster-job.tar.gz"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write(content.Bytes())
}
