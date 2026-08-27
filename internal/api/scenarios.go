package api

import (
	"net/http"
	"sort"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
)

type graphInput struct {
	Nodes           []flowNodeInput       `json:"nodes"`
	Edges           []domain.ScenarioEdge `json:"edges"`
	ExecutionPolicy map[string]any        `json:"executionPolicy"`
}

type flowNodeInput struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Position domain.GraphPosition `json:"position"`
	Data     struct {
		Label             string            `json:"label"`
		ReleaseID         string            `json:"releaseId"`
		Action            domain.ActionKind `json:"action"`
		HostGroup         string            `json:"hostGroup"`
		Values            map[string]any    `json:"values"`
		RunInputs         []string          `json:"runInputs"`
		DependencySources map[string]string `json:"dependencySources"`
	} `json:"data"`
}

func (input graphInput) domain() domain.ScenarioGraph {
	graph := domain.ScenarioGraph{Edges: input.Edges}
	for _, flow := range input.Nodes {
		graph.Nodes = append(graph.Nodes, domain.ScenarioNode{
			ID: flow.ID, Name: flow.Data.Label, ReleaseID: flow.Data.ReleaseID,
			Action: flow.Data.Action, HostGroup: flow.Data.HostGroup,
			Values: flow.Data.Values, RunInputs: flow.Data.RunInputs,
			DependencySources: flow.Data.DependencySources, Position: flow.Position,
		})
	}
	return graph
}

