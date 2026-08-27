package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"codex/platform-demo/internal/service"
)

var downloadNamePattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (h *Handler) exportEnvironmentRevision(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IncludeCredentialReferences bool `json:"includeCredentialReferences"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	document, err := h.platform.ExportEnvironmentRevision(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("revisionId"), input.IncludeCredentialReferences)
	if err != nil {
		writeError(w, err)
		return
	}
	name := downloadNamePattern.ReplaceAllString(document.Source.EnvironmentName, "-")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-r%d.json"`, name, document.Source.Revision))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(document)
}

func (h *Handler) previewEnvironmentImport(w http.ResponseWriter, r *http.Request) {
	var input service.EnvironmentImportRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	plan, err := h.platform.PreviewEnvironmentImport(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}

func (h *Handler) importEnvironment(w http.ResponseWriter, r *http.Request) {
	var input service.EnvironmentImportRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.ImportEnvironment(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, h.environmentDTO(r, environment))
}
