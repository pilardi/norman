package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// wsURL converts an HTTP server URL to its WebSocket equivalent (http→ws, https→wss).
func wsURL() string {
	base := serverURL()
	ws := strings.Replace(base, "http://", "ws://", 1)
	ws = strings.Replace(ws, "https://", "wss://", 1)
	return ws
}

// runWebSocketLoop connects to the WebSocket endpoint and displays incoming events.
// It runs concurrently with the REPL input loop.
func runWebSocketLoop(sessionID string) {
	wsURL := wsURL() + "/ws?session_id=" + url.PathEscape(sessionID)
	fmt.Printf("Connecting to real-time events at %s ...\n", wsURL)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WebSocket connection failed: %v\n", err)
		return
	}
	defer conn.Close()

	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				fmt.Fprintf(os.Stderr, "WebSocket read error: %v\n", err)
				return
			}
		}
	}()

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

// parseSlashCommand checks if a line is a slash-command and returns the command + args.
// Returns (command, args, isSlash) — only slash-commands trigger local actions.
func parseSlashCommand(line string) (string, []string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return "", nil, false
	}
	parts := strings.Fields(line[1:]) // strip leading '/' and split
	if len(parts) == 0 {
		return "", nil, false
	}
	return parts[0], parts[1:], true
}

// handleChat implements the interactive REPL chat mode.
// Usage: norman sessions open <workspace_name> [<session_id>]
func handleChat(args []string) {
	workspaceName := ""
	sessionID := ""

	if len(args) == 0 {
		printChatHelp()
		os.Exit(1)
	}

	// Parse args: first is workspace name, second optional is session ID.
	for i := 0; i < len(args); i++ {
		if args[i] == "-h" || args[i] == "--help" {
			printChatHelp()
			return
		} else if workspaceName == "" {
			workspaceName = args[i]
		} else if sessionID == "" {
			var found bool
			sessionID, found = resolveSessionID(args[i])
			if !found {
				fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", args[i])
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Too many arguments. Usage: norman sessions open <workspace_name> [<session_id>]\n")
			printChatHelp()
			os.Exit(1)
		}
	}

	if workspaceName == "" {
		fmt.Fprintln(os.Stderr, "Error: workspace name is required")
		printChatHelp()
		os.Exit(1)
	}

	// If only workspace provided (no session ID), resolve/create one.
	if workspaceName != "" && sessionID == "" {
		fmt.Printf("Resolving workspace '%s'...\n", workspaceName)
		workspacesBody, err := httpGet("/api/workspaces")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching workspaces: %v\n", err)
			os.Exit(1)
		}
		var workspaces []map[string]interface{}
		if err := json.Unmarshal(workspacesBody, &workspaces); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing workspaces response: %v\n", err)
			os.Exit(1)
		}
		foundID := ""
		for _, w := range workspaces {
			name, _ := w["name"].(string)
			if name == workspaceName {
				idStr, _ := w["id"].(string)
				foundID = idStr
				break
			}
		}
		if foundID == "" {
			fmt.Fprintf(os.Stderr, "Workspace '%s' not found. Run 'norman workspaces list'.\n", workspaceName)
			os.Exit(1)
		}

		// Check if an active session already exists for this workspace.
		sessionsBody, err := httpGet("/api/sessions")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching sessions: %v\n", err)
			os.Exit(1)
		}
		var sessions []map[string]interface{}
		if err := json.Unmarshal(sessionsBody, &sessions); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing sessions response: %v\n", err)
			os.Exit(1)
		}
		for _, s := range sessions {
			sid, _ := s["id"].(string)
			status, _ := s["status"].(string)
			if status == "active" {
				sessionID = sid
				fmt.Printf("Reusing existing active session '%s' for workspace '%s'\n", sid, workspaceName)
				break
			}
		}

		// No active session — create one.
		if sessionID == "" {
			fmt.Printf("Creating new session for workspace '%s'...\n", workspaceName)
			sessBody := map[string]string{"workspace_id": foundID, "client_type": "cli"}
			sessJSON, _ := json.Marshal(sessBody)
			sessReq, _ := http.NewRequestWithContext(context.Background(), "POST", serverURL()+"/api/sessions", bytes.NewReader(sessJSON))
			sessReq.Header.Set("Content-Type", "application/json")
			sessResp, err := (&http.Client{Timeout: 10 * time.Second}).Do(sessReq)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to create session: %v\n", err)
				os.Exit(1)
			}
			defer sessResp.Body.Close()
			if sessResp.StatusCode >= 400 {
				bodyBytes, _ := io.ReadAll(sessResp.Body)
				fmt.Fprintf(os.Stderr, "Session create failed (HTTP %d): %s\n", sessResp.StatusCode, string(bodyBytes))
				os.Exit(1)
			}
			var sess map[string]interface{}
			if err := json.NewDecoder(sessResp.Body).Decode(&sess); err != nil {
				fmt.Fprintf(os.Stderr, "Error parsing session response: %v\n", err)
				os.Exit(1)
			}
			sessionID = sess["id"].(string)
			fmt.Printf("Created session '%s' for workspace '%s'\n", sessionID, workspaceName)
		}
	}

	// Start real-time WebSocket event push loop (runs concurrently).
	go runWebSocketLoop(sessionID)

	// Enter REPL loop.
	fmt.Println("\n=== Norman Chat Session ===")
	fmt.Printf("Session: %s | Workspace resolved\n", sessionID)
	fmt.Println("Type a message or use slash-commands (/help, /tasks, /close, /new, /history [N], /sessions, /switch <id>, /archive <id>)")

	// Track turns for server-side transcript persistence.
	var turns []map[string]interface{}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		cmd, cmdArgs, isSlash := parseSlashCommand(line)

		if isSlash {
			switch cmd {
			case "help":
				printChatHelp()
			case "list", "tasks":
				listEmergentTasks(sessionID)
			case "close":
				fmt.Println("Closing session...")
				return
			case "new":
				// Start a new session (auto-create).
				fmt.Printf("Creating new session...\n")
				newSessBody := map[string]string{"client_type": "cli"}
				newSessJSON, _ := json.Marshal(newSessBody)
				newSessReq, _ := http.NewRequestWithContext(context.Background(), "POST", serverURL()+"/api/sessions", bytes.NewReader(newSessJSON))
				newSessReq.Header.Set("Content-Type", "application/json")
				newSessResp, err := (&http.Client{Timeout: 10 * time.Second}).Do(newSessReq)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to create new session: %v\n", err)
					continue
				}
				defer newSessResp.Body.Close()
				var newSess map[string]interface{}
				if err := json.NewDecoder(newSessResp.Body).Decode(&newSess); err != nil {
					fmt.Fprintf(os.Stderr, "Error parsing new session response: %v\n", err)
					continue
				}
				sessionID = newSess["id"].(string)
				fmt.Printf("New session '%s' created.\n", sessionID)
			case "history":
				showHistory(sessionID, cmdArgs)
			case "sessions":
				listSessions()
			case "switch":
				if len(cmdArgs) == 0 {
					fmt.Fprintln(os.Stderr, "Usage: /switch <session-id>")
					continue
				}
				sessionID = cmdArgs[0]
				fmt.Printf("Switched to session '%s'\n", sessionID[:8])
			case "archive":
				id := cmdArgs[0]
				if len(cmdArgs) == 0 {
					// Auto-archive current session.
					id = sessionID
				}
				archiveSession(id)
			default:
				fmt.Fprintf(os.Stderr, "Unknown slash-command: /%s\n", cmd)
			}
			continue
		}

		// Normal message — send to agent.
		if strings.TrimSpace(line) == "" {
			continue
		}

		// POST to session messages endpoint (Phase 1).
		body := map[string]interface{}{"message": line}
		if selectedProvider != "" {
			body["provider"] = selectedProvider
		}
		jsonBody, err := json.Marshal(body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling request: %v\n", err)
			continue
		}

		req, err := http.NewRequestWithContext(context.Background(), "POST", serverURL()+"/api/sessions/"+url.PathEscape(sessionID)+"/messages", bytes.NewReader(jsonBody))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "POST /api/sessions/%s/messages failed: %v\n", sessionID, err)
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, string(bodyBytes))
			continue
		}

		// Parse and display response.
		var result map[string]interface{}
		if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&result); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
			continue
		}

		// Track turn for transcript persistence.
		responseText := ""
		if resp, ok := result["response"].(string); ok {
			responseText = resp
		} else if msg, ok := result["message"].(string); ok {
			responseText = msg
		}

		turns = append(turns, map[string]interface{}{
			"role":      "user",
			"content":   line,
			"timestamp": time.Now().Format(time.RFC3339),
		})
		if responseText != "" {
			turns = append(turns, map[string]interface{}{
				"role":      "assistant",
				"content":   responseText,
				"timestamp": time.Now().Format(time.RFC3339),
			})
		}

		// Display agent response (structured or raw).
		displayResponse(result)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Read error: %v\n", err)
		os.Exit(1)
	}
}

