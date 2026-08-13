package api

import (
	"fmt"
	"net/http"
	"strings"

	"codex/platform-demo/internal/domain"
)

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.platform.Store().ListRuns(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	output := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		if detailed, getErr := h.platform.Store().GetRun(r.Context(), run.ID); getErr == nil {
			run = detailed
		}
		output = append(output, h.runDTO(r, run))
	}
	writeItems(w, output)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	if !h.canViewRun(r, currentUser(r), r.PathValue("id")) {
		writeError(w, domain.ErrForbidden)
		return
	}
	run, err := h.platform.Store().GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.runDTO(r, run))
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.platform.CancelRun(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, h.runDTO(r, run))
}

func (h *Handler) approveRun(w http.ResponseWriter, r *http.Request) {
	h.decideRun(w, r, "approved")
}

func (h *Handler) rejectRun(w http.ResponseWriter, r *http.Request) {
	h.decideRun(w, r, "rejected")
}

func (h *Handler) decideRun(w http.ResponseWriter, r *http.Request, decision string) {
	var input struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, err)
			return
		}
	}
	run, err := h.platform.DecideApproval(r.Context(), currentUser(r), r.PathValue("id"), decision, input.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.runDTO(r, run))
}

func (h *Handler) canViewRun(r *http.Request, user domain.User, runID string) bool {
	run, err := h.platform.Store().GetRun(r.Context(), runID)
	if err != nil {
		return false
	}
	if run.RequestedBy == user.ID {
		return true
	}
	if environment, getErr := h.platform.Store().GetEnvironment(r.Context(), run.EnvironmentID, false); getErr == nil && user.Role == domain.RoleEnvironmentOwner && environment.OwnerID == user.ID {
		return true
	}
	if run.ComponentReleaseID != "" && user.Role == domain.RoleComponentOwner {
		if release, getErr := h.platform.Store().GetComponentRelease(r.Context(), run.ComponentReleaseID); getErr == nil {
			if component, componentErr := h.platform.Store().GetComponent(r.Context(), release.ComponentID, false); componentErr == nil && component.OwnerID == user.ID {
				return true
			}
		}
	}
	if user.Role == domain.RoleComponentOwner {
		for _, metadata := range lockedStepMetadata(run.InputSnapshot) {
			releaseID, _ := metadata["releaseId"].(string)
			if releaseID == "" {
				continue
			}
			release, releaseErr := h.platform.Store().GetComponentRelease(r.Context(), releaseID)
			if releaseErr != nil {
				continue
			}
			component, componentErr := h.platform.Store().GetComponent(r.Context(), release.ComponentID, false)
			if componentErr == nil && component.OwnerID == user.ID {
				return true
			}
		}
	}
	if run.ScenarioRevisionID != "" && user.Role == domain.RoleScenarioOwner {
		if revision, getErr := h.platform.Store().GetScenarioRevision(r.Context(), run.ScenarioRevisionID); getErr == nil {
			if scenario, scenarioErr := h.platform.Store().GetScenario(r.Context(), revision.ScenarioID, false); scenarioErr == nil && scenario.OwnerID == user.ID {
				return true
			}
		}
	}
	return false
}

