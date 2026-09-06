package api

import (
	"fmt"
	"io"
	"mime"
	"net/http"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
)

func (h *Handler) readScenarioAcceptance(w http.ResponseWriter, r *http.Request) {
	definition, err := h.platform.Scenarios().ReadAcceptance(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, definition)
}

func (h *Handler) saveScenarioAcceptance(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExpectedRevisionDigest string                            `json:"expectedRevisionDigest"`
		Jobs                   []acceptanceJobInput              `json:"jobs"`
		Parameters             []domain.ParameterDefinition      `json:"parameters"`
		Values                 map[string]any                    `json:"values"`
		Bindings               []domain.ScenarioParameterBinding `json:"bindings"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	jobs := make([]domain.ScenarioAcceptanceJob, len(input.Jobs))
	for i, job := range input.Jobs {
		jobs[i] = domain.ScenarioAcceptanceJob(job)
	}
	definition, err := h.platform.Scenarios().SaveAcceptance(r.Context(), currentUser(r), r.PathValue("id"), service.ScenarioAcceptanceInput{ExpectedRevisionDigest: input.ExpectedRevisionDigest, Jobs: jobs, Parameters: input.Parameters, Values: input.Values, Bindings: input.Bindings})
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, definition)
}

func (h *Handler) listScenarioAcceptanceWorkspace(w http.ResponseWriter, r *http.Request) {
	workspace, err := h.platform.Scenarios().ListAcceptanceWorkspace(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, workspace)
}

func (h *Handler) readScenarioAcceptanceFile(w http.ResponseWriter, r *http.Request) {
	file, contents, err := h.platform.Scenarios().ReadAcceptanceFile(r.Context(), currentUser(r), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, err)
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Path}))
		_, _ = w.Write(contents)
		return
	}
	writeData(w, http.StatusOK, file)
}

func (h *Handler) saveScenarioAcceptanceFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		service.ScenarioWorkspaceExpectation
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if input.Path == "" {
		input.Path = r.URL.Query().Get("path")
	}
	file, err := h.platform.Scenarios().SaveAcceptanceFile(r.Context(), currentUser(r), r.PathValue("id"), input.Path, []byte(input.Content), input.ScenarioWorkspaceExpectation)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, file)
}

func (h *Handler) deleteScenarioAcceptanceFile(w http.ResponseWriter, r *http.Request) {
	expectation := service.ScenarioWorkspaceExpectation{ExpectedRevisionDigest: r.URL.Query().Get("expectedRevisionDigest"), ExpectedSHA256: optionalQueryValue(r, "expectedSha256"), ExpectedTreeSHA256: optionalQueryValue(r, "expectedTreeSha256")}
	workspace, err := h.platform.Scenarios().DeleteAcceptanceFile(r.Context(), currentUser(r), r.PathValue("id"), r.URL.Query().Get("path"), expectation)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, workspace)
}

func (h *Handler) uploadScenarioAcceptanceFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxWorkspaceFileBytes+(256<<10))
	if err := r.ParseMultipartForm(service.MaxWorkspaceFileBytes); err != nil {
		writeError(w, fmt.Errorf("%w: invalid acceptance file upload", domain.ErrInvalid))
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, fmt.Errorf("%w: file is required", domain.ErrInvalid))
		return
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, service.MaxWorkspaceFileBytes+1))
	if err != nil {
		writeError(w, err)
		return
	}
	expectation := service.ScenarioWorkspaceExpectation{ConfirmYAMLMigration: r.FormValue("confirmYamlMigration") == "true", ExpectedRevisionDigest: r.FormValue("expectedRevisionDigest"), ExpectedSHA256: optionalFormValue(r, "expectedSha256"), ExpectedTreeSHA256: optionalFormValue(r, "expectedTreeSha256")}
	result, err := h.platform.Scenarios().SaveAcceptanceFile(r.Context(), currentUser(r), r.PathValue("id"), r.FormValue("path"), contents, expectation)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, result)
}