// printChatHelp displays help for the sessions open REPL.
func printChatHelp() {
	fmt.Println(`sessions open — Interactive REPL chat mode

Usage: norman sessions open <workspace_name> [<session_id>]

Slash-commands (prefix with /):
  /help             Show this help
  /tasks            List emergent scheduled tasks for current session
  /close            Close the current session (save transcript)
  /new              Start a new chat session
  /history [N]      Show last N messages of current session
  /sessions         List all active sessions
  /switch <id>      Switch to another session by ID
  /archive <id>     Archive a session's transcript by session ID

Examples:
  norman sessions open my-project
  norman sessions open my-project abc12345`)
}

// listEmergentTasks lists tasks for the given session.
func listEmergentTasks(sessionID string) {
	body, err := httpGet("/api/tasks")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching tasks: %v\n", err)
		return
	}
	fmt.Println("Emergent scheduled tasks:")
	fmt.Printf("%s\n", string(body))
}

// showHistory displays the last N messages of a session.
func showHistory(sessionID string, args []string) {
	n := 10 // default
	if len(args) > 0 {
		if num, err := fmt.Sscanf(args[0], "%d", &n); err != nil || num == 0 {
			fmt.Fprintf(os.Stderr, "Invalid number: %s\n", args[0])
			return
		}
	}

	// Resolve session name to actual UUID if needed.
	actualID, found := resolveSessionID(sessionID)
	if !found {
		fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", sessionID)
		return
	}

	body, err := httpGet("/api/sessions/" + url.PathEscape(actualID) + "/history?limit=" + fmt.Sprintf("%d", n))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching history: %v\n", err)
		return
	}

	var turns []map[string]interface{}
	if err := json.Unmarshal(body, &turns); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing history: %v\n", err)
		return
	}

	fmt.Printf("\n--- Last %d messages ---\n", n)
	for i, t := range turns {
		role, _ := t["role"].(string)
		content, _ := t["content"].(string)
		ts, _ := t["timestamp"].(string)
		if ts != "" {
			fmt.Printf("[%s] %s: %s\n", ts, role, content)
		} else {
			fmt.Printf("%s: %s\n", role, content)
		}
		if i < len(turns)-1 {
			fmt.Println("---")
		}
	}
	fmt.Printf("---\n")
}

// resolveSessionID resolves a session name/short ID to the actual UUID.
func resolveSessionID(input string) (string, bool) {
	body, err := httpGet("/api/sessions")
	if err != nil {
		return input, false // fall back to original
	}
	var sessions []map[string]interface{}
	if err := json.Unmarshal(body, &sessions); err != nil {
		return input, false
	}
	for _, s := range sessions {
		name, _ := s["name"].(string)
		id, _ := s["id"].(string)
		if name == input || strings.HasPrefix(id[:8], input[:8]) {
			return id, true
		}
	}
	return input, false
}

// displayResponse formats and prints the agent's response.
func displayResponse(result map[string]interface{}) {
	if resp, ok := result["response"].(string); ok {
		fmt.Println(resp)
		return
	}
	if msg, ok := result["message"].(string); ok {
		fmt.Println(msg)
		return
	}
	// Fallback: print as formatted JSON.
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}
