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
	"github.com/labstack/echo/v4"
	"github.com/norman/norman/pkg/agent"
	"github.com/norman/norman/pkg/budget"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/version"
)

// resolveSessionID resolves a session name or short UUID prefix to its full ID.
// Matches by full name match or first-8-char UUID prefix; also checks archived sessions.
func (s *server) resolveSessionID(input string) (string, error) {
	// Check active sessions
	sessions := s.store.ListSessions()
	for _, sess := range sessions {
		name, _ := sess["name"].(string)
		idStr, _ := sess["id"].(string)
		if name == input || strings.HasPrefix(idStr[:8], input[:8]) {
			return idStr, nil
		}
	}

	// Also check archived sessions (single objects or arrays)
	dir := filepath.Join(s.store.Base(), "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("session %s not found", input)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".archive.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var items []map[string]interface{}
		if err := json.Unmarshal(data, &items); err != nil {
			continue
		}
		for _, sess := range items {
			name, _ := sess["name"].(string)
			idStr, _ := sess["id"].(string)
			if name == input || strings.HasPrefix(idStr[:8], input[:8]) {
				return idStr, nil
			}
		}
	}

	return "", fmt.Errorf("session %s not found", input)
}

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

	// A session must have a non-empty workspace_id — no derivation from index.
	if req.WorkspaceId == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "workspace_id is required"})
	}

	clientType := "custom"
	if req.ClientType != nil {
		clientType = string(*req.ClientType)
	}

	// Enforce max_active_sessions limit per workspace.
	maxActive := s.cfg.System.MaxActiveSessions
	if maxActive > 0 {
		activeCount := 0
		for _, sessMap := range s.store.ListSessions() {
			if status, ok := sessMap["status"].(string); ok && status == "active" {
				activeCount++
			}
		}
		if activeCount >= maxActive {
			return c.JSON(http.StatusConflict, map[string]string{
				"error": fmt.Sprintf("max_active_sessions (%d) reached for workspace %s", maxActive, req.WorkspaceId),
			})
		}
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
		if ws.Name != nil && *ws.Name == req.WorkspaceId {
			found = true
			break
		}
	}
	if !found {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found: " + req.WorkspaceId})
	}

	// Mark the session as active (no sandbox knowledge needed).
	sid := session.Id.String()
	s.workspaceManager.UpdateActivity(c.Request().Context(), sid)
	s.loop.MarkActive(req.WorkspaceId)

	s.wsHub.BroadcastSession(session.Id.String(), protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session created for workspace " + req.WorkspaceId),
	})

	return c.JSON(http.StatusCreated, session)
}

