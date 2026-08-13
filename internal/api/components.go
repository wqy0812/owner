package api

import (
	"net/http"
	"sort"

	"codex/platform-demo/internal/domain"
)

type componentDTO struct {
	domain.Component
	OwnerName     string                   `json:"ownerName"`
	LatestRelease *domain.ComponentRelease `json:"latestRelease,omitempty"`
	ReleaseCount  int                      `json:"releaseCount"`
}

type releaseInput struct {
	Version                string                     `json:"version"`
	Type                   domain.ReleaseType         `json:"type"`
	Status                 domain.ReleaseStatus       `json:"status"`
	State                  domain.ReleaseStatus       `json:"state"`
	ReleaseNotes           string                     `json:"releaseNotes"`
	Breaking               bool                       `json:"breaking"`
	RiskLevel              domain.RiskLevel           `json:"riskLevel"`
	EnvironmentConstraints map[string]any             `json:"environmentConstraints"`
	ParameterSchema        map[string]any             `json:"parameterSchema"`
	Dependencies           []componentDependencyInput `json:"dependencies"`
	Actions                []componentActionInput     `json:"actions"`
}

type componentDependencyInput struct {
	ComponentID       string `json:"componentId"`
	ReleaseID         string `json:"releaseId"`
	UpstreamComponent string `json:"upstreamComponentId"`
	UpstreamRelease   string `json:"upstreamReleaseId"`
	Purpose           string `json:"purpose"`
}

type componentActionInput struct {
	Name              string            `json:"name"`
	Kind              domain.ActionKind `json:"kind"`
	Type              domain.ActionKind `json:"type"`
	Playbook          string            `json:"playbook"`
	Tags              []string          `json:"tags"`
	Limit             string            `json:"limit"`
	HostGroup         string            `json:"hostGroup"`
	AllowedParameters []string          `json:"allowedParameters"`
	TimeoutSeconds    int               `json:"timeoutSeconds"`
	RiskLevel         domain.RiskLevel  `json:"riskLevel"`
	Risk              string            `json:"risk"`
	Destructive       bool              `json:"destructive"`
	FromReleaseID     string            `json:"fromReleaseId"`
	ToReleaseID       string            `json:"toReleaseId"`
}

func (input releaseInput) domain() domain.ComponentRelease {
	status := input.Status
	if status == "" {
		status = input.State
	}
	release := domain.ComponentRelease{
		Version: input.Version, Type: input.Type, Status: status, ReleaseNotes: input.ReleaseNotes,
		Breaking: input.Breaking, RiskLevel: input.RiskLevel,
		EnvironmentConstraints: input.EnvironmentConstraints, ParameterSchema: input.ParameterSchema,
	}
	for _, dependency := range input.Dependencies {
		componentID, releaseID := dependency.UpstreamComponent, dependency.UpstreamRelease
		if componentID == "" {
			componentID = dependency.ComponentID
		}
		if releaseID == "" {
			releaseID = dependency.ReleaseID
		}
		release.Dependencies = append(release.Dependencies, domain.ComponentDependency{UpstreamComponentID: componentID, UpstreamReleaseID: releaseID, Purpose: dependency.Purpose})
	}
	for _, inputAction := range input.Actions {
		kind := inputAction.Kind
		if kind == "" {
			kind = inputAction.Type
		}
		risk := inputAction.RiskLevel
		if risk == "" {
			if inputAction.Risk == "destructive" {
				risk = domain.RiskDestructive
			} else {
				risk = domain.RiskLow
			}
		}
		release.Actions = append(release.Actions, domain.ActionDefinition{
			Name: inputAction.Name, Kind: kind, Playbook: inputAction.Playbook, Tags: inputAction.Tags,
			Limit: inputAction.Limit, HostGroup: inputAction.HostGroup, AllowedParameters: inputAction.AllowedParameters,
			TimeoutSeconds: inputAction.TimeoutSeconds, RiskLevel: risk,
			Destructive:   inputAction.Destructive || inputAction.Risk == "destructive",
			FromReleaseID: inputAction.FromReleaseID, ToReleaseID: inputAction.ToReleaseID,
		})
	}
	return release
}

