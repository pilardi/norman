// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via Echo.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
	"github.com/gorilla/websocket"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/norman/norman/internal/api"
	"github.com/norman/norman/pkg/agent"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/memory"
	"github.com/norman/norman/pkg/env"
	"github.com/norman/norman/pkg/notify"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/scheduler"
	"github.com/norman/norman/pkg/skills"
)

// server holds all dependencies injected at startup.
type server struct {
	cfg        *config.Config
	store      *memory.Store
	rawSources *memory.RawSources
	activator  *skills.Activator
	loop       *agent.Loop
	wsHub      *WSHub
	notifier   *notify.Notifier
	scheduler  *scheduler.Scheduler

	// workspaceRegistry tracks created workspaces (in-memory + persisted).
	mu        sync.Mutex
	workspaces map[string]*protocol.Workspace
}

func main() {
	// --help / -h: print usage and exit.
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-h" {
			fmt.Fprintf(os.Stderr, `Usage: norman-server [OPTIONS]

Options:
  -v, --verbose    Enable server-side debug logging (Echo middleware)
  -h, --help       Show this help text and exit

Environment variables:
  NORMAN_STORE_DIR       Path to the memory store directory (default: $HOME/.norman/store)
  NORMAN_RAW_SOURCES_DIR Path to the raw sources store directory (default: $HOME/.norman/raw_sources)
  NORMAN_HOST            Server bind address (default: "127.0.0.1")
  NORMAN_PORT            Server port number (default: 8080)

Config file: ~/.norman/config.yaml

System defaults (overridable in config.yaml):
  default_model      gpt-4o
  server_port        8080
  host               127.0.0.1
  temperature        0.7
  max_tokens         32000
  skills.discovery   ~/.norman/skills/, /usr/share/norman/skills/
  skills.max_active  3
  notify.bell_enabled true
  notify.rate_limit_per_min 10

OpenShell: auto-resolved from OPENSHELL_GATEWAY_ADDRESS env var or
           metadata.json gateway_endpoint (falls back to localhost:8090).

Workspace-level overrides (per-workspace in config.yaml):
  base_dir       ~/.norman/workspaces/
  model          (empty = use system default)
  max_tokens     (0 = use system default)
  temperature    (0 = use system default)
  skill_overrides []
  system_prompt  (empty = use system default)

`+
				"Norman agent server — HTTP API + WebSocket hub for workspace management.\n")
			os.Exit(0)
		}
	}

	fmt.Fprintf(os.Stderr, "Norman agent server starting...\n")

	// Load configuration
	cfg, err := config.LoadDefault()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// --verbose CLI flag overrides config.yaml Logging.Verbose (server-side debug output).
	for _, arg := range os.Args[1:] {
		if arg == "--verbose" || arg == "-v" {
			cfg.System.Logging.Verbose = true
			break
		}
	}

	// Initialize memory store — path derived from environment, not hardcoded.
	// The server communicates with the environment via OpenShell; the store
	// location is now configurable and can be passed in or auto-detected.
	storeDir := os.Getenv("NORMAN_STORE_DIR")
	if storeDir == "" {
		storeDir = os.ExpandEnv("$HOME/.norman/store") // fallback for local dev
	}
	store, err := memory.NewStore(storeDir)
	if err != nil {
		log.Fatalf("failed to init memory store: %v", err)
	}

	// Discover skills
	skillsResult, err := skills.Discover(cfg.System.Skills.DiscoveryPaths)
	if err != nil {
		log.Printf("warning: skill discovery failed: %v (continuing without skills)", err)
	}

	// Build registry map
	registry := make(map[string]skills.SkillEntry)
	for _, entry := range skillsResult.Skills {
		registry[entry.Name] = entry
	}

	// Initialize activator
	activator := skills.NewActivator(registry, cfg.System.Skills.MaxActive)

	// Initialize WebSocket hub
	wsHub := NewWSHub()
	go wsHub.Run()

	// Extract server address from config (used for listen only).
	port := cfg.System.ServerPort
	host := cfg.System.Host

	// Resolve OpenShell gateway address: env var > metadata.json.
	openShellAddr := cfg.System.OpenShell.Address
	if openShellAddr == "" {
		openShellAddr = env.ResolveGatewayAddress()
	}

	// Load mTLS certificates from the same gateway directory as metadata.json.
	metadataPath := filepath.Join(os.ExpandEnv("$HOME"), ".config", "openshell", "gateways", "openshell")
	tlsConfig := env.ResolveMTLSConfig(metadataPath)

	// Create real OpenShell SDK client with TLS config if available.
	// If the client fails to connect, the server refuses to start (no fake fallback).
	openShellClient, err := env.NewClientWithTLS(openShellAddr, tlsConfig)
	if err != nil {
		log.Fatalf("[server] FATAL: OpenShell gateway connection failed at %s: %v\nServer refusing to start.", openShellAddr, err)
	}

	loopClient := env.NewSDKAdapter(openShellClient)

	// Initialize raw sources store for environment snapshots (context injection).
	rawSourcesDir := os.Getenv("NORMAN_RAW_SOURCES_DIR")
	if rawSourcesDir == "" {
		rawSourcesDir = os.ExpandEnv("$HOME/.norman/raw_sources") // fallback for local dev
	}
	rawSources, err := memory.NewRawSources(rawSourcesDir)
	if err != nil {
		log.Fatalf("failed to init raw sources store: %v", err)
	}

	// Create agent loop
	loop := agent.New(cfg, store, rawSources, activator, wsHub, loopClient)

	// Initialize notification notifier from config
	notifier := notify.New(wsHub, cfg.System.Notify.BellEnabled, cfg.System.Notify.SlackWebhook, cfg.System.Notify.RateLimitPerMin)

	// Initialize scheduler and wire results into loop's notification path
	resultsCh := make(chan scheduler.TaskResult, 100) // buffered to avoid blocking
	sched := scheduler.NewScheduler(resultsCh)

	// Wire OpenShell Exec() into the scheduler's runCommand adapter
	sched.WithRunCommand(func(ctx context.Context, provider, sandbox string, cmd []string) (*v1.ExecResult, error) {
		return openShellClient.RunCommand(ctx, provider, sandbox, cmd)
	})

	loop = loop.WithNotifier(notifier).WithResultsChannel(resultsCh)

	// Build server with all dependencies
	srv := &server{
		cfg:        cfg,
		store:      store,
		rawSources: rawSources,
		activator:  activator,
		loop:       loop,
		wsHub:      wsHub,
		notifier:   notifier,
		scheduler:  sched,
		workspaces: make(map[string]*protocol.Workspace),
	}

	// Load persisted workspaces from disk on startup
	if loaded, err := store.ListWorkspaces(); err != nil {
		log.Printf("server: failed to load workspaces from disk: %v", err)
	} else {
		for _, wsMap := range loaded {
			if name, ok := wsMap["name"].(string); ok {
				ws := &protocol.Workspace{
					Id:        nil,
					Name:      strPtr(name),
					Path:      strPtr(wsMap["path"].(string)),
					Status:    (*protocol.WorkspaceStatus)(strPtr(wsMap["status"].(string))),
					CreatedAt: nil,
				}
				if idStr, ok := wsMap["id"].(string); ok {
					id, err := uuid.Parse(idStr)
					if err == nil {
						ws.Id = &id
					}
				}
				srv.workspaces[name] = ws
			}
		}
		log.Printf("server: loaded %d persisted workspaces from disk", len(srv.workspaces))
	}

	e := echo.New()
	e.Use(middleware.Recover())

	// Conditional logging: only log requests when verbose mode is enabled.
	if cfg.System.Logging.Verbose {
		e.Use(middleware.Logger())
	}

	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Config endpoint — returns full system config (with unresolved secret placeholders).
	// Per-workspace overrides are included if workspace_id query param is provided.
	e.GET("/api/config", srv.GetConfig)
	e.PUT("/api/config", srv.PutConfig)

	// Session archive endpoint (Step 7d)
	e.POST("/api/sessions/archive", srv.ArchiveSession)

	// Chat-first interface: send a message to an active session (Phase 1)
	e.POST("/api/sessions/:id/messages", func(c echo.Context) error {
		return srv.SendMessage(c, c.Param("id"))
	})

	// Notification endpoints (Step 8d)
	e.GET("/api/notifications", srv.ListNotifications)
	e.POST("/api/notifications", srv.CreateNotification)
	e.DELETE("/api/notifications/:id", func(c echo.Context) error {
		return srv.DeleteNotification(c, c.Param("id"))
	})

	// Workspace endpoints (Step 9d)
	e.GET("/api/workspaces", srv.ListWorkspaces)
	e.POST("/api/workspaces", srv.CreateWorkspace)
	e.GET("/api/workspaces/:name", func(c echo.Context) error {
		return srv.GetWorkspace(c, c.Param("name"))
	})
	e.DELETE("/api/workspaces/:name", func(c echo.Context) error {
		return srv.DeleteWorkspace(c, c.Param("name"))
	})

	// Skill discovery path management (Step 14i)
	e.POST("/api/skills/paths", srv.AddDiscoveryPaths)

	// WebSocket hub for real-time push events
	e.GET("/ws", func(c echo.Context) error {
		conn, err := (&websocket.Upgrader{}).Upgrade(c.Response().Writer, c.Request(), nil)
		if err != nil {
			return err
		}
		// Extract optional session_id from query params for per-session filtering
		sessionID := c.QueryParam("session_id")
		wsHub.Add(conn, sessionID)
		defer wsHub.Remove(conn)

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return nil // client disconnected
			}
		}
	})

	// Register all API handlers (real implementations now)
	api.RegisterHandlers(e, srv)

	log.Printf("Norman Agent Server starting on %s:%d", host, port)

	// Start scheduler background goroutine
	schedCtx, schedCancel := context.WithCancel(context.Background())
	sched.Start(schedCtx)

	// Start idle timeout auto-cleanup goroutine (cleanup sessions inactive for >30min by default).
	go srv.idleCleanupLoop(30 * time.Minute)

	if err := e.Start(host + ":" + fmt.Sprintf("%d", port)); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
		schedCancel() // stop scheduler goroutine
		e.Logger.Info("shutting down...")
		e.Shutdown(context.Background())
	}()

	<-ctx.Done()
}

