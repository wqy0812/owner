package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"codex/platform-demo/internal/ansible"
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
	if errors.Is(err, domain.ErrInvalid) {
		diagnostic, readErr := h.platform.Execution().GetRunDiagnosticRecord(r.Context(), r.PathValue("id"))
		if readErr != nil {
			writeError(w, readErr)
			return
		}
		writeData(w, http.StatusOK, h.runDiagnosticDTO(r, diagnostic))
		return
	}
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

func (h *Handler) runIdentityDTO(r *http.Request, run domain.RunReadModel) map[string]any {
	output := map[string]any{
		"id": run.ID, "kind": run.Kind, "status": run.Status, "environmentId": run.EnvironmentID,
		"environmentRevisionId": run.EnvironmentRevisionID, "componentReleaseId": run.ComponentReleaseID,
		"scenarioRevisionId": run.ScenarioRevisionID, "action": run.Action, "destructive": run.Destructive,
		"requestedBy": run.RequestedBy, "artifactDigest": run.ArtifactDigest,
		"retryOfRunId": run.RetryOfRunID, "retryRootRunId": run.RetryRootRunID,
		"retryAttempt": run.RetryAttempt, "retryStartStep": run.RetryStartStep,
		"error": run.Error, "createdAt": run.CreatedAt, "startedAt": run.StartedAt, "finishedAt": run.FinishedAt,
	}
	if digest, code := h.platform.Execution().RunJobSummary(r.Context(), run.ID); digest != "" {
		output["jobDigest"] = digest
		output["exitCode"] = code
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
		output["name"] = name + " · 按备份恢复组件"
	}
	return output
}

func (h *Handler) runDiagnosticDTO(r *http.Request, run domain.RunReadModel) map[string]any {
	output := h.runIdentityDTO(r, run)
	output["snapshotError"] = "执行快照不可用，仅显示诊断信息；此记录不能作为执行或续跑来源。"
	if run.Error == "" {
		output["error"] = output["snapshotError"]
	}
	steps := make([]map[string]any, 0, len(run.Steps))
	for _, step := range run.Steps {
		steps = append(steps, runStepDTO(step))
	}
	output["steps"] = steps
	if run.Approval != nil {
		output["approval"] = run.Approval
	}
	return output
}

