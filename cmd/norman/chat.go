package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/norman/norman/pkg/client"
	"github.com/norman/norman/pkg/protocol"
)

const chatHelpText = "Type a message or use slash-commands (/help, /tasks, /close, /new, /history [N], /sessions, /switch <id>, /archive <id>)"

// CreateNewSession creates a new session for the given workspace and returns its ID.
// Used by both REPL and TUI.
func CreateNewSession(workspaceName string) (string, error) {
	return client.CreateNewSession(workspaceName)
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

// handleChat implements the interactive REPL chat mode.
// Usage: norman sessions open <workspace_name> [<session_id>]
func handleChat(args []string) {
	workspaceName := ""
	sessionID := ""

	_, err := getProtocolClient()
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

	// Fetch existing session data if a session ID was provided.
	var sess *protocol.Session
	if sessionID != "" {
		detail, err := client.GetSession(urlPathEscape(sessionID))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching session '%s': %v\n", sessionID, err)
			os.Exit(1)
		}
		if detail == nil {
			fmt.Fprintf(os.Stderr, "Error: session '%s' not found\n", sessionID)
			os.Exit(1)
		}
		// Convert SessionDetail to Session for display.
		sess = &protocol.Session{
			Id:        detail.Id,
			Name:      detail.Name,
			CreatedAt: detail.CreatedAt,
		}
	}

	// If only workspace provided (no session ID), resolve/create one.
	if sess == nil {
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
			newSessionID, handled := dispatchSlashCommand(cmd, cmdArgs, sessionID, workspaceName)
			if !handled {
				// Fall through to normal message handling.
			} else {
				sessionID = newSessionID
				continue
			}
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
