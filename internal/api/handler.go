package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
)

const sessionCookie = "newplatform_demo_session"

type contextKey string

const userContextKey contextKey = "authenticated-user"

type Handler struct {
	platform        *service.Platform
	router          *http.ServeMux
	static          http.Handler
	visibilityMu    sync.Mutex
	visibilityCache map[string]runVisibility
}

type runVisibility struct {
	visible   bool
	expiresAt time.Time
}

func NewHandler(platform *service.Platform, static http.Handler) *Handler {
	h := &Handler{platform: platform, router: http.NewServeMux(), static: static, visibilityCache: make(map[string]runVisibility)}
	h.routes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if h.static == nil {
			http.NotFound(w, r)
			return
		}
		h.static.ServeHTTP(w, r)
		return
	}
	// API responses describe live workflow state. They must never be reused by
	// the browser cache: a cached awaiting_approval detail can otherwise outlive
	// an approved list response and expose an already-consumed approval action.
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/v1/session/switch" || r.URL.Path == "/api/v1/session/users" {
		h.router.ServeHTTP(w, r)
		return
	}
	user, err := h.authenticate(r)
	if err != nil {
		writeError(w, err)
		return
	}
	h.router.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
}

func (h *Handler) routes() {
	h.router.HandleFunc("POST /api/v1/session/switch", h.switchSession)
	h.router.HandleFunc("GET /api/v1/session/users", h.listSessionUsers)
	h.router.HandleFunc("GET /api/v1/session/me", h.me)
	h.router.HandleFunc("GET /api/v1/events", h.events)
	h.router.HandleFunc("GET /api/v1/workbench", h.workbench)

	h.router.HandleFunc("GET /api/v1/components", h.listComponents)
	h.router.HandleFunc("POST /api/v1/components", h.createComponent)
	h.router.HandleFunc("GET /api/v1/components/{id}", h.getComponent)
	h.router.HandleFunc("PATCH /api/v1/components/{id}", h.updateComponent)
	h.router.HandleFunc("POST /api/v1/components/{id}/releases", h.createRelease)
	h.router.HandleFunc("PUT /api/v1/component-releases/{id}", h.updateRelease)
	h.router.HandleFunc("PUT /api/v1/component-releases/{id}/contract", h.updateReleaseContract)
	h.router.HandleFunc("GET /api/v1/component-releases/{id}/playbook", h.getReleasePlaybook)
	h.router.HandleFunc("PUT /api/v1/component-releases/{id}/playbook", h.saveReleasePlaybook)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/playbook", h.uploadReleasePlaybook)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/clone", h.cloneRelease)
	h.router.HandleFunc("GET /api/v1/component-releases/{id}/impact", h.releaseImpact)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/publish", h.publishRelease)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/candidate", h.setReleaseCandidate)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/deprecate", h.deprecateRelease)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/test-plan", h.previewReleaseTest)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/test-runs", h.testRelease)
	h.router.HandleFunc("GET /api/v1/component-releases/{id}/image-builds", h.listComponentImageBuilds)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/image-builds", h.startComponentImageBuild)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/artifacts/upload", h.uploadComponentArtifact)
	h.router.HandleFunc("POST /api/v1/component-releases/{id}/artifacts/register", h.registerComponentArtifact)
	h.router.HandleFunc("DELETE /api/v1/component-releases/{id}/artifacts/{alias}", h.deleteComponentArtifact)
	h.router.HandleFunc("GET /api/v1/image-builds/{id}", h.getComponentImageBuild)

	h.router.HandleFunc("GET /api/v1/scenarios", h.listScenarios)
	h.router.HandleFunc("POST /api/v1/scenarios", h.createScenario)
	h.router.HandleFunc("GET /api/v1/scenarios/{id}", h.getScenario)
	h.router.HandleFunc("POST /api/v1/scenarios/{id}/revisions", h.cloneScenarioRevision)
	h.router.HandleFunc("PUT /api/v1/scenario-revisions/{id}/graph", h.saveScenarioGraph)
	h.router.HandleFunc("POST /api/v1/scenario-revisions/{id}/validate", h.validateScenario)
	h.router.HandleFunc("GET /api/v1/scenario-revisions/{id}/candidate-release-set", h.candidateReleaseSet)
	h.router.HandleFunc("POST /api/v1/scenario-revisions/{id}/test-runs", h.testScenario)
	h.router.HandleFunc("POST /api/v1/scenario-revisions/{id}/runs", h.runScenario)
	h.router.HandleFunc("POST /api/v1/scenario-revisions/{id}/publish", h.publishScenario)
	h.router.HandleFunc("POST /api/v1/scenario-revisions/{id}/deprecate", h.deprecateScenario)
	h.router.HandleFunc("POST /api/v1/scenario-revisions/{id}/abandon", h.abandonScenarioRevision)

	h.router.HandleFunc("GET /api/v1/environments", h.listEnvironments)
	h.router.HandleFunc("POST /api/v1/environments", h.createEnvironment)
	h.router.HandleFunc("PUT /api/v1/environments/{id}/inventory", h.updateInventory)
	h.router.HandleFunc("PUT /api/v1/environments/{id}/facts", h.updateFacts)
	h.router.HandleFunc("PUT /api/v1/environments/{id}/variables", h.updateVariables)
	h.router.HandleFunc("PUT /api/v1/environments/{id}/credential-refs", h.updateCredentialRefs)
	h.router.HandleFunc("POST /api/v1/environments/{id}/health-checks", h.checkEnvironmentHealth)
	h.router.HandleFunc("POST /api/v1/environments/{id}/cluster-rollback-plan", h.previewEnvironmentRollback)
	h.router.HandleFunc("POST /api/v1/environments/{id}/cluster-rollback-runs", h.startEnvironmentRollback)
	h.router.HandleFunc("POST /api/v1/environments/{id}/revisions/{revisionId}/restore", h.restoreEnvironmentRevision)

	h.router.HandleFunc("GET /api/v1/runs", h.listRuns)
	h.router.HandleFunc("GET /api/v1/runs/{id}", h.getRun)
	h.router.HandleFunc("POST /api/v1/runs/{id}/cancel", h.cancelRun)
	h.router.HandleFunc("POST /api/v1/approvals/{id}/approve", h.approveRun)
	h.router.HandleFunc("POST /api/v1/approvals/{id}/reject", h.rejectRun)
	h.router.HandleFunc("POST /api/v1/approvals/batch", h.batchDecideRuns)

	h.router.HandleFunc("GET /api/v1/notifications", h.listNotifications)
	h.router.HandleFunc("PATCH /api/v1/notifications/{id}", h.markNotificationRead)
	h.router.HandleFunc("GET /api/v1/audit-events", h.listAuditEvents)
}

