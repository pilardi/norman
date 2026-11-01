package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/norman/norman/pkg/protocol"
)

// ListSessions returns all sessions.
func (s *server) ListSessions(w http.ResponseWriter, r *http.Request) {
	sessions := s.store.ListSessions()
	if len(sessions) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

// CreateSession creates a new session bound to a workspace.
func (s *server) CreateSession(w http.ResponseWriter, r *http.Request) {
	var req protocol.SessionCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	clientType := "custom"
	if req.ClientType != nil {
		clientType = string(*req.ClientType)
	}
	session, err := s.createSessionInternal(req.WorkspaceId, clientType)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found: " + req.WorkspaceId})
		return
	}

	// Ensure the OpenShell sandbox exists for this workspace, carrying the session UUID.
	sid := session.Id.String()
	_, err = s.workspaceManager.GetSession(r.Context(), req.WorkspaceId, sid)
	if err != nil {
		log.Printf("[sessions] failed to ensure sandbox for workspace %s: %v", req.WorkspaceId, err)
	}

	// Update last activity timestamp and signal "in use"
	s.workspaceManager.UpdateLastActivity(req.WorkspaceId)
	s.loop.MarkActive(req.WorkspaceId)

	s.wsHub.BroadcastSession(session.Id.String(), protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session created for workspace " + req.WorkspaceId),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(session)
}

// ArchiveSession archives a session by task ID.
func (s *server) ArchiveSession(w http.ResponseWriter, r *http.Request) {
	var req protocol.ArchiveSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	taskData, err := s.store.LoadTask(req.TaskId)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "task not found: " + req.TaskId})
		return
	}

	sessions := []interface{}{taskData}
	if err := s.store.ArchiveSession(req.TaskId, sessions); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionCompressed,
		TaskId:  &req.TaskId,
		Message: strPtr("Session archived for task " + req.TaskId),
	})

	w.WriteHeader(http.StatusNoContent)
}

// ArchiveSessionByID archives a session by path-based ID.
func (s *server) ArchiveSessionByID(w http.ResponseWriter, r *http.Request, id string) {
	sessionData, err := s.store.LoadSession(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "session not found: " + id})
		return
	}

	sessions := []interface{}{sessionData}
	if err := s.store.ArchiveSession(id, sessions); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionCompressed,
		TaskId:  &id,
		Message: strPtr("Session archived for task " + id),
	})

	w.WriteHeader(http.StatusNoContent)
}

// GetSession returns session data by ID.
func (s *server) GetSession(w http.ResponseWriter, r *http.Request, id string) {
	data, err := s.store.LoadSession(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// SendMessage handles POST /api/sessions/{id}/messages — the chat-first interface.
func (s *server) SendMessage(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Message     string `json:"message"`
		AttachTools bool   `json:"attach_tools"`
		Provider    string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	if req.Message == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "message is required"})
		return
	}

	ctx := r.Context()
	resp, err := s.loop.SendMessage(ctx, id, req.Message, req.Provider)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Update last activity timestamp for the session's workspace
	s.updateWorkspaceActivity(id)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// updateWorkspaceActivity updates the last activity timestamp for a session's workspace.
func (s *server) updateWorkspaceActivity(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Look up the session to find its workspace ID
	for _, ws := range s.workspaces {
		if ws.Id != nil && ws.Id.String() == sessionID {
			// Session belongs directly to this workspace
			if ws.Name != nil {
				s.workspaceManager.UpdateLastActivity(*ws.Name)
			}
			return
		}
	}

	// Fallback: check sessions store for workspace_id mapping
	data, err := s.store.LoadSession(sessionID)
	if err == nil {
		if wid, ok := data["workspace_id"].(string); ok && wid != "" {
			s.workspaceManager.UpdateLastActivity(wid)
		}
	}
}

// HealthCheck returns service health with scheduler status.
func (s *server) HealthCheck(w http.ResponseWriter, r *http.Request) {
	count, lastTick := s.scheduler.Status()
	resp := map[string]interface{}{
		"status":         "healthy",
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"scheduler": map[string]interface{}{
			"registered_tasks": count,
			"last_tick":        lastTick.Format(time.RFC3339),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// DeleteSession removes a session file from disk.
func (s *server) DeleteSession(w http.ResponseWriter, r *http.Request, id string) {
	var fullPath string
	dir := filepath.Join(s.store.Base(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), id+"-") || entry.Name() == id+".json" {
			fullPath = filepath.Join(dir, entry.Name())
			break
		}
	}

	if fullPath == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "session not found"})
		return
	}

	if err := os.Remove(fullPath); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session " + id[:8] + " closed"),
	})

	// Check if this was the last session for its workspace and mark idle
	s.checkWorkspaceIdle(id)

	w.WriteHeader(http.StatusOK)
}

// checkWorkspaceIdle determines if a deleted session was the last one for its workspace.
func (s *server) checkWorkspaceIdle(sessionID string) {
	// Look up the session's workspace
	var workspaceID string
	for _, ws := range s.workspaces {
		if ws.Id != nil && ws.Id.String() == sessionID {
			if ws.Name != nil && *ws.Name != "" {
				workspaceID = *ws.Name
			} else {
				workspaceID = ws.Id.String()
			}
			break
		}
	}

	if workspaceID == "" {
		data, err := s.store.LoadSession(sessionID)
		if err == nil {
			if wid, ok := data["workspace_id"].(string); ok && wid != "" {
				workspaceID = wid
			}
		}
	}

	if workspaceID == "" {
		return
	}

	// Check if any other sessions exist for this workspace
	s.mu.Lock()
	hasOther := false
	for _, ws := range s.workspaces {
		if ws.Id != nil && ws.Id.String() != sessionID {
			// Quick check: if workspace ID matches, there are other sessions
			if ws.Name != nil && *ws.Name == workspaceID {
				hasOther = true
				break
			}
		}
	}
	s.mu.Unlock()

	if !hasOther {
		s.loop.MarkIdle(workspaceID)
	}
}

// SessionHistory returns the last N conversation turns for a session.
func (s *server) SessionHistory(w http.ResponseWriter, r *http.Request, id string, params protocol.SessionHistoryParams) {
	limit := 10 // default
	if q := r.URL.Query().Get("limit"); q != "" {
		_, _ = fmt.Sscanf(q, "%d", &limit)
	}

	sessMap, err := s.store.LoadSession(id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
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

	_ = s.store.SaveSession(id.String(), map[string]interface{}{
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
