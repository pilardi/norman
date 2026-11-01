package main

import (
	"context"
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

// ListWorkspaces returns all registered workspaces (optionally filtered by status).
func (s *server) ListWorkspaces(w http.ResponseWriter, r *http.Request, params protocol.ListWorkspacesParams) {
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// CreateWorkspace creates a new workspace by provisioning an OpenShell sandbox
// and persisting the workspace metadata. The sandbox is created with context.json
// injected so that tools like `roger` can reach the Norman server from inside.
func (s *server) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var req protocol.WorkspaceCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	// Validate workspace name: non-empty, alphanumeric with underscores and hyphens
	if req.Name == "" || !isValidWorkspaceName(req.Name) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "workspace name must be non-empty and contain only alphanumeric characters, underscores, or hyphens",
		})
		return
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
	sb, err := s.workspaceManager.CreateWorkspace(r.Context(), req.Name, "")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to create sandbox for workspace %s: %v", req.Name, err),
		})
		return
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(ws)
}

// UpdateWorkspace applies a partial update to an existing workspace.
func (s *server) UpdateWorkspace(w http.ResponseWriter, r *http.Request, name string) {
	var req protocol.WorkspaceUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found"})
		return
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(ws)
}

// DeleteWorkspace removes a workspace.
func (s *server) DeleteWorkspace(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.workspaces[name]; !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found"})
		return
	}

	delete(s.workspaces, name)

	if err := s.store.DeleteWorkspace(name); err != nil {
		log.Printf("server: failed to delete workspace %s from disk: %v", name, err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Deleting workspace: " + name),
	})

	w.WriteHeader(http.StatusOK)
}

// GetWorkspace returns a workspace by name.
func (s *server) GetWorkspace(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(ws)
}

// StopWorkspace stops a workspace (frees the sandbox but keeps data).
func (s *server) StopWorkspace(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found"})
		return
	}

	// Transition: active → stopped (write snapshot + release sandbox).
	currentStatus := ws.Status
	if currentStatus != nil && *currentStatus == protocol.WorkspaceStatusStopped {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "workspace is already stopped",
		})
		return
	}

	// Write a snapshot of wiki entries and sessions for this workspace.
	snapshotDir := filepath.Join(s.store.Base(), "archived_snapshots", name)
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		log.Printf("[server] Error creating snapshot dir for %s: %v", name, err)
	} else {
		wikiPath := filepath.Join(snapshotDir, "wiki.jsonl")
		if f, err := os.Create(wikiPath); err == nil {
			_ = f.Close()
		}
		sessionPath := filepath.Join(snapshotDir, "sessions.jsonl")
		if f, err := os.Create(sessionPath); err == nil {
			_ = f.Close()
		}
	}

	// Close all sessions for this workspace (free agent loop resources).
	s.closeWorkspaceSessions(name)
	if err := s.workspaceManager.StopSandbox(context.Background(), name); err != nil {
		log.Printf("[server] Error stopping sandbox for workspace %s: %v", name, err)
	}

	stopped := protocol.WorkspaceStatusStopped
	ws.Status = &stopped
	now := time.Now()
	ws.LastUsed = &now

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace stopped: " + name),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(ws)
}

// RestoreWorkspace restores a stopped workspace to active.
func (s *server) RestoreWorkspace(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found"})
		return
	}

	currentStatus := ws.Status
	if currentStatus != nil && *currentStatus == protocol.WorkspaceStatusActive {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "workspace is already active",
		})
		return
	}

	active := protocol.WorkspaceStatusActive
	ws.Status = &active
	now := time.Now()
	ws.LastUsed = &now

	// Read snapshot back into the workspace if it exists.
	snapshotDir := filepath.Join(s.store.Base(), "archived_snapshots", name)
	if _, err := os.Stat(snapshotDir); err == nil {
		log.Printf("[server] Restoring snapshot for workspace %s from %s", name, snapshotDir)
	}

	// Start sandbox (always needed when restoring from stopped).
	if err := s.workspaceManager.StartSandbox(context.Background(), name); err != nil {
		log.Printf("[server] Error starting sandbox for restored workspace %s: %v", name, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to start sandbox"})
		return
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace restored to active: " + name),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(ws)
}

// PurgeWorkspace permanently deletes a workspace and all its traces.
func (s *server) PurgeWorkspace(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.workspaces[name]; !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found"})
		return
	}

	// Delete snapshot directory if it exists.
	snapshotDir := filepath.Join(s.store.Base(), "archived_snapshots", name)
	if err := os.RemoveAll(snapshotDir); err != nil {
		log.Printf("[server] Error removing snapshot dir for %s: %v", name, err)
	}

	// Close sessions and delete the sandbox to free resources.
	s.closeWorkspaceSessions(name)
	if err := s.workspaceManager.CloseSandbox(context.Background(), name); err != nil {
		log.Printf("[server] Error deleting sandbox for workspace %s: %v", name, err)
	}

	delete(s.workspaces, name)

	if err := s.store.DeleteWorkspace(name); err != nil {
		log.Printf("server: failed to delete workspace %s from disk: %v", name, err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace purged: " + name),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "purged"})
}

// isValidWorkspaceName validates that a workspace name contains only allowed characters.
func isValidWorkspaceName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if r < 'a' || r > 'z' || r < 'A' || r > 'Z' || r < '0' || r > '9' || r != '_' && r != '-' {
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
