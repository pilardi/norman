// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via Echo.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/norman/norman/internal/api"
	"github.com/norman/norman/pkg/protocol"
)

// stubHandler implements api.ServerInterface with no-op responses.
type stubHandler struct{}

func (s *stubHandler) ListSessions(c echo.Context) error          { return c.NoContent(http.StatusNotImplemented) }
func (s *stubHandler) CreateSession(c echo.Context) error         { return c.NoContent(http.StatusNotImplemented) }
func (s *stubHandler) GetSession(c echo.Context, id string) error { return c.NoContent(http.StatusNotImplemented) }
func (s *stubHandler) ListSkills(c echo.Context) error            { return c.NoContent(http.StatusNotImplemented) }
func (s *stubHandler) InstallSkill(c echo.Context) error          { return c.NoContent(http.StatusNotImplemented) }
func (s *stubHandler) RemoveSkill(c echo.Context, name string) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) ListTasks(c echo.Context, params protocol.ListTasksParams) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) CreateTask(c echo.Context) error { return c.NoContent(http.StatusNotImplemented) }
func (s *stubHandler) ScheduleTask(c echo.Context) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) ListScheduledTasks(c echo.Context) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) UnscheduleTask(c echo.Context, id string) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) CancelTask(c echo.Context, id string) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) GetTask(c echo.Context, id string) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) ReportTaskComplete(c echo.Context, id string) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) TailTaskLog(c echo.Context, id string, params protocol.TailTaskLogParams) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) ListWorkspaces(c echo.Context) error          { return c.NoContent(http.StatusNotImplemented) }
func (s *stubHandler) CreateWorkspace(c echo.Context) error         { return c.NoContent(http.StatusNotImplemented) }
func (s *stubHandler) DeleteWorkspace(c echo.Context, name string) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) GetWorkspace(c echo.Context, name string) error {
	return c.NoContent(http.StatusNotImplemented)
}
func (s *stubHandler) ConnectWebSocket(c echo.Context) error { return nil }

// WSHub manages WebSocket connections and broadcasts events to all clients.
type WSHub struct {
	mu        sync.RWMutex
	clients   map[*websocket.Conn]struct{}
	broadcast chan []byte
}

// NewWSHub creates a new WebSocket hub.
func NewWSHub() *WSHub {
	return &WSHub{
		clients:   make(map[*websocket.Conn]struct{}),
		broadcast: make(chan []byte, 256),
	}
}

// Run starts the hub's write loop in a goroutine.
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

// Broadcast sends a message to all connected WebSocket clients.
func (h *WSHub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	default:
		log.Printf("ws broadcast buffer full, dropping message")
	}
}

// Add registers a new client connection.
func (h *WSHub) Add(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

// Remove unregisters a client connection.
func (h *WSHub) Remove(c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// writeLoop drains the broadcast channel and sends messages to connected clients.
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

func main() {
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

	// WebSocket hub for real-time push events
	wsHub := NewWSHub()
	go wsHub.Run()

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

	// Register all generated API handlers (stub implementations for now)
	api.RegisterHandlers(e, &stubHandler{})

	log.Printf("Norman Agent Server starting on :%s", port)
	if err := e.Start(":" + port); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
		e.Logger.Info("shutting down...")
		e.Shutdown(context.Background())
	}()

	<-ctx.Done()
}
