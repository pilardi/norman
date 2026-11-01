package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/norman/norman/pkg/protocol"
)

// WSHub manages WebSocket connections and broadcasts events to clients.
type WSHub struct {
	mu             sync.RWMutex
	clients        map[*websocket.Conn]struct{}            // global clients (no session filter)
	sessionClients map[string]map[*websocket.Conn]struct{} // per-session client maps
	broadcast      chan []byte
	// responseChannels stores registered channels keyed by request ID.
	// Used for blocking AskUser/request_permission calls waiting on user input.
	responseChannels map[string]chan string
}

// NewWSHub creates a new WebSocket hub.
func NewWSHub() *WSHub {
	return &WSHub{
		clients:          make(map[*websocket.Conn]struct{}),
		sessionClients:   make(map[string]map[*websocket.Conn]struct{}),
		broadcast:        make(chan []byte, 256),
		responseChannels: make(map[string]chan string),
	}
}

// Run starts the hub's write loop.
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

// Broadcast sends a message to all connected clients.
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

// BroadcastMessage routes a plain text message to all registered response channels.
// This is used by the WS client-side handler to deliver user_response messages
// to blocking callers like AskUserBlocking.
func (h *WSHub) BroadcastMessage(msg string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range h.responseChannels {
		select {
		case ch <- msg:
		default:
			// channel full or closed — skip
		}
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

// ConnectWebSocket handles WebSocket connections for real-time push events.
func (s *server) ConnectWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// Extract optional session_id from query params for per-session filtering
	sessionID := r.URL.Query().Get("session_id")
	s.wsHub.Add(conn, sessionID)
	defer s.wsHub.Remove(conn)

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			return // client disconnected
		}
	}
}
