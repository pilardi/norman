package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/norman/norman/pkg/protocol"
)

// runWebSocketLoop connects to the WebSocket endpoint and displays incoming events.
// It runs concurrently with the REPL input loop.
const chatHelpText = "Type a message or use slash-commands (/help, /tasks, /close, /new, /history [N], /sessions, /switch <id>, /archive <id>)"

func runWebSocketLoop(sessionID string) {
	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create client: %v\n", err)
		return
	}

	params := &protocol.ConnectWebSocketParams{
		SessionId: sessionID,
	}

	resp, err := client.ConnectWebSocket(context.Background(), params)
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

	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create client: %v\n", err)
		os.Exit(1)
	}

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, chatHelpText)
		os.Exit(1)
	}

	// Parse args: first is workspace name, second optional is session ID.
	for i := 0; i < len(args); i++ {
		if args[i] == "-h" || args[i] == "--help" {
			fmt.Fprintln(os.Stderr, chatHelpText)
			return
		} else if workspaceName == "" {
			workspaceName = args[i]
		} else if sessionID == "" {
			var found bool
			var err error
			sessionID, found, err = resolveSessionID(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving session '%s': %v\n", args[i], err)
				os.Exit(1)
			}
			if !found {
				fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", args[i])
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Too many arguments. Usage: norman sessions open <workspace_name> [<session_id>]\n")
			fmt.Fprintln(os.Stderr, chatHelpText)
			os.Exit(1)
		}
	}

	if workspaceName == "" {
		fmt.Fprintln(os.Stderr, "Error: workspace name is required")
		fmt.Fprintln(os.Stderr, chatHelpText)
		os.Exit(1)
	}

	// If only workspace provided (no session ID), resolve/create one.
	var sess *protocol.Session
	if workspaceName != "" && sessionID == "" {
		// Fetch server version info for diagnostic output.
		client, err := getProtocolClient()
		if err == nil {
			rsp, err := client.GetVersion(context.Background())
			if err == nil {
				parsed, _ := protocol.ParseGetVersionResponse(rsp)
				v := parsed.GetJSON200()
				u := client.Server
				fmt.Printf("Connected to %s (%s %s)\n", u, v.Version, v.BuildDate)
			}
		}

		fmt.Printf("Resolving workspace '%s'...\n", workspaceName)

		// Resolve workspace name to ID using typed client methods.
		rsp, err := client.ListWorkspaces(context.Background(), nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching workspaces: %v\n", err)
			os.Exit(1)
		}
		parsed, err := protocol.ParseListWorkspacesResponse(rsp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing workspaces response: %v\n", err)
			os.Exit(1)
		}
		workspaces := parsed.GetJSON200()

		foundID := ""
		for _, w := range *workspaces {
			name := strVal(w.Name)
			if name == workspaceName || strings.EqualFold(name, workspaceName) {
				foundID = w.Id.String()
				break
			}
		}
		if foundID == "" {
			fmt.Fprintf(os.Stderr, "Workspace '%s' not found. Run 'norman workspaces list'.\n", workspaceName)
			os.Exit(1)
		}

		// Always create a new session (reuse only via explicit --session flag).
		fmt.Printf("Creating new session for workspace '%s'...\n", workspaceName)
		ct := protocol.Cli
		body := protocol.SessionCreateRequest{
			WorkspaceId: foundID,
			ClientType:  &ct,
		}

		sessRsp, err := client.CreateSession(context.Background(), body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create session: %v\n", err)
			os.Exit(1)
		}
		parsedSess, err := protocol.ParseCreateSessionResponse(sessRsp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing session response: %v\n", err)
			os.Exit(1)
		}
		sess = parsedSess.GetJSON201()
		if sess == nil {
			// Server returned an error response (e.g., 409 max sessions reached).
			body := string(parsedSess.GetBody())
			if body == "" {
				body = parsedSess.Status()
			}
			// Try to extract the "error" field from JSON body for a clean message.
			var errResp map[string]any
			if json.Unmarshal([]byte(body), &errResp) == nil {
				if e, ok := errResp["error"].(string); ok {
					body = e
				}
			}
			fmt.Fprintf(os.Stderr, "Error: session creation failed (status %d): %s\n", parsedSess.StatusCode(), body)
			os.Exit(1)
		}
		sessionID = sess.Id.String()
		fmt.Printf("Created session '%s' for workspace '%s'\n", *sess.Name, workspaceName)
	}

	// Start real-time WebSocket event push loop (runs concurrently).
	go runWebSocketLoop(sessionID)

	// Enter REPL loop.
	fmt.Println("\n=== Norman Chat Session ===")
	sessionName := "(unknown)"
	if sess != nil && sess.Name != nil {
		sessionName = *sess.Name
	}
	fmt.Printf("Session: %s | Workspace resolved\n", sessionName)
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
				fmt.Fprintln(os.Stderr, chatHelpText)
			case "list", "tasks":
				listEmergentTasks(sessionID)
			case "close":
				fmt.Println("Closing session...")
				return
			case "new":
				// Start a new session (auto-create).
				fmt.Printf("Creating new session...\n")
				ct := protocol.Cli
				body := protocol.SessionCreateRequest{
					ClientType: &ct,
				}

				newSessRsp, err := client.CreateSession(context.Background(), body)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Failed to create new session: %v\n", err)
					continue
				}
				parsedNewSess, err := protocol.ParseCreateSessionResponse(newSessRsp)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error parsing new session response: %v\n", err)
					continue
				}
				newSess := parsedNewSess.GetJSON201()
				sessionID = newSess.Id.String()
				fmt.Printf("New session '%s' created.\n", strVal(newSess.Name))
			case "history":
				showHistory(sessionID, cmdArgs)
			case "sessions":
				_ = handleSessionList([]string{})
			case "switch":
				if len(cmdArgs) == 0 {
					fmt.Fprintln(os.Stderr, "Usage: /switch <session-id>")
					continue
				}
				resolvedID, found, err := resolveSessionID(cmdArgs[0])
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error resolving session '%s': %v\n", cmdArgs[0], err)
					continue
				}
				if !found {
					fmt.Fprintf(os.Stderr, "Error: session '%s' not found.\n", cmdArgs[0])
					continue
				}
				sessionID = resolvedID
				fmt.Printf("Switched to session '%s'\n", cmdArgs[0])
			case "archive":
				id := cmdArgs[0]
				if len(cmdArgs) == 0 {
					// Auto-archive current session.
					id = sessionID
				}
				if err := handleSessionArchive(id); err != nil {
					fmt.Fprintf(os.Stderr, "Error archiving session: %v\n", err)
				}
			default:
				fmt.Fprintf(os.Stderr, "Unknown slash-command: /%s\n", cmd)
			}
			continue
		}

		// Normal message — send to agent.
		if strings.TrimSpace(line) == "" {
			continue
		}

		// POST to session messages endpoint (Phase 2).
		body := protocol.SendMessageRequest{
			Message: line,
		}
		if selectedProvider != "" {
			body.Provider = strPtr(selectedProvider)
		}

		client, err := getProtocolClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
			continue
		}

		msgRsp, err := client.SendMessage(context.Background(), sessionID, body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "send message to session %s: %v\n", shortID(sessionID), err)
			continue
		}

		parsedMsg, err := protocol.ParseSendMessageResponse(msgRsp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing message response: %v\n", err)
			continue
		}

		result := parsedMsg.GetJSON200()
		responseText := ""
		if result != nil && result.Response != nil {
			responseText = *result.Response
		}

		// Track turn for transcript persistence.
		_ = append(turns, map[string]interface{}{
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
		displayResponseTyped(result)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Read error: %v\n", err)
		os.Exit(1)
	}
}

