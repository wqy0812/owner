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
	build, err := h.platform.Catalog().StartImageBuild(r.Context(), currentUser(r), r.PathValue("id"), strings.TrimSpace(r.FormValue("environmentId")), strings.TrimSpace(r.FormValue("tag")), contents)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, build)
}

func (h *Handler) listComponentImageBuilds(w http.ResponseWriter, r *http.Request) {
	builds, err := h.platform.Catalog().ListImageBuilds(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, builds)
}

func (h *Handler) getComponentImageBuild(w http.ResponseWriter, r *http.Request) {
	build, err := h.platform.Catalog().GetImageBuild(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, build)
}

func (h *Handler) registerComponentImage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		LogicalName string `json:"logicalName"`
		SourceRef   string `json:"sourceRef"`
		Digest      string `json:"digest"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	image, err := h.platform.Catalog().RegisterImage(r.Context(), currentUser(r), r.PathValue("id"), input.LogicalName, input.SourceRef, input.Digest)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, image)
}

func (h *Handler) updateComponentImageSource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SourceRef string `json:"sourceRef"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	image, err := h.platform.Catalog().UpdateImageSource(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("name"), input.SourceRef)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, image)
}

func (h *Handler) deleteComponentImage(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.Catalog().DeleteImage(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("name")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": true})
}
