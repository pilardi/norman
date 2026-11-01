// Package main implements the Norman Agent Server — a single long-running process
// exposing HTTP API + WebSocket events via gorilla/mux.
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/norman/norman/internal/api"
	"github.com/norman/norman/pkg/protocol"
)

// registerRoutes registers all HTTP API routes on the gorilla/mux router.
func registerRoutes(r *mux.Router, srv *server) {
	// Register auto-generated routes from OpenAPI spec
	api.HandlerWithOptions(srv, api.GorillaServerOptions{
		BaseURL: "",
		Middlewares: []api.MiddlewareFunc{
			func(h http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if srv.cfg.System.Logging.Verbose {
						deadline, _ := req.Context().Deadline()
						log.Printf("[http] %s %s - status=%d latency=%v",
							req.Method, req.URL.Path, 0, time.Since(deadline))
					}
					h.ServeHTTP(w, req)
				})
			},
		},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"%s"}`, err.Error())
		},
	})

	// Health check endpoint — includes scheduler status (Phase 4).
	r.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		taskCount, lastTick := srv.scheduler.Status()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","scheduler_tasks":%d,"last_tick":"%s"}`, taskCount, lastTick.Format(time.RFC3339))
	}).Methods(http.MethodGet)

	// Session archive endpoint with custom logic (POST /api/sessions/:id)
	r.HandleFunc("/api/sessions/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := mux.Vars(req)["id"]
		log.Printf("[DEBUG] ArchiveSessionByID route matched, id=%s", id)

		sessionData, err := srv.store.LoadSession(id)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"error":"session not found: %s"}`, id)
			return
		}

		sessions := []interface{}{sessionData}
		if err := srv.store.ArchiveSession(id, sessions); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error":"%s"}`, err.Error())
			return
		}

		srv.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.SessionCompressed,
			TaskId:  &id,
			Message: strPtr("Session archived for task " + id),
		})

		w.WriteHeader(http.StatusNoContent)
	}).Methods(http.MethodPost)

	// WebSocket hub for real-time push events
	r.HandleFunc("/ws", func(w http.ResponseWriter, req *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
		if err != nil {
			return
		}
		sessionID := req.URL.Query().Get("session_id")
		srv.wsHub.Add(conn, sessionID)
		defer srv.wsHub.Remove(conn)

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return // client disconnected
			}
		}
	}).Methods(http.MethodGet)
}
