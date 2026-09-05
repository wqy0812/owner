package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
	"codex/platform-demo/internal/store"
)

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	page, size := 1, 50
	for key, target := range map[string]*int{"page": &page, "pageSize": &size} {
		if r.URL.Query().Has(key) {
			n, err := strconv.Atoi(r.URL.Query().Get(key))
			if err != nil {
				writeError(w, fmt.Errorf("%w: invalid %s", domain.ErrInvalid, key))
				return
			}
			*target = n
		}
	}
	runs, err := h.platform.Execution().ListRunSummaries(r.Context(), currentUser(r), store.RunListOptions{Archive: r.URL.Query().Get("archive"), Page: page, PageSize: size, Filter: r.URL.Query().Get("filter"), EnvironmentID: r.URL.Query().Get("environmentId")})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(runs)
}

func (h *Handler) batchApprovalCandidates(w http.ResponseWriter, r *http.Request) {
	items, err := h.platform.Execution().BatchApprovalCandidates(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, items)
}

func (h *Handler) listComponentReleaseRunEvidence(w http.ResponseWriter, r *http.Request) {
	runs, err := h.platform.Execution().ReleaseRunSummaries(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, runs)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	if !h.canViewRun(r, currentUser(r), r.PathValue("id")) {
		writeError(w, domain.ErrForbidden)
		return
	}
	if cleaned, err := h.platform.Execution().WasCleaned(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	} else if cleaned {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "run.cleaned", "message": "记录已按保留策略清理"}})
		return
	}
	run, err := h.platform.Execution().GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.runDTO(r, run))
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.platform.Execution().Cancel(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusAccepted, h.runDTO(r, run))
}

func (h *Handler) previewRunRetry(w http.ResponseWriter, r *http.Request) {
	plan, err := h.platform.Execution().PreviewRetry(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, plan)
}

func (h *Handler) retryRun(w http.ResponseWriter, r *http.Request) {
	var input service.RunRetryRequest
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	run, err := h.platform.Execution().Retry(r.Context(), currentUser(r), r.PathValue("id"), input)
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
		Reason            string                          `json:"reason"`
		DeliveryDecisions []service.DeliveryDecisionInput `json:"deliveryDecisions"`
	}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, err)
			return
		}
	}
	run, err := h.platform.Execution().DecideApproval(r.Context(), currentUser(r), r.PathValue("id"), decision, input.Reason, input.DeliveryDecisions)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.runDTO(r, run))
}

