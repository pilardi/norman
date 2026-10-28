// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via Echo.
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/norman/norman/internal/api"
	"github.com/norman/norman/pkg/protocol"
)

// registerRoutes registers all HTTP API routes on the Echo router.
func registerRoutes(e *echo.Echo, srv *server) {
	e.Use(middleware.Recover())

	// Conditional logging: only log requests when verbose mode is enabled.
	if srv.cfg.System.Logging.Verbose {
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
	e.GET("/api/workspaces", func(c echo.Context) error {
		var params protocol.ListWorkspacesParams
		if q := c.QueryParam("status"); q != "" {
			status := protocol.ListWorkspacesParamsStatus(q)
			params.Status = &status
		}
		return srv.ListWorkspaces(c, params)
	})
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

	// Workspace lifecycle transitions (P1)
	e.POST("/api/workspaces/:name/stop", func(c echo.Context) error {
		return srv.StopWorkspace(c, c.Param("name"))
	})
	e.POST("/api/workspaces/:name/restore", func(c echo.Context) error {
		return srv.RestoreWorkspace(c, c.Param("name"))
	})
	e.POST("/api/workspaces/:name/archive", func(c echo.Context) error {
		return srv.ArchiveWorkspace(c, c.Param("name"))
	})
	e.DELETE("/api/workspaces/:name/purge", func(c echo.Context) error {
		var req protocol.PurgeRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
		if !req.Confirm {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "confirm must be true to purge"})
		}
		return srv.PurgeWorkspace(c, c.Param("name"))
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
		srv.wsHub.Add(conn, sessionID)
		defer srv.wsHub.Remove(conn)

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return nil // client disconnected
			}
		}
	})

	// Register all API handlers (real implementations now)
	api.RegisterHandlers(e, srv)
}
