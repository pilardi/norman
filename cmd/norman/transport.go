package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/norman/norman/pkg/client"
	"github.com/norman/norman/pkg/protocol"
)

// runWebSocketLoop connects to the WebSocket endpoint and displays incoming events.
// It runs concurrently with the REPL input loop.
func runWebSocketLoop(sessionID string) {
	c, err := client.GetProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create client: %v\n", err)
		return
	}

	params := &protocol.ConnectWebSocketParams{
		SessionId: sessionID,
	}

	resp, err := c.ConnectWebSocket(context.Background(), params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WebSocket connection failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	// Upgrade the HTTP response to a WebSocket connection.
	// Convert http:// → ws:// and https:// → wss:// for the dialer.
	dialURL := resp.Request.URL.String()
	if strings.HasPrefix(dialURL, "http://") {
		dialURL = "ws://" + dialURL[len("http://"):]
	} else if strings.HasPrefix(dialURL, "https://") {
		dialURL = "wss://" + dialURL[len("https://"):]
	}

	// Strip WebSocket upgrade headers from the response before passing to Dialer.
	// The dialer adds its own Sec-Websocket-Version etc., so we must remove any
	// existing ones to avoid "duplicate header not allowed" errors.
	for _, key := range []string{"Sec-Websocket-Version", "Sec-Websocket-Accept", "Sec-Websocket-Extensions", "Sec-Websocket-Protocol"} {
		resp.Header.Del(key)
	}

	conn, _, err := websocket.DefaultDialer.Dial(dialURL, resp.Header)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WebSocket dial failed: %v\n", err)
		return
	}
	defer func() { _ = conn.Close() }()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n[ws] Connection closed: %v\n", err)
			return
		}

		var event map[string]interface{}
		if err := json.Unmarshal(msg, &event); err != nil {
			fmt.Fprintf(os.Stderr, "[ws] Bad JSON: %v\n", err)
			continue
		}

		displayWSMessage(event)
	}
}

// displayWSMessage formats and prints a WebSocket event for the user.
func displayWSMessage(event map[string]interface{}) {
	eventType, _ := event["event"].(string)
	message, _ := event["message"].(string)
	sessionID, _ := event["session_id"].(string)
	taskID, _ := event["task_id"].(string)
	step, _ := event["step"].(float64)
	threshold, _ := event["threshold"].(string)

	prefix := fmt.Sprintf("[ws] %s", eventType)
	if sessionID != "" {
		prefix += " @" + sessionID[:8]
	}

	switch eventType {
	case "task_progress":
		fmt.Printf("%s  step=%d task=%s\n", prefix, int(step), taskID)
	case "session_compressed":
		fmt.Printf("%s  budget threshold: %s\n", prefix, threshold)
	case "notification":
		fmt.Printf("%s  %s\n", prefix, message)
	default:
		data, _ := json.MarshalIndent(event, "", "  ")
		fmt.Printf("%s\n%s\n", prefix, string(data))
	}
}