// --- Real handler implementations ---

func (s *server) ListSessions(c echo.Context) error {
	sessions := s.store.ListSessions()
	if len(sessions) == 0 {
		return c.JSON(http.StatusOK, []map[string]interface{}{})
	}
	return c.JSON(http.StatusOK, sessions)
}

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

	s.wsHub.BroadcastSession(session.Id.String(), protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session created for workspace " + req.WorkspaceId),
	})

	return c.JSON(http.StatusCreated, session)
}

func (s *server) ArchiveSession(c echo.Context) error {
	var req protocol.ArchiveSessionRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Read task data for archival
	taskData, err := s.store.LoadTask(req.TaskId)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "task not found: " + req.TaskId})
	}

	// Convert to []interface{} for ArchiveSession
	sessions := []interface{}{taskData}

	if err := s.store.ArchiveSession(req.TaskId, sessions); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Broadcast WebSocket event
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionCompressed,
		TaskId:  &req.TaskId,
		Message: strPtr("Session archived for task " + req.TaskId),
	})

	return c.NoContent(http.StatusNoContent)
}

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
		Message    string `json:"message"`
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

func (s *server) DeleteSession(c echo.Context, id string) error {
	// Support both short prefix and full UUID
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

func (s *server) ListSkills(c echo.Context) error {
	skillsResult, err := skills.Discover(s.cfg.System.Skills.DiscoveryPaths)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	result := make([]map[string]interface{}, len(skillsResult.Skills))
	for i, entry := range skillsResult.Skills {
		result[i] = map[string]interface{}{
			"name":        entry.Name,
			"description": entry.Description,
			"tools":       entry.Tools,
			"path":        entry.Path,
		}
	}

	return c.JSON(http.StatusOK, result)
}

func (s *server) InstallSkill(c echo.Context) error {
	var req protocol.SkillInstallRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Installing skill: " + req.Name),
	})

	return c.NoContent(http.StatusCreated)
}