func (h *Handler) runDTO(r *http.Request, run domain.Run) map[string]any {
	output := map[string]any{
		"id": run.ID, "kind": run.Kind, "status": run.Status, "environmentId": run.EnvironmentID,
		"environmentRevisionId": run.EnvironmentRevisionID, "componentReleaseId": run.ComponentReleaseID,
		"scenarioRevisionId": run.ScenarioRevisionID, "action": run.Action, "destructive": run.Destructive,
		"createdBy": run.RequestedBy, "requestedBy": run.RequestedBy, "artifactDigest": run.ArtifactDigest,
		"error": run.Error, "createdAt": run.CreatedAt, "startedAt": run.StartedAt, "finishedAt": run.FinishedAt,
	}
	if environment, err := h.platform.Store().GetEnvironment(r.Context(), run.EnvironmentID, false); err == nil {
		output["environmentName"] = environment.Name
	}
	if requester, err := h.platform.Store().GetUser(r.Context(), run.RequestedBy); err == nil {
		output["createdByName"] = requester.Name
	}
	if run.ComponentReleaseID != "" {
		if release, err := h.platform.Store().GetComponentRelease(r.Context(), run.ComponentReleaseID); err == nil {
			output["version"] = release.Version
			if component, componentErr := h.platform.Store().GetComponent(r.Context(), release.ComponentID, false); componentErr == nil {
				output["componentId"], output["componentName"], output["name"] = component.ID, component.Name, component.Name+" "+release.Version+" test"
			}
		}
	}
	if run.ScenarioRevisionID != "" {
		if revision, err := h.platform.Store().GetScenarioRevision(r.Context(), run.ScenarioRevisionID); err == nil {
			if scenario, scenarioErr := h.platform.Store().GetScenario(r.Context(), revision.ScenarioID, false); scenarioErr == nil {
				output["scenarioId"], output["scenarioName"], output["name"] = scenario.ID, scenario.Name, scenario.Name
			}
		}
	}
	locked := lockedStepMetadata(run.InputSnapshot)
	steps := make([]map[string]any, 0, len(run.Steps))
	succeeded := 0
	for _, step := range run.Steps {
		item := map[string]any{
			"id": step.ID, "runId": step.RunID, "nodeId": step.NodeID, "name": step.Name,
			"status": step.Status, "exitCode": step.ExitCode, "summary": step.Summary,
			"startedAt": step.StartedAt, "finishedAt": step.FinishedAt,
		}
		if metadata := locked[step.NodeID]; metadata != nil {
			item["componentName"], item["action"] = metadata["componentName"], metadata["action"]
		}
		if parts := strings.Split(step.Name, " · "); len(parts) >= 2 {
			item["componentName"], item["action"] = parts[0], parts[len(parts)-1]
		}
		if step.Status == domain.RunSucceeded {
			succeeded++
		}
		steps = append(steps, item)
	}
	output["steps"] = steps
	if run.Status == domain.RunSucceeded {
		output["progress"] = 100
	} else if total := len(locked); total > 0 {
		output["progress"] = succeeded * 100 / total
	}
	if run.Approval != nil {
		output["approval"] = map[string]any{
			"id": run.Approval.ID, "runId": run.Approval.RunID, "status": run.Approval.Status,
			"riskReason":  "动作声明为 destructive，或包含 recovery / clean / destroy / uninstall。",
			"requestedAt": run.Approval.RequestedAt, "decidedAt": run.Approval.DecidedAt,
		}
	}
	logs, _ := h.platform.Store().ListRunLogs(r.Context(), run.ID, 0, 500)
	start := 0
	if len(logs) > 200 {
		start = len(logs) - 200
	}
	tail := make([]string, 0, len(logs)-start)
	for _, logLine := range logs[start:] {
		tail = append(tail, fmt.Sprintf("[%s] %s", logLine.Stream, logLine.Message))
	}
	output["logTail"] = tail
	if run.Status == domain.RunQueued {
		var position int
		_ = h.platform.Store().DB().QueryRowContext(r.Context(), `SELECT COUNT(*) FROM runs WHERE environment_id=? AND status='queued' AND created_at<=?`, run.EnvironmentID, run.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")).Scan(&position)
		output["queuePosition"] = position
	}
	return output
}

func lockedStepMetadata(snapshot map[string]any) map[string]map[string]any {
	output := map[string]map[string]any{}
	steps, _ := snapshot["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		nodeID, _ := step["nodeId"].(string)
		if nodeID != "" {
			output[nodeID] = step
		}
	}
	return output
}

func safeError(value any) string {
	return strings.TrimSpace(fmt.Sprint(value))
}
