package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/norman/norman/pkg/client"
	"github.com/norman/norman/pkg/protocol"
	"golang.org/x/sys/unix"
)

const chatHelpText = "Type a message or use slash-commands (/help, /tasks, /close, /new, /history [N], /sessions, /switch <id>, /archive <id>, /compress [--aggressive])"

// ClientState represents the current operational state of the REPL client.
type ClientState int32

const (
	StateIdle ClientState = iota
	StateSendingMessage
	StateExiting
)

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

	_, err := client.GetProtocolClient()
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
		detail, err := client.GetSession(client.UrlPathEscape(sessionID))
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
		c, err := client.GetProtocolClient()
		if err == nil {
			rsp, err := c.GetVersion(context.Background())
			if err == nil {
				parsed, _ := protocol.ParseGetVersionResponse(rsp)
				v := parsed.GetJSON200()
				u := c.Server
				fmt.Printf("Connected to %s (%s %s)\n", u, v.Version, v.BuildDate)
			}
		}

		fmt.Printf("Resolving workspace '%s'...\n", workspaceName)

		// Resolve workspace name to ID using typed client methods.
		cli, err := client.GetProtocolClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating API client: %v\n", err)
			os.Exit(1)
		}

		rsp, err := cli.ListWorkspaces(context.Background(), nil)
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
			name := client.StrVal(w.Name)
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

		sessRsp, err := cli.CreateSession(context.Background(), body)
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

	// Set up signal handling for Ctrl+C (SIGINT) to cancel requests without exiting
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create a cancellable context for the chat session
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())

	// Current client state
	state := new(atomic.Int32)
	state.Store(int32(StateIdle))

	// Context for the read loop to unblock on Ctrl+C when in StateIdle
	readCtx, readCancel := context.WithCancel(context.Background())

	// Handle signal in a goroutine
	go func() {
		for range sigChan {
			s := state.Load()
			if s == int32(StateSendingMessage) {
				fmt.Fprintln(os.Stderr, "\nInterrupt received. Cancelling current request...")
				if cancel != nil {
					cancel()
				}
			} else if s == int32(StateIdle) {
				fmt.Fprintln(os.Stderr, "\nInterrupt received. Exiting chat session...")
				state.Store(int32(StateExiting))
				if cancel != nil {
					cancel()
				}
				readCancel()
			}
		}
	}()

	// Enter REPL loop.
	fmt.Println("\n=== Norman Chat Session ===")
	sessionName := "(unknown)"
	if sess != nil && sess.Name != nil {
		sessionName = *sess.Name
	}
	fmt.Printf("Session: %s | Workspace resolved\n", sessionName)
	fmt.Println("Type a message or use slash-commands (/help, /tasks, /close, /new, /history [N], /sessions, /switch <id>, /archive <id>, /compress [--aggressive])")

	// Track turns for server-side transcript persistence.
	var turns []map[string]interface{}

	fd := int(os.Stdin.Fd())
	// Set non-blocking mode for stdin
	if err := unix.SetNonblock(fd, true); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not set stdin to non-blocking mode: %v\n", err)
	}
	defer func() {
		if err := unix.SetNonblock(fd, false); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not restore stdin to blocking mode: %v\n", err)
		}
	}()

	for {
		if state.Load() == int32(StateExiting) {
			fmt.Fprintln(os.Stderr, "\nExiting chat session.")
			break
		}

		// Read line interruptibly using non-blocking read
		line, err := readLineInterruptible(fd, readCtx)
		if err != nil {
			if err == context.Canceled {
				fmt.Fprintln(os.Stderr, "\nExiting chat session.")
				break
			}
			if strings.Contains(err.Error(), "interrupted system call") ||
				strings.Contains(err.Error(), "i/o interrupt") ||
				errors.Is(err, syscall.EINTR) {
				continue // interrupt or timeout, check state again
			}
			// Other errors (EOF, etc.)
			fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
			break
		}

		line = strings.TrimSpace(line)

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
		if line == "" {
			continue
		}

		// POST to session messages endpoint (Phase 2).
		body := protocol.SendMessageRequest{
			Message: line,
		}
		if selectedProvider != "" {
			body.Provider = client.StrPtr(selectedProvider)
		}

		cli, err := client.GetProtocolClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating client: %v\n", err)
			continue
		}

		// Set state to sending message
		state.Store(int32(StateSendingMessage))

		msgRsp, err := cli.SendMessage(ctx, sessionID, body)

		// Set state back to idle
		state.Store(int32(StateIdle))

		if err != nil {
			// Check for context cancellation
			if ctx.Err() == context.Canceled {
				fmt.Fprintln(os.Stderr, "\nRequest cancelled by user.")
				// Reset context for the next message
				var newCtx context.Context
				var newCancel context.CancelFunc
				newCtx, newCancel = context.WithCancel(context.Background())
				ctx = newCtx
				cancel = newCancel
				continue
			}
			fmt.Fprintf(os.Stderr, "send message to session %s: %v\n", client.ShortID(sessionID), err)
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
		} else {
			// Non-200 response: print the error body so the user sees what went wrong.
			bodyBytes := string(parsedMsg.GetBody())
			if bodyBytes == "" {
				bodyBytes = parsedMsg.Status()
			}
			var errResp map[string]any
			if json.Unmarshal([]byte(bodyBytes), &errResp) == nil {
				if e, ok := errResp["error"].(string); ok {
					bodyBytes = e
				}
			}
			fmt.Fprintf(os.Stderr, "Error: message failed (status %d): %s\n", parsedMsg.StatusCode(), bodyBytes)
			continue
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

	// Cancel context on exit
	if cancel != nil {
		cancel()
	}
}

// readLineInterruptible reads a line from the file descriptor in a non-blocking way
// that can be cancelled via the context.
func readLineInterruptible(fd int, cancelCtx context.Context) (string, error) {
	var buf []byte
	for {
		select {
		case <-cancelCtx.Done():
			return "", context.Canceled
		default:
		}

		var b [1]byte
		n, err := syscall.Read(fd, b[:])
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				// No data available, poll briefly
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if err == syscall.EINTR {
				continue
			}
			return "", err
		}
		if n == 0 {
			return "", io.EOF
		}
		if b[0] == '\n' || b[0] == '\r' {
			return string(buf), nil
		}
		buf = append(buf, b[0])
	}
}