func (s *server) RemoveSkill(c echo.Context, name string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Removing skill: " + name),
	})

	return c.NoContent(http.StatusOK)
}

// AddDiscoveryPaths adds new paths to the skill discovery configuration.
// Accepts a JSON body with "paths" array of strings.
func (s *server) AddDiscoveryPaths(c echo.Context) error {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if len(req.Paths) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "paths array must not be empty"})
	}

	if err := s.loop.AddDiscoveryPaths(req.Paths); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr(fmt.Sprintf("Added %d discovery path(s) for skill scanning", len(req.Paths))),
		Step:    intPtr(0),
	})

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"added": len(req.Paths),
		"paths": req.Paths,
	})
}

func (s *server) ListTasks(c echo.Context, params protocol.ListTasksParams) error {
	tasks, err := s.store.ListTasks()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Filter by status if provided
	if params.Status != nil && *params.Status != "" {
		filtered := make([]map[string]interface{}, 0)
		statusStr := string(*params.Status)
		for _, t := range tasks {
			if s, ok := t["status"].(string); ok && s == statusStr {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}

	return c.JSON(http.StatusOK, tasks)
}

func (s *server) CreateTask(c echo.Context) error {
	var req protocol.TaskCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// session_id is required — no auto-creation or guessing
	if req.SessionId == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "session_id is required"})
	}
	sessionID := req.SessionId

	// Validate session exists.
	if _, err := s.store.GetSession(sessionID); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("session %s not found", sessionID)})
	}

	task, err := s.loop.Submit(context.Background(), req.Prompt, req.WorkspaceId, sessionID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"id":           task.ID,
		"status":       string(task.Status),
		"prompt":       task.Prompt,
		"workspace_id": task.Workspace,
		"session_id":   sessionID,
	})
}

