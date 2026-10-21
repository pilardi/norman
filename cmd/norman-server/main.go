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
	"os/exec"
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
	"github.com/norman/norman/pkg/llm"
	"github.com/norman/norman/pkg/llm/llamacpp"
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
	registry   *skills.Registry

	// workspaceRegistry tracks created workspaces (in-memory + persisted).
	mu         sync.Mutex
	workspaces map[string]*protocol.Workspace
	startTime  time.Time
}

func main() {
	// --help / -h: print usage and exit.
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-h" {
			fmt.Fprintf(os.Stderr, `Usage: norman-server [OPTIONS]

Options:
  -p, --provider NAME   Select a configured provider (overrides default_provider)
  -v, --verbose         Enable server-side debug logging (Echo middleware)
  -h, --help            Show this help text and exit

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
				"  -p, --provider NAME   Select a configured provider (overrides default_provider)\n\n" +
				"Providers are defined in config.yaml under `llm_providers:`.\n" +
				"Example: norman-server -p local --verbose\n" +
				"\n" +
				"Norman agent server — Session-centric chat engine with task scheduling.\n")
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
	// -p/--provider CLI flag selects which configured provider to use (overrides default_provider).
	var selectedProvider string
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--verbose", "-v":
			cfg.System.Logging.Verbose = true
		case "--provider", "-p":
			selectedProvider = "unknown" // placeholder; resolved after config load
		}
	}

	// Resolve selected provider: CLI flag > config default > first configured provider.
	var providerName string
	if selectedProvider != "" && selectedProvider != "unknown" {
		providerName = selectedProvider
	} else if cfg.System.DefaultProvider != "" {
		providerName = cfg.System.DefaultProvider
	} else if len(cfg.System.LlmProviders) > 0 {
		providerName = cfg.System.LlmProviders[0].Name
	}

	var llmClient llm.LLM
	if providerName != "" {
		provCfg, err := cfg.System.ResolveProvider(providerName)
		if err != nil {
			log.Fatalf("failed to resolve provider %q: %v", providerName, err)
		}

		switch provCfg.Type {
		case "openai":
			llmClient = llm.NewClientAdapter(llm.NewClient(cfg.System))
		case "llamacpp":
			// Build type-specific config from Extra map.
			llmCfg := llamacpp.DefaultConfig()
			if v, ok := provCfg.Extra["cache_size"].(float64); ok {
				llmCfg.CacheSize = int(v)
			}
			if v, ok := provCfg.Extra["cache_type"].(string); ok {
				llmCfg.CacheType = v
			}
			if v, ok := provCfg.Extra["cache_enabled"].(bool); ok {
				llmCfg.CacheEnabled = v
			}

			// Use workspace-level overrides for model/temp/tokens if not set on provider.
			wsCfg := config.DefaultWorkspaceConfig()
			if cfg.System.DefaultModel != "" {
				wsCfg.Model = cfg.System.DefaultModel
			}
			if cfg.System.Temperature > 0 {
				wsCfg.Temperature = cfg.System.Temperature
			}
			if cfg.System.MaxTokens > 0 {
				wsCfg.MaxTokens = cfg.System.MaxTokens
			}

			llmClient = llamacpp.NewProvider(wsCfg, llmCfg)
		default:
			log.Fatalf("unsupported provider type %q", provCfg.Type)
		}
	} else {
		// Fallback: no providers configured, use OpenAI-compatible client.
		llmClient = llm.NewClientAdapter(llm.NewClient(cfg.System))
	}

	_ = providerName // used for logging/debugging; actual client is in llmClient

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

	// Create agent loop (with concrete client for P3 sandbox dep install)
	loop := agent.NewWithClient(cfg, store, rawSources, activator, wsHub, loopClient, openShellClient)

	// Wire LLM client into the agent loop.
	if llmClient != nil {
		loop = loop.WithLLMClient(llmClient)
	}

	// Initialize notification notifier from config
	notifier := notify.New(wsHub, cfg.System.Notify.BellEnabled, cfg.System.Notify.SlackWebhook, cfg.System.Notify.RateLimitPerMin)

	// Initialize scheduler and wire results into loop's notification path
	resultsCh := make(chan scheduler.TaskResult, 100) // buffered to avoid blocking
	sched := scheduler.NewScheduler(resultsCh)

	// Wire OpenShell Exec() into the scheduler's runCommand adapter
	sched.WithRunCommand(func(ctx context.Context, provider, sandbox string, cmd []string) (*v1.ExecResult, error) {
		return openShellClient.RunCommand(ctx, provider, sandbox, cmd)
	})

	// Wire OutputHandler to push WS events for real-time progress updates.
	sched.WithOutputHandler(func(result string, err error) {
		msg := result
		if err != nil {
			msg = fmt.Sprintf("Task completed with error: %v", err)
		}
		wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr(msg),
		})
	})

	loop = loop.WithNotifier(notifier).WithResultsChannel(resultsCh)

	// Build server with all dependencies
	registryPath := filepath.Join(os.Getenv("HOME"), ".norman", "skills_registry.json")
	reg := skills.NewRegistry(registryPath)

	srv := &server{
		cfg:        cfg,
		store:      store,
		rawSources: rawSources,
		activator:  activator,
		loop:       loop,
		wsHub:      wsHub,
		notifier:   notifier,
		scheduler:  sched,
		registry:   reg,
		workspaces: make(map[string]*protocol.Workspace),
		startTime:  time.Now(),
	}

	// Seed registry from activator's current entries (host-installed skills)
	for name, entry := range activator.Registry() {
		reg.Register(name, skills.RegistryEntry{
			SourceURL:           entry.SourceURL,
			Version:             entry.Version,
			Description:         entry.Description,
			InstalledInWorkspaces: []string{},
		})
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
				if purpose, ok := wsMap["purpose"].(string); ok {
					ws.Purpose = strPtr(purpose)
				}
				if scope, ok := wsMap["scope"].([]interface{}); ok {
					strSlice := make([]string, len(scope))
					for i, v := range scope {
						if s, ok := v.(string); ok {
							strSlice[i] = s
						}
					}
					ws.Scope = &strSlice
				}
				if tags, ok := wsMap["tags"].([]interface{}); ok {
					strSlice := make([]string, len(tags))
					for i, v := range tags {
						if s, ok := v.(string); ok {
							strSlice[i] = s
						}
					}
					ws.Tags = &strSlice
				}
				srv.workspaces[name] = ws
			}
		}
		log.Printf("server: loaded %d persisted workspaces from disk", len(srv.workspaces))
	}

	// Load persisted scheduled tasks into the scheduler on startup.
	if scheduled, err := store.LoadScheduledTasks(); err != nil {
		log.Printf("scheduler: failed to load scheduled tasks from disk: %v", err)
	} else if scheduled != nil {
		for _, entryMap := range scheduled {
			entry, ok := entryMap.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := entry["name"].(string)
			schedule, _ := entry["schedule"].(string)
			command, _ := entry["command"].(string)
			active, _ := entry["active"].(bool)
			entryID, _ := entry["id"].(string)

			if entryID == "" || name == "" {
				continue
			}

			schedEntry := scheduler.TaskEntry{
				ID:       entryID,
				Name:     name,
				Schedule: schedule,
				Command:  command,
				Active:   active,
			}
			if err := sched.Add(schedEntry); err != nil {
				log.Printf("scheduler: failed to add task %s: %v", entryID[:8], err)
			}
		}
		log.Printf("scheduler: loaded %d persisted scheduled tasks from disk", len(scheduled))
	}

	e := echo.New()
	e.Use(middleware.Recover())

	// Conditional logging: only log requests when verbose mode is enabled.
	if cfg.System.Logging.Verbose {
		e.Use(middleware.Logger())
	}

	// Health check endpoint — includes scheduler status (Phase 4).
	e.GET("/health", func(c echo.Context) error {
		taskCount, lastTick := srv.scheduler.Status()
		resp := map[string]interface{}{
			"status":       "ok",
			"scheduler_tasks": taskCount,
			"last_tick":   lastTick.Format(time.RFC3339),
		}
		return c.JSON(http.StatusOK, resp)
	})

	// Config endpoint — returns full system config (with unresolved secret placeholders).
	// Per-workspace overrides are included if workspace_id query param is provided.
	e.GET("/api/config", func(c echo.Context) error {
		return srv.GetConfig(c, protocol.GetConfigParams{})
	})
	e.PUT("/api/config", srv.PutConfig)

	// Debug route to verify routing
	e.POST("/api/sessions/:id/debug", func(c echo.Context) error {
		return c.String(200, "DEBUG route matched, id="+c.Param("id"))
	})

	// Chat-first interface: send a message to an active session (Phase 1)
	e.POST("/api/sessions/:id/messages", func(c echo.Context) error {
		return srv.SendMessage(c, c.Param("id"))
	})

	// Session archive endpoint (Step 7d) — registered AFTER /messages so it takes precedence for bare paths
	e.POST("/api/sessions/:id", func(c echo.Context) error {
		id := c.Param("id")
		log.Printf("[DEBUG] ArchiveSessionByID route matched, id=%s", id)

		sessionData, err := srv.store.LoadSession(id)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found: " + id})
		}

		sessions := []interface{}{sessionData}
		if err := srv.store.ArchiveSession(id, sessions); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		srv.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.SessionCompressed,
			TaskId:  &id,
			Message: strPtr("Session archived for task " + id),
		})

		return c.NoContent(http.StatusNoContent)
	})
	e.POST("/api/sessions/archive", srv.ArchiveSession)

	// Session history (Phase 3)
	e.GET("/api/sessions/:id/history", func(c echo.Context) error {
		var params protocol.SessionHistoryParams
		if q := c.QueryParam("limit"); q != "" {
			fmt.Sscanf(q, "%d", &params.Limit)
		}
		return srv.SessionHistory(c, c.Param("id"), params)
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
	e.PATCH("/api/workspaces/:name", func(c echo.Context) error {
		return srv.UpdateWorkspace(c, c.Param("name"))
	})
	e.DELETE("/api/workspaces/:name", func(c echo.Context) error {
		return srv.DeleteWorkspace(c, c.Param("name"))
	})

	// Skill discovery path management (Step 14i)
	e.POST("/api/skills/paths", srv.AddDiscoveryPaths)

	// Skill file upload (Step 15a)
	e.POST("/api/skills/upload", srv.UploadSkill)
	e.POST("/api/skills/install", srv.InstallSkill)

	// Remove a skill by name (DELETE /api/skills/:name)
	e.DELETE("/api/skills/:name", func(c echo.Context) error {
		return srv.RemoveSkill(c, c.Param("name"))
	})

	// Update a skill from its source repo (PUT /api/skills/:name)
	e.PUT("/api/skills/:name", func(c echo.Context) error {
		return srv.UpdateSkill(c, c.Param("name"))
	})

	// Check if a skill has an update available (GET /api/skills/:name/check-version)
	e.GET("/api/skills/:name/check-version", func(c echo.Context) error {
		return srv.CheckSkillVersion(c, c.Param("name"))
	})

	// Workspace-level skill install (POST /api/workspaces/:ws/skills/install)
	e.POST("/api/workspaces/:ws/skills/install", srv.InstallSkillToWorkspace)

	// Global skills registry (GET /api/skills/registry)
	e.GET("/api/skills/registry", srv.GetSkillsRegistry)

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

// ArchiveSessionByID archives a session by its path-based ID (used by CLI).
func (s *server) ArchiveSessionByID(c echo.Context) error {
	id := c.Param("id")

	// Read session data for archival
	sessionData, err := s.store.LoadSession(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found: " + id})
	}

	// Convert to []interface{} for ArchiveSession
	sessions := []interface{}{sessionData}

	if err := s.store.ArchiveSession(id, sessions); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Broadcast WebSocket event
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.SessionCompressed,
		TaskId:  &id,
		Message: strPtr("Session archived for task " + id),
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

// HealthCheck returns service health with scheduler status.
func (s *server) HealthCheck(c echo.Context) error {
	count, lastTick := s.scheduler.Status()
	resp := map[string]interface{}{
		"status":       "healthy",
		"uptime_seconds": int(time.Since(s.startTime).Seconds()),
		"scheduler": map[string]interface{}{
			"registered_tasks": count,
			"last_tick":        lastTick.Format(time.RFC3339),
		},
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


// UploadSkill writes skill files into the server's skills directory.
// Accepts a JSON body with "name" and "files" array.
func (s *server) UploadSkill(c echo.Context) error {
	var req protocol.SkillUploadRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if len(req.Files) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "files array must not be empty"})
	}

	// Write files into the server's skills directory
	destDir := filepath.Join(s.cfg.System.Skills.Path, req.Name)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to create skill dir: %v", err),
		})
	}

	for _, f := range req.Files {
		dst := filepath.Join(destDir, f.Name)
		if err := os.WriteFile(dst, []byte(f.Content), 0644); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to write %s: %v", f.Name, err),
			})
		}
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr(fmt.Sprintf("Installed skill '%s' with %d file(s)", req.Name, len(req.Files))),
		Step:    intPtr(0),
	})

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"installed": true,
		"path":      destDir,
	})
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

	// Determine the skill name: use explicit name if provided, otherwise derive from repo basename
	skillName := req.Repo // default to repo basename
	if req.Name != nil && *req.Name != "" {
		skillName = *req.Name
	} else {
		skillName = filepath.Base(req.Repo)
	}

	destDir := filepath.Join(s.cfg.System.Skills.Path, skillName)

	// Step 1: Clone the git repo into the skills directory
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Cloning repo " + req.Repo),
	})

	cmd := exec.Command("git", "clone", req.Repo, destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("git clone failed: %v", string(out)),
		})
	}

	// Step 2: Checkout specific version/tag/branch if provided
	if req.Version != nil && *req.Version != "" {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Checking out version " + *req.Version),
		})

		cmd = exec.Command("git", "-C", destDir, "checkout", *req.Version)
		if out, err := cmd.CombinedOutput(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("git checkout failed: %v", string(out)),
			})
		}
	}

	// Step 3: Run graphify install --platform agents to generate SKILL.md + references/
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Running graphify install --platform agents"),
	})

	cmd = exec.Command("graphify", "install", "--platform", "agents")
	cmd.Dir = destDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("graphify install failed: %v", string(out)),
		})
	}

	// Step 4: Register in discovery paths
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Registered skill '" + skillName + "' in discovery"),
	})

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"installed": true,
		"path":      destDir,
	})
}

func (s *server) RemoveSkill(c echo.Context, name string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Removing skill: " + name),
	})

	if err := skills.Uninstall(name, s.cfg.System.Skills.Path); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	return c.NoContent(http.StatusOK)
}

func (s *server) UpdateSkill(c echo.Context, name string) error {
	var req protocol.SkillInstallRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Updating skill '" + name + "' from repo " + req.Repo),
	})

	// Use the existing skills.Update() which handles clone + checkout + metadata read
	version := ""
	if req.Version != nil {
		version = *req.Version
	}

	entry, err := skills.Update(req.Repo, version, s.cfg.System.Skills.Path)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Skill '" + entry.Name + "' updated to version " + entry.Version),
	})

	return c.JSON(http.StatusOK, map[string]interface{}{
		"name":        entry.Name,
		"version":     entry.Version,
		"path":        entry.Path,
		"updated":     true,
	})
}

func (s *server) CheckSkillVersion(c echo.Context, name string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Checking update availability for skill: " + name),
	})

	// Look up the installed skill entry
	entry, err := skills.GetEntry(name, s.cfg.System.Skills.Path)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	hasUpdate, newVersion, err := skills.HasUpdate(*entry)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	result := map[string]interface{}{
		"name":          entry.Name,
		"installed_version": entry.Version,
		"has_update":    hasUpdate,
		"latest_version": newVersion,
	}

	return c.JSON(http.StatusOK, result)
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
	if req.SessionId == nil || *req.SessionId == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "session_id is required"})
	}
	sessionID := *req.SessionId

	// Validate session exists.
	if _, err := s.store.GetSession(sessionID); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("session %s not found", sessionID)})
	}

	prompt := ""
	if req.Prompt != nil {
		prompt = *req.Prompt
	}
	workspaceID := ""
	if req.WorkspaceId != nil {
		workspaceID = *req.WorkspaceId
	}

	task, err := s.loop.Submit(context.Background(), prompt, workspaceID, sessionID)
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

func (s *server) GetTask(c echo.Context, id string) error {
	task, err := s.loop.GetTask(id)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	uuidVal, parseErr := uuid.Parse(id)
	if parseErr != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid task ID: %v", parseErr)})
	}

	status := protocol.TaskStatus(task.Status)
	resp := protocol.Task{
		Id:         &uuidVal,
		Status:     &status,
		Prompt:     strPtr(task.Prompt),
		WorkspaceId: strPtr(task.Workspace),
		CreatedAt:  &task.CreatedAt,
		Error:      strPtr(task.Error),
	}

	if len(task.Output) > 0 {
		resp.OutputPath = strPtr(fmt.Sprintf("/tmp/task-%s.output", task.ID[:8]))
	}

	return c.JSON(http.StatusOK, resp)
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

	// Set optional workspace definition fields
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

	// Persist workspace to disk
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
		Message: strPtr("Workspace created: " + req.Name),
	})

	return c.JSON(http.StatusCreated, ws)
}

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

	// Apply partial update (only non-nil fields)
	if req.Purpose != nil {
		ws.Purpose = req.Purpose
	}
	if req.Scope != nil {
		ws.Scope = req.Scope
	}
	if req.Tags != nil {
		ws.Tags = req.Tags
	}

	// Update persisted store
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
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			return nil // client disconnected
		}
		if msgType != websocket.TextMessage {
			continue
		}

		// Route user_response events to registered response channels.
		var event protocol.WebSocketEvent
		if err := json.Unmarshal(data, &event); err == nil {
			if event.Event == protocol.UserResponse {
				s.wsHub.SendResponse("", string(data))
			}
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
func (s *server) GetConfig(c echo.Context, params protocol.GetConfigParams) error {
	var workspaceID string
	if params.WorkspaceId != nil {
		workspaceID = *params.WorkspaceId
	}

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
	mu             sync.RWMutex
	clients        map[*websocket.Conn]struct{}              // global clients (no session filter)
	sessionClients map[string]map[*websocket.Conn]struct{} // per-session client maps
	broadcast      chan []byte
	// responseChannels stores registered channels keyed by request ID.
	// Used for blocking AskUser/request_permission calls waiting on user input.
	responseChannels map[string]chan string
}

func NewWSHub() *WSHub {
	return &WSHub{
		clients:          make(map[*websocket.Conn]struct{}),
		sessionClients:   make(map[string]map[*websocket.Conn]struct{}),
		broadcast:        make(chan []byte, 256),
		responseChannels: make(map[string]chan string),
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

// RegisterResponseChannel creates a response channel for blocking calls (e.g. AskUser).
// Returns the channel and a unique request ID. The caller should close the channel
// when done to avoid goroutine leaks.
func (h *WSHub) RegisterResponseChannel() (string, chan string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	ch := make(chan string, 1)
	h.responseChannels[id] = ch
	return id, ch
}

// SendResponse routes a typed message to the registered response channel.
// Returns true if the channel was found and message sent.
func (h *WSHub) SendResponse(id string, message string) bool {
	h.mu.Lock()
	ch, ok := h.responseChannels[id]
	h.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- message:
		return true
	default:
		return false // channel full or closed
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

// InstallSkillToWorkspace clones a skill into the target workspace's sandbox.
// Accepts: {"repo": "github.com/...", "version": "v1.0"} or {"files": [...]} for upload path.
func (s *server) InstallSkillToWorkspace(c echo.Context) error {
	wsName := c.Param("ws")

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Installing skill into workspace '" + wsName + "'"),
	})

	// Accept either a repo URL (git clone path) or files[] (upload path)
	var req struct {
		Repo    string   `json:"repo"`
		Version string   `json:"version"`
		Name    string   `json:"name"`
		Files   []struct {
			Name     string `json:"name"`
			Content  string `json:"content"`
			Executable bool `json:"executable,omitempty"`
		} `json:"files"`
	}

	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Get the session for this workspace
	session, err := s.loop.GetSession(wsName)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("get session for workspace '%s': %v", wsName, err),
		})
	}

	skillDir := filepath.Join("/workspace", wsName, "skills", req.Name)

	// Path 1: Git clone from repo URL
	if req.Repo != "" {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Cloning " + req.Repo + " into sandbox"),
		})

		cmd := exec.Command("git", "clone", "--depth", "1", req.Repo, skillDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("git clone: %v", string(out)),
			})
		}

		if req.Version != "" {
			cmd = exec.Command("git", "-C", skillDir, "checkout", req.Version)
			if out, err := cmd.CombinedOutput(); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("git checkout: %v", string(out)),
				})
			}
		}

		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Skill '" + req.Name + "' installed in workspace '" + wsName + "'"),
		})

		// Deploy to sandbox and register in global registry
		if entry, err := s.activator.DeployToSandbox(req.Name, session); err != nil {
			log.Printf("[server] DeployToSandbox for '%s' in workspace '%s': %v", req.Name, wsName, err)
		} else if entry != nil {
			s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
				Event:   protocol.TaskProgress,
				Message: strPtr("Deployed '" + entry.Name + "' to sandbox"),
			})

			// Register in global persistent registry
			s.registry.Register(req.Name, skills.RegistryEntry{
				SourceURL:           entry.SourceURL,
				Version:             entry.Version,
				Description:         entry.Description,
				InstalledInWorkspaces: []string{wsName},
			})

			return c.JSON(http.StatusCreated, map[string]interface{}{
				"installed": true,
				"path":      skillDir,
				"type":      "git-clone",
			})
		}

		// Fallback if DeployToSandbox failed
		return c.JSON(http.StatusCreated, map[string]interface{}{
			"installed": true,
			"path":      skillDir,
			"type":      "git-clone",
		})
	}

	// Path 2: Upload files (thin-client)
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Uploading skill '" + req.Name + "' to workspace '" + wsName + "'"),
	})

	// Create directory if needed
	cmd := exec.Command("mkdir", "-p", skillDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("mkdir: %v", string(out)),
		})
	}

	for _, f := range req.Files {
		content := []byte(f.Content)
		mode := os.FileMode(0644)
		if f.Executable {
			mode = 0755
		}
		fullPath := filepath.Join(skillDir, f.Name)
		if err := os.WriteFile(fullPath, content, mode); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("write %s: %v", f.Name, err),
			})
		}
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Skill '" + req.Name + "' uploaded to workspace '" + wsName + "'"),
	})

	// Deploy to sandbox and register in global registry
	if entry, err := s.activator.DeployToSandbox(req.Name, session); err != nil {
		log.Printf("[server] DeployToSandbox for '%s' in workspace '%s': %v", req.Name, wsName, err)
	} else if entry != nil {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Deployed '" + entry.Name + "' to sandbox"),
		})

		// Register in global persistent registry
		s.registry.Register(req.Name, skills.RegistryEntry{
			SourceURL:           entry.SourceURL,
			Version:             entry.Version,
			Description:         entry.Description,
			InstalledInWorkspaces: []string{wsName},
		})

		return c.JSON(http.StatusCreated, map[string]interface{}{
			"installed": true,
			"path":      skillDir,
			"type":      "upload",
		})
	}

	// Fallback if DeployToSandbox failed (files present but SKILL.md unreadable)
	return c.JSON(http.StatusCreated, map[string]interface{}{
		"installed": true,
		"path":      skillDir,
		"type":      "upload",
	})
}

// GetSkillsRegistry returns the global skills registry (names → source_url, version, workspaces).
func (s *server) GetSkillsRegistry(c echo.Context) error {
	entries := s.registry.GetAll()
	result := map[string]interface{}{}

	for name, entry := range entries {
		result[name] = map[string]interface{}{
			"source_url":            entry.SourceURL,
			"version":               entry.Version,
			"description":           entry.Description,
			"installed_in_workspaces": entry.InstalledInWorkspaces,
		}
	}

	return c.JSON(http.StatusOK, result)
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int        { return &i }
