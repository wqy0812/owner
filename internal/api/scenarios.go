package api

import (
	"net/http"
	"sort"
	"strings"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
)

type graphInput struct {
	Nodes []flowNodeInput       `json:"nodes"`
	Edges []domain.ScenarioEdge `json:"edges"`
}

type flowNodeInput struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Position domain.GraphPosition `json:"position"`
	Data     struct {
		Label             string            `json:"label"`
		ReleaseID         string            `json:"releaseId"`
		Action            domain.ActionKind `json:"action"`
		ParameterValues   map[string]any    `json:"parameterValues"`
		DependencySources map[string]string `json:"dependencySources"`
	} `json:"data"`
}

func (input graphInput) domain() domain.ScenarioGraph {
	graph := domain.ScenarioGraph{Edges: input.Edges}
	for _, flow := range input.Nodes {
		graph.Nodes = append(graph.Nodes, domain.ScenarioNode{
			ID: flow.ID, Name: flow.Data.Label, ReleaseID: flow.Data.ReleaseID,
			Action:            flow.Data.Action,
			ParameterValues:   flow.Data.ParameterValues,
			DependencySources: flow.Data.DependencySources, Position: flow.Position,
		})
	}
	return graph
}

func (h *Handler) listScenarios(w http.ResponseWriter, r *http.Request) {
	scenarios, err := h.platform.Scenarios().List(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	releases, err := h.platform.Scenarios().ListReleaseDisplayMetadata(r.Context())
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
	scenario, err := h.platform.Scenarios().Create(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeScenarioDTO(w, r, http.StatusCreated, scenario)
}

func (h *Handler) deleteScenario(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.Scenarios().Delete(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) cloneScenarioRevision(w http.ResponseWriter, r *http.Request) {
	var input service.ScenarioCloneRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	revision, err := h.platform.Scenarios().CloneRevision(r.Context(), currentUser(r), r.PathValue("id"), input)
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
	plan, err := h.platform.Scenarios().PreviewClone(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}

func (h *Handler) abandonScenarioRevision(w http.ResponseWriter, r *http.Request) {
	scenario, err := h.platform.Scenarios().AbandonRevision(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeScenarioDTO(w, r, http.StatusOK, scenario)
}

func (h *Handler) getScenario(w http.ResponseWriter, r *http.Request) {
	scenario, err := h.platform.Scenarios().Get(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeScenarioDTO(w, r, http.StatusOK, scenario)
}

func (h *Handler) saveScenarioGraph(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Graph                  graphInput     `json:"graph"`
		ExpectedDigest         string         `json:"expectedDigest"`
		EnvironmentConstraints map[string]any `json:"environmentConstraints"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	graph := input.Graph.domain()
	revision, err := h.platform.Scenarios().SaveGraphWithDigest(r.Context(), currentUser(r), r.PathValue("id"), graph, input.ExpectedDigest, input.EnvironmentConstraints)
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusOK, revision)
}

func (h *Handler) validateScenario(w http.ResponseWriter, r *http.Request) {
	issues, err := h.platform.Scenarios().Validate(r.Context(), currentUser(r), r.PathValue("id"))
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

func (h *Handler) scenarioParameterOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := h.platform.Scenarios().ParameterOverview(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, overview)
}

func (h *Handler) candidateReleaseSet(w http.ResponseWriter, r *http.Request) {
	set, err := h.platform.Releases().CandidateReleaseSet(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, set)
}

func (h *Handler) publishScenario(w http.ResponseWriter, r *http.Request) {
	revision, err := h.platform.Releases().PublishScenario(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusOK, revision)
}

func (h *Handler) deprecateScenario(w http.ResponseWriter, r *http.Request) {
	revision, err := h.platform.Scenarios().Deprecate(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	h.writeRevisionDTO(w, r, http.StatusOK, revision)
}

func (h *Handler) writeScenarioDTO(w http.ResponseWriter, r *http.Request, status int, scenario domain.Scenario) {
	metadata, err := h.platform.Scenarios().ListReleaseDisplayMetadata(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, status, h.scenarioDTO(r, scenario, metadata))
}

func (h *Handler) writeRevisionDTO(w http.ResponseWriter, r *http.Request, status int, revision domain.ScenarioRevision) {
	metadata, err := h.platform.Scenarios().ListReleaseDisplayMetadata(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, status, h.revisionDTO(r, revision, metadata))
}

func (h *Handler) scenarioDTO(r *http.Request, scenario domain.Scenario, releases map[string]store.ReleaseDisplayMetadata) map[string]any {
	owner, _ := h.platform.Scenarios().GetUser(r.Context(), scenario.OwnerID)
	sort.SliceStable(scenario.Revisions, func(i, j int) bool { return scenario.Revisions[i].Revision > scenario.Revisions[j].Revision })
	revisions := make([]map[string]any, 0, len(scenario.Revisions))
	var current map[string]any
	for _, revision := range scenario.Revisions {
		dto := h.revisionDTO(r, revision, releases)
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
		"environmentConstraints": scenario.EnvironmentConstraints,
		"ownerId":                scenario.OwnerID, "ownerName": owner.Name, "currentRevisionId": scenario.CurrentRevisionID,
		"forkedFromScenarioId": scenario.ForkedFromScenarioID, "forkedFromRevisionId": scenario.ForkedFromRevisionID, "forkedFromDigest": scenario.ForkedFromDigest,
		"currentRevision": current, "revisions": revisions, "createdAt": scenario.CreatedAt, "updatedAt": scenario.UpdatedAt,
	}
}

func (h *Handler) revisionDTO(r *http.Request, revision domain.ScenarioRevision, releases map[string]store.ReleaseDisplayMetadata) map[string]any {
	nodes := make([]map[string]any, 0, len(revision.Graph.Nodes))
	for _, node := range revision.Graph.Nodes {
		data := map[string]any{
			"label": node.Name, "componentId": "", "contractAvailability": "missing", "releaseId": node.ReleaseID, "action": node.Action, "hostGroup": node.HostGroup,
			"parameterValues":   node.ParameterValues,
			"dependencySources": node.DependencySources,
		}
		if release, ok := releases[node.ReleaseID]; ok {
			data["componentId"], data["version"] = release.ComponentID, release.Version
			data["componentOwnerId"], data["componentOwnerName"] = release.OwnerID, release.OwnerName
			data["contractAvailability"] = "available"
			viewer := currentUser(r)
			if release.Status == domain.ReleaseDraft && !release.Candidate && viewer.ID != release.OwnerID && viewer.Role != domain.RolePlatformAdmin {
				data["contractAvailability"] = "unshared"
			}
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
	evidence, evidenceErr := h.platform.Scenarios().TestEvidence(r.Context(), revision.ID)
	evidenceDTO := func(mode string) map[string]any {
		runID := evidence[mode]
		item := map[string]any{"valid": runID != "", "runId": runID}
		if runID == "" {
			if evidenceErr != nil {
				item["reason"] = strings.TrimPrefix(evidenceErr.Error(), "conflict: ")
			} else {
				item["reason"] = "尚无有效测试证据"
			}
		}
		return item
	}
	return map[string]any{
		"id": revision.ID, "scenarioId": revision.ScenarioID, "revision": revision.Revision,
		"installationTest": evidenceDTO("install"), "upgradeTest": evidenceDTO("upgrade"),
		"sourceRevisionId": revision.SourceRevisionID, "sourceRunId": revision.SourceRunID,
		"upgradeConstraints": revision.UpgradeConstraints, "acceptanceJobs": revision.AcceptanceJobs,
		"acceptanceParameters": revision.AcceptanceParameters, "acceptanceValues": revision.AcceptanceValues, "acceptanceBindings": revision.AcceptanceBindings,
		"acceptanceWorkspaceRoot": revision.AcceptanceWorkspaceRoot, "acceptanceTreeSha256": revision.AcceptanceTreeSHA256,
		"revisionDigest": domain.ScenarioRevisionSpecDigest(revision), "digestVersion": revision.DigestVersion,
		"environmentConstraints": revision.EnvironmentConstraints, "state": state, "nodes": nodes, "edges": revision.Graph.Edges,
		"testPassedAt": revision.TestPassedAt, "releasedAt": revision.ReleasedAt, "abandonedAt": revision.AbandonedAt, "createdAt": revision.CreatedAt,
	}
}
