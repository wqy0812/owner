package api

import (
	"encoding/json"
	"net/http"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
)

func (h *Handler) listEnvironments(w http.ResponseWriter, r *http.Request) {
	environments, err := h.platform.ListEnvironments(r.Context(), currentUser(r))
	if err != nil {
		writeError(w, err)
		return
	}
	output := make([]map[string]any, 0, len(environments))
	for _, environment := range environments {
		output = append(output, h.environmentDTO(r, environment))
	}
	writeItems(w, output)
}

func (h *Handler) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Facts       map[string]any `json:"facts"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.CreateEnvironment(r.Context(), currentUser(r), domain.Environment{Name: input.Name, Description: input.Description}, input.Facts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, h.environmentDTO(r, environment))
}

func (h *Handler) updateInventory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Hosts        []service.InventoryHost `json:"hosts"`
		ChangeReason string                  `json:"changeReason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.UpdateInventory(r.Context(), currentUser(r), r.PathValue("id"), input.Hosts, input.ChangeReason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.environmentDTO(r, environment))
}

func (h *Handler) updateFacts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Facts        map[string]any `json:"facts"`
		ChangeReason string         `json:"changeReason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.UpdateEnvironmentFacts(r.Context(), currentUser(r), r.PathValue("id"), input.Facts, input.ChangeReason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.environmentDTO(r, environment))
}

func (h *Handler) updateVariables(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Variables    map[string]string `json:"variables"`
		ChangeReason string            `json:"changeReason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.UpdateEnvironmentVariables(r.Context(), currentUser(r), r.PathValue("id"), input.Variables, input.ChangeReason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.environmentDTO(r, environment))
}

func (h *Handler) updateCredentialRefs(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CredentialRefs []struct {
			Name      string `json:"name"`
			Kind      string `json:"kind"`
			Reference string `json:"reference"`
		} `json:"credentialRefs"`
		ChangeReason string `json:"changeReason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	refs := make([]domain.CredentialRef, 0, len(input.CredentialRefs))
	for _, item := range input.CredentialRefs {
		refs = append(refs, domain.CredentialRef{Name: item.Name, Kind: item.Kind, Reference: item.Reference})
	}
	environment, err := h.platform.UpdateCredentialRefs(r.Context(), currentUser(r), r.PathValue("id"), refs, input.ChangeReason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.environmentDTO(r, environment))
}

func (h *Handler) checkEnvironmentHealth(w http.ResponseWriter, r *http.Request) {
	check, err := h.platform.CheckEnvironmentHealth(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, check)
}

func (h *Handler) restoreEnvironmentRevision(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ChangeReason string `json:"changeReason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.RestoreEnvironmentRevision(r.Context(), currentUser(r), r.PathValue("id"), r.PathValue("revisionId"), input.ChangeReason)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.environmentDTO(r, environment))
}

func environmentRevisionDTO(revision domain.EnvironmentRevision) map[string]any {
	hosts := []service.InventoryHost{}
	var document service.InventoryDocument
	if json.Unmarshal(revision.Inventory, &document) == nil {
		hosts = document.Hosts
	}
	refs := make([]map[string]any, 0, len(revision.CredentialRefs))
	for _, ref := range revision.CredentialRefs {
		item := map[string]any{"name": ref.Name, "kind": ref.Kind, "configured": ref.Configured}
		if ref.Reference != "" {
			item["reference"] = ref.Reference
		}
		if ref.Configured || ref.Reference != "" {
			item["maskedReference"] = "••••••••"
		}
		refs = append(refs, item)
	}
	return map[string]any{
		"id": revision.ID, "environmentId": revision.EnvironmentID,
		"revision": revision.Revision, "facts": revision.Facts,
		"hosts":     hosts,
		"variables": revision.Variables, "credentialRefs": refs,
		"maxConcurrent": revision.MaxConcurrent,
		"createdBy":     revision.CreatedBy, "changeReason": revision.ChangeReason, "createdAt": revision.CreatedAt,
	}
}

func (h *Handler) environmentDTO(r *http.Request, environment domain.Environment) map[string]any {
	owner, _ := h.platform.Store().GetUser(r.Context(), environment.OwnerID)
	var revision any
	if environment.Revision != nil {
		revision = environmentRevisionDTO(*environment.Revision)
	}
	revisions := make([]map[string]any, 0, len(environment.Revisions))
	for _, item := range environment.Revisions {
		revisions = append(revisions, environmentRevisionDTO(item))
	}
	status := "ready"
	schedulingStatus := "idle"
	activeRunID := ""
	var activeStatus string
	if err := h.platform.Store().DB().QueryRowContext(r.Context(), `SELECT id,status FROM runs WHERE environment_id=? AND status IN ('running','awaiting_approval','queued') ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'awaiting_approval' THEN 1 ELSE 2 END, created_at LIMIT 1`, environment.ID).Scan(&activeRunID, &activeStatus); err == nil {
		status = "locked"
		schedulingStatus = activeStatus
	}
	return map[string]any{
		"id": environment.ID, "name": environment.Name, "description": environment.Description,
		"ownerId": environment.OwnerID, "ownerName": owner.Name, "currentRevisionId": environment.CurrentRevisionID,
		"currentRevision": revision, "revisions": revisions, "status": status, "schedulingStatus": schedulingStatus, "activeRunId": activeRunID,
		"healthCheck": environment.HealthCheck,
		"createdAt":   environment.CreatedAt, "updatedAt": environment.UpdatedAt,
	}
}

func (h *Handler) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	if currentUser(r).Role != domain.RoleEnvironmentOwner {
		writeError(w, domain.ErrForbidden)
		return
	}
	events, err := h.platform.Store().ListAudit(r.Context(), 500)
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, events)
}
