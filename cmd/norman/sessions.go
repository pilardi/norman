package main

import (
	"context"
	"fmt"
	"os"

	"github.com/norman/norman/pkg/protocol"
	"github.com/urfave/cli/v3"
)

// handleSessions manages sessions via CLI.
// shortID returns a truncated UUID representation (first 8 chars).
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func handleSessions(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() == 0 {
		fmt.Println(sessionHelpText)
		return nil
	}

	subcommand := cmd.Args().First()
	subargs := cmd.Args().Tail()

	switch subcommand {
	case "list":
		return handleSessionList(subargs)
	case "archive":
		if len(subargs) < 1 {
			return fmt.Errorf("usage: norman sessions archive <session_id>")
		}
		if err := handleSessionArchive(subargs[0]); err != nil {
			return fmt.Errorf("archive failed: %v", err)
		}
		return nil
	case "open", "chat":
		handleChat(subargs)
		return nil
	default:
		return fmt.Errorf("unknown sessions subcommand: %s\n\n%s", subcommand, sessionHelpText)
	}
}

const sessionHelpText = `sessions — Manage chat sessions

Usage: norman sessions <subcommand> [args...]

Subcommands:
  open <workspace_name> [<session_id>]   Open an interactive REPL chat session
  list                          List active chat sessions
  archive <session_id>          Archive a session's transcript`

// handleSessionList lists active sessions using the generated protocol client.
func handleSessionList(args []string) error {
	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.ListSessions(context.Background())
	if err != nil {
		return fmt.Errorf("error fetching sessions: %w", err)
	}

	parsed, err := protocol.ParseListSessionsResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing sessions response: %w", err)
	}

	if parsed.JSON200 == nil || len(*parsed.JSON200) == 0 {
		fmt.Println("No active sessions.")
		return nil
	}

	sessions := *parsed.JSON200
	fmt.Println("Active sessions:")
	for _, s := range sessions {
		name := "(unknown)"
		if s.Name != nil {
			name = *s.Name
		}
		status := "unknown"
		if s.SessionStatus != nil {
			status = string(*s.SessionStatus)
		}
		fmt.Printf("  %-20s status=%s\n", name, status)
	}
	return nil
}

// handleSessionArchive archives a session's transcript via POST /api/sessions/{id}.
func handleSessionArchive(sessionID string) error {
	actualID, found := resolveSessionID(sessionID)
	if !found {
		return fmt.Errorf("session '%s' not found, run 'norman sessions list' to see available sessions", sessionID)
	}

	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.ArchiveTask(context.Background(), protocol.ArchiveTaskRequest{TaskId: actualID})
	if err != nil {
		return fmt.Errorf("archive failed for task %s: %w", shortID(actualID), err)
	}

	_, err = protocol.ParseArchiveTaskResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing archive response: %w", err)
	}

	fmt.Printf("Task '%s' archived successfully.\n", shortID(actualID))
	return nil
}

// listSessions lists all active sessions via GET /api/sessions.
func listSessions() {
	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create API client: %v\n", err)
		return
	}

	resp, err := client.ListSessions(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching sessions: %v\n", err)
		return
	}

	parsed, err := protocol.ParseListSessionsResponse(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing sessions response: %v\n", err)
		return
	}

	if parsed.JSON200 == nil || len(*parsed.JSON200) == 0 {
		fmt.Println("--- Active Sessions ---\n(No active sessions)\n---")
		return
	}

	sessions := *parsed.JSON200
	fmt.Println("--- Active Sessions ---")
	for _, s := range sessions {
		name := "(unknown)"
		if s.Name != nil {
			name = *s.Name
		}
		status := "unknown"
		if s.SessionStatus != nil {
			status = string(*s.SessionStatus)
		}
		fmt.Printf("  %-20s status=%s\n", name, status)
	}
	fmt.Println("---")
}
