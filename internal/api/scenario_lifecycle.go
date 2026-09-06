package api

import (
	"net/http"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
)

func (h *Handler) previewScenarioFork(w http.ResponseWriter, r *http.Request) {
	var input service.ScenarioForkRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	plan, err := h.platform.Scenarios().PreviewFork(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}
func (h *Handler) forkScenario(w http.ResponseWriter, r *http.Request) {
	var input service.ScenarioForkRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	scenario, err := h.platform.Scenarios().Fork(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeScenarioDTO(w, r, http.StatusCreated, scenario)
}
func (h *Handler) reopenScenarioRevision(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExpectedRevisionDigest string `json:"expectedRevisionDigest"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	revision, err := h.platform.Scenarios().ReopenRevision(r.Context(), currentUser(r), r.PathValue("id"), input.ExpectedRevisionDigest)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusOK, revision)
}
func (h *Handler) saveScenarioUpgradeConstraints(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Edges                  []domain.ScenarioEdge `json:"edges"`
		ExpectedRevisionDigest string                `json:"expectedRevisionDigest"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	revision, err := h.platform.Scenarios().SaveUpgradeConstraints(r.Context(), currentUser(r), r.PathValue("id"), input.Edges, input.ExpectedRevisionDigest)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusOK, revision)
}
func (h *Handler) previewScenarioExecution(w http.ResponseWriter, r *http.Request) {
	var input service.ScenarioExecutionRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	kind := domain.RunScenario
	if input.TestOnly {
		kind = domain.RunScenarioTest
	}
	plan, err := h.platform.Execution().PreviewScenarioExecution(r.Context(), currentUser(r), r.PathValue("id"), input, kind)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}
func (h *Handler) startScenarioLifecycleTest(w http.ResponseWriter, r *http.Request) {
	h.startScenarioLifecycle(w, r, domain.RunScenarioTest)
}
func (h *Handler) startScenarioLifecycleRun(w http.ResponseWriter, r *http.Request) {
	h.startScenarioLifecycle(w, r, domain.RunScenario)
}
func (h *Handler) startScenarioLifecycle(w http.ResponseWriter, r *http.Request, kind domain.RunKind) {
	var input service.ScenarioExecutionRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	run, err := h.platform.Execution().StartScenarioExecution(r.Context(), currentUser(r), r.PathValue("id"), input, kind)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, h.runDTO(r, run))
}
