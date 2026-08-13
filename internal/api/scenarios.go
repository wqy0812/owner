package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"codex/platform-demo/internal/domain"
)

type graphInput struct {
	Nodes           []json.RawMessage     `json:"nodes"`
	Edges           []domain.ScenarioEdge `json:"edges"`
	ExecutionPolicy map[string]any        `json:"executionPolicy"`
}

type flowNodeInput struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Position domain.GraphPosition `json:"position"`
	Data     struct {
		Label     string            `json:"label"`
		ReleaseID string            `json:"releaseId"`
		Action    domain.ActionKind `json:"action"`
		HostGroup string            `json:"hostGroup"`
		Values    map[string]any    `json:"values"`
		Bindings  map[string]string `json:"bindings"`
		RunInputs []string          `json:"runInputs"`
	} `json:"data"`
}

func (input graphInput) domain() (domain.ScenarioGraph, error) {
	graph := domain.ScenarioGraph{Edges: input.Edges}
	for _, raw := range input.Nodes {
		var flow flowNodeInput
		if err := json.Unmarshal(raw, &flow); err != nil {
			return graph, fmt.Errorf("%w: invalid graph node: %v", domain.ErrInvalid, err)
		}
		if flow.Data.ReleaseID != "" {
			graph.Nodes = append(graph.Nodes, domain.ScenarioNode{
				ID: flow.ID, Name: flow.Data.Label, ReleaseID: flow.Data.ReleaseID,
				Action: flow.Data.Action, HostGroup: flow.Data.HostGroup,
				Values: flow.Data.Values, Bindings: flow.Data.Bindings, RunInputs: flow.Data.RunInputs, Position: flow.Position,
			})
			continue
		}
		var node domain.ScenarioNode
		if err := json.Unmarshal(raw, &node); err != nil {
			return graph, fmt.Errorf("%w: invalid domain graph node: %v", domain.ErrInvalid, err)
		}
		graph.Nodes = append(graph.Nodes, node)
	}
	return graph, nil
}

func (h *Handler) listScenarios(w http.ResponseWriter, r *http.Request) {
	scenarios, err := h.platform.ListScenarios(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	output := make([]map[string]any, 0, len(scenarios))
	for _, scenario := range scenarios {
		output = append(output, h.scenarioDTO(r, scenario))
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
	writeData(w, http.StatusCreated, h.scenarioDTO(r, scenario))
}

func (h *Handler) cloneScenarioRevision(w http.ResponseWriter, r *http.Request) {
	revision, err := h.platform.CloneScenarioRevision(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, h.revisionDTO(r, revision))
}

func (h *Handler) getScenario(w http.ResponseWriter, r *http.Request) {
	scenario, err := h.platform.GetScenario(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.scenarioDTO(r, scenario))
}

func (h *Handler) saveScenarioGraph(w http.ResponseWriter, r *http.Request) {
	var input graphInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	graph, err := input.domain()
	if err != nil {
		writeError(w, err)
		return
	}
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
	writeData(w, http.StatusOK, h.revisionDTO(r, revision))
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
	writeData(w, http.StatusOK, map[string]any{"valid": len(issues) == 0, "errors": messages, "issues": issues})
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
	writeData(w, http.StatusOK, h.revisionDTO(r, revision))
}

func (h *Handler) deprecateScenario(w http.ResponseWriter, r *http.Request) {
	revision, err := h.platform.DeprecateScenario(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.revisionDTO(r, revision))
}

func (h *Handler) scenarioDTO(r *http.Request, scenario domain.Scenario) map[string]any {
	owner, _ := h.platform.Store().GetUser(r.Context(), scenario.OwnerID)
	sort.SliceStable(scenario.Revisions, func(i, j int) bool { return scenario.Revisions[i].Revision > scenario.Revisions[j].Revision })
	revisions := make([]map[string]any, 0, len(scenario.Revisions))
	var current map[string]any
	for _, revision := range scenario.Revisions {
		dto := h.revisionDTO(r, revision)
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

func (h *Handler) revisionDTO(r *http.Request, revision domain.ScenarioRevision) map[string]any {
	nodes := make([]map[string]any, 0, len(revision.Graph.Nodes))
	for _, node := range revision.Graph.Nodes {
		data := map[string]any{
			"label": node.Name, "releaseId": node.ReleaseID, "action": node.Action, "hostGroup": node.HostGroup,
			"values": node.Values, "bindings": node.Bindings, "runInputs": node.RunInputs,
		}
		if release, err := h.platform.Store().GetComponentRelease(r.Context(), node.ReleaseID); err == nil {
			data["componentId"], data["version"] = release.ComponentID, release.Version
			if component, componentErr := h.platform.Store().GetComponent(r.Context(), release.ComponentID, false); componentErr == nil && node.Name == "" {
				data["label"] = component.Name
			}
		}
		if strings.Contains(node.HostGroup, "bootstrap") {
			data["phase"] = "BOOTSTRAP"
		} else {
			data["phase"] = "MANAGEMENT"
		}
		nodes = append(nodes, map[string]any{"id": node.ID, "type": "component", "position": node.Position, "data": data})
	}
	return map[string]any{
		"id": revision.ID, "scenarioId": revision.ScenarioID, "revision": revision.Revision,
		"state": revision.Status, "status": revision.Status, "nodes": nodes, "edges": revision.Graph.Edges,
		"graph": map[string]any{"nodes": nodes, "edges": revision.Graph.Edges}, "executionPolicy": revision.ExecutionPolicy,
		"testedAt": revision.TestPassedAt, "testPassedAt": revision.TestPassedAt, "releasedAt": revision.ReleasedAt, "createdAt": revision.CreatedAt,
	}
}

type runInput struct {
	EnvironmentID string         `json:"environmentId"`
	RunInput      map[string]any `json:"runInput"`
}
