package api

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
)

func (h *Handler) getReleasePlaybook(w http.ResponseWriter, r *http.Request) {
	actionID := strings.TrimSpace(r.URL.Query().Get("actionId"))
	if actionID == "" {
		writeError(w, fmt.Errorf("%w: actionId is required", domain.ErrInvalid))
		return
	}
	playbook, err := h.platform.Catalog().ReadActionSource(r.Context(), currentUser(r), r.PathValue("id"), actionID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, playbook)
}

func (h *Handler) saveReleasePlaybook(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ActionKind           domain.ActionKind  `json:"actionKind"`
		Content              string             `json:"content"`
		ExpectedSHA256       *string            `json:"expectedSha256"`
		ExpectedTreeSHA256   *string            `json:"expectedTreeSha256"`
		Action               *actionSourceInput `json:"action"`
		ConfirmYAMLMigration bool               `json:"confirmYamlMigration,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if input.ActionKind == "" || input.Action == nil || input.Action.Kind != input.ActionKind {
		writeError(w, fmt.Errorf("%w: complete action metadata matching actionKind is required", domain.ErrInvalid))
		return
	}
	if input.ExpectedSHA256 == nil || input.ExpectedTreeSHA256 == nil {
		writeError(w, fmt.Errorf("%w: expectedSha256 and expectedTreeSha256 are required", domain.ErrInvalid))
		return
	}
	playbook, err := h.platform.Catalog().SaveActionAtomic(r.Context(), currentUser(r), r.PathValue("id"), domain.ActionDefinition(*input.Action), []byte(input.Content), input.ExpectedSHA256, input.ExpectedTreeSHA256, input.ConfirmYAMLMigration)
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
	file, _, err := r.FormFile("playbook")
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
	kind := domain.ActionKind(strings.TrimSpace(r.FormValue("actionKind")))
	expectedSHA256 := optionalFormValue(r, "expectedSha256")
	expectedTreeSHA256 := optionalFormValue(r, "expectedTreeSha256")
	if kind == "" || expectedSHA256 == nil || expectedTreeSHA256 == nil {
		writeError(w, fmt.Errorf("%w: actionKind, expectedSha256 and expectedTreeSha256 are required", domain.ErrInvalid))
		return
	}
	var action actionSourceInput
	raw := strings.TrimSpace(r.FormValue("action"))
	if raw == "" {
		writeError(w, fmt.Errorf("%w: complete action metadata is required", domain.ErrInvalid))
		return
	}
	if err := json.Unmarshal([]byte(raw), &action); err != nil || action.Kind != kind {
		writeError(w, fmt.Errorf("%w: action metadata is malformed or does not match actionKind", domain.ErrInvalid))
		return
	}
	playbook, err := h.platform.Catalog().SaveActionAtomic(r.Context(), currentUser(r), r.PathValue("id"), domain.ActionDefinition(action), contents, expectedSHA256, expectedTreeSHA256, r.FormValue("confirmYamlMigration") == "true")
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, playbook)
}

func (h *Handler) deleteReleasePlaybook(w http.ResponseWriter, r *http.Request) {
	actionID := strings.TrimSpace(r.URL.Query().Get("actionId"))
	expectedSHA256 := optionalQueryValue(r, "expectedSha256")
	expectedTreeSHA256 := optionalQueryValue(r, "expectedTreeSha256")
	if expectedSHA256 == nil || expectedTreeSHA256 == nil {
		writeError(w, fmt.Errorf("%w: expectedSha256 and expectedTreeSha256 are required", domain.ErrInvalid))
		return
	}
	workspace, err := h.platform.Catalog().DeleteActionSource(r.Context(), currentUser(r), r.PathValue("id"), actionID, expectedSHA256, expectedTreeSHA256)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, workspace)
}

func (h *Handler) listReleasePlaybookWorkspace(w http.ResponseWriter, r *http.Request) {
	workspace, err := h.platform.Catalog().ListPlaybookWorkspace(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, workspace)
}

func (h *Handler) getReleasePlaybookWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	file, contents, err := h.platform.Catalog().ReadPlaybookWorkspaceFile(r.Context(), currentUser(r), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, err)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Type", file.MediaType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Path[strings.LastIndex(file.Path, "/")+1:]}))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(contents)
		return
	}
	writeData(w, http.StatusOK, file)
}

func (h *Handler) saveReleasePlaybookWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path               string  `json:"path"`
		Content            string  `json:"content"`
		ExpectedSHA256     *string `json:"expectedSha256"`
		ExpectedTreeSHA256 *string `json:"expectedTreeSha256"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if len([]byte(input.Content)) > service.MaxPlaybookBytes {
		writeError(w, fmt.Errorf("%w: online editable files cannot exceed 1 MiB", domain.ErrInvalid))
		return
	}
	if input.ExpectedSHA256 == nil {
		writeError(w, fmt.Errorf("%w: expectedSha256 is required", domain.ErrInvalid))
		return
	}
	file, err := h.platform.Catalog().SavePlaybookWorkspaceFileWithExpectation(r.Context(), currentUser(r), r.PathValue("id"), input.Path, []byte(input.Content), input.ExpectedSHA256, input.ExpectedTreeSHA256)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, file)
}

func (h *Handler) uploadReleasePlaybookWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxWorkspaceFileBytes+(256<<10))
	if err := r.ParseMultipartForm(service.MaxWorkspaceFileBytes); err != nil {
		writeError(w, fmt.Errorf("%w: malformed workspace upload: %v", domain.ErrInvalid, err))
		return
	}
	input, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, fmt.Errorf("%w: workspace file is required", domain.ErrInvalid))
		return
	}
	defer input.Close()
	contents, err := io.ReadAll(io.LimitReader(input, service.MaxWorkspaceFileBytes+1))
	if err != nil {
		writeError(w, err)
		return
	}
	if len(contents) > service.MaxWorkspaceFileBytes {
		writeError(w, fmt.Errorf("%w: workspace file exceeds 10 MiB", domain.ErrInvalid))
		return
	}
	path := strings.TrimSpace(r.FormValue("path"))
	if path == "" {
		path = header.Filename
	}
	expectedSHA256 := optionalFormValue(r, "expectedSha256")
	expectedTreeSHA256 := optionalFormValue(r, "expectedTreeSha256")
	if expectedSHA256 == nil {
		writeError(w, fmt.Errorf("%w: expectedSha256 is required", domain.ErrInvalid))
		return
	}
	file, err := h.platform.Catalog().SavePlaybookWorkspaceFileWithExpectation(r.Context(), currentUser(r), r.PathValue("id"), path, contents, expectedSHA256, expectedTreeSHA256)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, file)
}

func (h *Handler) renameReleasePlaybookWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		From               string  `json:"from"`
		To                 string  `json:"to"`
		ExpectedSHA256     *string `json:"expectedSha256"`
		ExpectedTreeSHA256 *string `json:"expectedTreeSha256"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if input.ExpectedSHA256 == nil {
		writeError(w, fmt.Errorf("%w: expectedSha256 is required", domain.ErrInvalid))
		return
	}
	workspace, err := h.platform.Catalog().RenamePlaybookWorkspaceFileWithExpectation(r.Context(), currentUser(r), r.PathValue("id"), input.From, input.To, input.ExpectedSHA256, input.ExpectedTreeSHA256)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, workspace)
}

func (h *Handler) deleteReleasePlaybookWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	expectedSHA256 := optionalQueryValue(r, "expectedSha256")
	expectedTreeSHA256 := optionalQueryValue(r, "expectedTreeSha256")
	if expectedSHA256 == nil {
		writeError(w, fmt.Errorf("%w: expectedSha256 is required", domain.ErrInvalid))
		return
	}
	workspace, err := h.platform.Catalog().DeletePlaybookWorkspaceFileWithExpectation(r.Context(), currentUser(r), r.PathValue("id"), r.URL.Query().Get("path"), expectedSHA256, expectedTreeSHA256)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, workspace)
}

func optionalFormValue(r *http.Request, name string) *string {
	values, ok := r.MultipartForm.Value[name]
	if !ok || len(values) == 0 {
		return nil
	}
	value := strings.TrimSpace(values[0])
	return &value
}

func optionalQueryValue(r *http.Request, name string) *string {
	values, ok := r.URL.Query()[name]
	if !ok || len(values) == 0 {
		return nil
	}
	value := strings.TrimSpace(values[0])
	return &value
}
