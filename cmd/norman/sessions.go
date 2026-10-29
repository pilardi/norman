package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

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
  archive <session_id>          Archive a session's transcript via POST /api/sessions/{id}`

// handleSessionList lists active sessions.
func handleSessionList(args []string) error {
	body, err := httpGet("/api/sessions")
	if err != nil {
		return fmt.Errorf("error fetching sessions: %v", err)
	}

	var sessions []map[string]interface{}
	if err := json.Unmarshal(body, &sessions); err != nil {
		return fmt.Errorf("error parsing sessions response: %v", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No active sessions.")
		return nil
	}

	fmt.Println("Active sessions:")
	for _, s := range sessions {
		name, _ := s["name"].(string)
		status, _ := s["status"].(string)
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

	url := serverURL() + "/api/sessions/" + actualID
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, nil)
	if err != nil {
		return fmt.Errorf("error creating archive request: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("archive failed for session %s: %v", shortID(actualID), err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d archiving session %s: %s", resp.StatusCode, shortID(actualID), string(body))
	}

	fmt.Printf("Session '%s' archived successfully.\n", shortID(actualID))
	return nil
}

// archiveSession archives a session's transcript via POST /api/sessions/{id}.
func archiveSession(sessionID string) {
	actualID, found := resolveSessionID(sessionID)
	if !found {
		fmt.Fprintf(os.Stderr, "Error: session '%s' not found. Run 'norman sessions list' to see available sessions.\n", sessionID)
		return
	}

	url := serverURL() + "/api/sessions/" + actualID
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating archive request: %v\n", err)
		os.Exit(1)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Archive failed for session %s: %v\n", shortID(actualID), err)
		os.Exit(1)
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "HTTP %d archiving session %s: %s\n", resp.StatusCode, shortID(actualID), string(body))
		os.Exit(1)
	}
	fmt.Printf("Session '%s' archived successfully.\n", shortID(actualID))
}

// listSessions lists all active sessions via GET /api/sessions.
func listSessions() {
	body, err := httpGet("/api/sessions")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching sessions: %v\n", err)
		return
	}
	fmt.Printf("\n--- Active Sessions ---\n%s\n---\n", string(body))
}
