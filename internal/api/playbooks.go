package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
)

func (h *Handler) getReleasePlaybook(w http.ResponseWriter, r *http.Request) {
	playbook, err := h.platform.ReadReleasePlaybook(r.Context(), currentUser(r), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, playbook)
}

func (h *Handler) saveReleasePlaybook(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	playbook, err := h.platform.SaveReleasePlaybook(r.Context(), currentUser(r), r.PathValue("id"), input.Filename, []byte(input.Content))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, playbook)
}

func (h *Handler) uploadReleasePlaybook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxPlaybookBytes+(256<<10))
	if err := r.ParseMultipartForm(service.MaxPlaybookBytes); err != nil {
		writeError(w, fmt.Errorf("%w: invalid Playbook upload: %v", domain.ErrInvalid, err))
		return
	}
	file, header, err := r.FormFile("playbook")
	if err != nil {
		writeError(w, fmt.Errorf("%w: playbook file is required", domain.ErrInvalid))
		return
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, service.MaxPlaybookBytes+1))
	if err != nil {
		writeError(w, fmt.Errorf("%w: read Playbook upload: %v", domain.ErrInvalid, err))
		return
	}
	if len(contents) > service.MaxPlaybookBytes {
		writeError(w, fmt.Errorf("%w: playbook exceeds 1 MiB", domain.ErrInvalid))
		return
	}
	filename := strings.TrimSpace(r.FormValue("filename"))
	if filename == "" {
		filename = header.Filename
	}
	playbook, err := h.platform.SaveReleasePlaybook(r.Context(), currentUser(r), r.PathValue("id"), filename, contents)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, playbook)
}
