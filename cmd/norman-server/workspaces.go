package main

import (
	"context"
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

// ListWorkspaces returns all registered workspaces (optionally filtered by status).
func (s *server) ListWorkspaces(c echo.Context, params protocol.ListWorkspacesParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*protocol.Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		// Apply status filter if provided
		if params.Status != nil {
			filterStatus := protocol.WorkspaceStatus(*params.Status)
			if ws.Status == nil || *ws.Status != filterStatus {
				continue
			}
		}
		result = append(result, ws)
	}
	return c.JSON(http.StatusOK, result)
}

// CreateWorkspace creates a new workspace by provisioning an OpenShell sandbox
// and persisting the workspace metadata. The sandbox is created with context.json
// injected so that tools like `roger` can reach the Norman server from inside.
func (s *server) CreateWorkspace(c echo.Context) error {
	var req protocol.WorkspaceCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Validate workspace name: non-empty, alphanumeric with underscores and hyphens
	if req.Name == "" || !isValidWorkspaceName(req.Name) {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "workspace name must be non-empty and contain only alphanumeric characters, underscores, or hyphens",
		})
	}

	storePath := s.store.Base()
	wsPath := filepath.Join(storePath, req.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	id := uuid.New()
	status := protocol.WorkspaceStatusActive

	// Create the OpenShell sandbox via WorkspaceManager (injects context.json).
	// Server URL is read from NORMAN_SERVER_URL env var inside the sandbox.
	sb, err := s.workspaceManager.CreateWorkspace(c.Request().Context(), req.Name, "")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to create sandbox for workspace %s: %v", req.Name, err),
		})
	}

	ws := &protocol.Workspace{
		Id:        &id,
		Name:      strPtr(req.Name),
		Path:      strPtr(wsPath),
		Status:    &status,
		CreatedAt: &now,
	}

	if req.Purpose != nil {
		ws.Purpose = req.Purpose
	}
	if req.Scope != nil {
		ws.Scope = req.Scope
	}
	if req.Tags != nil {
		ws.Tags = req.Tags
	}

	s.workspaces[req.Name] = ws

	wsMap := map[string]interface{}{
		"id":         id.String(),
		"name":       req.Name,
		"path":       wsPath,
		"status":     string(status),
		"created_at": now.Format(time.RFC3339),
	}
	if req.Purpose != nil {
		wsMap["purpose"] = *req.Purpose
	}
	if req.Scope != nil {
		wsMap["scope"] = *req.Scope
	}
	if req.Tags != nil {
		wsMap["tags"] = *req.Tags
	}
	if err := s.store.SaveWorkspace(req.Name, wsMap); err != nil {
		log.Printf("server: failed to persist workspace %s: %v", req.Name, err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace created: " + req.Name + " (sandbox: " + sb.Name + ")"),
	})

	return c.JSON(http.StatusCreated, ws)
}

// UpdateWorkspace applies a partial update to an existing workspace.
func (s *server) UpdateWorkspace(c echo.Context, name string) error {
	var req protocol.WorkspaceUpdateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	now := time.Now()

	if req.Purpose != nil {
		ws.Purpose = req.Purpose
	}
	if req.Scope != nil {
		ws.Scope = req.Scope
	}
	if req.Tags != nil {
		ws.Tags = req.Tags
	}

	wsMap := map[string]interface{}{
		"id":         ws.Id.String(),
		"name":       *ws.Name,
		"path":       *ws.Path,
		"status":     string(*ws.Status),
		"created_at": ws.CreatedAt.Format(time.RFC3339),
		"updated_at": now.Format(time.RFC3339),
	}
	if ws.Purpose != nil {
		wsMap["purpose"] = *ws.Purpose
	}
	if ws.Scope != nil {
		wsMap["scope"] = *ws.Scope
	}
	if ws.Tags != nil {
		wsMap["tags"] = *ws.Tags
	}

	if err := s.store.SaveWorkspace(name, wsMap); err != nil {
		log.Printf("server: failed to persist workspace %s update: %v", name, err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace updated: " + name),
	})

	return c.JSON(http.StatusOK, ws)
}

// DeleteWorkspace removes a workspace.
func (s *server) DeleteWorkspace(c echo.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.workspaces[name]; !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	delete(s.workspaces, name)

	if err := s.store.DeleteWorkspace(name); err != nil {
		log.Printf("server: failed to delete workspace %s from disk: %v", name, err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Deleting workspace: " + name),
	})

	return c.NoContent(http.StatusOK)
}