func (h *Handler) listScenarios(w http.ResponseWriter, r *http.Request) {
	scenarios, err := h.platform.ListScenarios(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	releases, err := h.platform.Store().ListReleaseDisplayMetadata(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	output := make([]map[string]any, 0, len(scenarios))
	for _, scenario := range scenarios {
		output = append(output, h.scenarioDTO(r, scenario, releases))
	}
	writeItems(w, output)
}

func (h *Handler) createScenario(w http.ResponseWriter, r *http.Request) {
	var input domain.Scenario
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	scenario, err := h.platform.CreateScenario(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeScenarioDTO(w, r, http.StatusCreated, scenario)
}

func (h *Handler) cloneScenarioRevision(w http.ResponseWriter, r *http.Request) {
	var input service.ScenarioCloneRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	revision, err := h.platform.CloneScenarioRevision(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusCreated, revision)
}

func (h *Handler) previewScenarioClone(w http.ResponseWriter, r *http.Request) {
	var input service.ScenarioCloneRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	plan, err := h.platform.PreviewScenarioClone(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}

func (h *Handler) abandonScenarioRevision(w http.ResponseWriter, r *http.Request) {
	scenario, err := h.platform.AbandonScenarioRevision(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeScenarioDTO(w, r, http.StatusOK, scenario)
}

func (h *Handler) getScenario(w http.ResponseWriter, r *http.Request) {
	scenario, err := h.platform.GetScenario(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeScenarioDTO(w, r, http.StatusOK, scenario)
}

func (h *Handler) saveScenarioGraph(w http.ResponseWriter, r *http.Request) {
	var input graphInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	graph := input.domain()
	if input.ExecutionPolicy == nil {
		if existing, getErr := h.platform.Store().GetScenarioRevision(r.Context(), r.PathValue("id")); getErr == nil {
			input.ExecutionPolicy = existing.ExecutionPolicy
		}
	}
	revision, err := h.platform.SaveScenarioGraph(r.Context(), currentUser(r), r.PathValue("id"), graph, input.ExecutionPolicy)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusOK, revision)
}

func (h *Handler) validateScenario(w http.ResponseWriter, r *http.Request) {
	issues, err := h.platform.ValidateScenario(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	messages := make([]string, 0, len(issues))
	for _, issue := range issues {
		message := issue.Message
		if issue.NodeID != "" {
			message = issue.NodeID + ": " + message
		}
		messages = append(messages, message)
	}
	writeData(w, http.StatusOK, map[string]any{"valid": len(issues) == 0, "errors": messages})
}

func (h *Handler) candidateReleaseSet(w http.ResponseWriter, r *http.Request) {
	set, err := h.platform.CandidateReleaseSet(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, set)
}

func (h *Handler) testScenario(w http.ResponseWriter, r *http.Request) {
	var input runInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	run, err := h.platform.StartScenarioTest(r.Context(), currentUser(r), r.PathValue("id"), input.EnvironmentID, input.RunInput)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, h.runDTO(r, run))
}

func (h *Handler) runScenario(w http.ResponseWriter, r *http.Request) {
	var input runInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	run, err := h.platform.StartScenarioRun(r.Context(), currentUser(r), r.PathValue("id"), input.EnvironmentID, input.RunInput)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, h.runDTO(r, run))
}

func (h *Handler) publishScenario(w http.ResponseWriter, r *http.Request) {
	revision, err := h.platform.PublishScenario(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusOK, revision)
}

func (h *Handler) deprecateScenario(w http.ResponseWriter, r *http.Request) {
	revision, err := h.platform.DeprecateScenario(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusOK, revision)
}

func (h *Handler) writeScenarioDTO(w http.ResponseWriter, r *http.Request, status int, scenario domain.Scenario) {
	metadata, err := h.platform.Store().ListReleaseDisplayMetadata(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, status, h.scenarioDTO(r, scenario, metadata))
}

func (h *Handler) writeRevisionDTO(w http.ResponseWriter, r *http.Request, status int, revision domain.ScenarioRevision) {
	metadata, err := h.platform.Store().ListReleaseDisplayMetadata(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, status, h.revisionDTO(revision, metadata))
}

func (h *Handler) scenarioDTO(r *http.Request, scenario domain.Scenario, releases map[string]store.ReleaseDisplayMetadata) map[string]any {
	owner, _ := h.platform.Store().GetUser(r.Context(), scenario.OwnerID)
	sort.SliceStable(scenario.Revisions, func(i, j int) bool { return scenario.Revisions[i].Revision > scenario.Revisions[j].Revision })
	revisions := make([]map[string]any, 0, len(scenario.Revisions))
	var current map[string]any
	for _, revision := range scenario.Revisions {
		dto := h.revisionDTO(revision, releases)
		revisions = append(revisions, dto)
		if revision.ID == scenario.CurrentRevisionID {
			current = dto
		}
	}
	if current == nil && len(revisions) > 0 {
		current = revisions[0]
	}
	return map[string]any{
		"id": scenario.ID, "slug": scenario.Slug, "name": scenario.Name, "description": scenario.Description,
		"ownerId": scenario.OwnerID, "ownerName": owner.Name, "currentRevisionId": scenario.CurrentRevisionID,
		"currentRevision": current, "revisions": revisions, "createdAt": scenario.CreatedAt, "updatedAt": scenario.UpdatedAt,
	}
}

func (h *Handler) revisionDTO(revision domain.ScenarioRevision, releases map[string]store.ReleaseDisplayMetadata) map[string]any {
	nodes := make([]map[string]any, 0, len(revision.Graph.Nodes))
	for _, node := range revision.Graph.Nodes {
		data := map[string]any{
			"label": node.Name, "releaseId": node.ReleaseID, "action": node.Action, "hostGroup": node.HostGroup,
			"values": node.Values, "runInputs": node.RunInputs,
			"dependencySources": node.DependencySources,
		}
		if release, ok := releases[node.ReleaseID]; ok {
			data["componentId"], data["version"] = release.ComponentID, release.Version
			if node.Name == "" {
				data["label"] = release.ComponentName
			}
		}
		nodes = append(nodes, map[string]any{"id": node.ID, "type": "component", "position": node.Position, "data": data})
	}
	state := revision.Status
	if revision.AbandonedAt != nil {
		state = "abandoned"
	}
	return map[string]any{
		"id": revision.ID, "scenarioId": revision.ScenarioID, "revision": revision.Revision,
		"state": state, "nodes": nodes, "edges": revision.Graph.Edges,
		"executionPolicy": revision.ExecutionPolicy,
		"testPassedAt":    revision.TestPassedAt, "releasedAt": revision.ReleasedAt, "abandonedAt": revision.AbandonedAt, "createdAt": revision.CreatedAt,
	}
}

type runInput struct {
	EnvironmentID string         `json:"environmentId"`
	RunInput      map[string]any `json:"runInput"`
}