// ArchiveTask archives a task's data for long-term storage.
func (s *server) ArchiveTask(c echo.Context) error {
	var req protocol.ArchiveTaskRequest
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

// ArchiveSessionByID archives a session by its ID.
func (s *server) ArchiveSessionByID(c echo.Context, id string) error {
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

// CompressSession compresses a session's conversation history.
func (s *server) CompressSession(c echo.Context, id string, params protocol.CompressSessionParams) error {
	// Resolve session ID (short UUID prefix or full name) to actual session ID
	resolvedID, err := s.resolveSessionID(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found: " + id})
	}

	// Load turns from JSONL file on disk
	turns, err := s.store.LoadSessionTurns(resolvedID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "failed to load session turns: " + err.Error()})
	}

	// Convert turns to budget.Turn format
	var budgetTurns []budget.Turn
	for _, turn := range turns {
		role, _ := turn["role"].(string)
		content, _ := turn["content"].(string)
		tokens := 0
		if t, ok := turn["tokens"].(int); ok {
			tokens = t
		} else if t, ok := turn["tokens"].(float64); ok {
			tokens = int(t)
		}

		pinned := false
		if p, ok := turn["pinned"].(bool); ok {
			pinned = p
		}

		createdAt := time.Now()
		if t, ok := turn["created_at"].(string); ok {
			if ct, err := time.Parse(time.RFC3339, t); err == nil {
				createdAt = ct
			}
		}

		budgetTurns = append(budgetTurns, budget.Turn{
			Role:      role,
			Content:   content,
			Tokens:    tokens,
			Pinned:    pinned,
			CreatedAt: createdAt,
		})
	}

	// Create session state
	budgetConfig := budget.NewBudgetConfig(128000) // default max tokens
	sessionState := budget.NewSessionState(budgetConfig)
	sessionState.Turns = budgetTurns
	sessionState.TokenCount = budget.CountTokensFromTurns(budgetTurns)

	// Create compressor with LLM client adapter
	if s.loop == nil || s.loop.GetLLMClient() == nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "LLM client not available for compression"})
	}

	adapter := agent.NewLLMClientAdapter(s.loop.GetLLMClient())
	compressor := budget.NewCompressor(adapter, budgetConfig)

	// Perform compression
	aggressive := params.Aggressive != nil && *params.Aggressive
	result, err := compressor.Summarize(sessionState, aggressive)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "compression failed: " + err.Error()})
	}

	// Save compressed turns back to the store
	for _, t := range sessionState.Turns {
		turn := map[string]interface{}{
			"role":    t.Role,
			"content": t.Content,
			"tokens":  t.Tokens,
			"pinned":  t.Pinned,
		}
		if !t.CreatedAt.IsZero() {
			turn["created_at"] = t.CreatedAt.Format(time.RFC3339)
		}
		if err := s.store.PersistSessionTurn(resolvedID, turn); err != nil {
			log.Printf("warn: failed to persist compressed turn for session %s: %v", resolvedID, err)
		}
	}

	// Broadcast compression event
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionCompressed,
		TaskId:  &resolvedID,
		Message: strPtr(fmt.Sprintf("Session compressed: %d turns → %d kept, saved ~%d tokens", result.CompressedCount, result.KeptCount, result.TokensSaved)),
	})

	// Return compression result
	compressedCount := result.CompressedCount
	keptCount := result.KeptCount
	tokensSaved := result.TokensSaved
	summary := result.Summary

	return c.JSON(http.StatusOK, protocol.CompressionResult{
		CompressedCount: &compressedCount,
		KeptCount:       &keptCount,
		Summary:         &summary,
		TokensSaved:     &tokensSaved,
	})
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
		Provider    string `json:"provider"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
	}

	ctx := c.Request().Context()

	// Use SendMessage to pass the provider override into the task.
	resp, err := s.loop.SendMessage(ctx, id, req.Message, req.Provider)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	log.Printf("server: sendMessage response for session %s - response_len=%d, token_usage=%+v", id, len(resp.Response), resp.TokenUsage)

	// Persist conversation turns to disk (user + assistant).
	if err := s.store.PersistSessionTurn(id, map[string]interface{}{
		"role":    "user",
		"content": req.Message,
	}); err != nil {
		log.Printf("warn: failed to persist user turn for session %s: %v", id, err)
	}
	if resp.Response != "" {
		turn := map[string]interface{}{
			"role":    "assistant",
			"content": resp.Response,
		}
		if resp.TokenUsage != nil {
			turn["token_usage"] = map[string]interface{}{
				"prompt_tokens":     resp.TokenUsage.PromptTokens,
				"completion_tokens": resp.TokenUsage.CompletionTokens,
				"total_tokens":      resp.TokenUsage.TotalTokens,
				"cached_tokens":     resp.TokenUsage.CachedTokens,
			}
		}
		if err := s.store.PersistSessionTurn(id, turn); err != nil {
			log.Printf("warn: failed to persist assistant turn for session %s: %v", id, err)
		}
	}

	// Update last activity timestamp for the session
	s.workspaceManager.UpdateActivity(ctx, id)

	return c.JSON(http.StatusOK, resp)
}

// HealthCheck returns service health with scheduler status and server version.
func (s *server) HealthCheck(c echo.Context) error {
	count, lastTick := s.scheduler.Status()
	resp := map[string]interface{}{
		"status":         "healthy",
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"scheduler": map[string]interface{}{
			"registered_tasks": count,
			"last_tick":        lastTick.Format(time.RFC3339),
		},
		"version":    version.Version,
		"build_date": version.Date,
	}
	return c.JSON(http.StatusOK, resp)
}

// DeleteSession removes a session file from disk.
func (s *server) DeleteSession(c echo.Context, id string) error {
	// Resolve session ID (short UUID prefix or full name) to actual session ID
	resolvedID, err := s.resolveSessionID(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	// Delegate session deletion to the store
	if err := s.store.DeleteSession(resolvedID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session " + resolvedID[:8] + " closed"),
	})

	// Check if this was the last session for its workspace and mark idle
	s.checkWorkspaceIdle(resolvedID)

	return c.NoContent(http.StatusOK)
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

func (s *server) SessionHistory(c echo.Context, id string, params protocol.SessionHistoryParams) error {
	limit := 10 // default
	if q := c.QueryParam("limit"); q != "" {
		_, _ = fmt.Sscanf(q, "%d", &limit)
	}

	// Resolve alias (short UUID prefix or full name) to actual session ID
	resolvedID, err := s.resolveSessionID(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	// Load turns from JSONL file on disk
	turns, err := s.store.LoadSessionTurns(resolvedID)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
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
// inactive for longer than the given timeout. Runs in a background goroutine.
func (s *server) idleCleanupLoop(timeout time.Duration) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sessions := s.store.ListSessions()

		now := time.Now()
		for _, sessMap := range sessions {
			updatedAtStr, ok := sessMap["updated_at"].(string)
			if !ok {
				continue
			}
			updatedAt, err := time.Parse(time.RFC3339, updatedAtStr)
			if err != nil {
				log.Printf("idle cleanup: bad updated_at in session file: %v", err)
				continue
			}

			if now.Sub(updatedAt) > timeout {
				sessionID, _ := sessMap["id"].(string)
				log.Printf("idle cleanup: deleting session %s (inactive for %v)", sessionID[:8], now.Sub(updatedAt).Round(time.Minute))
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