// createSessionInternal creates a new session bound to a workspace.
func (s *server) createSessionInternal(workspaceID string, clientType string) (*protocol.Session, error) {
	id := uuid.New()
	now := time.Now()
	status := protocol.SessionStatus("active")
	sessionName := id.String()[:8] + "-session"
	session := &protocol.Session{
		Id:          &id,
		Name:        strPtr(sessionName),
		WorkspaceId: strPtr(workspaceID),
		Status:      &status,
		CreatedAt:   &now,
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

func (s *server) ScheduleTask(c echo.Context) error {
	var req protocol.ScheduledTaskRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Scheduled task: " + req.Name),
	})

	return c.NoContent(http.StatusCreated)
}

func (s *server) ListScheduledTasks(c echo.Context) error {
	entries := s.scheduler.List()
	result := make([]protocol.ScheduledTask, 0, len(entries))
	for _, entry := range entries {
		status := protocol.ScheduledTaskStatusActive
		if !entry.Active {
			status = protocol.ScheduledTaskStatusPaused
		}
		id, err := uuid.Parse(entry.ID)
		if err != nil {
			log.Printf("list scheduled tasks: bad UUID %q: %v", entry.ID, err)
			continue
		}
		result = append(result, protocol.ScheduledTask{
			Id:          &id,
			Name:        &entry.Name,
			Schedule:    &entry.Schedule,
			Status:      &status,
			WorkspaceId: nil, // scheduler tasks don't carry workspace context in MVP
		})
	}
	return c.JSON(http.StatusOK, result)
}

func (s *server) UnscheduleTask(c echo.Context, id string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Unscheduling task: " + id[:8]),
	})

	return c.NoContent(http.StatusOK)
}

func (s *server) CancelTask(c echo.Context, id string) error {
	err := s.loop.CancelTask(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &id,
		Message: strPtr("Task cancelled"),
	})

	return c.NoContent(http.StatusOK)
}

func (s *server) GetTask(c echo.Context, id string) error {
	task, err := s.loop.GetTask(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"id":         task.ID,
		"status":     string(task.Status),
		"prompt":     task.Prompt,
		"output":     string(task.Output),
		"error":      task.Error,
		"created_at": task.CreatedAt.Format(time.RFC3339),
	})
}

func (s *server) ReportTaskComplete(c echo.Context, id string) error {
	var req protocol.TaskCompletion
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskComplete,
		TaskId:  &id,
		Message: strPtr("Task completed"),
	})

	return c.NoContent(http.StatusOK)
}

func (s *server) TailTaskLog(c echo.Context, id string, params protocol.TailTaskLogParams) error {
	lines := 10
	if params.Lines != nil && *params.Lines > 0 {
		lines = *params.Lines
	}

	task, err := s.loop.GetTask(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	logs, err := s.loop.GetLogs(c.Request().Context(), task.SessionId)
	if err != nil {
		log.Printf("tail task log: failed to get logs for session %s: %v", task.SessionId, err)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"lines": lines,
			"data":  []string{"no logs available"},
		})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &id,
		Message: strPtr("Tail " + id[:8] + " log (last " + string(rune(lines)) + " lines)"),
	})

	// Return the last `lines` entries from logs
	start := 0
	if len(logs) > lines {
		start = len(logs) - lines
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"lines": lines,
		"data":  logs[start:],
	})
}

func (s *server) ListWorkspaces(c echo.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make([]*protocol.Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		result = append(result, ws)
	}
	return c.JSON(http.StatusOK, result)
}