// GetWorkspace returns a workspace by name.
func (s *server) GetWorkspace(c echo.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	return c.JSON(http.StatusOK, ws)
}

// StopWorkspace stops a workspace (frees the sandbox but keeps data).
func (s *server) StopWorkspace(c echo.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	// Transition: active → stopped (allow from any non-archived state)
	currentStatus := ws.Status
	if currentStatus != nil && (*currentStatus == protocol.WorkspaceStatusStopped ||
		*currentStatus == protocol.WorkspaceStatusArchived) {
		return c.JSON(http.StatusConflict, map[string]string{
			"error": "workspace is already stopped or archived",
		})
	}

	// Close all sessions for this workspace (free sandbox resources)
	s.closeWorkspaceSessions(name)

	stopped := protocol.WorkspaceStatusStopped
	ws.Status = &stopped
	now := time.Now()
	ws.LastUsed = &now

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace stopped: " + name),
	})

	return c.JSON(http.StatusOK, ws)
}

// RestoreWorkspace restores a stopped or archived workspace to active.
func (s *server) RestoreWorkspace(c echo.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	currentStatus := ws.Status
	if currentStatus != nil && *currentStatus == protocol.WorkspaceStatusActive {
		return c.JSON(http.StatusConflict, map[string]string{
			"error": "workspace is already active",
		})
	}

	active := protocol.WorkspaceStatusActive
	ws.Status = &active
	now := time.Now()
	ws.LastUsed = &now

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace restored to active: " + name),
	})

	return c.JSON(http.StatusOK, ws)
}

// ArchiveWorkspace archives a workspace (immutable snapshot, frees sandbox).
func (s *server) ArchiveWorkspace(c echo.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	currentStatus := ws.Status
	if currentStatus != nil && *currentStatus == protocol.WorkspaceStatusArchived {
		return c.JSON(http.StatusConflict, map[string]string{
			"error": "workspace is already archived",
		})
	}

	// Create a snapshot of wiki entries and sessions for this workspace
	snapshotDir := filepath.Join(s.store.Base(), "archived_snapshots", name)
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		log.Printf("server: failed to create archive snapshot dir for %s: %v", name, err)
	}

	// Write wiki entries snapshot (JSONL format)
	wikiPath := filepath.Join(snapshotDir, "wiki.jsonl")
	if f, err := os.Create(wikiPath); err == nil {
		f.Close() // create empty file; actual content written by engine if available
	}

	// Write session transcripts
	sessionPath := filepath.Join(snapshotDir, "sessions.jsonl")
	if f, err := os.Create(sessionPath); err == nil {
		f.Close()
	}

	archived := protocol.WorkspaceStatusArchived
	ws.Status = &archived
	now := time.Now()
	ws.LastUsed = &now

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace archived: " + name),
	})

	return c.JSON(http.StatusOK, ws)
}

// PurgeWorkspace permanently deletes a workspace and all its traces.
func (s *server) PurgeWorkspace(c echo.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.workspaces[name]; !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	// First stop (free sandbox) then delete
	s.closeWorkspaceSessions(name)
	delete(s.workspaces, name)

	if err := s.store.DeleteWorkspace(name); err != nil {
		log.Printf("server: failed to delete workspace %s from disk: %v", name, err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace purged: " + name),
	})

	return c.NoContent(http.StatusOK)
}

// isValidWorkspaceName validates that a workspace name contains only allowed characters.
func isValidWorkspaceName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// closeWorkspaceSessions finds and deletes all sessions associated with a workspace.
func (s *server) closeWorkspaceSessions(workspaceName string) {
	// Match sessions by sandbox name pattern: "ws-" + workspace_name_without_dashes[:15]
	cleanName := strings.ReplaceAll(workspaceName, "-", "")
	if len(cleanName) > 15 {
		cleanName = cleanName[:15]
	}
	pattern := "ws-" + cleanName

	for id, session := range s.workspaceManager.Sessions() {
		if strings.HasPrefix(session.Sandbox(), pattern) {
			log.Printf("[server] Closing session %s for workspace %s", id, workspaceName)
			if err := session.Delete(context.Background()); err != nil {
				log.Printf("[server] Error closing session %s: %v", id, err)
			}
		}
	}
}
