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
	return h.canViewRun(r, user, runID)
}