func (s *server) CreateWorkspace(c echo.Context) error {
	var req protocol.WorkspaceCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Derive path: workspace dir is under the store's base directory.
	storePath := s.store.Base()
	wsPath := filepath.Join(storePath, req.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	id := uuid.New()
	status := protocol.WorkspaceStatusActive
	ws := &protocol.Workspace{
		Id:        &id,
		Name:      strPtr(req.Name),
		Path:      strPtr(wsPath),
		Status:    &status,
		CreatedAt: &now,
	}
	s.workspaces[req.Name] = ws

	// Persist workspace to disk
	wsMap := map[string]interface{}{
		"id":         id.String(),
		"name":       req.Name,
		"path":       wsPath,
		"status":     string(status),
		"created_at": now.Format(time.RFC3339),
	}
	if err := s.store.SaveWorkspace(req.Name, wsMap); err != nil {
		log.Printf("server: failed to persist workspace %s: %v", req.Name, err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace created: " + req.Name),
	})

	return c.JSON(http.StatusCreated, ws)
}

func (s *server) DeleteWorkspace(c echo.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.workspaces[name]; !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	delete(s.workspaces, name)

	// Delete workspace file from disk
	if err := s.store.DeleteWorkspace(name); err != nil {
		log.Printf("server: failed to delete workspace %s from disk: %v", name, err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Deleting workspace: " + name),
	})

	return c.NoContent(http.StatusOK)
}

func (s *server) GetWorkspace(c echo.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ws, ok := s.workspaces[name]
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	return c.JSON(http.StatusOK, ws)
}

func (s *server) ConnectWebSocket(c echo.Context) error {
	conn, err := (&websocket.Upgrader{}).Upgrade(c.Response().Writer, c.Request(), nil)
	if err != nil {
		return err
	}
	// Extract optional session_id from query params for per-session filtering
	sessionID := c.QueryParam("session_id")
	s.wsHub.Add(conn, sessionID)
	defer s.wsHub.Remove(conn)

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			return nil // client disconnected
		}
	}
}

// --- Notification handlers (Step 8d) ---

func (s *server) ListNotifications(c echo.Context) error {
	// Return a summary of recent notifications for display
	return c.JSON(http.StatusOK, []map[string]interface{}{
		{
			"message": "Notification system initialized",
			"type":    "info",
		},
	})
}

func (s *server) CreateNotification(c echo.Context) error {
	var req protocol.NotificationCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Push notification via notifier (WS + bell + Slack)
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
		log.Printf("notify: failed to deliver notification: %v", err)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"id":       time.Now().UnixNano(),
		"title":    req.Title,
		"severity": string(req.Severity),
	})
}

func (s *server) DeleteNotification(c echo.Context, id string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.Notification,
		Message: strPtr("Notification " + id + " dismissed"),
	})
	return c.NoContent(http.StatusOK)
}

// GetConfig returns the system config (with unresolved secret placeholders).
// If workspace_id query param is provided, returns merged per-workspace config.
// The response also includes the resolved OpenShell gateway address.
func (s *server) GetConfig(c echo.Context) error {
	workspaceID := c.QueryParam("workspace_id")

	if workspaceID != "" {
		// Return merged config for a specific workspace
		if ws, ok := s.cfg.Workspaces[workspaceID]; ok {
			return c.JSON(http.StatusOK, map[string]interface{}{
				"system":        s.cfg.System,
				"workspace":     ws,
				"merged":        s.cfg.MergeWorkspaceConfig(workspaceID, ws),
				"openshell":     resolveGatewayAddress(),
			})
		}
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	}

	// Return full system config (excluding Logging.Verbose) with resolved OpenShell gateway address.
	// Verbose is a client-side concern; the server may run with or without it independently.
	result := make(map[string]interface{})
	cfgCopy := *s.cfg.System
	cfgCopy.Logging = config.LoggingConfig{Verbose: false} // strip from API response
	result["system"] = cfgCopy
	result["openshell"] = resolveGatewayAddress()
	return c.JSON(http.StatusOK, result)
}

// PutConfig accepts a partial system config update and merges it into the server's config.
func (s *server) PutConfig(c echo.Context) error {
	var updates map[string]interface{}
	if err := c.Bind(&updates); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
	}

	// Merge updates into s.cfg.System
	if err := mergeUpdatesIntoSystem(s.cfg.System, updates); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Validate the merged config
	if err := s.cfg.Validate(); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Save to disk
	configPath := os.ExpandEnv("$HOME/.norman/config.yaml")
	if err := s.cfg.Save(configPath); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to save config"})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "config updated",
		"system":  s.cfg.System,
	})
}