func (h *Handler) listComponents(w http.ResponseWriter, r *http.Request) {
	components, err := h.platform.ListComponents(r.Context(), currentUser(r))
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
	component, err := h.platform.GetComponent(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.componentDTO(r, component))
}

func (h *Handler) createComponent(w http.ResponseWriter, r *http.Request) {
	var input domain.Component
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	component, err := h.platform.CreateComponent(r.Context(), currentUser(r), input)
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
	component, err := h.platform.UpdateComponent(r.Context(), currentUser(r), r.PathValue("id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.componentDTO(r, component))
}

func (h *Handler) createRelease(w http.ResponseWriter, r *http.Request) {
	var input releaseInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	release, err := h.platform.CreateRelease(r.Context(), currentUser(r), r.PathValue("id"), input.domain())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, release)
}

func (h *Handler) updateRelease(w http.ResponseWriter, r *http.Request) {
	var input releaseInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	release, err := h.platform.UpdateRelease(r.Context(), currentUser(r), r.PathValue("id"), input.domain())
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) cloneRelease(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version      string `json:"version"`
		ReleaseNotes string `json:"releaseNotes"`
		Breaking     bool   `json:"breaking"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	release, err := h.platform.CloneRelease(r.Context(), currentUser(r), r.PathValue("id"), input.Version, input.ReleaseNotes, input.Breaking)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, release)
}

func (h *Handler) releaseImpact(w http.ResponseWriter, r *http.Request) {
	report, err := h.platform.Impact(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.impactDTO(r, report))
}

func (h *Handler) publishRelease(w http.ResponseWriter, r *http.Request) {
	release, _, err := h.platform.PublishRelease(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) deprecateRelease(w http.ResponseWriter, r *http.Request) {
	release, err := h.platform.DeprecateRelease(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, release)
}

func (h *Handler) testRelease(w http.ResponseWriter, r *http.Request) {
	var input struct {
		EnvironmentID string         `json:"environmentId"`
		RunInput      map[string]any `json:"runInput"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	run, err := h.platform.StartComponentTest(r.Context(), currentUser(r), r.PathValue("id"), input.EnvironmentID, input.RunInput)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, h.runDTO(r, run))
}

func (h *Handler) componentDTO(r *http.Request, component domain.Component) componentDTO {
	user, _ := h.platform.Store().GetUser(r.Context(), component.OwnerID)
	sort.SliceStable(component.Releases, func(i, j int) bool { return component.Releases[i].CreatedAt.After(component.Releases[j].CreatedAt) })
	output := componentDTO{Component: component, OwnerName: user.Name, ReleaseCount: len(component.Releases)}
	if len(component.Releases) > 0 {
		latest := component.Releases[0]
		output.LatestRelease = &latest
	}
	return output
}

func (h *Handler) impactDTO(r *http.Request, report domain.ImpactReport) map[string]any {
	componentOwners := []map[string]any{}
	scenarioOwners := []map[string]any{}
	scenarios := map[string]map[string]any{}
	paths := make([][]string, 0)
	pathSeen := map[string]bool{}
	for _, recipient := range report.Recipients {
		user, _ := h.platform.Store().GetUser(r.Context(), recipient.UserID)
		entry := map[string]any{"id": recipient.UserID, "name": user.Name}
		if recipient.Role == domain.RoleComponentOwner {
			componentOwners = append(componentOwners, entry)
		} else {
			scenarioOwners = append(scenarioOwners, entry)
		}
		for _, scenarioID := range recipient.ScenarioIDs {
			if scenario, err := h.platform.GetScenario(r.Context(), currentUser(r), scenarioID); err == nil {
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
	return map[string]any{"componentOwners": componentOwners, "scenarioOwners": scenarioOwners, "scenarios": scenarioList, "paths": paths}
}
