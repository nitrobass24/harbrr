package api

import (
	"context"
	"net/http"
	"time"

	"github.com/autobrr/harbrr/internal/domain"
	"github.com/autobrr/harbrr/internal/notify"
	"github.com/autobrr/harbrr/internal/secrets"
)

// notificationResponse is the API view of a notification target. The destination URL
// is never echoed — it reads back as the <redacted> sentinel.
type notificationResponse struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	URL             string    `json:"url"`
	Enabled         bool      `json:"enabled"`
	OnHealthFailure bool      `json:"onHealthFailure"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// listNotifications returns all notification targets (URLs redacted).
func (rt *router) listNotifications(w http.ResponseWriter, r *http.Request) {
	list, err := rt.notify.ListNotifications(r.Context())
	if err != nil {
		rt.writeServiceError(w, "list notifications", err)
		return
	}
	out := make([]notificationResponse, 0, len(list))
	for _, n := range list {
		out = append(out, toNotificationResponse(n))
	}
	writeJSON(w, http.StatusOK, out)
}

// createNotification adds a notification target with its destination URL encrypted.
func (rt *router) createNotification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		Type            string `json:"type"`
		URL             string `json:"url"`
		OnHealthFailure *bool  `json:"onHealthFailure"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	n, err := rt.notify.CreateNotification(r.Context(), notify.CreateNotificationParams{
		Name: req.Name, Type: req.Type, URL: req.URL, OnHealthFailure: req.OnHealthFailure,
	})
	if err != nil {
		rt.writeServiceError(w, "create notification", err)
		return
	}
	writeJSON(w, http.StatusCreated, toNotificationResponse(n))
}

// getNotification returns one target (URL redacted).
func (rt *router) getNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "notification")
	if !ok {
		return
	}
	n, err := rt.notify.GetNotification(r.Context(), id)
	if err != nil {
		rt.writeServiceError(w, "get notification", err)
		return
	}
	writeJSON(w, http.StatusOK, toNotificationResponse(n))
}

// updateNotification patches a target (a new url rotates the destination).
func (rt *router) updateNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "notification")
	if !ok {
		return
	}
	var req struct {
		Name            *string `json:"name"`
		URL             *string `json:"url"`
		OnHealthFailure *bool   `json:"onHealthFailure"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := rt.notify.UpdateNotification(r.Context(), id, notify.UpdateNotificationParams{
		Name: req.Name, URL: req.URL, OnHealthFailure: req.OnHealthFailure,
	}); err != nil {
		rt.writeServiceError(w, "update notification", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteNotification removes a target.
func (rt *router) deleteNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "notification")
	if !ok {
		return
	}
	if err := rt.notify.DeleteNotification(r.Context(), id); err != nil {
		rt.writeServiceError(w, "delete notification", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// enableNotification / disableNotification toggle a target.
func (rt *router) enableNotification(w http.ResponseWriter, r *http.Request) {
	rt.setResourceEnabled(w, r, "notification", "set notification enabled", rt.notify.SetEnabled, true)
}

func (rt *router) disableNotification(w http.ResponseWriter, r *http.Request) {
	rt.setResourceEnabled(w, r, "notification", "set notification enabled", rt.notify.SetEnabled, false)
}

// testNotification sends a synthetic event to the target. A pass is {"ok":true}; a
// delivery failure is 200 {"ok":false,"error":<scrubbed>}; an unknown id 404.
func (rt *router) testNotification(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "notification")
	if !ok {
		return
	}
	rt.testEndpoint(w, r, "test notification", func(ctx context.Context) error {
		return rt.notify.TestNotification(ctx, id)
	})
}

// toNotificationResponse maps a target to its API view, redacting the destination URL.
func toNotificationResponse(n domain.Notification) notificationResponse {
	return notificationResponse{
		ID: n.ID, Name: n.Name, Type: n.Type, URL: secrets.Redacted, Enabled: n.Enabled,
		OnHealthFailure: n.OnHealthFailure, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}
}
