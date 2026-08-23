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
		Hosts []service.InventoryHost `json:"hosts"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.UpdateInventory(r.Context(), currentUser(r), r.PathValue("id"), input.Hosts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.environmentDTO(r, environment))
}

func (h *Handler) updateFacts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Facts map[string]any `json:"facts"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.UpdateEnvironmentFacts(r.Context(), currentUser(r), r.PathValue("id"), input.Facts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.environmentDTO(r, environment))
}

func (h *Handler) updateVariables(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Variables map[string]string `json:"variables"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	environment, err := h.platform.UpdateEnvironmentVariables(r.Context(), currentUser(r), r.PathValue("id"), input.Variables)
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
			Type      string `json:"type"`
			Kind      string `json:"kind"`
			Reference string `json:"reference"`
		} `json:"credentialRefs"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	refs := make([]domain.CredentialRef, 0, len(input.CredentialRefs))
	for _, item := range input.CredentialRefs {
		kind := item.Kind
		if kind == "" {
			kind = item.Type
		}
		refs = append(refs, domain.CredentialRef{Name: item.Name, Kind: kind, Reference: item.Reference})
	}
	environment, err := h.platform.UpdateCredentialRefs(r.Context(), currentUser(r), r.PathValue("id"), refs)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, h.environmentDTO(r, environment))
}

func (h *Handler) environmentDTO(r *http.Request, environment domain.Environment) map[string]any {
	owner, _ := h.platform.Store().GetUser(r.Context(), environment.OwnerID)
	var revision any
	if environment.Revision != nil {
		hosts := []service.InventoryHost{}
		var document service.InventoryDocument
		if json.Unmarshal(environment.Revision.Inventory, &document) == nil {
			hosts = document.Hosts
		}
		refs := make([]map[string]any, 0, len(environment.Revision.CredentialRefs))
		for _, ref := range environment.Revision.CredentialRefs {
			item := map[string]any{"name": ref.Name, "type": ref.Kind, "kind": ref.Kind, "configured": ref.Configured}
			if ref.Reference != "" {
				item["reference"] = ref.Reference
			}
			if ref.Configured || ref.Reference != "" {
				item["maskedReference"] = "••••••••"
			}
			refs = append(refs, item)
		}
		revision = map[string]any{
			"id": environment.Revision.ID, "environmentId": environment.Revision.EnvironmentID,
			"revision": environment.Revision.Revision, "facts": environment.Revision.Facts,
			"hosts": hosts, "inventory": json.RawMessage(environment.Revision.Inventory),
			"variables": environment.Revision.Variables, "credentialRefs": refs,
			"maxConcurrent": environment.Revision.MaxConcurrent, "maxConcurrentRuns": environment.Revision.MaxConcurrent,
			"createdAt": environment.Revision.CreatedAt,
		}
	}
	status := "ready"
	activeRunID := ""
	if err := h.platform.Store().DB().QueryRowContext(r.Context(), `SELECT id FROM runs WHERE environment_id=? AND status='running' ORDER BY created_at LIMIT 1`, environment.ID).Scan(&activeRunID); err == nil {
		status = "locked"
	}
	return map[string]any{
		"id": environment.ID, "name": environment.Name, "description": environment.Description,
		"ownerId": environment.OwnerID, "ownerName": owner.Name, "currentRevisionId": environment.CurrentRevisionID,
		"revision": revision, "currentRevision": revision, "status": status, "activeRunId": activeRunID,
		"createdAt": environment.CreatedAt, "updatedAt": environment.UpdatedAt,
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
