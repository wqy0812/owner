package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
)

type componentDTO struct {
	domain.Component
	OwnerName    string                        `json:"ownerName"`
	ReleaseCount int                           `json:"releaseCount"`
	ReleaseLines []domain.ComponentReleaseLine `json:"releaseLines"`
	ReadContext  *service.ComponentReadContext `json:"readContext,omitempty"`
}

// A Release contract is serialized once. Lines only carry its identity.
func (c componentDTO) MarshalJSON() ([]byte, error) {
	type plain componentDTO
	lines := make([]map[string]any, 0, len(c.ReleaseLines))
	for _, line := range c.ReleaseLines {
		ids := make([]string, 0, len(line.Releases))
		for _, release := range line.Releases {
			ids = append(ids, release.ID)
		}
		lines = append(lines, map[string]any{"id": line.ID, "componentId": line.ComponentID, "name": line.Name, "latestReleasedId": line.LatestReleasedID, "currentDraftId": line.CurrentDraftID, "evolutionEligible": line.EvolutionEligible, "evolutionParentId": line.EvolutionParentID, "evolutionBlockedReason": line.EvolutionBlockedReason, "releaseIds": ids, "createdAt": line.CreatedAt})
	}
	return json.Marshal(struct {
		plain
		Lines []map[string]any `json:"releaseLines"`
	}{plain(c), lines})
}

type releaseInput struct {
	Version                string                       `json:"version"`
	Status                 domain.ReleaseStatus         `json:"status"`
	ReleaseNotes           string                       `json:"releaseNotes"`
	Compatibility          domain.ReleaseCompatibility  `json:"compatibility"`
	RiskLevel              domain.RiskLevel             `json:"riskLevel"`
	EnvironmentConstraints map[string]any               `json:"environmentConstraints"`
	Parameters             []domain.ParameterDefinition `json:"parameters"`
	Dependencies           []componentDependencyInput   `json:"dependencies"`
	Actions                []componentActionInput       `json:"actions"`
}

type componentDependencyInput struct {
	Kind                string                    `json:"kind,omitempty"`
	UpstreamComponentID string                    `json:"upstreamComponentId"`
	UpstreamReleaseID   string                    `json:"upstreamReleaseId"`
	Purpose             string                    `json:"purpose"`
	ParameterMappings   []domain.ParameterMapping `json:"parameterMappings"`
}

type componentActionInput struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Kind                domain.ActionKind `json:"kind"`
	Tags                []string          `json:"tags"`
	HostGroup           string            `json:"hostGroup"`
	RequiredCredentials *[]string         `json:"requiredCredentials"`
	TimeoutSeconds      int               `json:"timeoutSeconds"`
	RiskLevel           domain.RiskLevel  `json:"riskLevel"`
	Destructive         bool              `json:"destructive"`
	Idempotent          bool              `json:"idempotent"`
	FromReleaseID       string            `json:"fromReleaseId"`
	ToReleaseID         string            `json:"toReleaseId"`
}

type releaseContractInput struct {
	Parameters   []domain.ParameterDefinition `json:"parameters"`
	Dependencies []componentDependencyInput   `json:"dependencies"`
}

func dependencyInputs(inputs []componentDependencyInput) []domain.ComponentDependency {
	dependencies := make([]domain.ComponentDependency, 0, len(inputs))
	for _, dependency := range inputs {
		dependencies = append(dependencies, domain.ComponentDependency{
			UpstreamComponentID: dependency.UpstreamComponentID,
			UpstreamReleaseID:   dependency.UpstreamReleaseID,
			Purpose:             dependency.Purpose,
			ParameterMappings:   dependency.ParameterMappings, Kind: dependency.Kind,
		})
	}
	return dependencies
}

