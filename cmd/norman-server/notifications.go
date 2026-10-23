package main

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
)

// ListNotifications returns a summary of recent notifications.
func (s *server) ListNotifications(c echo.Context) error {
	return c.JSON(http.StatusOK, []map[string]interface{}{
		{
			"message": "Notification system initialized",
			"type":    "info",
		},
	})
}

// CreateNotification pushes a notification via the notifier (WS + bell + Slack).
func (s *server) CreateNotification(c echo.Context) error {
	var req protocol.NotificationCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	n := notify.Notification{
		Title:     req.Title,
		Body:      req.Body,
		Severity:  string(req.Severity),
		Timestamp: time.Now(),
	}
	if req.TaskId != nil {
		n.TaskID = *req.TaskId
	}

	if err := s.notifier.Notify(n); err != nil {
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"id":       time.Now().UnixNano(),
		"title":    req.Title,
		"severity": string(req.Severity),
	})
}

// DeleteNotification dismisses a notification and broadcasts the event.
func (s *server) DeleteNotification(c echo.Context, id string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.Notification,
		Message: strPtr("Notification " + id + " dismissed"),
	})
	return c.NoContent(http.StatusOK)
}
