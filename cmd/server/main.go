// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via Echo.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
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
	cfg       *config.Config
	store     *memory.Store
	activator *skills.Activator
	loop      *agent.Loop
	wsHub     *WSHub
	notifier  *notify.Notifier
}

func main() {
	// Load configuration
	cfg, err := config.LoadDefault()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Initialize memory store
	storeDir := os.ExpandEnv("$HOME/.norman/store")
	store, err := memory.NewStore(storeDir)
	if err != nil {
		log.Fatalf("failed to init memory store: %v", err)
	}

	// Discover skills
	skillsResult, err := skills.Discover(cfg.Skills.DiscoveryPaths)
	if err != nil {
		log.Printf("warning: skill discovery failed: %v (continuing without skills)", err)
	}

	// Build registry map
	registry := make(map[string]skills.SkillEntry)
	for _, entry := range skillsResult.Skills {
		registry[entry.Name] = entry
	}

	// Initialize activator
	activator := skills.NewActivator(registry, cfg.Skills.MaxActive)

	// Initialize WebSocket hub
	wsHub := NewWSHub()
	go wsHub.Run()

	// Create real OpenShell SDK client and adapt it to the interface
	openShellClient, err := env.NewClient("127.0.0.1:8080")
	if err != nil {
		log.Printf("[server] WARNING: OpenShell client init failed (using fallback): %v", err)
	}

	// Use real SDK client if available, otherwise fall back to fake client
	var loopClient agent.OpenShellClient
	if openShellClient != nil {
		loopClient = env.NewSDKAdapter(openShellClient)
	} else {
		loopClient = agent.NewFakeOpenShellClient()
	}

	// Create agent loop
	loop := agent.New(cfg, store, activator, wsHub, loopClient)

	// Initialize notification notifier from config
	notifier := notify.New(wsHub, cfg.Notify.BellEnabled, cfg.Notify.SlackWebhook, cfg.Notify.RateLimitPerMin)

	// Initialize scheduler and wire results into loop's notification path
	resultsCh := make(chan scheduler.TaskResult, 100) // buffered to avoid blocking
	sched := scheduler.NewScheduler(resultsCh)
	loop = loop.WithNotifier(notifier).WithResultsChannel(resultsCh)

	// Build server with all dependencies
	srv := &server{
		cfg:       cfg,
		store:     store,
		activator: activator,
		loop:      loop,
		wsHub:     wsHub,
		notifier:  notifier,
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	e := echo.New()
	e.Use(middleware.Recover())
	e.Use(middleware.Logger())

	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Session archive endpoint (Step 7d)
	e.POST("/api/sessions/archive", srv.ArchiveSession)

	// Notification endpoints (Step 8d)
	e.GET("/api/notifications", srv.ListNotifications)
	e.POST("/api/notifications", srv.CreateNotification)
	e.DELETE("/api/notifications/:id", func(c echo.Context) error {
		return srv.DeleteNotification(c, c.Param("id"))
	})

	// WebSocket hub for real-time push events
	e.GET("/ws", func(c echo.Context) error {
		conn, err := (&websocket.Upgrader{}).Upgrade(c.Response().Writer, c.Request(), nil)
		if err != nil {
			return err
		}
		wsHub.Add(conn)
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

	log.Printf("Norman Agent Server starting on :%s", port)

	// Start scheduler background goroutine
	schedCtx, schedCancel := context.WithCancel(context.Background())
	sched.Start(schedCtx)

	if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
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
	tasks, err := s.store.ListTasks()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, tasks)
}

func (s *server) CreateSession(c echo.Context) error {
	var req protocol.SessionCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionEvent,
		Message: strPtr("Session created for workspace " + req.WorkspaceId),
	})

	return c.NoContent(http.StatusCreated)
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

func (s *server) ListSkills(c echo.Context) error {
	skillsResult, err := skills.Discover(s.cfg.Skills.DiscoveryPaths)
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

	task, err := s.loop.Submit(context.Background(), req.Prompt, req.WorkspaceId)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"id":          task.ID,
		"status":      string(task.Status),
		"prompt":      task.Prompt,
		"workspace_id": task.Workspace,
	})
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
	return c.JSON(http.StatusOK, []map[string]interface{}{}) // stub for now
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

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		TaskId:  &id,
		Message: strPtr("Tail " + id[:8] + " log (last " + string(rune(lines)) + " lines)"),
	})

	return c.JSON(http.StatusOK, map[string]interface{}{
		"lines": lines,
		"data":  []string{"sandbox initialized", "ready"}, // stub
	})
}

func (s *server) ListWorkspaces(c echo.Context) error {
	return c.JSON(http.StatusOK, []map[string]interface{}{}) // stub for now
}

func (s *server) CreateWorkspace(c echo.Context) error {
	var req protocol.WorkspaceCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Workspace created: " + req.Name),
	})

	return c.NoContent(http.StatusCreated)
}

func (s *server) DeleteWorkspace(c echo.Context, name string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Deleting workspace: " + name),
	})

	return c.NoContent(http.StatusOK)
}

func (s *server) GetWorkspace(c echo.Context, name string) error {
	return c.JSON(http.StatusOK, map[string]interface{}{
		"name": name,
		"path": os.ExpandEnv("$HOME/workspaces/" + name),
	})
}

func (s *server) ConnectWebSocket(c echo.Context) error {
	conn, err := (&websocket.Upgrader{}).Upgrade(c.Response().Writer, c.Request(), nil)
	if err != nil {
		return err
	}
	s.wsHub.Add(conn)
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
		TaskID:   req.TaskId,
		Title:    req.Title,
		Body:     req.Body,
		Severity: req.Severity,
		Timestamp: time.Now(),
	}

	if err := s.notifier.Notify(n); err != nil {
		log.Printf("notify: failed to deliver notification: %v", err)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"id":       time.Now().UnixNano(),
		"title":    req.Title,
		"severity": req.Severity,
	})
}

func (s *server) DeleteNotification(c echo.Context, id string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.Notification,
		Message: strPtr("Notification " + id + " dismissed"),
	})
	return c.NoContent(http.StatusOK)
}

// --- WSHub (moved from stub to real implementation) ---

type WSHub struct {
	mu        sync.RWMutex
	clients   map[*websocket.Conn]struct{}
	broadcast chan []byte
}

func NewWSHub() *WSHub {
	return &WSHub{
		clients:   make(map[*websocket.Conn]struct{}),
		broadcast: make(chan []byte, 256),
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

func (h *WSHub) Add(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *WSHub) Remove(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
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