// mergeUpdatesIntoSystem applies partial key-value updates into a SystemConfig.
func mergeUpdatesIntoSystem(cfg *config.SystemConfig, updates map[string]interface{}) error {
	for k, v := range updates {
		switch k {
		case "default_model":
			if s, ok := v.(string); ok {
				cfg.DefaultModel = s
			} else {
				return fmt.Errorf("default_model must be a string")
			}
		case "server_port":
			if f, ok := v.(float64); ok {
				cfg.ServerPort = int(f)
			} else {
				return fmt.Errorf("server_port must be a number")
			}
		case "host":
			if s, ok := v.(string); ok {
				cfg.Host = s
			} else {
				return fmt.Errorf("host must be a string")
			}
		case "max_active_skills":
			if f, ok := v.(float64); ok {
				cfg.Skills.MaxActive = int(f)
			} else {
				return fmt.Errorf("max_active_skills must be a number")
			}
		case "bell_enabled":
			if b, ok := v.(bool); ok {
				cfg.Notify.BellEnabled = b
			} else {
				return fmt.Errorf("bell_enabled must be a boolean")
			}
		case "rate_limit_per_min":
			if f, ok := v.(float64); ok {
				cfg.Notify.RateLimitPerMin = int(f)
			} else {
				return fmt.Errorf("rate_limit_per_min must be a number")
			}
		case "temperature":
			if f, ok := v.(float64); ok {
				cfg.Temperature = f
			} else {
				return fmt.Errorf("temperature must be a number")
			}
		case "max_tokens":
			if f, ok := v.(float64); ok {
				cfg.MaxTokens = int(f)
			} else {
				return fmt.Errorf("max_tokens must be a number")
			}
		case "openshell_address":
			if s, ok := v.(string); ok {
				cfg.OpenShell.Address = s
			} else {
				return fmt.Errorf("openshell_address must be a string")
			}
		default:
			return fmt.Errorf("unknown config key: %s", k)
		}
	}
	return nil
}

// resolveGatewayAddress returns the currently-resolved OpenShell gateway address.
func resolveGatewayAddress() string {
	addr := os.Getenv("OPENSHELL_GATEWAY_ADDRESS")
	if addr != "" {
		return addr
	}
	return env.ResolveGatewayAddress()
}

// --- WSHub (moved from stub to real implementation) ---

type WSHub struct {
	mu            sync.RWMutex
	clients       map[*websocket.Conn]struct{}          // global clients (no session filter)
	sessionClients map[string]map[*websocket.Conn]struct{} // per-session client maps
	broadcast     chan []byte
}

func NewWSHub() *WSHub {
	return &WSHub{
		clients:        make(map[*websocket.Conn]struct{}),
		sessionClients: make(map[string]map[*websocket.Conn]struct{}),
		broadcast:      make(chan []byte, 256),
	}
}

func (h *WSHub) Run() {
	go h.writeLoop()
	for msg := range h.broadcast {
		h.mu.RLock()
		for c := range h.clients {
			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("ws write error: %v", err)
			}
		}
		h.mu.RUnlock()
	}
}

func (h *WSHub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	default:
		log.Printf("ws broadcast buffer full, dropping message")
	}
}

// BroadcastEvent sends a structured WebSocketEvent to all connected clients.
func (h *WSHub) BroadcastEvent(event protocol.WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("ws marshal error: %v", err)
		return
	}
	h.Broadcast(data)
}

// BroadcastSession sends a WebSocketEvent only to clients subscribed to the given session_id.
func (h *WSHub) BroadcastSession(sessionID string, event protocol.WebSocketEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		log.Printf("ws marshal error: %v", err)
		return
	}
	h.mu.RLock()
	subs := h.sessionClients[sessionID]
	for c := range subs {
		if err := c.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("ws session write error for %s: %v", sessionID[:8], err)
		}
	}
	h.mu.RUnlock()
}

// Add registers a WebSocket connection with an optional session_id.
// If sessionID is empty, the client joins the global broadcast pool.
func (h *WSHub) Add(c *websocket.Conn, sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
	if sessionID != "" {
		if h.sessionClients[sessionID] == nil {
			h.sessionClients[sessionID] = make(map[*websocket.Conn]struct{})
		}
		h.sessionClients[sessionID][c] = struct{}{}
	}
}

// Remove unregisters a WebSocket connection from both global and session pools.
func (h *WSHub) Remove(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	for _, subs := range h.sessionClients {
		delete(subs, c)
	}
}

func (h *WSHub) writeLoop() {
	for msg := range h.broadcast {
		h.mu.RLock()
		for c := range h.clients {
			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("ws write error: %v", err)
			}
		}
		h.mu.RUnlock()
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int        { return &i }