func (input releaseInput) domain(existing *domain.ComponentRelease) domain.ComponentRelease {
	release := domain.ComponentRelease{
		Version: input.Version, Status: input.Status, ReleaseNotes: input.ReleaseNotes,
		Compatibility: input.Compatibility, RiskLevel: input.RiskLevel,
		EnvironmentConstraints: input.EnvironmentConstraints, Parameters: input.Parameters,
	}
	release.Dependencies = dependencyInputs(input.Dependencies)
	for index, inputAction := range input.Actions {
		risk := inputAction.RiskLevel
		if risk == "" {
			risk = domain.RiskLow
		}
		requiredCredentials := inputAction.RequiredCredentials
		if requiredCredentials == nil && existing != nil {
			for existingIndex := range existing.Actions {
				candidate := &existing.Actions[existingIndex]
				if (inputAction.ID != "" && candidate.ID == inputAction.ID) || (inputAction.ID == "" && existingIndex == index) {
					preserved := append([]string(nil), candidate.RequiredCredentials...)
					requiredCredentials = &preserved
					break
				}
			}
		}
		if requiredCredentials == nil {
			empty := []string{}
			requiredCredentials = &empty
		}
		release.Actions = append(release.Actions, domain.ActionDefinition{
			ID: inputAction.ID, Name: inputAction.Name, Kind: inputAction.Kind, Tags: inputAction.Tags,
			HostGroup:           inputAction.HostGroup,
			RequiredCredentials: append([]string(nil), (*requiredCredentials)...),
			TimeoutSeconds:      inputAction.TimeoutSeconds, RiskLevel: risk,
			Destructive: inputAction.Destructive, Idempotent: inputAction.Idempotent,
			FromReleaseID: inputAction.FromReleaseID, ToReleaseID: inputAction.ToReleaseID,
		})
	}
	return release
}