func (h *Handler) listSessionUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.platform.Store().ListUsers(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeItems(w, users)
}

func (h *Handler) authenticate(r *http.Request) (domain.User, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return domain.User{}, fmt.Errorf("%w: choose a demo identity", domain.ErrUnauthorized)
	}
	return h.platform.Store().UserBySession(r.Context(), tokenHash(cookie.Value))
}

func (h *Handler) switchSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID string `json:"userId"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	user, err := h.platform.Store().GetUser(r.Context(), input.UserID)
	if err != nil {
		writeError(w, err)
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, err)
		return
	}
	expires := time.Now().UTC().Add(24 * time.Hour)
	if err := h.platform.Store().CreateSession(r.Context(), tokenHash(token), user.ID, expires); err != nil {
		writeError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires, MaxAge: 86400,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	writeData(w, http.StatusOK, user)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, currentUser(r))
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func currentUser(r *http.Request) domain.User {
	user, _ := r.Context().Value(userContextKey).(domain.User)
	return user
}

func decodeJSON(r *http.Request, output any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("%w: malformed JSON body: %v", domain.ErrInvalid, err)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return fmt.Errorf("%w: request must contain one JSON value", domain.ErrInvalid)
	}
	return nil
}

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func writeItems(w http.ResponseWriter, items any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func writeError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, domain.ErrUnauthorized):
		status, code = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	}
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "internal server error"
	}
	var validation *domain.ValidationError
	var details any
	if errors.As(err, &validation) {
		details = validation.Details
	}
	var actionable *domain.ActionableError
	var explanation any
	if errors.As(err, &actionable) {
		explanation = actionable.Explanation
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "details": details, "explanation": explanation}})
}
