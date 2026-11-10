package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/protocol"
)

// WSMessageEvent represents a parsed WebSocket event message.
type WSMessageEvent struct {
	Event     string                 `json:"event"`
	Message   string                 `json:"message,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	TaskID    string                 `json:"task_id,omitempty"`
	Step      int                    `json:"step,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// startWebSocket connects to the server's WebSocket endpoint and streams events
// back via the model's wsMsg channel. It runs as a long-lived goroutine.
func startWebSocket(model *Model) tea.Cmd {
	return func() tea.Msg {
		client, err := getProtocolClient()
		if err != nil {
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("failed to create client: %v", err)}
		}

		params := &protocol.ConnectWebSocketParams{
			SessionId: model.sessionID,
		}

		resp, err := client.ConnectWebSocket(context.Background(), params)
		if err != nil {
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("WebSocket connection failed: %v", err)}
		}

		// Upgrade the HTTP response to a WebSocket connection.
		dialURL := resp.Request.URL.String()
		if strings.HasPrefix(dialURL, "http://") {
			dialURL = "ws://" + dialURL[len("http://"):]
		} else if strings.HasPrefix(dialURL, "https://") {
			dialURL = "wss://" + dialURL[len("https://"):]
		}

		// Strip WebSocket upgrade headers from the response before passing to Dialer.
		for _, key := range []string{"Sec-Websocket-Version", "Sec-Websocket-Accept", "Sec-Websocket-Extensions", "Sec-Websocket-Protocol"} {
			resp.Header.Del(key)
		}

		conn, _, err := websocket.DefaultDialer.Dial(dialURL, resp.Header)
		if err != nil {
			return WSMessageEvent{Event: "error", Message: fmt.Sprintf("WebSocket dial failed: %v", err)}
		}

		// Start a goroutine that continuously reads messages and sends them
		// back via the model's wsMsg channel. The connection stays open
		// until an error occurs.
		go func() {
			defer func() { _ = conn.Close() }()
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					model.wsMsg <- WSMessageEvent{Event: "connection_error", Message: fmt.Sprintf("Connection closed: %v", err)}
					return
				}

				var event map[string]interface{}
				if err := json.Unmarshal(msg, &event); err != nil {
					continue
				}

				wsEvt := WSMessageEvent{}
				if ev, ok := event["event"].(string); ok {
					wsEvt.Event = ev
				}
				if msg, ok := event["message"].(string); ok {
					wsEvt.Message = msg
				}
				if sid, ok := event["session_id"].(string); ok {
					wsEvt.SessionID = sid
				}
				if tid, ok := event["task_id"].(string); ok {
					wsEvt.TaskID = tid
				}
				if step, ok := event["step"].(float64); ok {
					wsEvt.Step = int(step)
				}

				// Non-blocking send to avoid goroutine blocking if channel is full.
				select {
				case model.wsMsg <- wsEvt:
				default:
					// Channel full — drop message to avoid blocking.
				}
			}
		}()

		return nil // No immediate message; connection is now streaming.
	}
}

// sendChatMessage sends a chat message to the current session via HTTP POST.
func sendChatMessage(sessionID, message string) {
	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
		return
	}

	body := protocol.SendMessageRequest{
		Message: message,
	}

	msgRsp, err := client.SendMessage(context.Background(), sessionID, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send message to session %s: %v\n", shortID(sessionID), err)
		return
	}

	parsedMsg, err := protocol.ParseSendMessageResponse(msgRsp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing message response: %v\n", err)
		return
	}

	result := parsedMsg.GetJSON200()
	if result != nil && result.Response != nil {
		fmt.Printf("\n[assistant] %s\n", *result.Response)
	}
}

// getProtocolClient returns a shared protocol client, lazily initialized.
func getProtocolClient() (*protocol.Client, error) {
	cfg, err := config.LoadDefault()
	if err != nil || cfg == nil || cfg.System == nil {
		def := config.EmbeddedDefaults()
		return protocol.NewClient(fmt.Sprintf("http://%s:%d", def.System.Host, def.System.ServerPort))
	}
	url := ""
	if cfg.System.ServerURL != "" {
		url = cfg.System.ServerURL
	} else {
		host := cfg.System.Host
		port := cfg.System.ServerPort
		if host == "" {
			host = config.EmbeddedDefaults().System.Host
		}
		if port == 0 {
			port = config.EmbeddedDefaults().System.ServerPort
		}
		url = fmt.Sprintf("http://%s:%d", host, port)
	}
	return protocol.NewClient(url)
}

// strVal returns the dereferenced value of s, or empty string if nil.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// shortID truncates a session ID for display.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
