package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"codex/platform-demo/internal/domain"
)

func artifactChecksum(r *http.Request) (string, error) {
	checksum := strings.TrimSpace(r.FormValue("sha256"))
	file, _, err := r.FormFile("checksumFile")
	if err == nil {
		defer file.Close()
		contents, readErr := io.ReadAll(io.LimitReader(file, 64<<10))
		if readErr != nil {
			return "", readErr
		}
		parsed, parseErr := serviceParseSHA256File(contents)
		if parseErr != nil {
			return "", parseErr
		}
		if checksum != "" && !strings.EqualFold(checksum, parsed) {
			return "", fmt.Errorf("%w: checksum text and checksum file do not match", domain.ErrInvalid)
		}
		checksum = parsed
	}
	if checksum == "" {
		return "", fmt.Errorf("%w: SHA-256 text or checksum file is required", domain.ErrInvalid)
	}
	return checksum, nil
}

func serviceParseSHA256File(contents []byte) (string, error) {
	fields := strings.Fields(string(contents))
	if len(fields) == 0 {
		return "", fmt.Errorf("%w: checksum file is empty", domain.ErrInvalid)
	}
	return fields[0], nil
}

func (h *Handler) uploadComponentArtifact(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, fmt.Errorf("%w: malformed artifact upload: %v", domain.ErrInvalid, err))
		return
	}
	defer r.MultipartForm.RemoveAll()
	checksum, err := artifactChecksum(r)
	if err != nil {
		writeError(w, err)
		return
	}
	file, header, err := r.FormFile("artifact")
	if err != nil {
		writeError(w, fmt.Errorf("%w: artifact file is required", domain.ErrInvalid))
		return
	}
	defer file.Close()
	artifact, err := h.platform.Catalog().UploadArtifact(r.Context(), currentUser(r), r.PathValue("id"), r.FormValue("environmentId"), r.FormValue("alias"), header.Filename, checksum, file)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, artifact)
}

func (h *Handler) registerComponentArtifact(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Alias     string `json:"alias"`
		Filename  string `json:"filename"`
		SourceURL string `json:"sourceUrl"`
		SHA256    string `json:"sha256"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	artifact, err := h.platform.Catalog().RegisterArtifact(r.Context(), currentUser(r), r.PathValue("id"), input.Alias, input.Filename, input.SourceURL, input.SHA256)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, artifact)
}

func (h *Handler) updateComponentArtifactSource(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SourceURL string `json:"sourceUrl"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	artifact, err := h.platform.Catalog().UpdateArtifactSource(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("alias"), input.SourceURL)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, artifact)
}

func (h *Handler) deleteComponentArtifact(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.Catalog().DeleteArtifact(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("alias")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"detached": true})
}
