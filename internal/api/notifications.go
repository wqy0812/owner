package api

import (
	"net/http"

	"codex/platform-demo/internal/domain"
)

func (h *Handler) listNotifications(w http.ResponseWriter, r *http.Request) {
	notifications, err := h.platform.Store().ListNotifications(r.Context(), currentUser(r).ID, false)
	if err != nil {
		writeError(w, err)
		return
	}
	output := make([]map[string]any, 0, len(notifications))
	for _, notification := range notifications {
		output = append(output, notificationDTO(notification))
	}
	writeItems(w, output)
}

func (h *Handler) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Read bool `json:"read"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if !input.Read {
		writeError(w, &domain.ValidationError{Message: "notifications cannot be reverted to unread in this demo"})
		return
	}
	if err := h.platform.Store().MarkNotificationRead(r.Context(), r.PathValue("id"), currentUser(r).ID); err != nil {
		writeError(w, err)
		return
	}
	notification, err := h.platform.Store().GetNotification(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeData(w, http.StatusOK, notificationDTO(notification))
}

func notificationDTO(notification domain.Notification) map[string]any {
	output := map[string]any{
		"id": notification.ID, "userId": notification.UserID, "type": notification.Type,
		"title": notification.Title, "body": notification.Body,
		"resourceUrl": notification.ResourceURL, "read": notification.ReadAt != nil,
		"readAt": notification.ReadAt, "createdAt": notification.CreatedAt,
	}
	output["payload"] = notification.Payload
	return output
}