// listEmergentTasks lists tasks for the given session.
func listEmergentTasks(sessionID string) {
	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create client: %v\n", err)
		return
	}

	rsp, err := client.ListTasks(context.Background(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching tasks: %v\n", err)
		return
	}

	parsed, err := protocol.ParseListTasksResponse(rsp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing tasks response: %v\n", err)
		return
	}

	tasks := parsed.GetJSON200()
	fmt.Println("Emergent scheduled tasks:")
	for _, t := range *tasks {
		fmt.Printf("  Task %s\n", t.Id.String())
	}
}

// resolveSessionID resolves a session name/short ID to the actual UUID.
// Returns (resolvedID, found, error). If an error occurred (e.g., server down),
// found=false and error is non-nil — caller should surface it instead of
// treating it as "not found".
func resolveSessionID(input string) (string, bool, error) {
	client, err := getProtocolClient()
	if err != nil {
		return input, false, fmt.Errorf("failed to create API client: %w", err)
	}

	rsp, err := client.ListSessions(context.Background())
	if err != nil {
		return input, false, err
	}

	parsed, err := protocol.ParseListSessionsResponse(rsp)
	if err != nil {
		return input, false, fmt.Errorf("error parsing sessions response: %w", err)
	}

	sessions := parsed.GetJSON200()
	for _, s := range *sessions {
		name := strVal(s.Name)
		id := s.Id.String()
		if name == input || strings.HasPrefix(id[:8], input[:8]) {
			return id, true, nil
		}
	}
	return input, false, nil
}

// displayResponseTyped formats and prints the agent's typed response.
func displayResponseTyped(result *protocol.AgentResponse) {
	if result != nil && result.Response != nil {
		fmt.Println(*result.Response)
		return
	}
	// Fallback: print as formatted JSON (for backward compat).
	data, _ := json.MarshalIndent(map[string]interface{}{}, "", "  ")
	fmt.Println(string(data))
}
