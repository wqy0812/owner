package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"codex/platform-demo/internal/domain"
)

func (h *Handler) startComponentImageBuild(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, (1<<20)+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, fmt.Errorf("%w: Dockerfile upload is too large or malformed", domain.ErrInvalid))
		return
	}
	file, _, err := r.FormFile("dockerfile")
	if err != nil {
		writeError(w, fmt.Errorf("%w: dockerfile is required", domain.ErrInvalid))
		return
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		writeError(w, fmt.Errorf("%w: read Dockerfile: %v", domain.ErrInvalid, err))
		return
	}
	build, err := h.platform.StartComponentImageBuild(r.Context(), currentUser(r), r.PathValue("id"), strings.TrimSpace(r.FormValue("environmentId")), strings.TrimSpace(r.FormValue("tag")), contents)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, build)
}

func (h *Handler) listComponentImageBuilds(w http.ResponseWriter, r *http.Request) {
	builds, err := h.platform.ListComponentImageBuilds(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, builds)
}

func (h *Handler) getComponentImageBuild(w http.ResponseWriter, r *http.Request) {
	build, err := h.platform.GetComponentImageBuild(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, build)
}
