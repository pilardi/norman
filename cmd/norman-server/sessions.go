package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/norman/norman/pkg/protocol"
)

// ListSessions returns all sessions.
func (s *server) ListSessions(c echo.Context) error {
	sessions := s.store.ListSessions()
	if len(sessions) == 0 {
		return c.JSON(http.StatusOK, []map[string]interface{}{})
	}
	return c.JSON(http.StatusOK, sessions)
}

// CreateSession creates a new session bound to a workspace.
func (s *server) CreateSession(c echo.Context) error {
	var req protocol.SessionCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	clientType := "custom"
	if req.ClientType != nil {
		clientType = string(*req.ClientType)
	}
	session, err := s.createSessionInternal(req.WorkspaceId, clientType)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Validate the workspace exists before ensuring its sandbox.
	found := false
	for _, ws := range s.workspaces {
		if ws.Id != nil && ws.Id.String() == req.WorkspaceId {
			found = true
			break
		}
	}
	if !found {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found: " + req.WorkspaceId})
	}

	// Ensure the OpenShell sandbox exists for this workspace, carrying the session UUID.
	sid := session.Id.String()
	_, err = s.workspaceManager.GetSession(c.Request().Context(), req.WorkspaceId, sid)
	if err != nil {
		log.Printf("[sessions] failed to ensure sandbox for workspace %s: %v", req.WorkspaceId, err)
	}

	s.wsHub.BroadcastSession(session.Id.String(), protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session created for workspace " + req.WorkspaceId),
	})

	return c.JSON(http.StatusCreated, session)
}

// ArchiveSession archives a session by task ID.
func (s *server) ArchiveSession(c echo.Context) error {
	var req protocol.ArchiveSessionRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	taskData, err := s.store.LoadTask(req.TaskId)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "task not found: " + req.TaskId})
	}

	sessions := []interface{}{taskData}
	if err := s.store.ArchiveSession(req.TaskId, sessions); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionCompressed,
		TaskId:  &req.TaskId,
		Message: strPtr("Session archived for task " + req.TaskId),
	})

	return c.NoContent(http.StatusNoContent)
}

// ArchiveSessionByID archives a session by path-based ID.
func (s *server) ArchiveSessionByID(c echo.Context) error {
	id := c.Param("id")

	sessionData, err := s.store.LoadSession(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found: " + id})
	}

	sessions := []interface{}{sessionData}
	if err := s.store.ArchiveSession(id, sessions); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionCompressed,
		TaskId:  &id,
		Message: strPtr("Session archived for task " + id),
	})

	return c.NoContent(http.StatusNoContent)
}

// GetSession returns session data by ID.
func (s *server) GetSession(c echo.Context, id string) error {
	data, err := s.store.LoadSession(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, data)
}

// SendMessage handles POST /api/sessions/{id}/messages — the chat-first interface.
func (s *server) SendMessage(c echo.Context, id string) error {
	var req struct {
		Message     string `json:"message"`
		AttachTools bool   `json:"attach_tools"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
	}

	ctx := c.Request().Context()
	resp, err := s.loop.SendMessage(ctx, id, req.Message)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, resp)
}

// HealthCheck returns service health with scheduler status.
func (s *server) HealthCheck(c echo.Context) error {
	count, lastTick := s.scheduler.Status()
	resp := map[string]interface{}{
		"status":         "healthy",
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"scheduler": map[string]interface{}{
			"registered_tasks": count,
			"last_tick":        lastTick.Format(time.RFC3339),
		},
	}
	return c.JSON(http.StatusOK, resp)
}

// DeleteSession removes a session file from disk.
func (s *server) DeleteSession(c echo.Context, id string) error {
	var fullPath string
	dir := filepath.Join(s.store.Base(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), id+"-") || entry.Name() == id+".json" {
			fullPath = filepath.Join(dir, entry.Name())
			break
		}
	}

	if fullPath == "" {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
	}

	if err := os.Remove(fullPath); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session " + id[:8] + " closed"),
	})

	return c.NoContent(http.StatusOK)
}

// SessionHistory returns the last N conversation turns for a session.
func (s *server) SessionHistory(c echo.Context, id string, params protocol.SessionHistoryParams) error {
	limit := 10 // default
	if q := c.QueryParam("limit"); q != "" {
		fmt.Sscanf(q, "%d", &limit)
	}

	sessMap, err := s.store.LoadSession(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	timestamps := sessMap["turns"]
	turns, ok := timestamps.([]interface{})
	if !ok {
		turns = []interface{}{}
	}

	start := 0
	if len(turns) > limit {
		start = len(turns) - limit
	}

	result := turns[start:]
	return c.JSON(http.StatusOK, result)
}

// createSessionInternal creates a new session bound to a workspace.
func (s *server) createSessionInternal(workspaceID string, clientType string) (*protocol.Session, error) {
	id := uuid.New()
	now := time.Now()
	status := protocol.SessionStatus("active")
	sessionName := id.String()[:8] + "-session"
	session := &protocol.Session{
		Id:            &id,
		Name:          strPtr(sessionName),
		WorkspaceId:   strPtr(workspaceID),
		SessionStatus: &status,
		CreatedAt:     &now,
	}

	s.store.SaveSession(id.String(), map[string]interface{}{
		"id":           id.String(),
		"name":         sessionName,
		"workspace_id": workspaceID,
		"client_type":  clientType,
		"status":       "active",
		"created_at":   now.Format(time.RFC3339),
	})

	s.wsHub.BroadcastSession(id.String(), protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session created for workspace " + workspaceID),
	})

	return session, nil
}

// idleCleanupLoop periodically scans sessions and deletes those that have been
// active for longer than the given timeout. Runs in a background goroutine.
func (s *server) idleCleanupLoop(timeout time.Duration) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sessions := s.store.ListSessions()

		now := time.Now()
		for _, sessMap := range sessions {
			createdAtStr, ok := sessMap["created_at"].(string)
			if !ok {
				continue
			}
			createdAt, err := time.Parse(time.RFC3339, createdAtStr)
			if err != nil {
				log.Printf("idle cleanup: bad created_at in session file: %v", err)
				continue
			}

			if now.Sub(createdAt) > timeout {
				sessionID, _ := sessMap["id"].(string)
				log.Printf("idle cleanup: deleting session %s (inactive for %v)", sessionID[:8], now.Sub(createdAt).Round(time.Minute))
				if err := s.store.DeleteSession(sessionID); err != nil {
					log.Printf("idle cleanup: failed to delete session %s: %v", sessionID, err)
				} else {
					s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
						Event:   protocol.SessionEvent,
						Message: strPtr("Session " + sessionID[:8] + " auto-closed (idle timeout)"),
					})
				}
			}
		}
	}
}
