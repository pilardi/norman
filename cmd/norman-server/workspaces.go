package main

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/norman/norman/pkg/protocol"
)

// ListWorkspaces returns all registered workspaces.
func (s *server) ListWorkspaces(c echo.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*protocol.Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
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

	storePath := s.store.Base()
	wsPath := filepath.Join(storePath, req.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	id := uuid.New()
	status := protocol.WorkspaceStatusActive

	// Create the OpenShell sandbox via WorkspaceManager (injects context.json).
	// Server URL is read from NORMAN_SERVER_URL env var inside the sandbox.
	session, err := s.workspaceManager.CreateWorkspace(c.Request().Context(), req.Name, "")
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
		Message: strPtr("Workspace created: " + req.Name + " (sandbox: " + session.SessionID() + ")"),
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
