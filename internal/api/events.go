package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/service"
)

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, fmt.Errorf("streaming is unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, "retry: 3000\nevent: connected\ndata: {}\n\n")
	flusher.Flush()

	events, unsubscribe := h.platform.Hub().Subscribe()
	defer unsubscribe()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	user := currentUser(r)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case event, open := <-events:
			if !open {
				return
			}
			if !h.eventVisible(r, user, event) {
				continue
			}
			payload, _ := json.Marshal(event.Data)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, payload)
			flusher.Flush()
		}
	}
}

func (h *Handler) eventVisible(r *http.Request, user domain.User, event service.Event) bool {
	if event.Type == "preparation.updated" {
		payload, ok := event.Data.(map[string]any)
		if !ok {
			return false
		}
		id, _ := payload["preparationId"].(string)
		_, err := h.platform.Preparations().Get(r.Context(), user, id)
		return err == nil
	}
	if !strings.HasPrefix(event.Type, "run.") && !strings.HasPrefix(event.Type, "approval.") {
		return true
	}
	payload, ok := event.Data.(map[string]any)
	if !ok {
		return false
	}
	runID, _ := payload["runId"].(string)
	if runID == "" {
		return event.Type == "run.worker_error" && user.Role == domain.RoleEnvironmentOwner
	}
	forceRefresh := event.Type != "run.log"
	return h.cachedRunVisibility(r, user, runID, forceRefresh)
}

const runVisibilityTTL = 2 * time.Second

func (h *Handler) cachedRunVisibility(r *http.Request, user domain.User, runID string, forceRefresh bool) bool {
	key := user.ID + "\x00" + runID
	now := time.Now()
	if !forceRefresh {
		h.visibilityMu.Lock()
		cached, ok := h.visibilityCache[key]
		if ok && now.Before(cached.expiresAt) {
			h.visibilityMu.Unlock()
			return cached.visible
		}
		// Serialize cache misses so one run.log fan-out performs one visibility
		// query rather than one query per subscriber.
		visible, err := h.platform.Execution().CanViewRun(r.Context(), user, runID)
		if err != nil {
			h.visibilityMu.Unlock()
			return false
		}
		h.storeRunVisibilityLocked(key, visible, now)
		h.visibilityMu.Unlock()
		return visible
	}
	visible, err := h.platform.Execution().CanViewRun(r.Context(), user, runID)
	if err != nil {
		return false
	}
	h.visibilityMu.Lock()
	h.storeRunVisibilityLocked(key, visible, now)
	h.visibilityMu.Unlock()
	return visible
}

func (h *Handler) storeRunVisibilityLocked(key string, visible bool, now time.Time) {
	if len(h.visibilityCache) >= 4096 {
		for cacheKey, value := range h.visibilityCache {
			if !now.Before(value.expiresAt) {
				delete(h.visibilityCache, cacheKey)
			}
		}
		if len(h.visibilityCache) >= 4096 {
			for cacheKey := range h.visibilityCache {
				delete(h.visibilityCache, cacheKey)
				break
			}
		}
	}
	h.visibilityCache[key] = runVisibility{visible: visible, expiresAt: now.Add(runVisibilityTTL)}
}