func (h *Handler) listComponents(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")
	if view != "" && view != "contracts" {
		writeError(w, fmt.Errorf("%w: invalid component view", domain.ErrInvalid))
		return
	}
	if view == "" {
		summaries, err := h.platform.Catalog().ListComponentSummaries(r.Context(), currentUser(r))
		if err != nil {
			writeError(w, err)
			return
		}
		writeItems(w, summaries)
		return
	}
	components, err := h.platform.Catalog().ListComponents(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	output := make([]componentDTO, 0, len(components))
	for _, component := range components {
		output = append(output, h.componentDTO(r, component))
	}
	writeItems(w, output)
}

func (h *Handler) getComponent(w http.ResponseWriter, r *http.Request) {
	component, err := h.platform.Catalog().Get(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	readContext, err := h.platform.Catalog().ReadContext(r.Context(), currentUser(r), component)
	if err != nil {
		writeError(w, err)
		return
	}
	for i := range component.Releases {
		component.Releases[i].PlaybookFileCount = len(component.Releases[i].PlaybookFiles)
		component.Releases[i].PlaybookFiles = nil
	}
	dto := h.componentDTO(r, component)
	dto.ReadContext = &readContext
	writeData(w, http.StatusOK, dto)
}

func (h *Handler) createComponent(w http.ResponseWriter, r *http.Request) {
	var input domain.Component
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	component, err := h.platform.Catalog().Create(r.Context(), currentUser(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, h.componentDTO(r, component))
}

func (h *Handler) updateComponent(w http.ResponseWriter, r *http.Request) {
	var input domain.Component
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	component, err := h.platform.Catalog().Update(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.componentDTO(r, component))
}

func (h *Handler) updateRelease(w http.ResponseWriter, r *http.Request) {
	var input releaseInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	existing, err := h.platform.Catalog().GetComponentRelease(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	release, err := h.platform.Catalog().UpdateRelease(r.Context(), currentUser(r), r.PathValue("id"), input.domain(&existing))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) updateReleaseContract(w http.ResponseWriter, r *http.Request) {
	var input releaseContractInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	release, err := h.platform.Catalog().UpdateReleaseContract(
		r.Context(), currentUser(r), r.PathValue("id"), input.Parameters, dependencyInputs(input.Dependencies),
	)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) createReleaseDraft(w http.ResponseWriter, r *http.Request) {
	var input service.ReleaseDraftRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	release, err := h.platform.Catalog().CreateReleaseDraft(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, release)
}

func (h *Handler) previewReleaseDraft(w http.ResponseWriter, r *http.Request) {
	var input service.ReleaseDraftRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	plan, err := h.platform.Catalog().PreviewReleaseDraft(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}

func (h *Handler) renameReleaseLine(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	line, err := h.platform.Catalog().RenameReleaseLine(r.Context(), currentUser(r), r.PathValue("id"), input.Name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, line)
}

func (h *Handler) releaseImpact(w http.ResponseWriter, r *http.Request) {
	var report domain.ImpactReport
	var err error
	if r.URL.Query().Get("operation") == "publish" {
		report, err = h.platform.Catalog().PublicationImpact(r.Context(), currentUser(r), r.PathValue("id"))
	} else {
		report, err = h.platform.Catalog().Impact(r.Context(), currentUser(r), r.PathValue("id"))
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.impactDTO(r, report))
}

func (h *Handler) publishRelease(w http.ResponseWriter, r *http.Request) {
	release, _, err := h.platform.Releases().PublishRelease(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) deprecateRelease(w http.ResponseWriter, r *http.Request) {
	release, err := h.platform.Catalog().DeprecateRelease(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) restoreRelease(w http.ResponseWriter, r *http.Request) {
	release, err := h.platform.Catalog().RestoreRelease(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) deleteRelease(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.Catalog().DeleteRelease(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) setReleaseCandidate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Candidate bool `json:"candidate"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	release, err := h.platform.Catalog().SetReleaseCandidate(r.Context(), currentUser(r), r.PathValue("id"), input.Candidate)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) submitReleaseReview(w http.ResponseWriter, r *http.Request) {
	release, err := h.platform.Catalog().SubmitReleaseReview(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) previewReleaseReview(w http.ResponseWriter, r *http.Request) {
	preview, err := h.platform.Catalog().PreviewReleaseReview(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, preview)
}

func (h *Handler) decideReleaseReview(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Decision              string `json:"decision"`
		Comment               string `json:"comment"`
		ExpectedPreviewDigest string `json:"expectedPreviewDigest"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if input.Decision != "approve" && input.Decision != "reject" {
		writeError(w, fmt.Errorf("%w: decision must be approve or reject", domain.ErrInvalid))
		return
	}
	release, err := h.platform.Catalog().DecideReleaseReview(r.Context(), currentUser(r), r.PathValue("id"), input.Decision == "approve", input.Comment, input.ExpectedPreviewDigest)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) testRelease(w http.ResponseWriter, r *http.Request) {
	var input service.ComponentTestRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	run, err := h.platform.Execution().StartComponentTest(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, h.runDTO(r, run))
}

func (h *Handler) previewReleaseTest(w http.ResponseWriter, r *http.Request) {
	var input service.ComponentTestRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	plan, err := h.platform.Execution().PreviewComponentTest(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}

func (h *Handler) componentDTO(r *http.Request, component domain.Component) componentDTO {
	user, _ := h.platform.Catalog().GetUser(r.Context(), component.OwnerID)
	sort.SliceStable(component.Releases, func(i, j int) bool { return component.Releases[i].CreatedAt.After(component.Releases[j].CreatedAt) })
	output := componentDTO{Component: component, OwnerName: user.Name, ReleaseCount: len(component.Releases), ReleaseLines: []domain.ComponentReleaseLine{}}
	lineIndex := map[string]int{}
	for _, release := range component.Releases {
		index, ok := lineIndex[release.LineID]
		if !ok {
			index = len(output.ReleaseLines)
			lineIndex[release.LineID] = index
			output.ReleaseLines = append(output.ReleaseLines, domain.ComponentReleaseLine{ID: release.LineID, ComponentID: component.ID, Name: release.LineName, Releases: []domain.ComponentRelease{}, CreatedAt: release.CreatedAt})
		}
		line := &output.ReleaseLines[index]
		line.Releases = append(line.Releases, release)
		if release.CreatedAt.Before(line.CreatedAt) {
			line.CreatedAt = release.CreatedAt
		}
		if release.Status == domain.ReleaseDraft && line.CurrentDraftID == "" {
			line.CurrentDraftID = release.ID
		}
		if release.Status == domain.ReleaseReleased && line.LatestReleasedID == "" {
			line.LatestReleasedID = release.ID
		}
	}
	for index := range output.ReleaseLines {
		line := &output.ReleaseLines[index]
		if line.CurrentDraftID != "" {
			line.EvolutionBlockedReason = "发布线已有活动 Draft"
			continue
		}
		var latestPublished *domain.ComponentRelease
		for releaseIndex := range line.Releases {
			release := &line.Releases[releaseIndex]
			if release.ReleasedAt == nil || (latestPublished != nil && !release.ReleasedAt.After(*latestPublished.ReleasedAt)) {
				continue
			}
			latestPublished = release
		}
		if latestPublished == nil {
			line.EvolutionBlockedReason = "发布线尚无已发布版本"
		} else if latestPublished.Status == domain.ReleaseDeprecated {
			line.EvolutionBlockedReason = fmt.Sprintf("最后一个曾发布版本 %s 已废弃，请创建新发布线", latestPublished.Version)
		} else {
			line.EvolutionEligible = true
			line.EvolutionParentID = latestPublished.ID
		}
	}
	sort.SliceStable(output.ReleaseLines, func(i, j int) bool { return output.ReleaseLines[i].CreatedAt.After(output.ReleaseLines[j].CreatedAt) })
	return output
}

func (h *Handler) impactDTO(r *http.Request, report domain.ImpactReport) map[string]any {
	componentOwners := []map[string]any{}
	scenarioOwners := []map[string]any{}
	scenarios := map[string]map[string]any{}
	paths := make([][]string, 0)
	pathSeen := map[string]bool{}
	for _, recipient := range report.Recipients {
		user, _ := h.platform.Catalog().GetUser(r.Context(), recipient.UserID)
		entry := map[string]any{"id": recipient.UserID, "name": user.Name}
		if recipient.Role == domain.RoleComponentOwner {
			componentOwners = append(componentOwners, entry)
		} else {
			scenarioOwners = append(scenarioOwners, entry)
		}
		for _, scenarioID := range recipient.ScenarioIDs {
			if scenario, err := h.platform.Scenarios().Get(r.Context(), currentUser(r), scenarioID); err == nil {
				scenarios[scenarioID] = map[string]any{"id": scenario.ID, "name": scenario.Name}
			}
		}
		for _, path := range recipient.Paths {
			key := ""
			for _, name := range path.ComponentNames {
				key += "\x00" + name
			}
			if !pathSeen[key] {
				pathSeen[key] = true
				paths = append(paths, path.ComponentNames)
			}
		}
	}
	scenarioList := make([]map[string]any, 0, len(scenarios))
	for _, scenario := range scenarios {
		scenarioList = append(scenarioList, scenario)
	}
	sort.Slice(scenarioList, func(i, j int) bool { return scenarioList[i]["name"].(string) < scenarioList[j]["name"].(string) })
	return map[string]any{
		"changeKind": report.ChangeKind, "lineId": report.LineID, "lineName": report.LineName,
		"fromReleaseId": report.FromReleaseID, "toReleaseId": report.ToReleaseID,
		"componentOwners": componentOwners, "scenarioOwners": scenarioOwners, "scenarios": scenarioList,
		"paths": paths, "scenarioRunCount": report.ScenarioRunCount,
	}
}
