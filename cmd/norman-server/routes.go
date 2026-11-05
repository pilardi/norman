// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via Echo.
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/norman/norman/pkg/protocol"
)

// registerRoutes registers all HTTP API routes on the Echo router.
func registerRoutes(e *echo.Echo, srv *server) {
	e.Use(middleware.Recover())

	// Conditional logging: only log requests when verbose mode is enabled.
	if srv.cfg.System.Logging.Verbose {
		e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
			LogStatus:  true,
			LogLatency: true,
			LogMethod:  true,
			LogURI:     true,
			LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
				statusText := http.StatusText(v.Status)
				msg := fmt.Sprintf("[http] %s %s - status=%d (%s) latency=%v",
					v.Method,
					v.URI,
					v.Status,
					statusText,
					v.Latency,
				)
				log.Printf("%s", msg)
				return nil
			},
		}))
	}

	// --- Custom routes not covered by auto-generated handlers ---

	// Debug route to verify routing
	e.POST("/api/sessions/:id/debug", func(c echo.Context) error {
		return c.String(200, "DEBUG route matched, id="+c.Param("id"))
	})

	// Global skills registry (GET /api/skills/registry)
	e.GET("/api/skills/registry", srv.GetSkillsRegistry)

	// Workspace-level skill install (POST /api/workspaces/:ws/skills/install)
	e.POST("/api/workspaces/:ws/skills/install", srv.InstallSkillToWorkspace)

	// --- Register all auto-generated API handlers (echo-based), including /ws. ---
	protocol.RegisterHandlers(e, srv)

	// Override the auto-generated /ws GET handler: Echo's default writes "OK"
	// after WebSocket upgrade already wrote headers, causing a superfluous
	// WriteHeader warning. Set Committed before and after Upgrade() to
	// prevent the GET template from writing a body.
	e.GET("/ws", func(c echo.Context) error {
		// Prevent Echo's GET template from writing "OK" body — Upgrade()
		// writes 101 headers directly to the underlying http.ResponseWriter,
		// bypassing Echo's Response object. Set Committed early and after.
		c.Response().Committed = true
		err := srv.ConnectWebSocket(c, protocol.ConnectWebSocketParams{SessionId: c.QueryParam("session_id")})
		if err != nil {
			return err
		}
		// Re-confirm committed — Upgrade() may have bypassed our flag.
		c.Response().Committed = true
		return nil
	})
}
