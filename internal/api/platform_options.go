package api

import (
	"fmt"
	"net/http"

	"codex/platform-demo/internal/domain"
)

func (h *Handler) listPlatformOptionCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := h.platform.PlatformOptions().List(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, categories)
}

func (h *Handler) createPlatformOptionCategory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Label               string `json:"label"`
		ParentCategoryID    string `json:"parentCategoryId"`
		EnvironmentRequired bool   `json:"environmentRequired"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	category, err := h.platform.PlatformOptions().CreateCategory(r.Context(), currentUser(r), input.Label, input.ParentCategoryID, input.EnvironmentRequired)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, category)
}

func (h *Handler) deletePlatformOptionCategory(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.PlatformOptions().DeleteCategory(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) renamePlatformOptionCategory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Label   *string `json:"label"`
		Retired *bool   `json:"retired"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	var category any
	var err error
	if input.Retired != nil {
		category, err = h.platform.PlatformOptions().SetCategoryRetired(r.Context(), currentUser(r), r.PathValue("id"), *input.Retired)
	} else if input.Label != nil {
		category, err = h.platform.PlatformOptions().RenameCategory(r.Context(), currentUser(r), r.PathValue("id"), *input.Label)
	} else {
		err = fmt.Errorf("%w: label or retired is required", domain.ErrInvalid)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, category)
}

func (h *Handler) createPlatformOption(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Label          string `json:"label"`
		ParentOptionID string `json:"parentOptionId"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	option, err := h.platform.PlatformOptions().CreateOption(r.Context(), currentUser(r), r.PathValue("id"), input.Label, input.ParentOptionID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusCreated, option)
}

func (h *Handler) deletePlatformOption(w http.ResponseWriter, r *http.Request) {
	if err := h.platform.PlatformOptions().DeleteOption(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *Handler) renamePlatformOption(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Label   *string `json:"label"`
		Retired *bool   `json:"retired"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	var option any
	var err error
	if input.Retired != nil {
		option, err = h.platform.PlatformOptions().SetOptionRetired(r.Context(), currentUser(r), r.PathValue("id"), *input.Retired)
	} else if input.Label != nil {
		option, err = h.platform.PlatformOptions().RenameOption(r.Context(), currentUser(r), r.PathValue("id"), *input.Label)
	} else {
		err = fmt.Errorf("%w: label or retired is required", domain.ErrInvalid)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, option)
}