func (h *Handler) runDTO(r *http.Request, run domain.Run) map[string]any {
	output := h.runIdentityDTO(r, domain.ReadModelFromRun(run))
	for key, value := range map[string]string{"executionMode": string(run.Snapshot.ScenarioExecution.Mode), "sourceRevisionId": run.Snapshot.ScenarioExecution.SourceRevisionID, "baselineRunId": run.Snapshot.ScenarioExecution.Baseline.RunID} {
		if value != "" {
			output[key] = value
		}
	}
	if run.Kind == domain.RunEnvironmentRollback && run.Snapshot.Recovery.ResetBoundaryDigest != "" {
		name, _ := output["environmentName"].(string)
		output["name"] = name + " · 重置环境"
	}
	locked := lockedStepMetadata(run.Snapshot)
	steps := make([]map[string]any, 0, len(run.Steps))
	succeeded := 0
	for _, step := range run.Steps {
		item := runStepDTO(step)
		if metadata := locked[step.NodeID]; metadata != nil {
			item["componentName"], item["action"] = metadata.ComponentName, metadata.Action
			for _, parent := range run.Snapshot.Plan.ParentSteps {
				if parent.ActionID == metadata.ParentActionID && parent.SourceNodeID == metadata.SourceNodeID {
					item["parentAction"] = parent.Action
					break
				}
			}
			if metadata.ReleaseID != "" {
				item["role"] = ansible.RoleName(metadata.ReleaseID)
			}
			item["contentDigest"] = metadata.WorkspaceDigest
			item["limit"] = metadata.Limit
			item["sourceType"] = metadata.SourceType
			item["stage"] = metadata.Stage
			item["acceptanceJobId"] = metadata.AcceptanceJobID
			item["scenarioRevisionId"] = metadata.ScenarioRevisionID
			item["phase"] = metadata.Phase
			item["parentActionId"] = metadata.ParentActionID
			item["actionId"] = metadata.ActionID
			item["sourceNodeId"] = metadata.SourceNodeID
			item["releaseId"] = metadata.ReleaseID
			item["playbookDigest"] = metadata.PlaybookDigest
			item["workspaceDigest"] = metadata.WorkspaceDigest
		}

		if step.Status == domain.RunSucceeded {
			succeeded++
		}
		steps = append(steps, item)
	}
	output["steps"] = steps
	purposeCounts := map[string]int{"components": 0, "finalVerification": 0, "acceptance": 0, "total": len(locked)}
	for _, step := range locked {
		purpose := "components"
		if step.Stage == "target_verify" {
			purpose = "finalVerification"
		}
		if step.SourceType == "scenario_acceptance" || step.Phase == "acceptance" {
			purpose = "acceptance"
		}
		purposeCounts[purpose]++
	}
	output["purposeCounts"] = purposeCounts
	if run.Status == domain.RunSucceeded {
		output["progress"] = 100
	} else if total := len(locked); total > 0 {
		output["progress"] = succeeded * 100 / total
	}
	if run.Approval != nil {
		riskReason := "动作声明为 destructive，或包含 recovery / clean / destroy / uninstall。"
		artifactTransfers := run.Snapshot.Delivery.ArtifactTransfers
		imageTransfers := run.Snapshot.Delivery.ImageTransfers
		deliveryRequirements := run.Snapshot.Delivery.Requirements
		if run.Kind == domain.RunEnvironmentRollback {
			riskReason = "按依赖逆序回滚所选组件，恢复后检查通过才确认完成；原始备份与恢复记录保留。"
			if digest := run.Snapshot.Recovery.ResetBoundaryDigest; digest != "" {
				riskReason = "根据来源 Run 恢复全部待恢复的集群组件，保留 File Station 和镜像仓库；恢复检查通过且无剩余待恢复项后才确认完成。"
			}
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
	if v := run.Snapshot.Delivery.ArtifactTransfers; v != nil {
		output["artifactTransfers"] = v
	}
	if v := run.Snapshot.Delivery.ImageTransfers; v != nil {
		output["imageTransfers"] = v
	}
	if v := run.Snapshot.Delivery.Requirements; v != nil {
		output["deliveryRequirements"] = v
	}
	if v := run.Snapshot.Delivery.Decisions; v != nil {
		output["deliveryDecisions"] = v
	}
	if v := run.DeliveryResults; len(v) > 0 {
		output["deliveryResults"] = v
	}
	if v := run.Snapshot.Inputs.ResolvedParametersByNode; v != nil {
		output["resolvedParametersByNode"] = v
	}
	if backups := lockedBackupMetadata(run.Snapshot); len(backups) > 0 {
		output["backups"] = backups
	}
	if run.Status == domain.RunQueued {
		var position int
		position, _ = h.platform.Execution().QueuedRunPosition(r.Context(), run.EnvironmentID, run.CreatedAt)
		output["queuePosition"] = position
	}
	return output
}

func lockedBackupMetadata(snapshot domain.RunSnapshot) []map[string]any {
	out := []map[string]any{}
	for _, step := range snapshot.Plan.Steps {
		if step.BackupRef == "" || step.Backup == nil {
			continue
		}
		b := step.Backup
		out = append(out, map[string]any{"nodeId": step.NodeID, "componentId": step.ComponentID, "componentName": step.ComponentName, "releaseId": step.ReleaseID, "action": step.Action, "backupRef": step.BackupRef, "installRunId": b.InstallRunID, "capturedAt": b.CapturedAt, "playbookSha256": b.PlaybookSHA256})
	}
	return out
}
func lockedStepMetadata(snapshot domain.RunSnapshot) map[string]*domain.RunPlanStep {
	out := map[string]*domain.RunPlanStep{}
	for i := range snapshot.Plan.Steps {
		step := &snapshot.Plan.Steps[i]
		if step.NodeID != "" {
			out[step.NodeID] = step
		}
	}
	return out
}

func (h *Handler) verifiedJobEligibility(w http.ResponseWriter, r *http.Request) {
	value, err := h.platform.Execution().VerifiedRunJobEligibility(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, 200, value)
}

func runStepDTO(step domain.RunStep) map[string]any {
	return map[string]any{
		"id": step.ID, "runId": step.RunID, "nodeId": step.NodeID, "name": step.Name,
		"status": step.Status, "exitCode": step.ExitCode, "summary": step.Summary,
		"startedAt": step.StartedAt, "finishedAt": step.FinishedAt,
	}
}
