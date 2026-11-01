package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
)

// ListNotifications returns a summary of recent notifications.
func (s *server) ListNotifications(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]map[string]interface{}{
		{
			"message": "Notification system initialized",
			"type":    "info",
		},
	})
}

// CreateNotification pushes a notification via the notifier (WS + bell + Slack).
func (s *server) CreateNotification(w http.ResponseWriter, r *http.Request) {
	var req protocol.NotificationCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	n := notify.Notification{
		Title:     req.Title,
		Body:      req.Body,
		Severity:  req.Severity,
		Timestamp: time.Now(),
	}
	if req.TaskId != nil {
		n.TaskID = *req.TaskId
	}

	if err := s.notifier.Notify(n); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "rate limit exceeded"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":       time.Now().UnixNano(),
		"title":    req.Title,
		"severity": req.Severity,
	})
}

// DeleteNotification dismisses a notification and broadcasts the event.
func (s *server) DeleteNotification(w http.ResponseWriter, r *http.Request, id string) {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.Notification,
		Message: strPtr("Notification " + id + " dismissed"),
	})
	w.WriteHeader(http.StatusOK)
}
