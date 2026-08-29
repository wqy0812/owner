package api

import (
	"errors"
	"fmt"
	"net/http"

	"codex/platform-demo/internal/domain"
)

func (h *Handler) requireCatalogRepository(r *http.Request) error {
	if err := domain.ValidateRole(currentUser(r), domain.RoleEnvironmentOwner); err != nil {
		return err
	}
	if h.catalogRepos == nil {
		return fmt.Errorf("%w: Catalog backup is not enabled", domain.ErrConflict)
	}
	return nil
}

func (h *Handler) getCatalogRepository(w http.ResponseWriter, r *http.Request) {
	if err := h.requireCatalogRepository(r); err != nil {
		writeError(w, err)
		return
	}
	status, err := h.catalogRepos.Status(r.Context())
	if err != nil {
		writeError(w, fmt.Errorf("%w: inspect Catalog repository: %v", domain.ErrConflict, err))
		return
	}
	writeData(w, http.StatusOK, status)
}

func (h *Handler) createCatalogRepository(w http.ResponseWriter, r *http.Request) {
	if err := h.requireCatalogRepository(r); err != nil {
		writeError(w, err)
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	status, err := h.catalogRepos.Create(r.Context(), input.Path)
	if err != nil {
		writeError(w, catalogRepositoryError(err, domain.ErrInvalid))
		return
	}
	h.platform.RecordAudit(r.Context(), currentUser(r), "catalog_repository.created", "catalog_repository", status.Path, map[string]any{"branch": status.Branch})
	writeData(w, http.StatusCreated, status)
}

func (h *Handler) connectCatalogRepository(w http.ResponseWriter, r *http.Request) {
	if err := h.requireCatalogRepository(r); err != nil {
		writeError(w, err)
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	status, err := h.catalogRepos.Connect(r.Context(), input.Path)
	if err != nil {
		writeError(w, catalogRepositoryError(err, domain.ErrInvalid))
		return
	}
	h.platform.RecordAudit(r.Context(), currentUser(r), "catalog_repository.connected", "catalog_repository", status.Path, map[string]any{"branch": status.Branch})
	writeData(w, http.StatusOK, status)
}

func (h *Handler) planCatalogRestore(w http.ResponseWriter, r *http.Request) {
	if err := h.requireCatalogRepository(r); err != nil {
		writeError(w, err)
		return
	}
	var input struct {
		Ref string `json:"ref"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	plan, err := h.catalogRepos.Plan(r.Context(), input.Ref)
	if err != nil {
		writeError(w, catalogRepositoryError(err, domain.ErrInvalid))
		return
	}
	writeData(w, http.StatusOK, plan)
}

func (h *Handler) restoreCatalogRepository(w http.ResponseWriter, r *http.Request) {
	if err := h.requireCatalogRepository(r); err != nil {
		writeError(w, err)
		return
	}
	var input struct {
		Ref                string `json:"ref"`
		ExpectedPlanDigest string `json:"expectedPlanDigest"`
		Confirmation       string `json:"confirmation"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	result, err := h.catalogRepos.Restore(r.Context(), input.Ref, input.ExpectedPlanDigest, input.Confirmation)
	if err != nil {
		writeError(w, catalogRepositoryError(err, domain.ErrConflict))
		return
	}
	h.platform.RecordAudit(r.Context(), currentUser(r), "catalog.restored", "catalog_repository", input.Ref, map[string]any{"gitCommit": result.GitCommit, "catalogSha256": result.CatalogSHA256, "counts": result.Counts})
	writeData(w, http.StatusCreated, result)
}

func catalogRepositoryError(err, fallback error) error {
	for _, sentinel := range []error{domain.ErrInvalid, domain.ErrConflict, domain.ErrForbidden, domain.ErrNotFound} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return fmt.Errorf("%w: %v", fallback, err)
}