func (h *Handler) batchDecideRuns(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ApprovalIDs []string `json:"approvalIds"`
		Decision    string   `json:"decision"`
		Reason      string   `json:"reason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	runs, err := h.platform.Execution().BatchDecideApprovals(r.Context(), currentUser(r), input.ApprovalIDs, input.Decision, input.Reason)
	if err != nil {
		writeError(w, err)
		return
	}
	output := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		output = append(output, h.runDTO(r, run))
	}
	writeData(w, http.StatusOK, output)
}

func (h *Handler) canViewRun(r *http.Request, user domain.User, runID string) bool {
	visible, err := h.platform.Execution().CanViewRun(r.Context(), user, runID)
	return err == nil && visible
}

func (h *Handler) runDTO(r *http.Request, run domain.Run) map[string]any {
	output := map[string]any{
		"id": run.ID, "kind": run.Kind, "status": run.Status, "environmentId": run.EnvironmentID,
		"environmentRevisionId": run.EnvironmentRevisionID, "componentReleaseId": run.ComponentReleaseID,
		"scenarioRevisionId": run.ScenarioRevisionID, "action": run.Action, "destructive": run.Destructive,
		"requestedBy": run.RequestedBy, "artifactDigest": run.ArtifactDigest,
		"retryOfRunId": run.RetryOfRunID, "retryRootRunId": run.RetryRootRunID,
		"retryAttempt": run.RetryAttempt, "retryStartStep": run.RetryStartStep,
		"error": run.Error, "createdAt": run.CreatedAt, "startedAt": run.StartedAt, "finishedAt": run.FinishedAt,
	}
	if archive, err := h.platform.Execution().RunArchive(r.Context(), run.ID); err == nil && archive != nil {
		output["archive"] = archive
	}
	if environment, err := h.platform.Execution().GetEnvironment(r.Context(), run.EnvironmentID, false); err == nil {
		output["environmentName"] = environment.Name
	}
	if requester, err := h.platform.Execution().GetUser(r.Context(), run.RequestedBy); err == nil {
		output["createdByName"] = requester.Name
	}
	if run.ComponentReleaseID != "" {
		if release, err := h.platform.Execution().GetComponentRelease(r.Context(), run.ComponentReleaseID); err == nil {
			output["version"] = release.Version
			if component, componentErr := h.platform.Execution().GetComponent(r.Context(), release.ComponentID, false); componentErr == nil {
				output["componentId"], output["componentName"], output["name"] = component.ID, component.Name, component.Name+" "+release.Version+" test"
			}
		}
	}
	if run.ScenarioRevisionID != "" {
		if revision, err := h.platform.Execution().GetScenarioRevision(r.Context(), run.ScenarioRevisionID); err == nil {
			if scenario, scenarioErr := h.platform.Execution().GetScenario(r.Context(), revision.ScenarioID, false); scenarioErr == nil {
				output["scenarioId"], output["scenarioName"], output["name"] = scenario.ID, scenario.Name, scenario.Name
			}
		}
	}
	if run.Kind == domain.RunEnvironmentRollback {
		name, _ := output["environmentName"].(string)
		output["name"] = name + " · 整集群回滚至干净状态"
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
		riskReason := "动作声明为 destructive，或包含 recovery / clean / destroy / uninstall。"
		artifactTransfers, _ := run.InputSnapshot["artifactTransfers"].([]any)
		imageTransfers, _ := run.InputSnapshot["imageTransfers"].([]any)
		deliveryRequirements, _ := run.InputSnapshot["deliveryRequirements"].([]any)
		if run.Kind == domain.RunEnvironmentRollback {
			riskReason = "整集群回滚会按逆序执行所有已安装组件的 rollback，并在成功后删除安装清单与备份基线。"
		} else if len(deliveryRequirements) > 0 {
			riskReason = fmt.Sprintf("有 %d 项内容未命中环境目标；请逐项选择直接使用来源或平移到环境目标。", len(deliveryRequirements))
		} else if len(artifactTransfers) > 0 || len(imageTransfers) > 0 {
			riskReason = fmt.Sprintf("目标环境与版本来源不一致：需平移 %d 个镜像、%d 个介质。", len(imageTransfers), len(artifactTransfers))
		}
		output["approval"] = map[string]any{
			"id": run.Approval.ID, "runId": run.Approval.RunID, "status": run.Approval.Status,
			"riskReason":  riskReason,
			"requestedAt": run.Approval.RequestedAt, "decidedAt": run.Approval.DecidedAt,
			"decidedBy": run.Approval.DecidedBy, "decision": run.Approval.Decision, "reason": run.Approval.Reason,
		}
	}
	if value, ok := run.InputSnapshot["artifactTransfers"]; ok {
		output["artifactTransfers"] = value
	}
	if value, ok := run.InputSnapshot["imageTransfers"]; ok {
		output["imageTransfers"] = value
	}
	for _, key := range []string{"deliveryRequirements", "deliveryDecisions", "deliveryResults"} {
		if value, ok := run.InputSnapshot[key]; ok {
			output[key] = value
		}
	}
	logs, _ := h.platform.Execution().ListRunLogTail(r.Context(), run.ID, 200)
	tail := make([]string, 0, len(logs))
	for _, logLine := range logs {
		tail = append(tail, fmt.Sprintf("[%s] %s", logLine.Stream, logLine.Message))
	}
	output["logTail"] = tail
	if resolved, ok := run.InputSnapshot["resolvedParametersByNode"]; ok {
		output["resolvedParametersByNode"] = resolved
	}
	if backups := lockedBackupMetadata(run.InputSnapshot); len(backups) > 0 {
		output["backups"] = backups
	}
	if run.Status == domain.RunQueued {
		var position int
		position, _ = h.platform.Execution().QueuedRunPosition(r.Context(), run.EnvironmentID, run.CreatedAt)
		output["queuePosition"] = position
	}
	return output
}

func lockedBackupMetadata(snapshot map[string]any) []map[string]any {
	output := make([]map[string]any, 0)
	steps, _ := snapshot["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		backup, _ := step["backup"].(map[string]any)
		backupRef, _ := step["backupRef"].(string)
		if backupRef == "" || backup == nil {
			continue
		}
		item := map[string]any{
			"nodeId": step["nodeId"], "componentId": step["componentId"], "componentName": step["componentName"],
			"releaseId": step["releaseId"], "action": step["action"], "backupRef": backupRef,
			"installRunId": backup["installRunId"], "capturedAt": backup["capturedAt"],
			"playbookSha256": backup["playbookSha256"],
		}
		output = append(output, item)
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
